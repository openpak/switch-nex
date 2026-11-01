package smm2store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/openpak/switch-nex/protocols"
)

// These run against a real Postgres: set SMM2_TEST_DATABASE_URL to a scratch database.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("SMM2_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SMM2_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DROP SCHEMA smm2 CASCADE")
		s.Close()
	})
	s.pool.Exec(ctx, "TRUNCATE smm2.users, smm2.courses, smm2.relations")
	return s
}

func TestStoreCourseLifecycleAndAPI(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.PutUser(ctx, 77, &protocols.SMM2UserRecord{Name: "Maker", Mii: []byte{1, 2}, Country: "GB", Region: 1}); err != nil {
		t.Fatal(err)
	}
	u, ok, err := s.GetUser(ctx, 77)
	if err != nil || !ok || u.Name != "Maker" || len(u.Mii) != 2 {
		t.Fatalf("user %+v %v %v", u, ok, err)
	}
	id, err := s.NewCourse(ctx, protocols.SMM2Course{Owner: 77, Name: "Castle", Description: "d", Style: 1, Theme: 2, Raw: []byte("raw")})
	if err != nil || id < 1000000 {
		t.Fatalf("id %d %v", id, err)
	}
	if list, total, _ := s.Search(ctx, protocols.SMM2Query{}); len(list) != 0 || total != 0 {
		t.Fatal("a pending course is listed")
	}
	if ok, _ := s.CompleteCourse(ctx, 88, id, 10); ok {
		t.Fatal("a stranger completed a course")
	}
	if ok, _ := s.CompleteCourse(ctx, 77, id, 4321); !ok {
		t.Fatal("complete failed")
	}
	if ok, _ := s.SetRelation(ctx, 77, id, protocols.SMM2RelEntire, 900); !ok {
		t.Fatal("relation failed")
	}
	if n, _ := s.CountByOwner(ctx, 77); n != 1 {
		t.Fatalf("count %d", n)
	}
	got, _ := s.GetCourses(ctx, []uint64{id})
	c := got[id]
	if c.Name != "Castle" || c.Style != 1 || c.Theme != 2 || c.BodySize != 4321 || c.Rel[protocols.SMM2RelEntire] != 900 || c.Status != 1 {
		t.Fatalf("course %+v", c)
	}
	s.pool.Exec(ctx, "UPDATE smm2.courses SET stars=5, plays=9 WHERE data_id=$1", int64(id))
	id2, _ := s.NewCourse(ctx, protocols.SMM2Course{Owner: 77, Name: "100% Sky_run"})
	s.CompleteCourse(ctx, 77, id2, 1)
	if list, _, _ := s.Search(ctx, protocols.SMM2Query{Text: "100%"}); len(list) != 1 || list[0].DataID != id2 {
		t.Fatalf("text search (like-escaped): %+v", list)
	}
	if list, _, _ := s.Search(ctx, protocols.SMM2Query{Sort: "stars"}); len(list) != 2 || list[0].DataID != id {
		t.Fatalf("stars sort: %+v", list)
	}

	api := &API{Store: s, Key: "k"}
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	get := func(path, key string) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if key != "" {
			req.Header.Set("X-Internal-Key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp, []byte(b.String())
	}
	if resp, _ := get("/v1/courses", ""); resp.StatusCode != 401 {
		t.Fatalf("no key: %d", resp.StatusCode)
	}
	_, body := get("/v1/courses?sort=stars", "k")
	var list struct {
		Total   int
		Courses []courseJSON
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Total != 2 || list.Courses[0].ID != protocols.SMM2CourseCode(id) {
		t.Fatalf("list %s (%v)", body, err)
	}
	if cj := list.Courses[0]; cj.Game != "smm2" || cj.MakerName != "Maker" || cj.MakerID != protocols.SMM2MakerCode(77) ||
		cj.Stars != 5 || cj.Plays != 9 || cj.Style != "Super Mario Bros. 3" || cj.Theme != "Castle" {
		t.Fatalf("course json %+v", cj)
	}
	_, body = get("/v1/courses/"+protocols.SMM2CourseCode(id), "k")
	var one courseJSON
	if err := json.Unmarshal(body, &one); err != nil || one.DataID != id {
		t.Fatalf("get %s", body)
	}
	if resp, _ := get("/v1/courses/"+protocols.SMM2CourseCode(id+5), "k"); resp.StatusCode != 404 {
		t.Fatalf("missing course: %d", resp.StatusCode)
	}
	_, body = get("/v1/courses?maker="+protocols.SMM2MakerCode(77)+"&q=castle", "k")
	if !strings.Contains(string(body), `"total":1`) {
		t.Fatalf("maker+q filter: %s", body)
	}
	_, body = get("/v1/makers/"+protocols.SMM2MakerCode(77), "k")
	var mk makerJSON
	if err := json.Unmarshal(body, &mk); err != nil || mk.Courses != 2 || mk.Stars != 5 || mk.Name != "Maker" {
		t.Fatalf("maker %s", body)
	}
	if resp, _ := get("/v1/courses/"+protocols.SMM2CourseCode(id)+"/thumbnail", "k"); resp.StatusCode != 404 {
		t.Fatalf("thumbnail without blobs: %d", resp.StatusCode)
	}

	// Deleting hides it everywhere.
	if ok, _ := s.DeleteCourse(ctx, 77, id); !ok {
		t.Fatal("delete")
	}
	if resp, _ := get("/v1/courses/"+protocols.SMM2CourseCode(id), "k"); resp.StatusCode != 404 {
		t.Fatalf("deleted course: %d", resp.StatusCode)
	}
}
