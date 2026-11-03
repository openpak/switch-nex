// Splatoon 2's Splatfest methods on Ranking (0x70).
//
// Layouts are from the public NintendoClients wiki, "Ranking Protocol (Splatoon 2)"
// (https://github.com/kinnay/NintendoClients/wiki/Ranking-Protocol-(Splatoon-2)), not from a
// capture of this server:
//
//	0x12 (18) GetFestivalResult(festivalId u32) -> FestivalResult
//	0x13 (19) UploadFestivalVote(FestivalUploadVoteParam{festivalId u32, themeId u8}) -> none
//	0x14 (20) UploadFestivalScore(FestivalUploadScoreParam{uniqueId u64, region u8, festivalId u32,
//	          teamId u8, score u32, battleGatheringId u32, battleResult u32, applicationBuffer qBuffer}) -> none
//	0x15 (21) DeleteFestival(festivalId u32) -> none
//
// FestivalResult is written as structure revision 0: festivalId u32, numParticipants u32,
// teamParticipants list<u32>, teamScores list<u32>, updatedTime DateTime. Revision 1 adds a u8 and
// widens the scores to u64; a client that knows revision 1 still reads a revision-0 structure by its
// header version. Which revision the Switch build asks for, and what the wiki's unnamed u8 means,
// are not established.
//
// NOT implemented: 0x19 (25) GetEventMatchResult and 0x18/0x1A (24/26). The wiki lists the names only;
// no request or response layout is public. They stay NotImplemented until a capture shows them
// (docs/splatoon2-fest.md).
//
// These ids collide with other titles' Ranking methods (0x12 GetCompetitionInfo, 0x13 SubmitTime),
// so everything here is gated on the title name.
//
// The team tallies answered to the game are in memory and reset on restart. Each player's summed
// contribution (the score field of 0x14) is also kept in Env.Scores, board "fest<id>", and served
// through the rankings contract; the first upload per (festival, player) is a splatfests_joined event.
package protocols

import (
	"fmt"
	"log"
	"sync"

	emit "github.com/openpak/rewards-emit"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
	"github.com/openpak/switch-nex/rmc"
)

const (
	methodRankingS2GetFestivalResult   uint32 = 0x12
	methodRankingS2UploadFestivalVote  uint32 = 0x13
	methodRankingS2UploadFestivalScore uint32 = 0x14
	methodRankingS2DeleteFestival      uint32 = 0x15

	fest2Teams    = 3  // slots in the result lists; a two-team fest leaves the third at zero
	fest2MaxFests = 64 // festivals tracked at once; the client sends ids off the network
)

type festTally struct {
	votes  map[uint64]uint8 // pid -> team voted for (one vote per player)
	scores [fest2Teams]uint64
}

type festivalStore struct {
	mu    sync.Mutex
	fests map[uint32]*festTally
}

func (s *festivalStore) tally(id uint32, create bool) *festTally {
	if s.fests == nil {
		s.fests = map[uint32]*festTally{}
	}
	t := s.fests[id]
	if t == nil && create && len(s.fests) < fest2MaxFests {
		t = &festTally{votes: map[uint64]uint8{}}
		s.fests[id] = t
	}
	return t
}

// festivalResult is the FestivalResult body for a fest (zeros when nothing is recorded).
func (s *festivalStore) festivalResult(id uint32) []byte {
	s.mu.Lock()
	var parts [fest2Teams]uint32
	var scores [fest2Teams]uint32
	if t := s.tally(id, false); t != nil {
		for _, team := range t.votes {
			parts[team]++
		}
		for i, v := range t.scores {
			if v > 0xffffffff {
				v = 0xffffffff
			}
			scores[i] = uint32(v)
		}
	}
	s.mu.Unlock()
	var total uint32
	for _, p := range parts {
		total += p
	}
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.U32(id)
		w.U32(total)
		w.List(fest2Teams)
		for _, p := range parts {
			w.U32(p)
		}
		w.List(fest2Teams)
		for _, v := range scores {
			w.U32(v)
		}
		w.U64(nowDateTime())
	})
	return w.Bytes()
}

// rankingSplatoon2 handles the Splatfest methods; ok is false for any other method.
func rankingSplatoon2(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) (resp []byte, ok bool) {
	r := rmc.NewReader(msg.Body)
	st := &env.festivals
	switch msg.MethodID {
	case methodRankingS2GetFestivalResult:
		id, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		return success(msg, st.festivalResult(id)), true

	case methodRankingS2UploadFestivalVote:
		_, in, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		id, e1 := in.U32()
		team, e2 := in.U8()
		if e1 != nil || e2 != nil || team >= fest2Teams {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		st.mu.Lock()
		if t := st.tally(id, true); t != nil {
			t.votes[c.PID] = team // a repeat vote replaces the earlier one
		}
		st.mu.Unlock()
		log.Printf("[%s] ranking: pid=%s festival %d vote team=%d", env.Name, redact.Tag(c.PID), id, team)
		return success(msg, nil), true

	case methodRankingS2UploadFestivalScore:
		_, in, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		_, e0 := in.U64()
		_, e1 := in.U8()
		id, e2 := in.U32()
		team, e3 := in.U8()
		score, e4 := in.U32()
		if e0 != nil || e1 != nil || e2 != nil || e3 != nil || e4 != nil || team >= fest2Teams {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		st.mu.Lock()
		if t := st.tally(id, true); t != nil {
			t.scores[team] += uint64(score)
		}
		st.mu.Unlock()
		keepFestivalScore(env, c.PID, id, score)
		// One Splatfest joined per (festival, player); the distinct key is the festival id.
		env.Rewards.Event(emit.Event{ID: fmt.Sprintf("s2:fest:%d:%d", id, c.PID), Metric: "splatfests_joined",
			Player: &emit.Identity{Namespace: "switch", Subject: fmt.Sprintf("%016x", c.PID)}, Keys: []string{fmt.Sprint(id)}})
		log.Printf("[%s] ranking: pid=%s festival %d score team=%d +%d", env.Name, redact.Tag(c.PID), id, team, score)
		return success(msg, nil), true

	case methodRankingS2DeleteFestival:
		id, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		st.mu.Lock()
		delete(st.fests, id)
		st.mu.Unlock()
		return success(msg, nil), true
	}
	return nil, false
}
