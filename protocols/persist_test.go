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

// Published notification data and non-MK8D tournaments survive a restart, and a tournament id
// is never reused even after the newest one was deleted.
func TestRestartKeepsNotifAndTournaments(t *testing.T) {
	st := scorestore.NewMemory()
	a := &Env{Name: "mario-tennis", Scores: st}
	a.putNotifData(0xabc, notificationEvent{SourcePID: 0xabc, Type: 3, Param1: 1, Param2: 2, StrParam: "x", Param3: 4})
	t1 := a.tournaments.create(0xabc, 1, []byte{1, 2, 3})
	a.keepTournament(t1)
	t2 := a.tournaments.create(0xdef, 2, []byte{9})
	a.keepTournament(t2)
	a.tournaments.remove(t2.id, 0xdef)
	a.dropTournament(t2)

	b := &Env{Name: "mario-tennis", Scores: st}
	ev, ok := b.notifDataOf(0xabc, 3)
	if !ok || ev.StrParam != "x" || ev.Param3 != 4 || ev.SourcePID != 0xabc {
		t.Fatalf("notif = %+v %v", ev, ok)
	}
	all := b.tournaments.all()
	if len(all) != 1 || all[0].id != t1.id || all[0].owner != 0xabc || string(all[0].blob) != "\x01\x02\x03" {
		t.Fatalf("tournaments = %+v", all)
	}
	if n := b.tournaments.create(1, 1, []byte{1}); n.id <= t2.id {
		t.Fatalf("id %d reused (last handed out %d)", n.id, t2.id)
	}
}
