package protocols

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	emit "github.com/openpak/rewards-emit"
	"github.com/openpak/switch-nex/internal/lb"
	"github.com/openpak/switch-nex/internal/lb/lbtest"
	"github.com/openpak/switch-nex/internal/rankingsapi"
	"github.com/openpak/switch-nex/internal/scorestore"
	"github.com/openpak/switch-nex/rmc"
)

func scoredEnv(name string) *Env {
	e := NewEnv(name, "127.0.0.1", 22000)
	e.Ranking, e.Ranking2 = true, true
	e.Scores = scorestore.NewMemory()
	return e
}

func boardRows(t *testing.T, e *Env, board string) rankingsapi.Page {
	t.Helper()
	p, err := scorestore.Source{Store: e.Scores, Title: e.Name}.Rows(context.Background(), board, rankingsapi.Query{Limit: 10})
	if err != nil {
		t.Fatalf("board %s: %v", board, err)
	}
	return p
}

func TestGenericUploadScoreIsPersisted(t *testing.T) {
	e := scoredEnv("mario-golf-super-rush")
	up := func(pid uint64, cat, score uint32, order, mode uint8) {
		w := rmc.NewWriter()
		scoreData(w, cat, score, order, mode, []byte{1}, 0)
		w.U64(5)
		rankCall(t, e, pid, methodRankingUploadScore, w.Bytes())
	}
	up(1, 26, 1000, 1, 0)
	up(1, 26, 900, 1, 0) // worse in a descending category: kept out
	up(2, 26, 1037, 1, 0)
	up(1, 3, 90000, 0, 0) // ascending category
	up(1, 3, 95000, 0, 0)
	up(1, 4, 10, 1, 0)
	up(1, 4, 5, 1, 1) // update mode 1 overwrites
	up(9, 8, 1, 7, 0) // order flag out of range: not a score we read
	p := boardRows(t, e, "26")
	if p.Total != 2 || p.Rows[0].PID != "2" || p.Rows[0].Score != 1037 || p.Rows[1].Score != 1000 {
		t.Fatalf("board 26: %+v", p)
	}
	if p := boardRows(t, e, "3"); p.Rows[0].Score != 90000 {
		t.Fatalf("board 3: %+v", p)
	}
	if p := boardRows(t, e, "4"); p.Rows[0].Score != 5 {
		t.Fatalf("board 4: %+v", p)
	}
	if _, err := (scorestore.Source{Store: e.Scores, Title: e.Name}).Rows(context.Background(), "8", rankingsapi.Query{Limit: 1}); err == nil {
		t.Fatal("a body with an unknown order flag was stored")
	}
	// the answer the game gets is unchanged
	w := rmc.NewWriter()
	scoreData(w, 26, 1, 1, 0, nil, 0)
	w.U64(0)
	if resp := rankCall(t, e, 3, methodRankingUploadScore, w.Bytes()); len(resp) != 1 || resp[0] != 1 {
		t.Fatalf("answer %x", resp)
	}
}

func TestScorePackUploadPersistsScore(t *testing.T) {
	e := scoredEnv("clubhouse-games-51")
	w := rmc.NewWriter()
	scoreData(w, 12, 777, 1, 0, nil, 0)
	w.QBuffer([]byte{1, 2})
	if resp := rankCall(t, e, 4, methodRankingSubmitTime, w.Bytes()); len(resp) != 1 || resp[0] != 0 {
		t.Fatalf("answer %x", resp)
	}
	if p := boardRows(t, e, "12"); p.Total != 1 || p.Rows[0].Score != 777 || p.Rows[0].PID != "4" {
		t.Fatalf("%+v", p)
	}
}

func TestRanking2PutScoreIsPersisted(t *testing.T) {
	e := scoredEnv("super-mario-bros-35")
	put := func(pid uint64, cat, score uint32) {
		w := rmc.NewWriter()
		w.List(1)
		w.StructOf(0, func(w *rmc.Writer) { w.U32(cat); w.U32(score); w.U64(0) })
		w.U64(1)
		ok, _, errCode := decodeResponse(Dispatch(e, conn(pid), request(ProtoRanking2, 0x01, w.Bytes())))
		if errCode != 0 || !ok {
			t.Fatalf("put: %#x", errCode)
		}
	}
	put(1, 2, 40)
	put(1, 2, 30)
	put(2, 2, 50)
	p := boardRows(t, e, BoardRanking2(2))
	if p.Total != 2 || p.Rows[0].PID != "2" || p.Rows[1].Score != 40 {
		t.Fatalf("%+v", p)
	}
	bs, _ := scorestore.Source{Store: e.Scores, Title: e.Name}.Boards(context.Background())
	if len(bs) != 1 || bs[0].Name != "Board 2 (set 2)" || bs[0].Order != "desc" {
		t.Fatalf("%+v", bs)
	}
}

func TestNoStoreStillAcknowledges(t *testing.T) {
	e := scoredEnv("arms")
	e.Scores = nil
	w := rmc.NewWriter()
	scoreData(w, 1, 1, 1, 0, nil, 0)
	w.U64(0)
	if resp := rankCall(t, e, 1, methodRankingUploadScore, w.Bytes()); len(resp) != 1 || resp[0] != 1 {
		t.Fatalf("%x", resp)
	}
}

type eventSink struct {
	mu  sync.Mutex
	got []map[string]any
	srv *httptest.Server
	c   *emit.Client
}

func newSink(t *testing.T) *eventSink {
	s := &eventSink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in struct{ Events []map[string]any }
		json.Unmarshal(b, &in)
		s.mu.Lock()
		s.got = append(s.got, in.Events...)
		s.mu.Unlock()
		w.Write([]byte(`{"results":[]}`))
	}))
	s.c = emit.New(emit.Config{URL: s.srv.URL, Key: "k", Source: "t", Flush: 10 * time.Millisecond})
	t.Cleanup(s.srv.Close)
	return s
}

func (s *eventSink) events() []map[string]any {
	s.c.Close(context.Background())
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got
}

func TestSplatfestScoreEmitsAndPersists(t *testing.T) {
	sink := newSink(t)
	e := scoredEnv("splatoon-2")
	e.Rewards = sink.c
	score := func(pid uint64, fest, s uint32) {
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.U64(1)
			w.U8(0)
			w.U32(fest)
			w.U8(1)
			w.U32(s)
			w.U32(0)
			w.U32(0)
			w.QBuffer(nil)
		})
		ok, _, errCode := decodeResponse(Dispatch(e, conn(pid), request(ProtoRanking, methodRankingS2UploadFestivalScore, w.Bytes())))
		if errCode != 0 || !ok {
			t.Fatalf("fest score: %#x", errCode)
		}
	}
	score(1, 9, 500)
	score(1, 9, 250) // same player, same fest: one event, summed contribution
	score(2, 9, 100)
	score(1, 10, 5)
	p := boardRows(t, e, "fest9")
	if p.Total != 2 || p.Rows[0].PID != "1" || p.Rows[0].Score != 750 {
		t.Fatalf("%+v", p)
	}
	evs := sink.events()
	keys := map[string]bool{}
	for _, ev := range evs {
		if ev["metric"] != "splatfests_joined" || ev["identity"].(map[string]any)["namespace"] != "switch" {
			t.Fatalf("%v", ev)
		}
		keys[ev["event_id"].(string)] = true
	}
	// every upload carries the same event id for a (festival, player): rewards dedupes them
	if len(keys) != 3 {
		t.Fatalf("event ids %v", keys)
	}
}

func TestMK8DCompetitionUploadEmitsTournamentEntered(t *testing.T) {
	sink := newSink(t)
	e, _, _ := mk8dEnv()
	e.Rewards = sink.c
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.U32(31) // tournament
		w.U32(2)  // season
		w.U32(0)
		w.U32(1234)
		w.U8(0)
		w.U32(0)
		w.Bool(true)
		w.QBuffer(nil)
	})
	rankCall(t, e, 41, methodRankingSubmitScore, w.Bytes())
	evs := sink.events()
	if len(evs) != 1 || evs[0]["metric"] != "kart_tournaments_entered" || evs[0]["event_id"] != "mk8d:tour:31:2:41:entered" {
		t.Fatalf("%v", evs)
	}
	keys, _ := evs[0]["keys"].([]any)
	if len(keys) != 1 || keys[0] != "31" {
		t.Fatalf("keys %v", evs[0])
	}
}

// With the core attached the Ranking2 board lives there: a put goes to the core, the answer to
// GetRanking and EstimateMyRank is built from it, and nothing is kept in memory.
func TestRanking2BoardIsServedFromTheCore(t *testing.T) {
	e := scoredEnv("super-mario-bros-35")
	ids := lbtest.NewIDs()
	ids.Auto = true
	e.Scores.UseCore(&scorestore.Core{C: &lb.Client{LB: lbtest.New(), ID: ids}})
	put := func(pid uint64, cat, score uint32, misc uint64) {
		w := rmc.NewWriter()
		w.List(1)
		w.StructOf(0, func(w *rmc.Writer) { w.U32(cat); w.U32(score); w.U64(misc) })
		w.U64(pid + 1000)
		if ok, _, errCode := decodeResponse(Dispatch(e, conn(pid), request(ProtoRanking2, 0x01, w.Bytes()))); errCode != 0 || !ok {
			t.Fatalf("put: %#x", errCode)
		}
	}
	put(1, 2, 40, 5)
	put(1, 2, 30, 6) // not a best
	put(2, 2, 50, 7)
	rows, base, total := e.r2board(2, 0, 10)
	if total != 2 || base != 0 || len(rows) != 2 || rows[0].pid != 2 || rows[0].misc != 7 || rows[0].nexUniqueID != 1002 || rows[1].score != 40 {
		t.Fatalf("board %+v %d %d", rows, base, total)
	}
	if rank, score, n := e.r2rank(2, 1); rank != 2 || score != 40 || n != 2 {
		t.Fatalf("rank %d %d %d", rank, score, n)
	}
	if rank, _, n := e.r2rank(2, 9); rank != 3 || n != 2 {
		t.Fatalf("unranked rank %d of %d", rank, n)
	}
	if len(e.r2common.board(2)) != 0 {
		t.Fatal("the board was also kept in memory")
	}
}
