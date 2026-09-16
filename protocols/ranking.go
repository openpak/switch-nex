// Ranking (0x70) — the common-data profile store and the competition surface.
//
//	0x04 UploadCommonData        -> empty; the profile blob is kept per player
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
package protocols

import (
	"sync"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// Ranking method numbers.
const (
	methodRankingUploadCommonData    uint32 = 0x04
	methodRankingCompetitionRanking  uint32 = 0x10
	methodRankingSubmitScore         uint32 = 0x11
	methodRankingGetCompetitionInfo  uint32 = 0x12
	methodRankingSubmitTime          uint32 = 0x13
	methodRankingCommonDataByPIDList uint32 = 0x16
)

// commonDataMaxEntries bounds a by-PID request. The count arrives off the
// network; without a bound an absurd one loops for a very long time over a body
// a few bytes long.
const commonDataMaxEntries = 512

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
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case methodRankingUploadCommonData: // (Buffer commonData, …) -> empty
		if blob, err := r.Buffer(); err == nil {
			env.ranking.put(c.PID, blob)
		}
		return success(msg, nil)

	case methodRankingCommonDataByPIDList:
		return success(msg, commonDataByPIDs(env, msg.Body))

	case methodRankingSubmitScore, methodRankingSubmitTime:
		// A deposit is acknowledged with one zero byte, not an empty body.
		return success(msg, []byte{0})

	case methodRankingGetCompetitionInfo, methodRankingCompetitionRanking:
		return emptyList(msg)

	default:
		return notImplemented(env, msg)
	}
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
	w := rmc.NewWriter()
	w.List(len(pids))
	for _, pid := range pids {
		w.Buffer(env.ranking.get(pid)) // absent renders as a zero-length blob
	}
	return w.Bytes()
}
