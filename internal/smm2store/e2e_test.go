package smm2store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/openpak/switch-nex/protocols"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// TestEndToEndAgainstMinIO is the test client for the upload path: it plays the
// console's part against a real Postgres and a real S3 (MinIO). It runs when
// SMM2_TEST_DATABASE_URL and SMM2_TEST_S3_ENDPOINT / _ACCESS / _SECRET are set.
func TestEndToEndAgainstMinIO(t *testing.T) {
	endpoint := os.Getenv("SMM2_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("SMM2_TEST_S3_ENDPOINT not set")
	}
	s := testStore(t)
	blobs := NewBlobs(S3Config{Endpoint: endpoint, PublicURL: endpoint, Bucket: "nex-smm2-test",
		Access: os.Getenv("SMM2_TEST_S3_ACCESS"), Secret: os.Getenv("SMM2_TEST_S3_SECRET")})
	if err := blobs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // the test bucket holds nothing worth keeping
		del := func(path string) {
			req, _ := http.NewRequest("DELETE", blobs.presignPath("DELETE", endpoint, path, time.Minute), nil)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}
		rows, _ := s.pool.Query(context.Background(), "SELECT data_id FROM smm2.courses")
		for rows != nil && rows.Next() {
			var id int64
			rows.Scan(&id)
			del(fmt.Sprintf("/nex-smm2-test/course/%d.bcd", id))
			for _, typ := range []int{1, 2, 3, 5} {
				del(fmt.Sprintf("/nex-smm2-test/relation/%d/%d", id, typ))
			}
		}
		del("/nex-smm2-test")
	})
	if err := blobs.EnsureBucket(); err != nil {
		t.Fatalf("second EnsureBucket: %v", err)
	}
	env := protocols.NewEnv("smm2-e2e", "127.0.0.1", 22330)
	env.DataStore = protocols.DSSMM2
	env.SMM2, env.SMM2Blobs = s, blobs
	c := &prudplite.Connection{PID: 4242, Host: "127.0.0.1"}

	do := func(method uint32, body []byte) []byte {
		t.Helper()
		resp := protocols.Dispatch(env, c, &rmc.RMCMessage{IsRequest: true, Protocol: protocols.ProtoDataStore, MethodID: method, CallID: 1, Body: body})
		m, err := rmc.Decode(resp)
		if err != nil || m.IsError {
			t.Fatalf("method %#x: %v %#x", method, err, m.ErrorCode)
		}
		return m.Body
	}
	upload := func(url string, fields [][2]string, data []byte) {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, f := range fields {
			mw.WriteField(f[0], f[1])
		}
		fw, _ := mw.CreateFormFile("file", "x")
		fw.Write(data)
		mw.Close()
		resp, err := http.Post(url, mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("S3 POST: %s %s", resp.Status, b)
		}
	}
	readPost := func(b []byte, rel bool) (uint64, string, [][2]string) {
		_, info, err := rmc.NewReader(b).Struct()
		if err != nil {
			t.Fatal(err)
		}
		var first uint64
		if rel { // the relation answer opens with the id as a string
			ds, _ := info.String()
			fmt.Sscan(ds, &first)
		} else {
			first, _ = info.U64()
		}
		url, _ := info.String()
		info.ListHeader()
		n, _ := info.ListHeader()
		var f [][2]string
		for range n {
			_, kv, _ := info.Struct()
			k, _ := kv.String()
			v, _ := kv.String()
			f = append(f, [2]string{k, v})
		}
		return first, url, f
	}

	// PreparePostObjectCourse -> S3 POST -> CompletePostObjectsCourse
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.String("E2E Course")
		w.String("made by the test client")
		w.U32(0)
		w.Bool(false)
		w.U8(2)
		w.U8(3)
		for range 4 {
			w.U32(0)
		}
		w.QBuffer(nil)
		w.U8(0)
		w.U32(0)
		w.U16(0)
		w.U16(0)
		w.Bool(false)
		w.U32(0)
		w.U32(0)
		w.EmptyList()
	})
	param := w.Bytes()
	id, url, fields := readPost(do(0x42, param), false)
	body := bytes.Repeat([]byte("level"), 1000)
	upload(url, fields, body)

	cw := rmc.NewWriter()
	cw.StructOf(0, func(w *rmc.Writer) {
		for range 5 {
			w.String("")
		}
		w.U64(id)
		w.Raw(param)
	})
	do(0x44, cw.Bytes())
	got, _ := s.GetCourses(context.Background(), []uint64{id})
	if got[id].Status != protocols.SMM2Complete || got[id].BodySize != uint32(len(body)) {
		t.Fatalf("after complete: %+v", got[id])
	}

	// An over-limit upload is refused by the signed policy.
	_, url2, fields2 := readPost(do(0x42, param), false)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields2 {
		mw.WriteField(f[0], f[1])
	}
	fw, _ := mw.CreateFormFile("file", "x")
	fw.Write(make([]byte, 5<<20))
	mw.Close()
	if resp, err := http.Post(url2, mw.FormDataContentType(), &buf); err == nil {
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			t.Fatal("an oversize body was accepted")
		}
	}

	// Thumbnail relation object (method 132), then the read API serves it.
	rw := rmc.NewWriter()
	rw.StructOf(0, func(w *rmc.Writer) {
		w.String(fmt.Sprint(id))
		w.U32(protocols.SMM2RelEntire)
		w.U32(100)
		w.U32(0)
		w.U32(0)
		w.EmptyList()
	})
	_, rurl, rfields := readPost(do(0x84, rw.Bytes()), true)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{1}, 96)...)
	upload(rurl, rfields, jpeg)

	// Download the body through PrepareGetObject's presigned URL.
	dw := rmc.NewWriter()
	dw.StructOf(0, func(w *rmc.Writer) { w.U64(id) })
	_, gi, _ := rmc.NewReader(do(0x19, dw.Bytes())).Struct()
	gurl, _ := gi.String()
	resp, err := http.Get(gurl)
	if err != nil {
		t.Fatal(err)
	}
	dl, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(dl, body) {
		t.Fatalf("download: %s, %d bytes", resp.Status, len(dl))
	}

	api := &API{Store: s, Blobs: blobs, Key: "k"}
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/courses/"+protocols.SMM2CourseCode(id)+"/thumbnail", nil)
	req.Header.Set("X-Internal-Key", "k")
	tr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := io.ReadAll(tr.Body)
	tr.Body.Close()
	if tr.StatusCode != 200 || !bytes.Equal(img, jpeg) || tr.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail: %s %q %d bytes", tr.Status, tr.Header.Get("Content-Type"), len(img))
	}
}
