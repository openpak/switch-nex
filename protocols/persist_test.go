package protocols

import (
	"testing"

	"github.com/openpak/switch-nex/internal/scorestore"
)

// A new Env on the same store (a restart) sees the boards, common data and Splatfest tallies.
func TestRestartKeepsRankingState(t *testing.T) {
	st := scorestore.NewMemory()
	a := &Env{Name: "splatoon-2", Scores: st}
	a.hydrate()
	if a.r2common.submit(7, r2Entry{pid: 1, score: 50, misc: 3, nexUniqueID: 9}) {
		a.keepR2Board(7, r2Entry{pid: 1, score: 50, misc: 3, nexUniqueID: 9})
	}
	c := r2Common{userName: "ann", mii: []byte{1, 2}, binaryData: []byte{3}}
	a.keepR2Common(1, c)
	a.ranking.put(a, 2, []byte("blob"))
	a.keepFestVote(4, 1, 2)
	a.keepFestSum(4, [fest2Teams]uint64{5, 6, 7})

	b := &Env{Name: "splatoon-2", Scores: st}
	b.hydrate()
	if got := b.r2common.board(7); len(got) != 1 || got[0].score != 50 || got[0].misc != 3 || got[0].nexUniqueID != 9 {
		t.Fatalf("board = %+v", got)
	}
	if got, ok := b.r2common.get(1); !ok || got.userName != "ann" || len(got.mii) != 2 {
		t.Fatalf("common = %+v", got)
	}
	if string(b.ranking.get(2)) != "blob" {
		t.Fatal("ranking common data lost")
	}
	tl := b.festivals.tally(4, false)
	if tl == nil || tl.votes[1] != 2 || tl.scores != [fest2Teams]uint64{5, 6, 7} {
		t.Fatalf("tally = %+v", tl)
	}
}
