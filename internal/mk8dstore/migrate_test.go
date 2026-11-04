package mk8dstore

import (
	"context"
	"os"
	"testing"

	"github.com/openpak/switch-nex/internal/lb"
	"github.com/openpak/switch-nex/internal/lb/lbtest"
	"github.com/openpak/switch-nex/protocols"
)

func coreWith(f *lbtest.Fake, ids *lbtest.IDs) *Scores {
	return &Scores{C: &lb.Client{LB: f, ID: ids}, T: MK8DTitle}
}

func legacyCount(t *testing.T, s *Store, q string) (n int) {
	t.Helper()
	if err := s.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The old tables move into the core without losing a row: legacy rows stay (only marked), a PID with
// no account link waits for its link, and a second pass finds nothing to do.
func TestMigrateLegacyKeepsEveryRow(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fake, ids := lbtest.New(), lbtest.NewIDs()
	s.Core = coreWith(fake, ids)
	for _, p := range []uint64{1, 2, 3} {
		ids.Link(p)
	}
	// pid 4 has no link yet.
	for _, r := range []struct {
		cat, pid, score int64
		mode            int
	}{{5, 1, 90000, 0}, {5, 2, 85000, 0}, {5, 4, 70000, 0}, {6, 3, 60000, 1}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO mk8d.scores (category, pid, unique_id, score, update_mode, groups, param) VALUES ($1,$2,$3,$4,$5,'\x0102',7)`,
			r.cat, r.pid, r.pid+100, r.score, r.mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range [][]int64{{30, 1, 1, 500}, {30, 1, 2, 800}, {30, 2, 1, 900}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO mk8d.competition_scores (competition, season, pid, score, team, team_score, metadata) VALUES ($1,$2,$3,$4,1,10,'\x09')`,
			r[0], r[1], r[2], r[3]); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.MigrateLegacy(ctx)
	if err != nil || st.Scores != 3 || st.Competitions != 3 || st.NoLink != 1 {
		t.Fatalf("first pass %+v %v", st, err)
	}
	// Nothing was dropped.
	if n := legacyCount(t, s, `SELECT count(*) FROM mk8d.scores`); n != 4 {
		t.Fatalf("legacy scores %d", n)
	}
	if n := legacyCount(t, s, `SELECT count(*) FROM mk8d.competition_scores`); n != 3 {
		t.Fatalf("legacy competition rows %d", n)
	}
	rows, total, err := s.Ranking(ctx, protocols.RankQuery{Category: 5, GroupIndex: 0xFF, Length: 10})
	if err != nil || total != 2 || len(rows) != 2 || rows[0].PID != 2 || rows[0].Score != 85000 || rows[0].UniqueID != 102 || rows[0].Param != 7 || len(rows[0].Groups) != 2 {
		t.Fatalf("migrated board %+v total %d %v", rows, total, err)
	}
	seasons, err := s.CompetitionScores(ctx, 30, 1, 20)
	_ = seasons
	if err != nil {
		t.Fatal(err)
	}
	// The second pass moves nothing; the unlinked PID is still waiting.
	if st, err = s.MigrateLegacy(ctx); err != nil || st.Scores != 0 || st.Competitions != 0 || st.NoLink != 1 {
		t.Fatalf("second pass %+v %v", st, err)
	}
	ids.Link(4)
	if st, err = s.MigrateLegacy(ctx); err != nil || st.Scores != 1 || st.NoLink != 0 {
		t.Fatalf("after linking %+v %v", st, err)
	}
	rows, _, _ = s.Ranking(ctx, protocols.RankQuery{Category: 5, GroupIndex: 0xFF, Length: 10})
	if len(rows) != 3 || rows[0].PID != 4 || rows[0].Score != 70000 {
		t.Fatalf("after linking board %+v", rows)
	}
	if n := legacyCount(t, s, `SELECT count(*) FROM mk8d.scores WHERE migrated`); n != 4 {
		t.Fatalf("migrated flags %d", n)
	}
}

// A last-upload-wins board keeps the newer entry the core already holds.
func TestMigrateDoesNotOverwriteNewerEntry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fake, ids := lbtest.New(), lbtest.NewIDs()
	ids.Auto = true
	s.Core = coreWith(fake, ids)
	if _, err := s.UploadScore(ctx, 1, 11, score(7, 50000, 1)); err != nil { // live, overwrite mode
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO mk8d.scores (category, pid, unique_id, score, update_mode) VALUES (7,1,11,99000,1)`); err != nil {
		t.Fatal(err)
	}
	st, err := s.MigrateLegacy(ctx)
	if err != nil || st.Scores != 0 || st.Superseded != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	rows, _, _ := s.Ranking(ctx, protocols.RankQuery{Category: 7, GroupIndex: 0xFF, Length: 5})
	if len(rows) != 1 || rows[0].Score != 50000 {
		t.Fatalf("board %+v", rows)
	}
	if n := legacyCount(t, s, `SELECT count(*) FROM mk8d.scores`); n != 1 {
		t.Fatalf("legacy row dropped: %d", n)
	}
}

// Restart: the server holds no scores. A fresh Store and Scores over the same core and the same
// database serve what the old ones wrote, and the update mode flips the board rule.
func TestRestartKeepsScores(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fake, ids := lbtest.New(), lbtest.NewIDs()
	ids.Auto = true
	s.Core = coreWith(fake, ids)
	for pid, ms := range map[uint64]uint32{1: 80000, 2: 81000, 3: 79000} {
		if _, err := s.UploadScore(ctx, pid, pid+100, score(9, ms, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CompetitionUpload(ctx, 1, protocols.CompetitionUpload{ID: 40, Season: 1, Score: 77}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, mustDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.Core = coreWith(fake, ids) // a new process: empty caches, same core
	rows, total, err := s2.Ranking(ctx, protocols.RankQuery{Category: 9, GroupIndex: 0xFF, Length: 10})
	if err != nil || total != 3 || len(rows) != 3 || rows[0].PID != 3 || rows[2].PID != 2 {
		t.Fatalf("after restart %+v %d %v", rows, total, err)
	}
	if ok, _ := s2.UploadScore(ctx, 3, 103, score(9, 79500, 0)); ok {
		t.Fatal("a slower time replaced the best after a restart")
	}
	seasons, err := s2.CompetitionScores(ctx, 40, 0, 5)
	if err != nil || len(seasons) != 1 || len(seasons[0].Scores) != 1 || seasons[0].Scores[0].Score != 77 {
		t.Fatalf("competition after restart %+v %v", seasons, err)
	}
}

// A PID with no account link is acknowledged and stores nothing; a core outage is an error the
// caller logs, never a panic.
func TestUnlinkedAndCoreDown(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fake, ids := lbtest.New(), lbtest.NewIDs()
	s.Core = coreWith(fake, ids)
	if ok, err := s.UploadScore(ctx, 9, 1, score(1, 1000, 0)); err != nil || ok {
		t.Fatalf("unlinked: %v %v", ok, err)
	}
	ids.Auto = true
	fake.Down = true
	if _, err := s.UploadScore(ctx, 9, 1, score(1, 1000, 0)); err == nil {
		t.Fatal("outage not reported")
	}
	if _, _, err := s.Ranking(ctx, protocols.RankQuery{Category: 1, GroupIndex: 0xFF, Length: 5}); err == nil {
		t.Fatal("outage not reported on read")
	}
}

func mustDSN(t *testing.T) string {
	t.Helper()
	return os.Getenv("MK8D_TEST_DATABASE_URL")
}
