package mk8dstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openpak/switch-nex/protocols"
)

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

	s.PutCommonData(ctx, 1, 11, []byte("a"))
	s.PutCommonData(ctx, 2, 22, []byte("b"))
	s.PutScorePack(ctx, 1, 5, []byte("m"))
	s.PutScorePack(ctx, 2, 5, []byte("m"))
	oa, err := s.NewObject(ctx, protocols.MK8DObject{Owner: 1, Name: "ghost", Slot: -1})
	if err != nil {
		t.Fatal(err)
	}
	ob, _ := s.NewObject(ctx, protocols.MK8DObject{Owner: 2, Name: "ghost", Slot: -1})
	if _, err := s.SaveTournament(ctx, 1, 1, []byte("t")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveTournament(ctx, 2, 1, []byte("t")); err != nil {
		t.Fatal(err)
	}

	n, err := s.ErasePIDs(ctx, []uint64{1}, blobs)
	if err != nil || n == 0 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
	if got, _ := s.CommonData(ctx, []uint64{1, 2}); len(got) != 1 || got[2] == nil {
		t.Fatalf("common data %v", got)
	}
	if got, _ := s.ScorePacks(ctx, []uint64{1, 2}, 5); len(got) != 1 || got[2] == nil {
		t.Fatalf("score packs %v", got)
	}
	objs, _ := s.Objects(ctx, []uint64{oa, ob})
	if _, ok := objs[oa]; ok {
		t.Fatal("ghost kept")
	}
	if _, ok := objs[ob]; !ok {
		t.Fatal("other account's ghost lost")
	}
	ts, _ := s.ListTournaments(ctx, 10)
	owners := map[uint64]int{}
	for _, tt := range ts {
		owners[tt.Owner]++
	}
	if owners[1] != 0 || owners[0] != 1 || owners[2] != 1 {
		t.Fatalf("tournament owners %v (the erased one stays with owner 0)", owners)
	}
	mu.Lock()
	if len(deleted) != 1 {
		t.Fatalf("bucket deletes %v", deleted)
	}
	mu.Unlock()
	if n, err := s.ErasePIDs(ctx, []uint64{1}, blobs); err != nil || n != 0 {
		t.Fatalf("second run: rows=%d err=%v", n, err)
	}
}
