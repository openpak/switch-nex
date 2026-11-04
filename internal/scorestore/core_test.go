package scorestore

import (
	"context"
	"os"
	"testing"

	"github.com/openpak/switch-nex/internal/lb"
	"github.com/openpak/switch-nex/internal/lb/lbtest"
)

func pgStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("SWITCH_NEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SWITCH_NEX_TEST_DATABASE_URL not set")
	}
	s, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	clean := func() {
		s.be.(*pg).pool.Exec(context.Background(), "DELETE FROM scores.entries WHERE title LIKE 'test-%'")
	}
	clean()
	t.Cleanup(func() { clean(); s.Close() })
	return s
}

func newCore(f *lbtest.Fake, ids *lbtest.IDs) *Core { return &Core{C: &lb.Client{LB: f, ID: ids}} }

func localCount(t *testing.T, s *Store, title string) (n, migrated int) {
	t.Helper()
	err := s.be.(*pg).pool.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE migrated) FROM scores.entries WHERE title=$1`, title).Scan(&n, &migrated)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// Linked players go straight to the core; an unlinked player's row stays local and is imported once
// the link exists. Nothing is deleted locally, and a second import finds nothing.
func TestCoreRoutingAndImport(t *testing.T) {
	s := pgStore(t)
	ctx := context.Background()
	title := "test-core"
	f, ids := lbtest.New(), lbtest.NewIDs()
	ids.Link(1)
	ids.Link(2)
	s.UseCore(newCore(f, ids))

	s.SubmitFull(ctx, title, "r2-5", 1, 100, Desc, false, 11, 7)
	s.SubmitFull(ctx, title, "r2-5", 2, 300, Desc, false, 12, 8)
	s.SubmitFull(ctx, title, "r2-5", 3, 200, Desc, false, 13, 9) // no link yet
	s.Add(ctx, title, "fest1", 1, 40, Desc)
	s.Add(ctx, title, "fest1", 1, 2, Desc)
	s.Add(ctx, title, "fest1", 3, 5, Desc) // no link yet

	if n, _ := localCount(t, s, title); n != 2 {
		t.Fatalf("local rows %d, want the two unlinked ones", n)
	}
	rows, total, err := s.Page(ctx, title, "r2-5", 0, 10)
	if err != nil || total != 2 || len(rows) != 2 || rows[0].PID != 2 || rows[0].UID != 12 || rows[0].Misc != 8 {
		t.Fatalf("core page %+v %d %v", rows, total, err)
	}
	if pos, sc, tot, err := s.PositionOf(ctx, title, "r2-5", 1); err != nil || pos != 2 || sc != 100 || tot != 2 {
		t.Fatalf("position %d %d %d %v", pos, sc, tot, err)
	}
	if fr, _, _ := s.Page(ctx, title, "fest1", 0, 10); len(fr) != 1 || fr[0].Score != 42 {
		t.Fatalf("fest sum %+v", fr)
	}

	st, err := s.Import(ctx, title)
	if err != nil || st.Imported != 0 || st.Waiting != 2 {
		t.Fatalf("import before link %+v %v", st, err)
	}
	ids.Link(3)
	if st, err = s.Import(ctx, title); err != nil || st.Imported != 2 || st.Waiting != 0 {
		t.Fatalf("import after link %+v %v", st, err)
	}
	if n, m := localCount(t, s, title); n != 2 || m != 2 {
		t.Fatalf("local rows %d migrated %d: nothing may be dropped", n, m)
	}
	rows, total, _ = s.Page(ctx, title, "r2-5", 0, 10)
	if total != 3 || rows[1].PID != 3 || rows[1].Score != 200 || rows[1].UID != 13 {
		t.Fatalf("after import %+v %d", rows, total)
	}
	if fr, _, _ := s.Page(ctx, title, "fest1", 0, 10); len(fr) != 2 {
		t.Fatalf("fest after import %+v", fr)
	}
	if st, _ = s.Import(ctx, title); st.Imported != 0 {
		t.Fatalf("second import %+v", st)
	}
}

// A core outage keeps the score locally, and a restart (a new Store over the same database and the
// same core) serves the old scores and finishes the import.
func TestCoreOutageAndRestart(t *testing.T) {
	s := pgStore(t)
	ctx := context.Background()
	title := "test-outage"
	f, ids := lbtest.New(), lbtest.NewIDs()
	ids.Auto = true
	s.UseCore(newCore(f, ids))
	s.Submit(ctx, title, "7", 1, 50, Desc, false)
	f.Down = true
	if err := s.Submit(ctx, title, "7", 2, 70, Desc, false); err != nil {
		t.Fatalf("an outage must not lose the score: %v", err)
	}
	if n, _ := localCount(t, s, title); n != 1 {
		t.Fatalf("outage row kept locally: %d", n)
	}
	f.Down = false

	s2, err := Open(ctx, os.Getenv("SWITCH_NEX_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.UseCore(newCore(f, ids))
	if st, err := s2.Import(ctx, title); err != nil || st.Imported != 1 {
		t.Fatalf("import after restart %+v %v", st, err)
	}
	rows, total, err := s2.Page(ctx, title, "7", 0, 10)
	if err != nil || total != 2 || rows[0].PID != 2 || rows[1].PID != 1 {
		t.Fatalf("board after restart %+v %d %v", rows, total, err)
	}
}

// Rows already in the old table (before the core existed) are pending and move on the first import.
func TestLegacyRowsMigrate(t *testing.T) {
	s := pgStore(t)
	ctx := context.Background()
	title := "test-legacy"
	s.Submit(ctx, title, "3", 1, 900, Asc, false) // written with no core
	s.Submit(ctx, title, "3", 2, 800, Asc, false)
	f, ids := lbtest.New(), lbtest.NewIDs()
	ids.Auto = true
	s.UseCore(newCore(f, ids))
	st, err := s.Import(ctx, title)
	if err != nil || st.Imported != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	rows, total, _ := s.Page(ctx, title, "3", 0, 10)
	if total != 2 || rows[0].PID != 2 || rows[0].Score != 800 {
		t.Fatalf("%+v", rows)
	}
	if n, m := localCount(t, s, title); n != 2 || m != 2 {
		t.Fatalf("local %d migrated %d", n, m)
	}
}
