package scorestore

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/openpak/switch-nex/internal/rankingsapi"
)

func stores(t *testing.T) map[string]*Store {
	m := map[string]*Store{"memory": NewMemory()}
	if dsn := os.Getenv("SWITCH_NEX_TEST_DATABASE_URL"); dsn != "" {
		s, err := Open(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		s.be.(*pg).pool.Exec(context.Background(), "DELETE FROM scores.entries WHERE title LIKE 'test-%'")
		t.Cleanup(func() {
			s.be.(*pg).pool.Exec(context.Background(), "DELETE FROM scores.entries WHERE title LIKE 'test-%'")
			s.Close()
		})
		m["postgres"] = s
	}
	return m
}

func TestBestPerBoardHonoursOrder(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			title := "test-" + name
			// desc board: highest kept
			s.Submit(ctx, title, "7", 1, 100, Desc, false)
			s.Submit(ctx, title, "7", 1, 50, Desc, false)
			s.Submit(ctx, title, "7", 2, 300, Desc, false)
			// asc board: lowest kept
			s.Submit(ctx, title, "8", 1, 900, Asc, false)
			s.Submit(ctx, title, "8", 1, 800, Asc, false)
			s.Submit(ctx, title, "8", 1, 950, Asc, false)
			s.Submit(ctx, title, "8", 3, 700, Asc, false)
			// overwrite always wins
			s.Submit(ctx, title, "9", 1, 10, Desc, false)
			s.Submit(ctx, title, "9", 1, 5, Desc, true)

			src := Source{Store: s, Title: title}
			bs, err := src.Boards(ctx)
			if err != nil || len(bs) != 3 || bs[0].ID != "7" || bs[0].Order != "desc" || bs[1].Order != "asc" || bs[0].Name != "Board 7" {
				t.Fatalf("boards %v %v", bs, err)
			}
			p, err := src.Rows(ctx, "7", rankingsapi.Query{Limit: 10, Around: "1"})
			if err != nil || p.Total != 2 || p.Rows[0].PID != "2" || p.Rows[0].Score != 300 || p.Rows[1].Score != 100 || p.Rows[1].Rank != 2 || len(p.Around) != 2 {
				t.Fatalf("desc %+v %v", p, err)
			}
			p, _ = src.Rows(ctx, "8", rankingsapi.Query{Limit: 10})
			if p.Rows[0].PID != "3" || p.Rows[1].Score != 800 {
				t.Fatalf("asc %+v", p)
			}
			p, _ = src.Rows(ctx, "9", rankingsapi.Query{Limit: 10})
			if p.Rows[0].Score != 5 {
				t.Fatalf("overwrite %+v", p)
			}
			if _, err := src.Rows(ctx, "nope", rankingsapi.Query{Limit: 10}); err != rankingsapi.ErrNoBoard {
				t.Fatalf("unknown board: %v", err)
			}
		})
	}
}

func TestAddSumsAndBigPID(t *testing.T) {
	ctx := context.Background()
	const big = 0xaaaaaaaaaaaa0001
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			title := "test-add-" + name
			s.Add(ctx, title, "fest5", big, 10, Desc)
			s.Add(ctx, title, "fest5", big, 15, Desc)
			s.Add(ctx, title, "fest5", 2, 30, Desc)
			src := Source{Store: s, Title: title}
			bs, _ := src.Boards(ctx)
			if len(bs) != 1 || bs[0].Name != "Splatfest 5" {
				t.Fatalf("%v", bs)
			}
			p, _ := src.Rows(ctx, "fest5", rankingsapi.Query{Limit: 10, Around: "12297829382472990721"})
			if p.Rows[0].PID != "2" || p.Rows[1].PID != "12297829382472990721" || p.Rows[1].Score != 25 || len(p.Around) != 2 {
				t.Fatalf("%+v", p)
			}
		})
	}
}

func TestNilStoreIsInert(t *testing.T) {
	var s *Store
	if s.Submit(context.Background(), "t", "1", 1, 1, Desc, false) != nil || s.Add(context.Background(), "t", "1", 1, 1, Desc) != nil || s.Durable() {
		t.Fatal("nil store")
	}
	s.Close()
}

func TestDefaultNames(t *testing.T) {
	for in, want := range map[string]string{"3": "Board 3", "r2-4": "Board 4 (set 2)", "fest12": "Splatfest 12"} {
		if got := DefaultName(in); got != want {
			t.Fatalf("%s: %s", in, got)
		}
	}
}

func TestContractOverHTTP(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	s.Submit(ctx, "t", "26", 11, 1037, Desc, false)
	s.Submit(ctx, "t", "26", 12, 900, Desc, false)
	srv := httptest.NewServer(rankingsapi.Handler("switch", Source{Store: s, Title: "t"}))
	defer srv.Close()
	c := rankingsapi.NewClient(srv.URL)
	l, err := c.Boards(ctx)
	if err != nil || l.Network != "switch" || len(l.Boards) != 1 || l.Boards[0].Name != "Board 26" || l.Boards[0].Order != "desc" {
		t.Fatalf("%+v %v", l, err)
	}
	p, err := c.Rows(ctx, "26", rankingsapi.Query{Limit: 25, Around: "12"})
	if err != nil || p.Total != 2 || p.Rows[0].PID != "11" || p.Rows[0].Display != "1,037" || len(p.Around) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := c.Rows(ctx, "99", rankingsapi.Query{Limit: 1}); err != rankingsapi.ErrNoBoard {
		t.Fatalf("%v", err)
	}
}

func TestErasePIDs(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			title := "test-erase-" + name
			s.Submit(ctx, title, "7", 0xaaaa0001, 100, Desc, false)
			s.Submit(ctx, title, "7", 0xbbbb0001, 200, Desc, false)
			s.Submit(ctx, title+"-2", "9", 0xaaaa0001, 5, Asc, false)
			s.PutBlob(ctx, title, "r2board", "12/aaaa0001", []byte("x"))
			s.PutBlob(ctx, title, "r2board", "12/bbbb0001", []byte("x"))
			s.PutBlob(ctx, title, "r2common", "aaaa0001", []byte("x"))
			s.PutBlob(ctx, title, "rkcommon", "aaaa0001", []byte("x"))
			s.PutBlob(ctx, title, "festvote", "3/aaaa0001", []byte("x"))
			s.PutBlob(ctx, title, "festsum", "3", []byte("x"))
			s.PutBlob(ctx, title, "recall", "seen", []byte("x"))
			n, err := s.ErasePIDs(ctx, []uint64{0xaaaa0001})
			if err != nil || n != 6 {
				t.Fatalf("rows=%d err=%v (want 2 scores + 4 blobs)", n, err)
			}
			if rows, _ := s.be.list(ctx, title, "7"); len(rows) != 1 || rows[0].pid != 0xbbbb0001 {
				t.Fatalf("scores %v", rows)
			}
			if m, _ := s.Blobs(ctx, title, "r2board"); len(m) != 1 {
				t.Fatalf("r2board %v", m)
			}
			if m, _ := s.Blobs(ctx, title, "festsum"); len(m) != 1 {
				t.Fatal("tally must stay")
			}
			if m, _ := s.Blobs(ctx, title, "recall"); len(m) != 1 {
				t.Fatal("the recall table belongs to its process")
			}
			if n, err := s.ErasePIDs(ctx, []uint64{0xaaaa0001}); err != nil || n != 0 {
				t.Fatalf("second run: %d %v", n, err)
			}
			// A PID above 2^63 survives the bigint wrap.
			s.Submit(ctx, title, "8", 0xaaaaaaaaaaaa0001, 1, Desc, false)
			if n, err := s.ErasePIDs(ctx, []uint64{0xaaaaaaaaaaaa0001}); err != nil || n != 1 {
				t.Fatalf("high pid: %d %v", n, err)
			}
		})
	}
}
