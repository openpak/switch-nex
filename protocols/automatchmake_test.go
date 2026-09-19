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
