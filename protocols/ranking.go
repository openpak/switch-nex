// Ranking (0x70) — in-memory scores and common data. UploadScore keeps the
// better score per (category, PID) the way the Mario Tennis Aces store ruled
// on the working reference; GetRanking answers an empty board plus real
// common data. The Splatoon 2 splatfest methods answer empty lists, matching
// the measured zero-byte reference answers.
package protocols

import (
	"sync"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

type scoreKey struct {
	category uint32
	pid      uint64
}

type scoreEntry struct {
	score uint32
	param uint64
}

type rankingStore struct {
	mu     sync.Mutex
	scores map[scoreKey]scoreEntry
	common map[uint64][]byte
	better func(old, new uint32) bool // category ordering: lower-better vs higher-better
}

func (rs *rankingStore) upload(pid, category, score, param uint64, updateMode uint8) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.scores == nil {
		rs.scores = map[scoreKey]scoreEntry{}
	}
	k := scoreKey{category: uint32(category), pid: pid}
	old, ok := rs.scores[k]
	// UpdateMode 2 = UpdateModeDeleteOld: forced overwrite (MTA measured rule).
	if !ok || updateMode == 2 || rs.betterIfBetter(old.score, uint32(score)) {
		rs.scores[k] = scoreEntry{score: uint32(score), param: param}
	}
}

func (rs *rankingStore) betterIfBetter(old, next uint32) bool {
	if rs.better != nil {
		return rs.better(old, next)
	}
	return next > old // default: higher is better
}

func (rs *rankingStore) putCommon(pid uint64, blob []byte) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.common == nil {
		rs.common = map[uint64][]byte{}
	}
	rs.common[pid] = blob
}

func (rs *rankingStore) getCommon(pid uint64) ([]byte, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	blob, ok := rs.common[pid]
	return blob, ok
}

func rankingHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01: // UploadScore(RankingScoreData{category u32, score u32, orderBy u32, updateMode u32, groups buf, param u64, ...})
		// ScoreData is a structure: header, then category, score, orderBy,
		// updateMode, groups, param.
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		category, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		score, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if _, err := r.U32(); err != nil { // orderBy
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		updateMode, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if _, err := r.Buffer(); err != nil { // groups
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		param, err := r.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		env.ranking.upload(c.PID, uint64(category), uint64(score), param, uint8(updateMode))
		return success(msg, nil)

	case 0x02, 0x03, 0x07, 0x08: // DeleteScore(s), ChangeAttributes
		return success(msg, nil)

	case 0x04: // UploadCommonData(PID?, buffer, DateTime) — buffer is the last field we keep
		return success(msg, uploadCommonData(env, c, r))

	case 0x06: // GetCommonData -> buffer (the reference answered NotFound when absent;
		// MK8D reads its racer name/Mii blob from here)
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			if blob, ok := env.ranking.getCommon(c.PID); ok {
				w.Buffer(blob)
			} else {
				w.Buffer(nil)
			}
			return w.Bytes()
		}())

	case 0x05: // DeleteCommonData
		env.ranking.putCommon(c.PID, nil)
		return success(msg, nil)

	case 0x09, 0x0C, 0x0D: // GetRanking / ByPIDList / ByUniqueIDList -> RankingResult{list, total, sinceTime}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.EmptyList()
				w.U32(0)
				w.U64(0)
			})
			return w.Bytes()
		}())

	case 0x0A: // GetApproxOrder -> u32
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U32(0)
			return w.Bytes()
		}())

	case 0x12, 0x13, 0x19: // GetFestivalResult, fest ranking, GetEventMatchResult (S2 splatfest)
		return emptyList(msg)

	default:
		return notImplemented(env, msg)
	}
}

// uploadCommonData stores the trailing buffer from an UploadScoreCommonData
// style request: parse best-effort, keep the last buffer seen.
func uploadCommonData(env *Env, c *prudplite.Connection, r *rmc.Reader) []byte {
	var last []byte
	for r.Remaining() > 4 {
		buf, err := r.Buffer()
		if err != nil {
			break
		}
		last = buf
	}
	env.ranking.putCommon(c.PID, last)
	return nil
}
