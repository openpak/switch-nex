// Ranking2 (0x7A) — the per-category board and the common data (player name +
// Mii) that renders it.
//
//	0x01 PutScore          -> empty; scores are kept, best-per-category
//	0x02 GetCommonData     -> Ranking2CommonData for the REQUESTED player
//	0x03 PutCommonData     -> empty
//	0x04 DeleteCommonData  -> empty
//	0x05 GetRanking        -> Ranking2Info{list, lowestRank, numRankedIn, season}
//	0x07 GetCategorySetting-> Ranking2CategorySetting
//	0x0B GetEstimateMyRank -> Ranking2EstimateScoreRankOutput
//
// Three shapes here are unforgiving and were all got wrong before:
//
//   - CommonData's mii and binary blobs are QBuffers (u16 length), not Buffers,
//     and an unnamed player's name is a zero-length string, not a lone NUL. A
//     board cites players whose common data we do not hold, so those empty
//     names are the normal case, and one byte each shifts everything after.
//   - GetCommonData for a player we know nothing about answers an EMPTY
//     STRUCTURE. Zero bytes is not the same thing: the client extracts a
//     structure from the empty reply and overruns its buffer (2306-0116).
//   - CategorySetting's reset month is a u16. Written as a u32 it inserts two
//     bytes and the rest of the structure reads shifted.
//
// The board's lowest rank is a CONFIGURATION ceiling, not a count of entrants —
// the same number has to come back from GetCategorySetting, because the two
// describe one category. Reporting a count there means zero on an empty board,
// and the game asks for the board again forever without ever drawing it.
package protocols

import (
	"log"
	"sort"
	"sync"
	"time"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// ranking2LowestRank is the ceiling the category retains.
const ranking2LowestRank uint32 = 10000

type r2Common struct {
	userName   string
	mii        []byte
	binaryData []byte
}

// write renders the Ranking2CommonData structure. A zero value renders as the
// empty structure a player we hold nothing for gets.
func (c r2Common) write(w *rmc.Writer) {
	w.StructOf(1, func(w *rmc.Writer) {
		w.StringEmptyZero(c.userName)
		w.QBuffer(c.mii)
		w.QBuffer(c.binaryData)
	})
}

type r2Entry struct {
	pid         uint64
	nexUniqueID uint64
	misc        uint64
	score       uint32
}

type r2CommonStore struct {
	mu     sync.Mutex
	data   map[uint64]r2Common
	boards map[uint32]map[uint64]r2Entry
}

func (s *r2CommonStore) get(pid uint64) (r2Common, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data[pid]
	return c, ok
}

func (s *r2CommonStore) put(pid uint64, c r2Common) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[uint64]r2Common{}
	}
	s.data[pid] = c
}

func (s *r2CommonStore) drop(pid uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, pid)
}

// submit keeps the best score per (category, player).
func (s *r2CommonStore) submit(category uint32, e r2Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.boards == nil {
		s.boards = map[uint32]map[uint64]r2Entry{}
	}
	if s.boards[category] == nil {
		s.boards[category] = map[uint64]r2Entry{}
	}
	if old, ok := s.boards[category][e.pid]; ok && old.score >= e.score {
		return
	}
	s.boards[category][e.pid] = e
}

// board returns a category's entries, best first.
func (s *r2CommonStore) board(category uint32) []r2Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]r2Entry, 0, len(s.boards[category]))
	for _, e := range s.boards[category] {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].pid < out[j].pid
	})
	return out
}

func ranking2Handler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01: // PutScore(list<Ranking2ScoreData>, u64 nexUniqueID) -> empty
		return success(msg, putRanking2Score(env, c, r))

	case 0x02: // GetCommonData(u32 optionFlags, PID target, u64 uniqueID)
		if _, err := r.U32(); err != nil { // optionFlags
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		target, err := r.U64()
		if err != nil || target == 0 {
			target = c.PID
		}
		common, _ := env.r2common.get(target) // a miss renders as the empty structure
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			common.write(w)
			return w.Bytes()
		}())

	case 0x03: // PutCommonData(Ranking2CommonData, u64 uniqueID) -> empty
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		userName, err := r.String()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		mii, err := r.QBuffer()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		binaryData, err := r.QBuffer()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		env.r2common.put(c.PID, r2Common{userName: userName, mii: mii, binaryData: binaryData})
		return success(msg, nil)

	case 0x04: // DeleteCommonData -> empty
		env.r2common.drop(c.PID)
		return success(msg, nil)

	case 0x05: // GetRanking(Ranking2GetParam) -> Ranking2Info
		return success(msg, ranking2Info(env, r))

	case 0x07: // GetCategorySetting(u32 category) -> Ranking2CategorySetting
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.U32(0)                  // MinScore
				w.U32(999999999)          // MaxScore
				w.U32(ranking2LowestRank) // LowestRank — agrees with GetRanking
				w.U16(4095)               // ResetMonth: every month (u16, not u32)
				w.U8(0)                   // ResetDay
				w.U8(0)                   // ResetHour
				w.U8(0)                   // ResetMode: no reset — a season that
				w.U8(0)                   // MaxSeasonsToGoBack: expires would drop
				w.Bool(true)              // ScoreOrder: scores we do not keep elsewhere
			})
			return w.Bytes()
		}())

	case 0x0B: // GetEstimateMyRank(param{category, seasonsBack}) -> estimate
		return success(msg, estimateMyRank(env, c, r))

	default:
		return notImplemented(env, msg)
	}
}

// putRanking2Score records the submitted scores. This is the method that fills
// the board: without it the board can only ever be empty, whatever GetRanking
// does downstream.
func putRanking2Score(env *Env, c *prudplite.Connection, r *rmc.Reader) []byte {
	n, err := r.ListHeader()
	if err != nil || n > 256 { // the count comes off the network; bound it
		return nil
	}
	type submitted struct {
		category uint32
		score    uint32
		misc     uint64
	}
	scores := make([]submitted, 0, n)
	for range n {
		if _, _, err := r.StructHeader(); err != nil {
			return nil
		}
		category, err := r.U32()
		if err != nil {
			return nil
		}
		score, err := r.U32()
		if err != nil {
			return nil
		}
		misc, err := r.U64()
		if err != nil {
			misc = 0
		}
		scores = append(scores, submitted{category: category, score: score, misc: misc})
	}
	uniqueID, _ := r.U64()
	for _, s := range scores {
		env.r2common.submit(s.category, r2Entry{pid: c.PID, nexUniqueID: uniqueID, misc: s.misc, score: s.score})
	}
	return nil
}

// ranking2Info renders a category's board.
func ranking2Info(env *Env, r *rmc.Reader) []byte {
	category, offset, length := readRanking2GetParam(r)
	all := env.r2common.board(category)

	start := min(int(offset), len(all))
	end := len(all)
	if length > 0 && start+int(length) < end {
		end = start + int(length)
	}

	w := rmc.NewWriter()
	w.StructOf(1, func(w *rmc.Writer) {
		w.List(end - start)
		for i := start; i < end; i++ {
			e := all[i]
			common, _ := env.r2common.get(e.pid)
			w.StructOf(1, func(w *rmc.Writer) {
				w.U64(e.misc)
				w.U64(e.nexUniqueID)
				w.U64(e.pid)
				w.U32(uint32(i + 1)) // rank counts from one
				w.U32(e.score)
				common.write(w)
			})
		}
		w.U32(ranking2LowestRank)
		w.U32(uint32(len(all)))
		w.U32(0) // Season (i32)
	})
	return w.Bytes()
}

// readRanking2GetParam pulls the category and window out of the get parameter,
// defaulting to the whole board when the game names no window.
func readRanking2GetParam(r *rmc.Reader) (category, offset, length uint32) {
	if _, _, err := r.StructHeader(); err != nil {
		return 0, 0, 0
	}
	category, _ = r.U32()
	offset, _ = r.U32()
	length, _ = r.U32()
	return category, offset, length
}

// estimateMyRank answers where the caller sits. Our rank is exact — it is
// computed over the whole list — so the sampling rate is one; the real service
// estimates from a sample because it ranks millions of players.
func estimateMyRank(env *Env, c *prudplite.Connection, r *rmc.Reader) []byte {
	if _, _, err := r.StructHeader(); err != nil {
		return nil
	}
	category, _ := r.U32()
	all := env.r2common.board(category)

	rank := uint32(len(all) + 1) // unranked sits just behind the last
	var score uint32
	for i, e := range all {
		if e.pid == c.PID {
			rank, score = uint32(i+1), e.score
			break
		}
	}

	w := rmc.NewWriter()
	w.StructOf(1, func(w *rmc.Writer) {
		w.U32(rank)
		w.U32(uint32(len(all)))
		w.U32(score)
		w.U32(category)
		w.U32(0) // Season (i32)
		w.U8(1)  // SamplingRate: exact
	})
	return w.Bytes()
}

// ---------------------------------------------------------------------------
// MatchmakeReferee 0x78
// ---------------------------------------------------------------------------

// Referee method numbers. EndRound is 3, and 4 is the PARTIAL report — not the
// no-report variant, which is 5. Serving the no-report behaviour on 4 answers
// the wrong question and leaves 5 unanswered.
const (
	methodRefereeStartRound         uint32 = 1
	methodRefereeGetStartRoundParam uint32 = 2
	methodRefereeEndRound           uint32 = 3
	methodRefereeEndRoundPartial    uint32 = 4
	methodRefereeEndRoundNoReport   uint32 = 5
	methodRefereeRoundParticipants  uint32 = 6
	methodRefereeNotSummarizedRound uint32 = 7
	methodRefereeGetRound           uint32 = 8
	methodRefereeStatsPrimary       uint32 = 9
	methodRefereeStatsPrimaries     uint32 = 10
	methodRefereeStatsAll           uint32 = 11
	methodRefereeCreateStats        uint32 = 12
	methodRefereeGetOrCreateStats   uint32 = 13
	methodRefereeResetStats         uint32 = 14
)

type round struct {
	gid     uint32
	pids    []uint64
	params  []byte
	started time.Time
}

type roundStore struct {
	mu     sync.Mutex
	next   uint64
	rounds map[uint64]round
}

const roundLifetime = time.Hour

func (rs *roundStore) start(rd round) uint64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.rounds == nil {
		rs.rounds = map[uint64]round{}
	}
	for id, old := range rs.rounds {
		if time.Since(old.started) > roundLifetime {
			delete(rs.rounds, id)
		}
	}
	rs.next++
	rd.started = time.Now()
	rs.rounds[rs.next] = rd
	return rs.next
}

func (rs *roundStore) get(id uint64) (round, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rd, ok := rs.rounds[id]
	return rd, ok
}

func (rs *roundStore) end(id uint64) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.rounds[id]
	delete(rs.rounds, id)
	return ok
}

func refereeHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case methodRefereeStartRound:
		return startRound(env, c, msg, r)

	case methodRefereeGetStartRoundParam: // (u64 roundID) -> the StartRound body, echoed
		roundID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		rd, ok := env.referee.get(roundID)
		if !ok {
			return errorResp(msg, rmc.ResultRefereeRoundGone)
		}
		return success(msg, rd.params)

	case methodRefereeEndRound, methodRefereeEndRoundPartial:
		// (param{u64 roundID, list<results>}) — the results are read past.
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		roundID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		return closeRound(env, msg, roundID)

	case methodRefereeEndRoundNoReport: // (u64 roundID)
		roundID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		return closeRound(env, msg, roundID)

	case methodRefereeRoundParticipants: // (u64 roundID) -> list<PID>
		roundID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		rd, ok := env.referee.get(roundID)
		if !ok {
			return errorResp(msg, rmc.ResultRefereeRoundGone)
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.List(len(rd.pids))
			for _, pid := range rd.pids {
				w.U64(pid)
			}
			return w.Bytes()
		}())

	case methodRefereeNotSummarizedRound, methodRefereeStatsPrimaries, methodRefereeStatsAll:
		// Nothing is summarised and no stats are kept: an empty list is the
		// honest answer and the one the game moves on from.
		return emptyList(msg)

	case methodRefereeGetRound: // (u64 roundID) -> round record
		return success(msg, zeroStruct())

	case methodRefereeStatsPrimary, methodRefereeCreateStats, methodRefereeGetOrCreateStats:
		return success(msg, zeroStruct())

	case methodRefereeResetStats:
		return success(msg, nil)

	default:
		return notImplemented(env, msg)
	}
}

// startRound registers a round and tells the other participants it began. The
// game also learns by polling GetStartRoundParam, so the push is the fast path
// rather than the only one.
func startRound(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, r *rmc.Reader) []byte {
	if _, _, err := r.StructHeader(); err != nil {
		return errorResp(msg, rmc.ResultRefereeInvalidArg)
	}
	if _, err := r.U32(); err != nil { // personalDataCategory
		return errorResp(msg, rmc.ResultRefereeInvalidArg)
	}
	gid, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultRefereeInvalidArg)
	}
	pids, err := readPIDList(r)
	if err != nil || len(pids) == 0 {
		return errorResp(msg, rmc.ResultRefereeInvalidArg)
	}
	// Every named player must really sit in the claimed gathering.
	g, ok := env.Match.get(gid)
	if !ok {
		return errorResp(msg, rmc.ResultRefereeNotJoined)
	}
	for _, pid := range pids {
		if !g.participants[pid] {
			return errorResp(msg, rmc.ResultRefereeNotJoined)
		}
	}
	roundID := env.referee.start(round{gid: gid, pids: pids, params: msg.Body})
	log.Printf("[%s] referee: round %d started gid=%d pids=%d", env.Name, roundID, gid, len(pids))

	for _, pid := range pids {
		if pid == c.PID {
			continue
		}
		env.notify(pid, notificationEvent{
			SourcePID: c.PID,
			Type:      NotifyRefereeRoundStarted,
			Param1:    uint64(gid),
			Param2:    roundID,
		})
	}

	w := rmc.NewWriter()
	w.U64(roundID)
	return success(msg, w.Bytes())
}

func closeRound(env *Env, msg *rmc.RMCMessage, roundID uint64) []byte {
	if !env.referee.end(roundID) {
		return errorResp(msg, rmc.ResultRefereeRoundGone)
	}
	return success(msg, nil)
}

// zeroStruct is an empty versioned structure — a valid "nothing here" for a
// method whose answer is a structure. An empty BODY is a different thing and
// overruns the client's reader.
func zeroStruct() []byte {
	w := rmc.NewWriter()
	w.StructOf(1, func(*rmc.Writer) {})
	return w.Bytes()
}
