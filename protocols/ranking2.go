// Ranking2 (0x7A) and MatchmakeReferee (0x78) — Super Mario Bros. 35's two
// extra protocols, ported behaviour-for-behaviour from the OpenPak SMB35
// server (servers/nex/titles/super-mario-bros-35/protocols.go):
//
// Ranking2: common data (player name + Mii) keyed by PID and NEX unique ID,
// missing entries answer Ranking2::InvalidArgument (the game reads it as
// "nothing yet"); an always-empty board with LowestRank 10000; one category
// setting for every category; zeroed rank estimates echoing the category.
//
// MatchmakeReferee: in-memory rounds with a one-hour lifetime; StartRound
// validates that every named participant really sits in the claimed
// gathering; GetStartRoundParam echoes the raw StartRound request body
// (the Eagle-variant trailing fields the store never decodes ride along).
//
// Deviation from the ported source: the round-started notification push
// (type 116000) is not sent — switch-nex's transport has no server-initiated
// DATA yet; the game learns of rounds by polling, as with PAC-MAN 99 where
// the reference's push was measured never honoured.
package protocols

import (
	"log"
	"sync"
	"time"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

type r2Common struct {
	userName   string
	mii        []byte
	binaryData []byte
}

type r2CommonStore struct {
	mu   sync.Mutex
	data map[uint64]map[uint64]r2Common
}

func (s *r2CommonStore) get(pid, uniqueID uint64) (r2Common, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		return r2Common{}, false
	}
	c, ok := s.data[pid][uniqueID]
	return c, ok
}

func (s *r2CommonStore) put(pid, uniqueID uint64, c r2Common) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[uint64]map[uint64]r2Common{}
	}
	if s.data[pid] == nil {
		s.data[pid] = map[uint64]r2Common{}
	}
	s.data[pid][uniqueID] = c
}

func ranking2Handler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01: // PutScore -> empty
		return success(msg, nil)

	case 0x02: // GetCommonData(u32 optionFlags, PID, u64 uniqueID) -> Ranking2CommonData
		if _, err := r.U32(); err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		pid, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		uniqueID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		common, ok := env.r2common.get(pid, uniqueID)
		if !ok {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.String(common.userName)
				w.Buffer(common.mii)
				w.Buffer(common.binaryData)
			})
			return w.Bytes()
		}())

	case 0x03: // PutCommonData(Ranking2CommonData{userName, mii, binary}, u64 uniqueID)
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		userName, err := r.String()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		mii, err := r.Buffer()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		binaryData, err := r.Buffer()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		uniqueID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		env.r2common.put(c.PID, uniqueID, r2Common{userName: userName, mii: mii, binaryData: binaryData})
		return success(msg, nil)

	case 0x04: // DeleteCommonData
		return success(msg, nil)

	case 0x05: // GetRanking(Ranking2GetParam) -> Ranking2Info{list, lowestRank, numRankedIn, season}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.EmptyList()
				w.U32(10000) // LowestRank — the reference's empty-board constant
				w.U32(0)     // NumRankedIn
				w.U32(0)     // Season
			})
			return w.Bytes()
		}())

	case 0x06: // GetRankingByPrincipalID -> empty Ranking2Info
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.EmptyList()
				w.U32(10000)
				w.U32(0)
				w.U32(0)
			})
			return w.Bytes()
		}())

	case 0x07: // GetCategorySetting(u32) -> fixed setting (the reference answered one setting for all)
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.U32(0)         // MinScore
				w.U32(999999999) // MaxScore
				w.U32(10000)     // LowestRank
				w.U32(4095)      // ResetMonth: all twelve months
				w.U8(0)          // ResetDay
				w.U8(0)          // ResetHour
				w.U8(2)          // ResetMode
				w.U8(3)          // MaxSeasonsToGoBack
				w.Bool(false)    // ScoreOrder ascending
			})
			return w.Bytes()
		}())

	case 0x0A: // GetEstimateScoreRank(input{category u32, seasonsBack u8, score u32}) -> zeros echoing category
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		category, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultRanking2InvalidArg)
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.U32(0)        // Rank
				w.U32(0)        // Length
				w.U32(0)        // Score
				w.U32(category) // Category (echoed)
				w.U32(0)        // Season (i32)
				w.U8(0)         // SamplingRate
			})
			return w.Bytes()
		}())

	default:
		return notImplemented(env, msg)
	}
}

// ---------------------------------------------------------------------------
// MatchmakeReferee 0x78
// ---------------------------------------------------------------------------

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
	case 0x01: // StartRound(param{u32 personalDataCategory, u32 gid, list<PID>}[+eagle trailing])
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
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U64(roundID)
			return w.Bytes()
		}())

	case 0x02: // GetStartRoundParam(u64 roundID) -> echo of the StartRound request body
		roundID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		rd, ok := env.referee.get(roundID)
		if !ok {
			return errorResp(msg, rmc.ResultRefereeRoundGone)
		}
		return success(msg, rd.params)

	case 0x03: // EndRound(param{u64 roundID, list<results>}) — results read past
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		roundID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		return closeRound(env, msg, roundID)

	case 0x04: // EndRoundWithoutReport(u64 roundID)
		roundID, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultRefereeInvalidArg)
		}
		return closeRound(env, msg, roundID)

	default:
		return notImplemented(env, msg)
	}
}

func closeRound(env *Env, msg *rmc.RMCMessage, roundID uint64) []byte {
	if !env.referee.end(roundID) {
		return errorResp(msg, rmc.ResultRefereeRoundGone)
	}
	return success(msg, nil)
}
