package mk8dstore

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/openpak/switch-nex/internal/rankingsapi"
	"github.com/openpak/switch-nex/protocols"
)

func TestTournamentID(t *testing.T) {
	if id := tournamentID(12, 3); id != "t12s3" {
		t.Fatal(id)
	}
	if c, s, ok := parseTournamentID("t12s3"); !ok || c != 12 || s != 3 {
		t.Fatal(c, s, ok)
	}
	for _, bad := range []string{"12", "t12", "ts3", "x1s2", "t1sx"} {
		if _, _, ok := parseTournamentID(bad); ok {
			t.Fatalf("%q parsed", bad)
		}
	}
}

func TestRankingsSource(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i, ms := range []uint32{90000, 80000, 85000} {
		if _, err := s.UploadScore(ctx, uint64(i+1), 0, score(5, ms, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UploadScore(ctx, 9, 0, score(7, 70000, 0)); err != nil {
		t.Fatal(err)
	}
	src := Rankings{Store: s}
	srv := httptest.NewServer(rankingsapi.Handler("switch", src))
	defer srv.Close()
	c := rankingsapi.NewClient(srv.URL)

	l, err := c.Boards(ctx)
	if err != nil || l.Network != "switch" || len(l.Boards) != 2 || l.Boards[0].ID != "5" ||
		l.Boards[0].Kind != "time" || l.Boards[0].Order != "asc" {
		t.Fatalf("boards %+v %v", l, err)
	}
	p, err := c.Rows(ctx, "5", rankingsapi.Query{Limit: 2, Around: "1"})
	if err != nil || p.Total != 3 || len(p.Rows) != 2 {
		t.Fatalf("rows %+v %v", p, err)
	}
	if p.Rows[0].PID != "2" || p.Rows[0].Rank != 1 || p.Rows[0].Score != 80000 || p.Rows[0].Display != "1:20.000" || p.Rows[1].PID != "3" {
		t.Fatalf("order %+v", p.Rows)
	}
	if len(p.Around) != 3 || p.Around[2].PID != "1" || p.Around[2].Rank != 3 {
		t.Fatalf("around %+v", p.Around)
	}
	if p, _ := c.Rows(ctx, "5", rankingsapi.Query{Limit: 25, Offset: 2}); len(p.Rows) != 1 || p.Rows[0].PID != "1" {
		t.Fatalf("offset %+v", p)
	}
	if _, err := c.Rows(ctx, "99", rankingsapi.Query{Limit: 5}); err != rankingsapi.ErrNoBoard {
		t.Fatalf("missing board: %v", err)
	}

	for pid, sc := range map[uint64]uint32{1: 500, 2: 900} {
		if err := s.CompetitionUpload(ctx, pid, competitionFor(4, 1, sc)); err != nil {
			t.Fatal(err)
		}
	}
	p, err = c.Rows(ctx, "t4s1", rankingsapi.Query{Limit: 5})
	if err != nil || p.Total != 2 || p.Rows[0].PID != "2" || p.Rows[0].Display != "900" {
		t.Fatalf("tournament %+v %v", p, err)
	}
	raw, _ := json.Marshal(p.Rows[0])
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, k := range []string{"name", "nickname", "avatar", "account_id"} {
		if _, ok := m[k]; ok {
			t.Fatalf("row carries %s", k)
		}
	}
}

func competitionFor(id, season, sc uint32) protocols.CompetitionUpload {
	return protocols.CompetitionUpload{ID: id, Season: season, Score: sc}
}
