// Ranking (0x70) — the common-data profile store and the competition surface.
//
//	0x01 UploadScore              -> bool true; the score is acknowledged, not stored
//	0x04 UploadCommonData        -> empty; the profile blob is kept per player
//	0x09 GetRanking              -> empty RankingResult
//	0x0C GetRankingByPIDList     -> empty RankingResult
//	0x0D GetRankingByUniqueIDList-> empty RankingResult
//	0x10 GetCompetitionRanking   -> empty board
//	0x11 SubmitScore             -> ONE zero byte
//	0x12 GetCompetitionInfo      -> empty competition list
//	0x13 SubmitTime              -> ONE zero byte
//	0x16 GetCommonDataByPIDList  -> one entry per requested PID, IN ORDER
//
// 0x16 is the one that matters most: it renders every other player's name and
// Mii, and left unanswered it is the wall the worldwide modes hit. It never
// returns an error — a player we hold nothing for is an empty entry, which is a
// valid answer, and the ORDER is the contract because the client pairs the nth
// reply with the nth PID it asked about.
//
// The two submissions answer a single zero byte rather than an empty body. An
// empty answer is a different length and the client counts.
//
// 0x09 is Mario Golf's first call after CONNECT (mode 1, category 1280, a
// window of one around itself). Left unanswered it is a hard wall: the game
// takes the NotImplemented and hangs up one second in, which reads on the
// console as "could not connect to the server" (2026-09-20).
package protocols

import (
	"log"
	"sync"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
	"github.com/openpak/switch-nex/rmc"
)

// Ranking method numbers.
const (
	methodRankingUploadScore         uint32 = 0x01
	methodRankingUploadCommonData    uint32 = 0x04
	methodRankingGetRanking          uint32 = 0x09
	methodRankingByPIDList           uint32 = 0x0C
	methodRankingByUniqueIDList      uint32 = 0x0D
	methodRankingCompetitionRanking  uint32 = 0x10 // GetCompetitionRankingScore (MK8D wiki 16)
	methodRankingSubmitScore         uint32 = 0x11 // UploadCompetitionRankingScore (17)
	methodRankingGetCompetitionInfo  uint32 = 0x12 // GetCompetitionInfo (18)
	methodRankingSubmitTime          uint32 = 0x13 // UploadScorePack (19)
	methodRankingGetScorePack        uint32 = 0x14 // GetScorePack (20)
	methodRankingCommonDataByPIDList uint32 = 0x16
)

// commonDataMaxEntries bounds a by-PID request. The count arrives off the
// network; without a bound an absurd one loops for a very long time over a body
// a few bytes long.
const commonDataMaxEntries = 512

// commonDataMaxLen bounds one stored profile.
const commonDataMaxLen = 4096

type rankingStore struct {
	mu     sync.Mutex
	common map[uint64][]byte
}

func (rs *rankingStore) put(pid uint64, blob []byte) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.common == nil {
		rs.common = map[uint64][]byte{}
	}
	rs.common[pid] = blob
}

func (rs *rankingStore) get(pid uint64) []byte {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.common[pid]
}

func rankingHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if env.Name == "splatoon-2" {
		if resp, ok := rankingSplatoon2(env, c, msg); ok {
			return resp
		}
	}
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case methodRankingUploadScore: // (RankingScoreData, u64 uniqueID) -> bool true
		// Mario Golf posts its match points here the moment a ranked match
		// ends (2026-09-21, console: EndParticipation then 0x70/0x01 with
		// category 26, score 1037). Answering NotImplemented disconnects the
		// client with 2306-0103 and the game reports the points as lost, so
		// the upload is acknowledged, never refused. Scores are not stored —
		// same stance as DataStore: shapes first, persistence later.
		logUploadScore(env, c, msg.Body)
		storeUploadScore(env, c, msg.Body)
		return boolTrue(msg)

	case methodRankingUploadCommonData: // (Buffer commonData, …) -> empty
		// MK8D's profile is 132 bytes; the cap keeps a blob inside the QBuffer
		// (u16) it is served back in.
		if blob, err := r.Buffer(); err == nil && len(blob) <= commonDataMaxLen {
			env.ranking.put(c.PID, blob)
			if env.mk8dReady() {
				uid, _ := r.U64()
				ctx, cancel := ctxDB()
				if err := env.MK8D.PutCommonData(ctx, c.PID, uid, blob); err != nil {
					log.Printf("[%s] mk8d: common data: %v", env.Name, err)
				}
				cancel()
			}
		}
		return success(msg, nil)

	case methodRankingCommonDataByPIDList:
		return success(msg, commonDataByPIDs(env, msg.Body))

	case methodRankingSubmitScore: // UploadCompetitionRankingScore
		competitionUpload(env, c, msg)
		// Answered with one zero byte, not an empty body (kept as measured).
		return success(msg, []byte{0})

	case methodRankingSubmitTime: // UploadScorePack(RankingScoreData, qBuffer)
		scorePackUpload(env, c, msg)
		return success(msg, []byte{0})

	case methodRankingGetScorePack:
		return scorePackGet(env, msg)

	case methodRankingGetRanking, methodRankingByPIDList, methodRankingByUniqueIDList:
		// RankingResult from the store; the empty board without one (a valid
		// answer, unlike an error).
		return rankingBoard(env, c, msg)

	case methodRankingCompetitionRanking:
		return competitionRanking(env, msg)

	case methodRankingGetCompetitionInfo:
		return competitionInfo(env, msg)

	default:
		return notImplemented(env, msg)
	}
}

// logUploadScore reads what it can of an UploadScore body for the log: a
// structure version byte, then the RankingScoreData category and score.
// Best effort only — a shape we do not recognise is still acknowledged,
// because refusing is what walls the mode off entirely.
func logUploadScore(env *Env, c *prudplite.Connection, body []byte) {
	sd, err := readRankingScoreData(rmc.NewReader(body))
	if err != nil {
		logHex(env, c.PID, "UploadScore unparsed", body)
		return
	}
	log.Printf("[%s] ranking: pid=%s upload category=%d score=%d order=%d update_mode=%d groups=%x param=%d",
		env.Name, redact.Tag(c.PID), sd.Category, sd.Score, sd.OrderBy, sd.UpdateMode, sd.Groups, sd.Param)
}

// storeUploadScore keeps the score when the title has a store.
func storeUploadScore(env *Env, c *prudplite.Connection, body []byte) {
	if !env.mk8dReady() {
		return
	}
	r := rmc.NewReader(body)
	sd, err := readRankingScoreData(r)
	if err != nil {
		return
	}
	uid, _ := r.U64()
	ctx, cancel := ctxDB()
	defer cancel()
	if _, err := env.MK8D.UploadScore(ctx, c.PID, uid, sd); err != nil {
		log.Printf("[%s] mk8d: store score: %v", env.Name, err)
	}
}

func competitionUpload(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) {
	logHex(env, c.PID, "UploadCompetitionRankingScore", msg.Body) // field meanings are unconfirmed
	if !env.mk8dReady() {
		return
	}
	u, err := readCompetitionUpload(msg.Body)
	if err != nil || len(u.Metadata) > 0x100 {
		return
	}
	ctx, cancel := ctxDB()
	defer cancel()
	if err := env.MK8D.CompetitionUpload(ctx, c.PID, u); err != nil {
		log.Printf("[%s] mk8d: competition upload: %v", env.Name, err)
	}
}

// competitionRanking answers GetCompetitionRankingScore: the last N+1 seasons.
func competitionRanking(env *Env, msg *rmc.RMCMessage) []byte {
	empty := writeCompetitionSeasons(nil)
	if !env.mk8dReady() {
		return success(msg, empty)
	}
	_, p, err := rmc.NewReader(msg.Body).Struct()
	if err != nil {
		return success(msg, empty)
	}
	id, err := p.U32()
	if err != nil {
		return success(msg, empty)
	}
	_, length, err := readResultRange(p)
	if err != nil || length > 5 {
		return success(msg, empty)
	}
	ctx, cancel := ctxDB()
	defer cancel()
	seasons, err := env.MK8D.CompetitionScores(ctx, id, length, 20)
	if err != nil {
		log.Printf("[%s] mk8d: competition scores: %v", env.Name, err)
		return success(msg, empty)
	}
	return success(msg, writeCompetitionSeasons(seasons))
}

// competitionInfo answers GetCompetitionInfo: the competitions by popularity.
func competitionInfo(env *Env, msg *rmc.RMCMessage) []byte {
	empty := writeCompetitionList(nil)
	if !env.mk8dReady() {
		return success(msg, empty)
	}
	_, p, err := rmc.NewReader(msg.Body).Struct()
	if err != nil {
		return success(msg, empty)
	}
	if _, err = p.U8(); err != nil {
		return success(msg, empty)
	}
	offset, length, err := readResultRange(p)
	if err != nil || length > 100 {
		return success(msg, empty)
	}
	if length == 0 {
		length = 100
	}
	ctx, cancel := ctxDB()
	defer cancel()
	list, err := env.MK8D.CompetitionList(ctx, int(offset), int(length))
	if err != nil {
		log.Printf("[%s] mk8d: competition list: %v", env.Name, err)
		return success(msg, empty)
	}
	return success(msg, writeCompetitionList(list))
}

// scorePackUpload keeps an UploadScorePack metadata blob per player and category.
func scorePackUpload(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) {
	if !env.mk8dReady() {
		return
	}
	r := rmc.NewReader(msg.Body)
	sd, err := readRankingScoreData(r)
	if err != nil {
		logHex(env, c.PID, "UploadScorePack unparsed", msg.Body)
		return
	}
	meta, err := r.QBuffer()
	if err != nil || len(meta) > commonDataMaxLen {
		return
	}
	ctx, cancel := ctxDB()
	defer cancel()
	env.MK8D.PutScorePack(ctx, c.PID, sd.Category, meta)
	if _, err := env.MK8D.UploadScore(ctx, c.PID, 0, sd); err != nil {
		log.Printf("[%s] mk8d: score pack score: %v", env.Name, err)
	}
}

// scorePackGet answers GetScorePack: one qBuffer per requested PID, in order.
func scorePackGet(env *Env, msg *rmc.RMCMessage) []byte {
	build := func(blobs [][]byte) []byte {
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.List(len(blobs))
			for _, b := range blobs {
				w.QBuffer(b)
			}
		})
		return success(msg, w.Bytes())
	}
	r := rmc.NewReader(msg.Body)
	pids, err := readU64List(r, commonDataMaxEntries)
	if err != nil {
		return build(nil)
	}
	category, err := r.U32()
	if err != nil {
		return build(nil)
	}
	blobs := make([][]byte, len(pids))
	if env.mk8dReady() {
		ctx, cancel := ctxDB()
		defer cancel()
		if got, err := env.MK8D.ScorePacks(ctx, pids, category); err == nil {
			for i, pid := range pids {
				blobs[i] = got[pid]
			}
		}
	}
	return build(blobs)
}

// commonDataByPIDs answers one entry per requested PID, in the order asked.
// Never an error: an unreadable request falls back to the empty list rather
// than refusing, because refusing is what walls the mode off entirely.
func commonDataByPIDs(env *Env, body []byte) []byte {
	empty := func() []byte {
		w := rmc.NewWriter()
		w.EmptyList()
		return w.Bytes()
	}
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil || n == 0 || n > commonDataMaxEntries {
		return empty()
	}
	pids := make([]uint64, 0, n)
	for range n {
		pid, err := r.U64()
		if err != nil {
			return empty()
		}
		pids = append(pids, pid)
	}
	// The shape Nintendo answers: a structure (version 0) holding the list, each
	// profile a QBuffer (u16 length). Bare u32 Buffers parse harmlessly while
	// every entry is empty, but the first real profile makes MK8D read garbage
	// and abort ~10 s later with 2162-0001 (console, 2026-10-08).
	var stored map[uint64][]byte
	if env.mk8dReady() {
		ctx, cancel := ctxDB()
		stored, _ = env.MK8D.CommonData(ctx, pids)
		cancel()
	}
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.List(len(pids))
		for _, pid := range pids {
			blob := env.ranking.get(pid)
			if blob == nil {
				blob = stored[pid]
			}
			w.QBuffer(blob) // absent renders as a zero-length blob
		}
	})
	return w.Bytes()
}
