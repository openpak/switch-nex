package protocols

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/openpak/switch-nex/rmc"
)

// fakeMK8D is a minimal in-memory MK8DBackend: enough ordering to prove the
// wire. Update and rank semantics are tested on Postgres (internal/mk8dstore).
type fakeMK8D struct {
	mu      sync.Mutex
	scores  map[uint32]map[uint64]RankingScore
	uploads []RankingScore
	last    RankQuery
	common  map[uint64][]byte
	packs   map[uint64][]byte
	comp    []CompetitionUpload
	tours   []MK8DTournament
	objs    map[uint64]*MK8DObject
	next    uint64
}

func newFakeMK8D() *fakeMK8D {
	return &fakeMK8D{scores: map[uint32]map[uint64]RankingScore{}, common: map[uint64][]byte{}, packs: map[uint64][]byte{},
		objs: map[uint64]*MK8DObject{}, next: 100}
}

func (f *fakeMK8D) UploadScore(_ context.Context, pid, _ uint64, s RankingScore) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, s)
	if f.scores[s.Category] == nil {
		f.scores[s.Category] = map[uint64]RankingScore{}
	}
	f.scores[s.Category][pid] = s
	return true, nil
}
func (f *fakeMK8D) Ranking(_ context.Context, q RankQuery) ([]RankRow, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = q
	var rows []RankRow
	for pid, s := range f.scores[q.Category] {
		rows = append(rows, RankRow{PID: pid, UniqueID: pid + 1000, Category: q.Category, Score: s.Score, Groups: s.Groups, Param: s.Param, CommonData: f.common[pid]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Score < rows[j].Score })
	for i := range rows {
		rows[i].Order = uint32(i + 1)
	}
	return rows, len(rows), nil
}
func (f *fakeMK8D) PutCommonData(_ context.Context, pid, _ uint64, b []byte) error {
	f.common[pid] = b
	return nil
}
func (f *fakeMK8D) CommonData(_ context.Context, pids []uint64) (map[uint64][]byte, error) {
	return f.common, nil
}
func (f *fakeMK8D) PutScorePack(_ context.Context, pid uint64, _ uint32, m []byte) error {
	f.packs[pid] = m
	return nil
}
func (f *fakeMK8D) ScorePacks(context.Context, []uint64, uint32) (map[uint64][]byte, error) {
	return f.packs, nil
}
func (f *fakeMK8D) CompetitionUpload(_ context.Context, _ uint64, p CompetitionUpload) error {
	f.comp = append(f.comp, p)
	return nil
}
func (f *fakeMK8D) CompetitionScores(_ context.Context, id uint32, n uint32, _ int) ([]CompetitionSeason, error) {
	return []CompetitionSeason{{Season: 3, Participants: 2, Scores: []CompetitionScore{
		{Rank: 1, PID: 9, Score: 777, Updated: time.Date(2026, 10, 1, 2, 3, 4, 0, time.UTC), TeamID: 1, Metadata: []byte{1, 2}},
	}}}, nil
}
func (f *fakeMK8D) CompetitionList(context.Context, int, int) ([]CompetitionSummary, error) {
	return []CompetitionSummary{{ID: 5, Participants: 12}}, nil
}
func (f *fakeMK8D) SaveTournament(_ context.Context, owner uint64, v uint8, blob []byte) (uint32, error) {
	id := uint32(len(f.tours) + 1)
	f.tours = append(f.tours, MK8DTournament{ID: id, Owner: owner, Version: v, Blob: blob})
	return id, nil
}
func (f *fakeMK8D) ListTournaments(context.Context, int) ([]MK8DTournament, error) {
	return f.tours, nil
}
func (f *fakeMK8D) DeleteTournament(_ context.Context, id uint32, owner uint64) (bool, error) {
	for i, t := range f.tours {
		if t.ID == id && t.Owner == owner {
			f.tours = append(f.tours[:i], f.tours[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeMK8D) NewObject(_ context.Context, o MK8DObject) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	o.DataID, o.Created, o.Updated = f.next, time.Now(), time.Now()
	f.objs[o.DataID] = &o
	return o.DataID, nil
}
func (f *fakeMK8D) CompleteObject(_ context.Context, owner, id uint64, size uint32) (bool, error) {
	o := f.objs[id]
	if o == nil || o.Owner != owner {
		return false, nil
	}
	o.Status = MK8DComplete
	if size > 0 {
		o.Size = size
	}
	return true, nil
}
func (f *fakeMK8D) DeleteObject(_ context.Context, owner, id uint64) (bool, error) {
	o := f.objs[id]
	if o == nil || o.Owner != owner {
		return false, nil
	}
	o.Status = MK8DDeleted
	return true, nil
}
func (f *fakeMK8D) Objects(_ context.Context, ids []uint64) (map[uint64]MK8DObject, error) {
	out := map[uint64]MK8DObject{}
	for _, id := range ids {
		if o := f.objs[id]; o != nil && o.Status != MK8DDeleted {
			out[id] = *o
		}
	}
	return out, nil
}
func (f *fakeMK8D) SlotObject(_ context.Context, owner uint64, slot uint16) (MK8DObject, bool, error) {
	for _, o := range f.objs {
		if o.Owner == owner && o.Slot == int(slot) && o.Status == MK8DComplete {
			return *o, true, nil
		}
	}
	return MK8DObject{}, false, nil
}
func (f *fakeMK8D) SearchObjects(_ context.Context, q MK8DSearch) ([]MK8DObject, int, error) {
	var out []MK8DObject
	for _, o := range f.objs {
		if o.Status == MK8DComplete && (len(q.DataTypes) == 0 || q.DataTypes[0] == o.DataType) {
			out = append(out, *o)
		}
	}
	return out, len(out), nil
}

func mk8dEnv() (*Env, *fakeMK8D, *fakeBlobs) {
	e := testEnv()
	e.Ranking = true
	e.DataStore = DSMK8D
	f, b := newFakeMK8D(), &fakeBlobs{sizes: map[string]int64{}}
	e.MK8D, e.MK8DBlobs = f, b
	return e, f, b
}

func scoreData(w *rmc.Writer, category, score uint32, orderBy, mode uint8, groups []byte, param uint64) {
	w.StructOf(0, func(w *rmc.Writer) {
		w.U32(category)
		w.U32(score)
		w.U8(orderBy)
		w.U8(mode)
		w.List(len(groups))
		for _, g := range groups {
			w.U8(g)
		}
		w.U64(param)
	})
}

func rankCall(t *testing.T, e *Env, pid uint64, method uint32, body []byte) []byte {
	t.Helper()
	ok, resp, errCode := decodeResponse(Dispatch(e, conn(pid), request(ProtoRanking, method, body)))
	if errCode != 0 || !ok {
		t.Fatalf("ranking %#x: ok=%v error %#x", method, ok, errCode)
	}
	return resp
}

func orderParam(w *rmc.Writer, calc, gi, gn uint8, offset uint32, length uint8) {
	w.StructOf(0, func(w *rmc.Writer) {
		w.U8(calc)
		w.U8(gi)
		w.U8(gn)
		w.U8(0)
		w.U32(offset)
		w.U8(length)
	})
}

func TestMK8DUploadScoreWireAndAnswer(t *testing.T) {
	e, f, _ := mk8dEnv()
	w := rmc.NewWriter()
	scoreData(w, 17, 83210, 0, 1, []byte{3, 4}, 99)
	w.U64(555)
	resp := rankCall(t, e, 41, methodRankingUploadScore, w.Bytes())
	if !bytes.Equal(resp, []byte{1}) {
		t.Fatalf("UploadScore must keep answering bool true: %x", resp)
	}
	if len(f.uploads) != 1 {
		t.Fatal("score not stored")
	}
	u := f.uploads[0]
	if u.Category != 17 || u.Score != 83210 || u.UpdateMode != 1 || !bytes.Equal(u.Groups, []byte{3, 4}) || u.Param != 99 {
		t.Fatalf("decoded %+v", u)
	}
}

func TestMK8DUnparsedUploadStillAcknowledged(t *testing.T) {
	e, f, _ := mk8dEnv()
	resp := rankCall(t, e, 41, methodRankingUploadScore, []byte{1, 2, 3})
	if !bytes.Equal(resp, []byte{1}) || len(f.uploads) != 0 {
		t.Fatalf("resp %x uploads %d", resp, len(f.uploads))
	}
}

func decodeRankingResult(t *testing.T, body []byte) (rows []RankRow, total uint32) {
	t.Helper()
	r := rmc.NewReader(body)
	_, p, err := r.Struct()
	if err != nil {
		t.Fatal(err)
	}
	n, _ := p.ListHeader()
	for range n {
		_, e, err := p.Struct()
		if err != nil {
			t.Fatal(err)
		}
		var row RankRow
		row.PID, _ = e.U64()
		row.UniqueID, _ = e.U64()
		row.Order, _ = e.U32()
		row.Category, _ = e.U32()
		row.Score, _ = e.U32()
		gn, _ := e.ListHeader()
		for range gn {
			g, _ := e.U8()
			row.Groups = append(row.Groups, g)
		}
		row.Param, _ = e.U64()
		row.CommonData, err = e.Buffer()
		if err != nil {
			t.Fatal(err)
		}
		if e.Remaining() != 0 {
			t.Fatalf("rank row has %d trailing bytes", e.Remaining())
		}
		rows = append(rows, row)
	}
	total, _ = p.U32()
	if _, err := p.U64(); err != nil { // sinceTime
		t.Fatal(err)
	}
	if p.Remaining() != 0 || r.Remaining() != 0 {
		t.Fatal("RankingResult has trailing bytes")
	}
	return
}

func TestMK8DGetRankingModesAndShape(t *testing.T) {
	e, f, _ := mk8dEnv()
	for pid, ms := range map[uint64]uint32{1: 90000, 2: 80000, 3: 85000} {
		w := rmc.NewWriter()
		scoreData(w, 4, ms, 0, 0, []byte{1}, pid)
		w.U64(pid + 1000)
		rankCall(t, e, pid, methodRankingUploadScore, w.Bytes())
	}
	put := rmc.NewWriter()
	put.Buffer([]byte("profile"))
	put.U64(1)
	rankCall(t, e, 2, methodRankingUploadCommonData, put.Bytes())

	body := func(mode uint8, offset uint32, length uint8) []byte {
		w := rmc.NewWriter()
		w.U8(mode)
		w.U32(4)
		orderParam(w, 1, 0xFF, 0, offset, length)
		w.U64(0)
		w.U64(3)
		return w.Bytes()
	}
	rows, total := decodeRankingResult(t, rankCall(t, e, 3, methodRankingGetRanking, body(RankModeAround, 0, 1)))
	if total != 3 || len(rows) != 3 { // the fake ignores windows; the query is what matters
		t.Fatalf("rows %d total %d", len(rows), total)
	}
	if f.last.Mode != RankModeAround || f.last.Self != 3 || f.last.Length != 1 || f.last.Category != 4 || f.last.Calc != 1 || f.last.GroupIndex != 0xFF {
		t.Fatalf("query %+v", f.last)
	}
	if rows[0].PID != 2 || rows[0].Score != 80000 || rows[0].Order != 1 || string(rows[0].CommonData) != "profile" || rows[0].Param != 2 || !bytes.Equal(rows[0].Groups, []byte{1}) {
		t.Fatalf("top row %+v", rows[0])
	}
	rankCall(t, e, 3, methodRankingGetRanking, body(RankModeSelf, 0, 1))
	if f.last.Mode != RankModeSelf {
		t.Fatalf("mode %d", f.last.Mode)
	}
	rankCall(t, e, 3, methodRankingGetRanking, body(RankModeGlobal, 10, 20))
	if f.last.Offset != 10 || f.last.Length != 20 {
		t.Fatalf("window %+v", f.last)
	}

	// By PID list / unique id list.
	w := rmc.NewWriter()
	w.List(2)
	w.U64(1)
	w.U64(2)
	w.U8(0)
	w.U32(4)
	orderParam(w, 0, 0xFF, 0, 0, 10)
	w.U64(0)
	rankCall(t, e, 3, methodRankingByPIDList, w.Bytes())
	if len(f.last.PIDs) != 2 || f.last.PIDs[1] != 2 || f.last.Category != 4 {
		t.Fatalf("pid list query %+v", f.last)
	}
	rankCall(t, e, 3, methodRankingByUniqueIDList, w.Bytes())
	if len(f.last.UniqueIDs) != 2 {
		t.Fatalf("unique list query %+v", f.last)
	}
}

func TestMK8DBoardsWithoutStoreStayEmpty(t *testing.T) {
	e := testEnv()
	e.Ranking = true
	for _, m := range []uint32{methodRankingGetRanking, methodRankingByPIDList, methodRankingByUniqueIDList} {
		rows, total := decodeRankingResult(t, rankCall(t, e, 5, m, nil))
		if len(rows) != 0 || total != 0 {
			t.Fatalf("method %#x not empty", m)
		}
	}
	// A garbled request on a title with a store is also an empty board, not an error.
	e2, _, _ := mk8dEnv()
	rows, _ := decodeRankingResult(t, rankCall(t, e2, 5, methodRankingGetRanking, []byte{9}))
	if len(rows) != 0 {
		t.Fatal("garbled request produced rows")
	}
}

func TestMK8DOneZeroByteAnswersAndCompetition(t *testing.T) {
	e, f, _ := mk8dEnv()
	up := rmc.NewWriter()
	up.StructOf(0, func(w *rmc.Writer) {
		w.U32(5)
		w.U32(3)
		w.U32(0)
		w.U32(777)
		w.U8(1)
		w.U32(40)
		w.Bool(true)
		w.QBuffer([]byte{7, 8})
	})
	if r := rankCall(t, e, 9, methodRankingSubmitScore, up.Bytes()); !bytes.Equal(r, []byte{0}) {
		t.Fatalf("0x11 answer %x", r)
	}
	if len(f.comp) != 1 {
		t.Fatal("competition score not stored")
	}
	if c := f.comp[0]; c.ID != 5 || c.Season != 3 || c.Score != 777 || c.TeamID != 1 || c.TeamScore != 40 || !c.First || !bytes.Equal(c.Metadata, []byte{7, 8}) {
		t.Fatalf("decoded %+v", c)
	}
	pack := rmc.NewWriter()
	scoreData(pack, 2, 1, 0, 0, nil, 0)
	pack.QBuffer([]byte("pack"))
	if r := rankCall(t, e, 9, methodRankingSubmitTime, pack.Bytes()); !bytes.Equal(r, []byte{0}) {
		t.Fatalf("0x13 answer %x", r)
	}
	if string(f.packs[9]) != "pack" {
		t.Fatal("score pack not stored")
	}

	// GetCompetitionRankingScore.
	g := rmc.NewWriter()
	g.StructOf(0, func(w *rmc.Writer) {
		w.U32(5)
		w.StructOf(0, func(w *rmc.Writer) { w.U32(0); w.U32(2) })
	})
	r := rmc.NewReader(rankCall(t, e, 9, methodRankingCompetitionRanking, g.Bytes()))
	if n, _ := r.ListHeader(); n != 1 {
		t.Fatalf("seasons %d", n)
	}
	_, s, _ := r.Struct()
	if season, _ := s.U32(); season != 3 {
		t.Fatalf("season %d", season)
	}
	if n, _ := s.ListHeader(); n != 1 {
		t.Fatal("scores")
	}
	_, sc, _ := s.Struct()
	rank, _ := sc.U32()
	pid, _ := sc.U64()
	score, _ := sc.U32()
	when, _ := sc.U64()
	team, _ := sc.U8()
	meta, _ := sc.QBuffer()
	if rank != 1 || pid != 9 || score != 777 || when>>26 != 2026 || team != 1 || !bytes.Equal(meta, []byte{1, 2}) || sc.Remaining() != 0 {
		t.Fatalf("score %d %d %d %x %d %x", rank, pid, score, when, team, meta)
	}
	if participants, _ := s.U32(); participants != 2 {
		t.Fatal("participants")
	}
	if n, _ := s.ListHeader(); n != 4 || s.Remaining() != 16 {
		t.Fatalf("team scores %d, %d left", n, s.Remaining())
	}

	// GetCompetitionInfo.
	gi := rmc.NewWriter()
	gi.StructOf(0, func(w *rmc.Writer) {
		w.U8(1)
		w.StructOf(0, func(w *rmc.Writer) { w.U32(0); w.U32(10) })
	})
	r = rmc.NewReader(rankCall(t, e, 9, methodRankingGetCompetitionInfo, gi.Bytes()))
	if n, _ := r.ListHeader(); n != 1 {
		t.Fatal("info count")
	}
	_, ci, _ := r.Struct()
	if id, _ := ci.U32(); id != 5 {
		t.Fatal("info id")
	}
	if n, _ := ci.U32(); n != 12 {
		t.Fatal("info participants")
	}
}

func TestMK8DOneZeroByteAnswersWithoutStore(t *testing.T) {
	e := testEnv()
	e.Ranking = true
	for _, m := range []uint32{methodRankingSubmitScore, methodRankingSubmitTime} {
		if r := rankCall(t, e, 9, m, []byte{1}); !bytes.Equal(r, []byte{0}) {
			t.Fatalf("%#x: %x", m, r)
		}
	}
}

func TestMK8DScorePackOrder(t *testing.T) {
	e, f, _ := mk8dEnv()
	f.packs[2] = []byte("two")
	w := rmc.NewWriter()
	w.List(2)
	w.U64(1)
	w.U64(2)
	w.U32(3)
	r := rmc.NewReader(rankCall(t, e, 1, methodRankingGetScorePack, w.Bytes()))
	_, s, _ := r.Struct()
	if n, _ := s.ListHeader(); n != 2 {
		t.Fatal("count")
	}
	a, _ := s.QBuffer()
	b, _ := s.QBuffer()
	if len(a) != 0 || string(b) != "two" {
		t.Fatalf("%q %q", a, b)
	}
}

func TestMK8DTournamentsAreDurableWithStore(t *testing.T) {
	e, f, _ := mk8dEnv()
	e.Match = NewMatchmakingStore()
	blob := rmc.NewWriter()
	blob.U32(0) // object id left at 0 by the console
	blob.U64(3)
	create := rmc.NewWriter()
	create.Struct(1, blob.Bytes())
	_, resp, code := decodeResponse(Dispatch(e, conn(3), request(ProtoMatchmakeExt, 0x42, create.Bytes())))
	if code != 0 || len(f.tours) != 1 {
		t.Fatalf("create: code %#x tours %d", code, len(f.tours))
	}
	_, content, err := rmc.NewReader(resp).Struct()
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := content.U32(); id != 1 {
		t.Fatalf("echoed descriptor keeps object id %d, want the assigned 1", id)
	}
	_, resp, _ = decodeResponse(Dispatch(e, conn(3), request(ProtoMatchmakeExt, 0x39, nil)))
	if n, _ := rmc.NewReader(resp).ListHeader(); n != 1 {
		t.Fatalf("search lists %d", n)
	}
	del := rmc.NewWriter()
	del.U32(1)
	if _, _, code := decodeResponse(Dispatch(e, conn(4), request(ProtoMatchmakeExt, 0x43, del.Bytes()))); code == 0 {
		t.Fatal("a stranger deleted a tournament")
	}
	if _, _, code := decodeResponse(Dispatch(e, conn(3), request(ProtoMatchmakeExt, 0x43, del.Bytes()))); code != 0 || len(f.tours) != 0 {
		t.Fatal("owner delete failed")
	}
}

func TestTournamentWithID(t *testing.T) {
	if got := TournamentWithID([]byte{0, 0, 0, 0, 9}, 258); !bytes.Equal(got, []byte{2, 1, 0, 0, 9}) {
		t.Fatalf("%x", got)
	}
	if got := TournamentWithID([]byte{1, 0, 0, 0}, 5); !bytes.Equal(got, []byte{1, 0, 0, 0}) {
		t.Fatal("an assigned id was overwritten")
	}
}

// --- ghosts ---

func preparePostGhost(name string, dataType uint16, size uint32, slot uint16, tags ...string) []byte {
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.U32(size)
		w.String(name)
		w.U16(dataType)
		w.QBuffer([]byte{0xAA, 0xBB})
		w.StructOf(0, func(w *rmc.Writer) { w.U8(0); w.EmptyList() }) // permission: public
		w.StructOf(0, func(w *rmc.Writer) { w.U8(3); w.EmptyList() }) // delete permission
		w.U32(0)
		w.U16(0xFFFF)
		w.U32(0)
		w.List(len(tags))
		for _, tg := range tags {
			w.String(tg)
		}
		w.EmptyList() // rating init params
		w.StructOf(0, func(w *rmc.Writer) { w.U16(slot); w.Bool(true) })
		w.EmptyList() // extra data
	})
	return w.Bytes()
}

func dsCall(t *testing.T, e *Env, pid uint64, method uint32, body []byte) []byte {
	t.Helper()
	_, resp, code := decodeResponse(Dispatch(e, conn(pid), request(ProtoDataStore, method, body)))
	if code != 0 {
		t.Fatalf("datastore %#x: error %#x", method, code)
	}
	return resp
}

func TestMK8DGhostUploadAndDownload(t *testing.T) {
	e, f, blobs := mk8dEnv()
	r := rmc.NewReader(dsCall(t, e, 7, 0x18, preparePostGhost("ghost", 12, 5000, 3, "tt")))
	_, info, err := r.Struct()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := info.U64()
	url, _ := info.String()
	if url != "https://ds.test/nex-smm2/" || id == 0 {
		t.Fatalf("post info %d %q", id, url)
	}
	if n, _ := info.ListHeader(); n != 0 {
		t.Fatal("headers")
	}
	if n, _ := info.ListHeader(); n != 2 {
		t.Fatal("form fields")
	}
	o := f.objs[id]
	if o == nil || o.DataType != 12 || o.Slot != 3 || o.Owner != 7 || o.Name != "ghost" || len(o.Tags) != 1 || o.Status != MK8DPending || len(o.Raw) == 0 {
		t.Fatalf("stored %+v", o)
	}
	// Not downloadable before the body lands.
	if _, _, code := decodeResponse(Dispatch(e, conn(8), request(ProtoDataStore, 0x19, getBody(id)))); code != rmc.ResultDataStoreNotFound|rmc.ErrorCodeBit && code == 0 {
		t.Fatalf("pending object was served: %#x", code)
	}
	blobs.sizes[MK8DObjectKey(id)] = 4900
	cw := rmc.NewWriter()
	cw.StructOf(0, func(w *rmc.Writer) { w.U64(id); w.Bool(true) })
	dsCall(t, e, 7, 0x1A, cw.Bytes())
	if f.objs[id].Status != MK8DComplete || f.objs[id].Size != 4900 {
		t.Fatalf("complete: %+v", f.objs[id])
	}

	gr := rmc.NewReader(dsCall(t, e, 8, 0x19, getBody(id)))
	_, gi, _ := gr.Struct()
	get, _ := gi.String()
	if get != "https://ds.test/get/ghost/"+uitoa(id)+".bin" {
		t.Fatalf("get url %q", get)
	}
	gi.ListHeader()
	if size, _ := gi.U32(); size != 4900 {
		t.Fatalf("size %d", size)
	}
	gi.Buffer()
	if gid, _ := gi.U64(); gid != id || gi.Remaining() != 0 {
		t.Fatal("get info tail")
	}

	// GetMeta with the meta option returns the meta binary.
	mw := rmc.NewWriter()
	mw.StructOf(0, func(w *rmc.Writer) {
		w.U64(id)
		w.StructOf(0, func(w *rmc.Writer) { w.U64(0); w.U16(0) })
		w.U8(4)
		w.U64(0)
	})
	mresp := dsCall(t, e, 8, 0x08, mw.Bytes())
	mr := rmc.NewReader(mresp)
	_, mi, _ := mr.Struct()
	mid, _ := mi.U64()
	owner, _ := mi.U64()
	size, _ := mi.U32()
	name, _ := mi.String()
	dt, _ := mi.U16()
	meta, _ := mi.QBuffer()
	if mid != id || owner != 7 || size != 4900 || name != "ghost" || dt != 12 || !bytes.Equal(meta, []byte{0xAA, 0xBB}) {
		t.Fatalf("meta %d %d %d %q %d %x", mid, owner, size, name, dt, meta)
	}

	// Persistence lookup by owner and slot.
	pw := rmc.NewWriter()
	pw.U64(7)
	pw.U16(3)
	pr := rmc.NewReader(dsCall(t, e, 8, 0x1D, pw.Bytes()))
	_, pi, _ := pr.Struct()
	po, _ := pi.U64()
	ps, _ := pi.U16()
	pd, _ := pi.U64()
	if po != 7 || ps != 3 || pd != id {
		t.Fatal("persistence info")
	}

	// A stranger cannot delete; the owner can.
	del := rmc.NewWriter()
	del.StructOf(0, func(w *rmc.Writer) { w.U64(id); w.U64(0) })
	if _, _, code := decodeResponse(Dispatch(e, conn(8), request(ProtoDataStore, 0x04, del.Bytes()))); code == 0 {
		t.Fatal("stranger deleted a ghost")
	}
	dsCall(t, e, 7, 0x04, del.Bytes())
	if _, _, code := decodeResponse(Dispatch(e, conn(8), request(ProtoDataStore, 0x19, getBody(id)))); code == 0 {
		t.Fatal("deleted ghost still downloads")
	}
}

func getBody(id uint64) []byte {
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.U64(id)
		w.U32(0)
		w.StructOf(0, func(w *rmc.Writer) { w.U64(0); w.U16(0) })
		w.U64(0)
		w.EmptyList()
	})
	return w.Bytes()
}

func TestMK8DOversizedGhostRefused(t *testing.T) {
	e, _, _ := mk8dEnv()
	_, _, code := decodeResponse(Dispatch(e, conn(7), request(ProtoDataStore, 0x18, preparePostGhost("g", 1, mk8dGhostMax+1, 1))))
	if code == 0 {
		t.Fatal("oversized ghost accepted")
	}
}

func TestMK8DDataStoreWithoutStoreKeepsStub(t *testing.T) {
	e := testEnv()
	e.DataStore = DSMK8D
	_, _, code := decodeResponse(Dispatch(e, conn(7), request(ProtoDataStore, 0x08, nil)))
	if code == 0 {
		t.Fatal("GetMeta with no store should be NotFound like the stub")
	}
}
