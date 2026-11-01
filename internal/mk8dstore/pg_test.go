package mk8dstore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openpak/switch-nex/protocols"
	"github.com/openpak/switch-nex/rmc"
)

// These run against a real Postgres: set MK8D_TEST_DATABASE_URL to a scratch database.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("MK8D_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MK8D_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DROP SCHEMA mk8d CASCADE")
		s.Close()
	})
	s.pool.Exec(ctx, "TRUNCATE mk8d.scores, mk8d.common_data, mk8d.score_packs, mk8d.tournaments, mk8d.competition_scores, mk8d.objects")
	return s
}

func score(cat, ms uint32, mode uint8) protocols.RankingScore {
	return protocols.RankingScore{Category: cat, Score: ms, UpdateMode: mode, Groups: []byte{1, 2}}
}

func TestUpdateSemantics(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if ok, err := s.UploadScore(ctx, 1, 11, score(5, 90000, 0)); err != nil || !ok {
		t.Fatalf("first upload: %v %v", ok, err)
	}
	if ok, _ := s.UploadScore(ctx, 1, 11, score(5, 95000, 0)); ok {
		t.Fatal("a slower time replaced the best in best mode")
	}
	if ok, _ := s.UploadScore(ctx, 1, 11, score(5, 88000, 0)); !ok {
		t.Fatal("a faster time was not stored")
	}
	if ok, _ := s.UploadScore(ctx, 1, 11, score(5, 88000, 0)); ok {
		t.Fatal("an equal time counted as an improvement")
	}
	rows, _, _ := s.Ranking(ctx, protocols.RankQuery{Category: 5, GroupIndex: 0xFF, Length: 5})
	if len(rows) != 1 || rows[0].Score != 88000 {
		t.Fatalf("board %+v", rows)
	}
	// update mode 1 always overwrites, even with a worse score
	if ok, _ := s.UploadScore(ctx, 1, 11, score(5, 99000, 1)); !ok {
		t.Fatal("overwrite mode did not store")
	}
	rows, _, _ = s.Ranking(ctx, protocols.RankQuery{Category: 5, GroupIndex: 0xFF, Length: 5})
	if rows[0].Score != 99000 {
		t.Fatalf("overwrite: %d", rows[0].Score)
	}
	// categories are independent
	s.UploadScore(ctx, 1, 11, score(6, 70000, 0))
	if rows, _, _ := s.Ranking(ctx, protocols.RankQuery{Category: 6, GroupIndex: 0xFF, Length: 5}); len(rows) != 1 || rows[0].Score != 70000 {
		t.Fatalf("category 6: %+v", rows)
	}
}

func seedBoard(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, p := range []struct {
		pid uint64
		ms  uint32
		grp byte
	}{{1, 80000, 1}, {2, 81000, 2}, {3, 81000, 1}, {4, 82000, 2}, {5, 90000, 1}} {
		sc := score(9, p.ms, 0)
		sc.Groups = []byte{p.grp}
		if _, err := s.UploadScore(ctx, p.pid, p.pid+100, sc); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.PutCommonData(ctx, 2, 102, []byte("common-2"))
}

func pids(rows []protocols.RankRow) []uint64 {
	var o []uint64
	for _, r := range rows {
		o = append(o, r.PID)
	}
	return o
}

func TestRankingModes(t *testing.T) {
	s := testStore(t)
	seedBoard(t, s)
	ctx := context.Background()
	q := protocols.RankQuery{Category: 9, GroupIndex: 0xFF, Length: 10}

	rows, total, err := s.Ranking(ctx, q)
	if err != nil || total != 5 || len(rows) != 5 {
		t.Fatalf("global: %v %d %d", err, total, len(rows))
	}
	if got := pids(rows); got[0] != 1 || got[1] != 2 || got[2] != 3 || got[4] != 5 {
		t.Fatalf("order %v (ties break by who got there first)", got)
	}
	// standard ranking (1224): the tie shares rank 2 and the next is 4
	if rows[1].Order != 2 || rows[2].Order != 2 || rows[3].Order != 4 {
		t.Fatalf("standard ranks: %d %d %d", rows[1].Order, rows[2].Order, rows[3].Order)
	}
	if string(rows[1].CommonData) != "common-2" || rows[1].UniqueID != 102 || rows[0].Groups[0] != 1 {
		t.Fatalf("row 1 %+v", rows[1])
	}
	// ordinal ranking (1234)
	ord := q
	ord.Calc = 1
	rows, _, _ = s.Ranking(ctx, ord)
	if rows[2].Order != 3 || rows[3].Order != 4 {
		t.Fatalf("ordinal ranks: %d %d", rows[2].Order, rows[3].Order)
	}
	// window
	w := q
	w.Offset, w.Length = 1, 2
	rows, total, _ = s.Ranking(ctx, w)
	if total != 5 || len(rows) != 2 || rows[0].PID != 2 {
		t.Fatalf("offset window: %v total %d", pids(rows), total)
	}
	// around own entry: a window of three centred on player 4
	a := q
	a.Mode, a.Self, a.Length = protocols.RankModeAround, 4, 3
	rows, _, _ = s.Ranking(ctx, a)
	if got := pids(rows); len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("around: %v", got)
	}
	a.Length = 1
	if rows, _, _ = s.Ranking(ctx, a); len(rows) != 1 || rows[0].PID != 4 || rows[0].Order != 4 {
		t.Fatalf("around window of one: %+v", rows)
	}
	a.Self = 99
	if rows, _, _ = s.Ranking(ctx, a); len(rows) != 0 {
		t.Fatal("a player without a score got a window")
	}
	// own entry only: global rank, not rank among one
	self := q
	self.Mode, self.Self = protocols.RankModeSelf, 5
	if rows, _, _ = s.Ranking(ctx, self); len(rows) != 1 || rows[0].PID != 5 || rows[0].Order != 5 {
		t.Fatalf("self: %+v", rows)
	}
	// by PID list keeps global ranks
	bp := q
	bp.PIDs = []uint64{4, 1, 77}
	rows, _, _ = s.Ranking(ctx, bp)
	if got := pids(rows); len(got) != 2 || got[0] != 1 || got[1] != 4 || rows[1].Order != 4 {
		t.Fatalf("by pid: %v %+v", got, rows)
	}
	// by unique id
	bu := q
	bu.UniqueIDs = []uint64{105}
	if rows, _, _ = s.Ranking(ctx, bu); len(rows) != 1 || rows[0].PID != 5 {
		t.Fatalf("by unique id: %+v", rows)
	}
	// group filter: group index 0 equal to 2 keeps players 2 and 4
	g := q
	g.GroupIndex, g.GroupNum = 0, 2
	rows, total, _ = s.Ranking(ctx, g)
	if got := pids(rows); total != 2 || len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Fatalf("group: %v total %d", got, total)
	}
	// the top-1000 ceiling
	hi := q
	hi.Offset = 5000
	if rows, _, _ = s.Ranking(ctx, hi); len(rows) != 0 {
		t.Fatal("offset past the ceiling returned rows")
	}
}

func TestCommonDataAndScorePacks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.PutCommonData(ctx, 7, 70, []byte("a"))
	s.PutCommonData(ctx, 7, 70, []byte("b"))
	got, _ := s.CommonData(ctx, []uint64{7, 8})
	if string(got[7]) != "b" || len(got) != 1 {
		t.Fatalf("%v", got)
	}
	s.PutScorePack(ctx, 7, 3, []byte("pk"))
	pk, _ := s.ScorePacks(ctx, []uint64{7, 8}, 3)
	if string(pk[7]) != "pk" || len(pk) != 1 {
		t.Fatalf("%v", pk)
	}
	if pk, _ := s.ScorePacks(ctx, []uint64{7}, 4); len(pk) != 0 {
		t.Fatal("score pack leaked across categories")
	}
}

func TestCompetitions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	up := func(pid uint64, season, score uint32) {
		if err := s.CompetitionUpload(ctx, pid, protocols.CompetitionUpload{ID: 21, Season: season, Score: score, TeamID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	up(1, 1, 100)
	up(2, 1, 300)
	up(1, 1, 200) // the console sends its running total: last upload wins
	up(3, 2, 50)
	seasons, err := s.CompetitionScores(ctx, 21, 1, 20)
	if err != nil || len(seasons) != 2 {
		t.Fatalf("seasons %v %v", seasons, err)
	}
	s1 := seasons[0]
	if s1.Season != 1 || s1.Participants != 2 || s1.Scores[0].PID != 2 || s1.Scores[0].Score != 300 || s1.Scores[1].Score != 200 || s1.Scores[1].Rank != 2 {
		t.Fatalf("season 1 %+v", s1)
	}
	if seasons[1].Season != 2 || len(seasons[1].Scores) != 1 {
		t.Fatalf("season 2 %+v", seasons[1])
	}
	if only, _ := s.CompetitionScores(ctx, 21, 0, 20); len(only) != 1 || only[0].Season != 2 {
		t.Fatalf("current season only: %+v", only)
	}
	if none, _ := s.CompetitionScores(ctx, 999, 1, 20); len(none) != 0 {
		t.Fatal("unknown competition has seasons")
	}
	up2 := protocols.CompetitionUpload{ID: 22, Season: 1, Score: 1}
	s.CompetitionUpload(ctx, 9, up2)
	list, _ := s.CompetitionList(ctx, 0, 10)
	if len(list) != 2 || list[0].ID != 21 || list[0].Participants != 3 {
		t.Fatalf("list %+v", list)
	}
}

func TestTournamentsDescriptorAndOwnership(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	w := rmc.NewWriter()
	w.U32(0)
	w.U64(5)
	w.List(2)
	w.U32(1)
	w.U32(2)
	w.QBuffer(nil)
	w.U32(7)
	w.String("123456789012")
	w.StructOf(0, func(w *rmc.Writer) {
		for range 4 {
			w.U32(0)
		}
		w.U64(uint64(2026)<<26 | 10<<22 | 3<<17)
		w.U64(0)
	})
	id, err := s.SaveTournament(ctx, 5, 1, w.Bytes())
	if err != nil || id == 0 {
		t.Fatal(id, err)
	}
	list, _ := s.ListTournaments(ctx, 10)
	if len(list) != 1 || list[0].Blob[0] != byte(id) {
		t.Fatalf("list %+v (the assigned id must land in the descriptor)", list)
	}
	d := ParseDescriptor(list[0].Blob)
	if d.Code != "123456789012" || len(d.Attributes) != 2 || d.Start == nil || d.Start.Year() != 2026 || d.End != nil {
		t.Fatalf("descriptor %+v", d)
	}
	if ok, _ := s.DeleteTournament(ctx, id, 6); ok {
		t.Fatal("non-owner deleted")
	}
	if ok, _ := s.DeleteTournament(ctx, id, 5); !ok {
		t.Fatal("owner delete failed")
	}
	if list, _ := s.ListTournaments(ctx, 10); len(list) != 0 {
		t.Fatal("deleted tournament listed")
	}
}

func TestObjectsGhostLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id1, err := s.NewObject(ctx, protocols.MK8DObject{Owner: 7, Name: "g1", DataType: 3, Slot: 2, Tags: []string{"a"}, Raw: []byte{1}})
	if err != nil || id1 < 1000000 {
		t.Fatal(id1, err)
	}
	if got, total, _ := s.SearchObjects(ctx, protocols.MK8DSearch{Limit: 10}); total != 0 || len(got) != 0 {
		t.Fatal("a pending object is listed")
	}
	if ok, _ := s.CompleteObject(ctx, 8, id1, 10); ok {
		t.Fatal("a stranger completed an object")
	}
	if ok, _ := s.CompleteObject(ctx, 7, id1, 4321); !ok {
		t.Fatal("complete failed")
	}
	id2, _ := s.NewObject(ctx, protocols.MK8DObject{Owner: 7, Name: "g2", DataType: 3, Slot: 2})
	s.CompleteObject(ctx, 7, id2, 5)
	// the same slot keeps only the newest object
	if got, _ := s.Objects(ctx, []uint64{id1}); len(got) != 0 {
		t.Fatal("the replaced slot object is still live")
	}
	o, ok, _ := s.SlotObject(ctx, 7, 2)
	if !ok || o.DataID != id2 {
		t.Fatalf("slot object %+v", o)
	}
	other, _ := s.NewObject(ctx, protocols.MK8DObject{Owner: 9, Name: "g3", DataType: 4, Slot: -1, Tags: []string{"x", "y"}})
	s.CompleteObject(ctx, 9, other, 1)
	if got, total, _ := s.SearchObjects(ctx, protocols.MK8DSearch{DataTypes: []uint16{4}, Limit: 10}); total != 1 || got[0].DataID != other {
		t.Fatalf("by type: %+v", got)
	}
	if got, _, _ := s.SearchObjects(ctx, protocols.MK8DSearch{Tags: []string{"y"}, Limit: 10}); len(got) != 1 {
		t.Fatalf("by tag: %+v", got)
	}
	if got, _, _ := s.SearchObjects(ctx, protocols.MK8DSearch{Owners: []uint64{7}, Limit: 10}); len(got) != 1 || got[0].DataID != id2 {
		t.Fatalf("by owner: %+v", got)
	}
	if ok, _ := s.DeleteObject(ctx, 9, id2); ok {
		t.Fatal("stranger deleted")
	}
	if ok, _ := s.DeleteObject(ctx, 7, id2); !ok {
		t.Fatal("owner delete failed")
	}
	if _, ok, _ := s.SlotObject(ctx, 7, 2); ok {
		t.Fatal("deleted object still in its slot")
	}
}

func TestReadAPI(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedBoard(t, s)
	s.UploadScore(ctx, 1, 101, score(10, 70000, 0))
	gid, _ := s.NewObject(ctx, protocols.MK8DObject{Owner: 1, Name: "ghost", DataType: 9, Slot: 1, Meta: []byte{0xAB}})
	s.CompleteObject(ctx, 1, gid, 12)
	hidden, _ := s.NewObject(ctx, protocols.MK8DObject{Owner: 2, Name: "private", DataType: 9, Slot: 1, Permission: 3})
	s.CompleteObject(ctx, 2, hidden, 12)
	s.CompetitionUpload(ctx, 1, protocols.CompetitionUpload{ID: 3, Season: 1, Score: 500, TeamID: 0})
	s.CompetitionUpload(ctx, 2, protocols.CompetitionUpload{ID: 3, Season: 1, Score: 700, TeamID: 1})

	api := &API{Store: s}
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	get := func(path string) (int, []byte) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	var pg struct {
		Total, Offset, Limit int
		Items                []map[string]any
	}
	code, body := get("/api/v1/courses/9/rankings?limit=2")
	if code != 200 || json.Unmarshal(body, &pg) != nil || pg.Total != 5 || pg.Limit != 2 || len(pg.Items) != 2 {
		t.Fatalf("rankings %d %s", code, body)
	}
	if it := pg.Items[0]; it["player"] != "1" || it["time_ms"].(float64) != 80000 || it["time"] != "1:20.000" || it["rank"].(float64) != 1 || it["ghost"].(float64) != float64(gid) {
		t.Fatalf("first ranking %v", it)
	}
	if pg.Items[1]["ghost"] != nil {
		t.Fatalf("a private ghost is linked: %v", pg.Items[1])
	}
	code, body = get("/api/v1/courses/9/rankings?limit=2&offset=4")
	json.Unmarshal(body, &pg)
	if len(pg.Items) != 1 || pg.Items[0]["rank"].(float64) != 5 {
		t.Fatal("offset page")
	}
	if code, _ = get("/api/v1/courses/x/rankings"); code != 400 {
		t.Fatalf("bad course: %d", code)
	}
	if code, _ = get("/api/v1/courses/9/rankings?limit=-1"); code != 400 {
		t.Fatalf("bad limit: %d", code)
	}
	if code, body = get("/api/v1/courses/777/rankings"); code != 200 || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("empty course: %d %s", code, body)
	}

	_, body = get("/api/v1/courses")
	var cs struct{ Items []course }
	json.Unmarshal(body, &cs)
	if len(cs.Items) != 2 || cs.Items[0].Course != 9 || cs.Items[0].Ranked != 5 || cs.Items[0].Ghosts != 1 || cs.Items[1].Course != 10 {
		t.Fatalf("courses %s", body)
	}

	_, body = get("/api/v1/players/1/rankings")
	var pr struct{ Items []playerRank }
	json.Unmarshal(body, &pr)
	if len(pr.Items) != 2 || pr.Items[0].Course != 9 || pr.Items[0].Rank != 1 || pr.Items[0].Of != 5 || pr.Items[0].Time != "1:20.000" {
		t.Fatalf("player rankings %s", body)
	}
	if code, _ = get("/api/v1/players/abc/rankings"); code != 400 {
		t.Fatalf("bad pid: %d", code)
	}
	_, body = get("/api/v1/players/4242/rankings")
	if !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("unknown player: %s", body)
	}

	_, body = get("/api/v1/courses/9/ghosts")
	var gh struct {
		Total int
		Items []Object
	}
	json.Unmarshal(body, &gh)
	if gh.Total != 1 || len(gh.Items) != 1 || gh.Items[0].ID != gid || *gh.Items[0].Course != 9 || *gh.Items[0].TimeMS != 80000 || gh.Items[0].MetaHex != "ab" || gh.Items[0].Owner != "1" {
		t.Fatalf("ghosts %s", body)
	}
	if code, _ = get("/api/v1/objects/" + itoa(hidden)); code != 404 {
		t.Fatalf("private object: %d", code)
	}
	if code, _ = get("/api/v1/objects/" + itoa(gid)); code != 200 {
		t.Fatalf("object: %d", code)
	}
	if code, _ = get("/api/v1/objects/" + itoa(gid) + "/body"); code != 501 {
		t.Fatalf("body without blobs: %d", code)
	}

	_, body = get("/api/v1/tournaments")
	var tl struct {
		Total int
		Items []tournament
	}
	json.Unmarshal(body, &tl)
	if tl.Total != 1 || tl.Items[0].ID != 3 || tl.Items[0].Participants != 2 {
		t.Fatalf("tournaments %s", body)
	}
	_, body = get("/api/v1/tournaments/3")
	var one tournament
	json.Unmarshal(body, &one)
	if len(one.Scores) != 2 || one.Scores[0].Player != "2" || one.Scores[0].Score != 700 || one.Scores[0].Rank != 1 || one.Season != 1 {
		t.Fatalf("tournament %s", body)
	}
	if code, _ = get("/api/v1/tournaments/99"); code != 404 {
		t.Fatalf("missing tournament: %d", code)
	}
	if code, _ = get("/api/v1/tournaments/3?season=0"); code != 400 {
		t.Fatalf("bad season: %d", code)
	}
	if _, body = get("/api/v1/tournaments/3?season=2"); strings.Contains(string(body), `"rank"`) || !strings.Contains(string(body), `"season":2`) {
		t.Fatalf("empty season: %s", body)
	}
}

func itoa(v uint64) string { return strconv.FormatUint(v, 10) }

func TestFormatTime(t *testing.T) {
	for in, want := range map[int64]string{0: "0:00.000", 83210: "1:23.210", 600001: "10:00.001", -5: "0:00.000"} {
		if got := FormatTime(in); got != want {
			t.Errorf("%d: %q want %q", in, got, want)
		}
	}
}
