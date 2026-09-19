package protocols

import "testing"

// TestAutoMatchmakeTogetherAndPasswords: two players searching back to back
// share one room (Kirby's Dream Buffet hosted two before open-at-create), and
// a password room takes only searchers with the same password.
func TestAutoMatchmakeTogetherAndPasswords(t *testing.T) {
	e := NewEnv("kirbys-dream-buffet", "10.0.0.1", 22380)
	e.Match = NewMatchmakingStore()
	e.ForceOpenParticipation = true
	search := func(pid uint64, password string) uint32 {
		sess := &Session{MaxParticipants: 4, UserPassword: password}
		autoMatchmake(e, conn(pid), request(ProtoMatchmakeExt, 0x28, nil), sess)
		return e.Match.sessionsOf(pid)[0].GID
	}

	private := search(1, "1234")
	if a, b := search(2, ""), search(3, ""); a != b || a == private {
		t.Fatalf("random searchers: gid %d and %d, private room %d", a, b, private)
	}
	if got := search(4, "1234"); got != private {
		t.Fatalf("password searcher got gid %d, want %d", got, private)
	}
}

// TestAutoMatchmakeJoinerLearnsTheRoom: the joiner is announced to host and
// itself, and hears one Participate naming the host.
func TestAutoMatchmakeJoinerLearnsTheRoom(t *testing.T) {
	e := NewEnv("kirbys-dream-buffet", "10.0.0.1", 22380)
	e.Match = NewMatchmakingStore()
	e.ForceOpenParticipation = true
	got := map[uint64][]uint64{} // recipient -> Param2 of each Participate
	e.sent = func(pid uint64, ev notificationEvent) {
		if ev.Type == NotifyParticipate {
			got[pid] = append(got[pid], ev.Param2)
		}
	}
	autoMatchmake(e, conn(1), request(ProtoMatchmakeExt, 0x28, nil), &Session{MaxParticipants: 4})
	got = map[uint64][]uint64{}
	autoMatchmake(e, conn(2), request(ProtoMatchmakeExt, 0x28, nil), &Session{MaxParticipants: 4})

	if len(got[1]) != 1 || got[1][0] != 2 {
		t.Fatalf("host heard %v, want [2]", got[1])
	}
	if len(got[2]) != 2 || !(got[2][0] == 2 && got[2][1] == 1 || got[2][0] == 1 && got[2][1] == 2) {
		t.Fatalf("joiner heard %v, want itself and the host", got[2])
	}
}
