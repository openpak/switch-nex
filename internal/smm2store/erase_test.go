package smm2store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openpak/switch-nex/protocols"
)

func TestBlobsDeleteTreatsMissingAsDone(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/b/gone" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		if r.URL.Path == "/b/bad" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	b := NewBlobs(S3Config{Endpoint: srv.URL, Bucket: "b", Access: "a", Secret: "s"})
	if err := b.Delete("here"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete("gone"); err != nil {
		t.Fatalf("a missing object is already erased: %v", err)
	}
	if err := b.Delete("bad"); err == nil {
		t.Fatal("a refused delete must fail so the erasure is retried")
	}
	if len(got) != 3 || got[0] != "DELETE /b/here" {
		t.Fatalf("requests %v", got)
	}
}

func TestErasePIDs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var mu sync.Mutex
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		deleted = append(deleted, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	blobs := NewBlobs(S3Config{Endpoint: srv.URL, Bucket: "b", Access: "a", Secret: "s"})

	s.PutUser(ctx, 77, &protocols.SMM2UserRecord{Name: "A"})
	s.PutUser(ctx, 88, &protocols.SMM2UserRecord{Name: "B"})
	pub, _ := s.NewCourse(ctx, protocols.SMM2Course{Owner: 77, Name: "published"})
	s.CompleteCourse(ctx, 77, pub, 10)
	pend, _ := s.NewCourse(ctx, protocols.SMM2Course{Owner: 77, Name: "pending"})
	s.SetRelation(ctx, 77, pend, protocols.SMM2RelEntire, 5) // no-op for status<>2? the relation is filed on the pending course
	gone, _ := s.NewCourse(ctx, protocols.SMM2Course{Owner: 77, Name: "deleted"})
	s.DeleteCourse(ctx, 77, gone)
	other, _ := s.NewCourse(ctx, protocols.SMM2Course{Owner: 88, Name: "other"})
	s.CompleteCourse(ctx, 88, other, 10)

	n, err := s.ErasePIDs(ctx, []uint64{77}, blobs)
	if err != nil || n == 0 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
	if _, ok, _ := s.GetUser(ctx, 77); ok {
		t.Fatal("profile kept")
	}
	if _, ok, _ := s.GetUser(ctx, 88); !ok {
		t.Fatal("other profile lost")
	}
	got, _ := s.GetCourses(ctx, []uint64{pub, pend, gone, other})
	if c, ok := got[pub]; !ok || c.Owner != 0 {
		t.Fatalf("published course must stay with owner 0: %+v", c)
	}
	if _, ok := got[pend]; ok {
		t.Fatal("pending course kept")
	}
	if _, ok := got[gone]; ok {
		t.Fatal("deleted course kept")
	}
	if c := got[other]; c.Owner != 88 {
		t.Fatal("other account's course touched")
	}
	mu.Lock()
	if len(deleted) < 2 {
		t.Fatalf("bucket bodies not removed: %v", deleted)
	}
	mu.Unlock()
	if n, err := s.ErasePIDs(ctx, []uint64{77}, blobs); err != nil || n != 0 {
		t.Fatalf("second run: rows=%d err=%v", n, err)
	}
}
