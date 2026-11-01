package protocols

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openpak/switch-nex/rmc"
)

type memBackend struct {
	mu      sync.Mutex
	users   map[uint64]*smm2User
	courses map[uint64]*SMM2Course
	next    uint64
}

func newMem() *memBackend {
	return &memBackend{users: map[uint64]*smm2User{}, courses: map[uint64]*SMM2Course{}, next: 1000000}
}

func (m *memBackend) PutUser(_ context.Context, pid uint64, u *smm2User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[pid] = u
	return nil
}
func (m *memBackend) GetUser(_ context.Context, pid uint64) (*smm2User, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[pid]
	return u, ok, nil
}
func (m *memBackend) NewCourse(_ context.Context, c SMM2Course) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	c.DataID, c.Status, c.Created, c.Updated, c.Rel = m.next, SMM2Pending, time.Now(), time.Now(), map[uint8]uint32{}
	m.courses[c.DataID] = &c
	return c.DataID, nil
}
func (m *memBackend) own(owner, id uint64) *SMM2Course {
	c := m.courses[id]
	if c == nil || c.Owner != owner || c.Status == SMM2Deleted {
		return nil
	}
	return c
}
func (m *memBackend) UpdateCourse(_ context.Context, u SMM2Course) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.own(u.Owner, u.DataID)
	if c == nil {
		return false, nil
	}
	c.Name, c.Description, c.Style, c.Theme = u.Name, u.Description, u.Style, u.Theme
	return true, nil
}
func (m *memBackend) CompleteCourse(_ context.Context, owner, id uint64, size uint32) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.own(owner, id)
	if c == nil {
		return false, nil
	}
	c.Status, c.BodySize = SMM2Complete, size
	return true, nil
}
func (m *memBackend) SetRelation(_ context.Context, owner, id uint64, typ uint8, size uint32) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.own(owner, id)
	if c == nil {
		return false, nil
	}
	c.Rel[typ] = size
	return true, nil
}
func (m *memBackend) LatestPending(_ context.Context, owner uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best uint64
	for id, c := range m.courses {
		if c.Owner == owner && c.Status == SMM2Pending && id > best {
			best = id
		}
	}
	return best, nil
}
func (m *memBackend) DeleteCourse(_ context.Context, owner, id uint64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.own(owner, id)
	if c == nil {
		return false, nil
	}
	c.Status = SMM2Deleted
	return true, nil
}
func (m *memBackend) GetCourses(_ context.Context, ids []uint64) (map[uint64]SMM2Course, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[uint64]SMM2Course{}
	for _, id := range ids {
		if c := m.courses[id]; c != nil {
			out[id] = *c
		}
	}
	return out, nil
}
func (m *memBackend) Search(_ context.Context, q SMM2Query) ([]SMM2Course, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SMM2Course
	for _, c := range m.courses {
		if c.Status != SMM2Complete {
			continue
		}
		if len(q.Owners) > 0 {
			hit := false
			for _, o := range q.Owners {
				hit = hit || o == c.Owner
			}
			if !hit {
				continue
			}
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if q.Sort == "stars" && out[i].Stars != out[j].Stars {
			return out[i].Stars > out[j].Stars
		}
		return out[i].DataID > out[j].DataID
	})
	total := len(out)
	if q.Offset < len(out) {
		out = out[q.Offset:]
	} else {
		out = nil
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, total, nil
}
func (m *memBackend) CountByOwner(_ context.Context, owner uint64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.courses {
		if c.Owner == owner && c.Status != SMM2Deleted {
			n++
		}
	}
	return n, nil
}

type fakeBlobs struct{ sizes map[string]int64 }

func (f *fakeBlobs) PostForm(key string, max uint32, _ time.Duration) (string, [][2]string, error) {
	return "https://ds.test/nex-smm2/", [][2]string{{"key", key}, {"policy", fmt.Sprint(max)}}, nil
}
func (f *fakeBlobs) GetURL(key string, _ time.Duration) string { return "https://ds.test/get/" + key }
func (f *fakeBlobs) Size(key string) (int64, bool)             { n, ok := f.sizes[key]; return n, ok }

func courseEnv() (*Env, *memBackend, *fakeBlobs) {
	e := smm2Env()
	mem, blobs := newMem(), &fakeBlobs{sizes: map[string]int64{}}
	e.SMM2, e.SMM2Blobs = mem, blobs
	return e, mem, blobs
}

func prepareBody(name, desc string, style, theme uint8) []byte {
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.String(name)
		w.String(desc)
		w.U32(0)
		w.Bool(false)
		w.U8(style)
		w.U8(theme)
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
	return w.Bytes()
}

func call(t *testing.T, e *Env, pid uint64, method uint32, body []byte) []byte {
	t.Helper()
	_, resp, errCode := decodeResponse(Dispatch(e, conn(pid), request(ProtoDataStore, method, body)))
	if errCode != 0 {
		t.Fatalf("method %#x: error %#x", method, errCode)
	}
	return resp
}

func upload(t *testing.T, e *Env, b *fakeBlobs, pid uint64, name string) uint64 {
	t.Helper()
	r := rmc.NewReader(call(t, e, pid, 0x42, prepareBody(name, "desc of "+name, 1, 2)))
	_, info, err := r.Struct()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := info.U64()
	url, _ := info.String()
	if !strings.HasPrefix(url, "https://ds.test/nex-smm2/") {
		t.Fatalf("upload url %q", url)
	}
	if n, _ := info.ListHeader(); n != 0 {
		t.Fatalf("headers: %d", n)
	}
	if n, _ := info.ListHeader(); n != 2 { // the form fields
		t.Fatalf("form fields: %d", n)
	}
	b.sizes[SMM2BodyKey(id)] = 4321 // the console's S3 POST landed
	cw := rmc.NewWriter()
	cw.StructOf(0, func(w *rmc.Writer) {
		for range 5 {
			w.String("")
		}
		w.U64(id)
		w.Raw(prepareBody(name, "desc of "+name, 1, 2))
	})
	call(t, e, pid, 0x44, cw.Bytes())
	return id
}

// readCourseInfo walks CourseInfo field by field (datastore_smm2 CourseInfo.load)
// and insists it is consumed exactly.
func readCourseInfo(t *testing.T, r *rmc.Reader) (id uint64, code, name string, owner uint64, thumb string) {
	t.Helper()
	_, c, err := r.Struct()
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	id, err = c.U64()
	must(err)
	code, err = c.String()
	must(err)
	owner, err = c.U64()
	must(err)
	name, err = c.String()
	must(err)
	_, err = c.String() // description
	must(err)
	for range 2 { // style, theme
		_, err = c.U8()
		must(err)
	}
	_, err = c.U64() // upload time
	must(err)
	for range 4 { // difficulty, tag1, tag2, unk
		_, err = c.U8()
		must(err)
	}
	_, err = c.U32()
	must(err)
	_, err = c.U16()
	must(err)
	_, err = c.U16()
	must(err)
	_, err = c.QBuffer()
	must(err)
	maps := func() {
		n, err := c.ListHeader()
		must(err)
		for range n {
			_, err = c.U8()
			must(err)
			_, err = c.U32()
			must(err)
		}
	}
	maps() // play stats
	maps() // ratings
	maps() // unk
	_, _, err = c.Struct()
	must(err) // time stats
	maps()    // comment stats
	for range 4 {
		_, err = c.U8()
		must(err)
	}
	for i := range 2 {
		_, th, err := c.Struct()
		must(err)
		url, _ := th.String()
		if i == 1 {
			thumb = url
		}
	}
	if c.Remaining() != 0 {
		t.Fatalf("CourseInfo has %d unread bytes", c.Remaining())
	}
	return
}

func TestSMM2CourseCodeRoundTrip(t *testing.T) {
	for _, id := range []uint64{1, 1000000, 1234567, 1<<36 - 1} {
		code := SMM2CourseCode(id)
		if len(code) != 11 || code[3] != '-' || code[7] != '-' {
			t.Fatalf("code %q", code)
		}
		got, ok := SMM2ParseCourseCode(code)
		if !ok || got != id {
			t.Fatalf("%d -> %q -> %d %v", id, code, got, ok)
		}
		if got, ok := SMM2ParseCourseCode(strings.ToLower(strings.ReplaceAll(code, "-", ""))); !ok || got != id {
			t.Fatalf("lower-case, no dashes: %v", ok)
		}
	}
	bad := SMM2CourseCode(1000000)
	bad = bad[:10] + string("0123456789BCDFGHJKLMNPQRSTVWXY"[(strings.IndexByte("0123456789BCDFGHJKLMNPQRSTVWXY", bad[10])+1)%30])
	if _, ok := SMM2ParseCourseCode(bad); ok {
		t.Fatal("a mistyped code parsed")
	}
}

// TestSMM2UploadThenBrowse: prepare, upload the body, complete, then the course
// shows in GetCourses, Latest and PostedBy, and downloads through PrepareGetObject.
func TestSMM2UploadThenBrowse(t *testing.T) {
	e, mem, blobs := courseEnv()
	id := upload(t, e, blobs, 77, "Castle Rush")
	other := upload(t, e, blobs, 88, "Sky Run")
	if mem.courses[id].Name != "Castle Rush" || mem.courses[id].Status != SMM2Complete || mem.courses[id].BodySize != 4321 {
		t.Fatalf("stored %+v", mem.courses[id])
	}
	if mem.courses[id].Style != 1 || mem.courses[id].Theme != 2 {
		t.Fatalf("style/theme %d/%d", mem.courses[id].Style, mem.courses[id].Theme)
	}

	// GetCourses: one found, one NotFound, results in request order.
	gw := rmc.NewWriter()
	gw.StructOf(0, func(w *rmc.Writer) { w.List(2); w.U64(id); w.U64(9); w.U32(0x1FF) })
	r := rmc.NewReader(call(t, e, 77, 0x46, gw.Bytes()))
	if n, _ := r.ListHeader(); n != 1 {
		t.Fatalf("courses: %d", n)
	}
	gid, code, name, owner, _ := readCourseInfo(t, r)
	if gid != id || code != SMM2CourseCode(id) || name != "Castle Rush" || owner != 77 {
		t.Fatalf("course %d %q %q %d", gid, code, name, owner)
	}
	if n, _ := r.ListHeader(); n != 2 {
		t.Fatalf("results: %d", n)
	}
	if a, _ := r.U32(); a != resultSuccess {
		t.Fatalf("first result %#x", a)
	}
	if b, _ := r.U32(); b != resultNotFoundInList {
		t.Fatalf("second result %#x", b)
	}

	// SearchCoursesLatest: newest first.
	lw := rmc.NewWriter()
	lw.StructOf(0, func(w *rmc.Writer) {
		w.U32(0x1FF)
		w.StructOf(0, func(w *rmc.Writer) { w.U32(0); w.U32(10) })
	})
	r = rmc.NewReader(call(t, e, 77, 0x49, lw.Bytes()))
	if n, _ := r.ListHeader(); n != 2 {
		t.Fatalf("latest: %d", n)
	}
	if first, _, _, _, _ := readCourseInfo(t, r); first != other {
		t.Fatalf("latest first = %d, want %d", first, other)
	}

	// SearchCoursesPostedBy owner 77.
	pw := rmc.NewWriter()
	pw.StructOf(0, func(w *rmc.Writer) {
		w.U32(0x1FF)
		w.StructOf(0, func(w *rmc.Writer) { w.U32(0); w.U32(10) })
		w.List(1)
		w.U64(77)
	})
	r = rmc.NewReader(call(t, e, 99, 0x4A, pw.Bytes()))
	if n, _ := r.ListHeader(); n != 1 {
		t.Fatalf("posted by: %d", n)
	}
	if gid, _, _, owner, _ := readCourseInfo(t, r); gid != id || owner != 77 {
		t.Fatalf("posted by -> %d owner %d", gid, owner)
	}
	if ok, _ := r.Bool(); !ok || r.Remaining() != 0 {
		t.Fatalf("trailer: %v, %d left", ok, r.Remaining())
	}

	// Download.
	dw := rmc.NewWriter()
	dw.StructOf(0, func(w *rmc.Writer) {
		w.U64(id)
		w.U32(0)
		w.StructOf(0, func(w *rmc.Writer) { w.U16(0); w.U32(0) })
		w.U64(0)
	})
	r = rmc.NewReader(call(t, e, 99, 0x19, dw.Bytes()))
	_, info, err := r.Struct()
	if err != nil {
		t.Fatal(err)
	}
	if url, _ := info.String(); url != "https://ds.test/get/"+SMM2BodyKey(id) {
		t.Fatalf("download url %q", url)
	}
	info.ListHeader()
	if size, _ := info.U32(); size != 4321 {
		t.Fatalf("download size %d", size)
	}
	info.Buffer()
	if got, _ := info.U64(); got != id {
		t.Fatalf("download id %d", got)
	}

	// A pending course is not downloadable or listed.
	pending := rmc.NewReader(call(t, e, 77, 0x42, prepareBody("Draft", "", 0, 0)))
	_, pinfo, _ := pending.Struct()
	pid, _ := pinfo.U64()
	dw2 := rmc.NewWriter()
	dw2.StructOf(0, func(w *rmc.Writer) { w.U64(pid) })
	_, _, errCode := decodeResponse(Dispatch(e, conn(99), request(ProtoDataStore, 0x19, dw2.Bytes())))
	if errCode == 0 {
		t.Fatal("a pending course downloaded")
	}
}

func TestSMM2ThumbnailsAndDelete(t *testing.T) {
	e, mem, blobs := courseEnv()
	id := upload(t, e, blobs, 77, "Pic")
	rw := rmc.NewWriter()
	rw.StructOf(0, func(w *rmc.Writer) {
		w.String(fmt.Sprint(id))
		w.U32(SMM2RelEntire)
		w.U32(900)
		w.U32(0)
		w.U32(0)
		w.EmptyList()
	})
	r := rmc.NewReader(call(t, e, 77, 0x84, rw.Bytes()))
	_, info, err := r.Struct()
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := info.String(); s != fmt.Sprint(id) {
		t.Fatalf("relation data id %q", s)
	}
	if url, _ := info.String(); url != "https://ds.test/nex-smm2/" {
		t.Fatalf("relation url %q", url)
	}
	if mem.courses[id].Rel[SMM2RelEntire] != 900 {
		t.Fatalf("relation not recorded: %v", mem.courses[id].Rel)
	}
	gw := rmc.NewWriter()
	gw.StructOf(0, func(w *rmc.Writer) { w.List(1); w.U64(id); w.U32(0x1FF) })
	r = rmc.NewReader(call(t, e, 77, 0x46, gw.Bytes()))
	r.ListHeader()
	if _, _, _, _, thumb := readCourseInfo(t, r); thumb != "https://ds.test/get/"+SMM2RelationKey(id, SMM2RelEntire) {
		t.Fatalf("entire thumbnail url %q", thumb)
	}

	// Only the owner deletes.
	dw := rmc.NewWriter()
	dw.StructOf(0, func(w *rmc.Writer) { w.U64(id); w.U64(0) })
	if _, _, errCode := decodeResponse(Dispatch(e, conn(88), request(ProtoDataStore, 0x04, dw.Bytes()))); errCode == 0 {
		t.Fatal("a stranger deleted a course")
	}
	call(t, e, 77, 0x04, dw.Bytes())
	if mem.courses[id].Status != SMM2Deleted {
		t.Fatal("owner delete did not stick")
	}
}

func TestSMM2UploadCapAndUserInfoCount(t *testing.T) {
	e, _, blobs := courseEnv()
	call(t, e, 77, 0x2F, registerUserBody("Maker"))
	for i := range SMM2MaxCourses {
		upload(t, e, blobs, 77, fmt.Sprintf("c%d", i))
	}
	if _, _, errCode := decodeResponse(Dispatch(e, conn(77), request(ProtoDataStore, 0x42, prepareBody("one too many", "", 0, 0)))); errCode == 0 {
		t.Fatal("the 33rd course was accepted")
	}
	r := rmc.NewReader(call(t, e, 77, 0x3C, nil))
	if ok, _ := r.Bool(); ok {
		t.Fatal("CanPostCourse true at 32/32")
	}
	if e.smm2Posted(77) != SMM2MaxCourses {
		t.Fatalf("posted %d", e.smm2Posted(77))
	}
}

// A restart: profiles come back from the durable store.
func TestSMM2UsersSurviveRestart(t *testing.T) {
	e, mem, _ := courseEnv()
	call(t, e, 77, 0x2F, registerUserBody("Maker"))
	e2 := smm2Env()
	e2.SMM2, e2.SMM2Blobs = mem, &fakeBlobs{}
	r := rmc.NewReader(call(t, e2, 77, 0x30, getUsersBody(77)))
	if n, _ := r.ListHeader(); n != 1 {
		t.Fatalf("users after restart: %d", n)
	}
	readUserInfo(t, r, 77, "Maker")
}
