package protocols

import (
	"encoding/hex"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// TestAutoMatchmakeTogetherAndPasswords: two players searching back to back
// share one room (Kirby's Dream Buffet hosted two before open-at-create), and
// a password room takes only searchers with the same password.
func TestAutoMatchmakeTogetherAndPasswords(t *testing.T) {
	e := NewEnv("kirbys-dream-buffet", "10.0.0.1", 22380)
	e.Match = NewMatchmakingStore()
	e.ForceOpenParticipation = true
	search := func(pid uint64, password string) uint32 {
		sess := &Session{MaxParticipants: 4, UserPassword: password}
		autoMatchmake(e, conn(pid), request(ProtoMatchmakeExt, 0x28, nil), sess, nil)
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

// TestAutoMatchmakeParsesGolfCriteria: a captured console 0x28 body decodes
// to the source session plus the search criteria the room is matched on.
func TestAutoMatchmakeParsesGolfCriteria(t *testing.T) {
	body, _ := hex.DecodeString("0051010000002b0000000000000000000000000000000000000000000000010004005f00000000000000100a000000000000010000007300000002000000060000000300000000000000030100000601000000000000c56b00400001000000000000000000000064000000000000000000240000000300000001000004010000040040525600060000000000000000060040557347490003010000000000000000010000000000000000010000000000000000000000000000010000010001000000008200000006000000020033000100000400323539000100000100000b0031303733373639343133000200320001000001000002003100010101050000000100002c00000003000000040040525600060000000000000000040040534900060200000000000000060040557347490003010101000000000100000008000000ffffffff6400000000000000000400000003000000")
	r := rmc.NewReader(body)
	if _, _, err := r.StructHeader(); err != nil {
		t.Fatal(err)
	}
	sess, err := readSession(r)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if sess.GameMode != 2 || len(sess.Attributes) != 6 || sess.Attributes[2] != 259 {
		t.Fatalf("session = gameMode %d attrs %v", sess.GameMode, sess.Attributes)
	}
	criteria, err := readSearchCriteriaList(r)
	if err != nil {
		t.Fatalf("criteria: %v", err)
	}
	if len(criteria) != 1 {
		t.Fatalf("%d criteria", len(criteria))
	}
	c := criteria[0]
	if c.GameMode != "2" || len(c.Attribs) != 6 || c.Attribs[0] != "3" || c.Attribs[2] != "259" || c.Attribs[5] != "1073769413" {
		t.Fatalf("criteria = gameMode %q attribs %q", c.GameMode, c.Attribs)
	}
}

// reverted with the attribute check — same-mode searches vary their
// attributes, so it separated everything. It returns with the real
// mode-marker fix, once the template log names the field.
// TestAutoMatchmakeMatchesModeAttribute: with MatchModeAttribute (Golf), a
// room takes searchers naming its primary option and turns away the rest —
// while the later per-player attributes never decide. Without criteria (the
// pre-param path) everything meets, as before.
func TestAutoMatchmakeMatchesModeAttribute(t *testing.T) {
	e := NewEnv("mario-golf-super-rush", "10.0.0.1", 22130)
	e.Match = NewMatchmakingStore()
	e.ForceOpenParticipation = true
	e.MatchModeAttribute = true
	host := &Session{MaxParticipants: 8, GameMode: 2, Attributes: []uint32{3, 0, 259, 262, 0, 1073769413}}
	autoMatchmake(e, conn(1), request(ProtoMatchmakeExt, 0x28, nil), host, []*searchCriteria{{
		Attribs: []string{"3", "", "259", "", "", "1073769413"}, GameMode: "2",
	}})
	gid := e.Match.sessionsOf(1)[0].GID
	search := func(pid uint64, crit *searchCriteria) uint32 {
		sess := &Session{MaxParticipants: 8, GameMode: 2, Attributes: []uint32{1, 0, 320, 1, 0, 1073769413}}
		autoMatchmake(e, conn(pid), request(ProtoMatchmakeExt, 0x28, nil), sess, []*searchCriteria{crit})
		return e.Match.sessionsOf(pid)[0].GID
	}

	// Same primary option meets, whatever the player's own later settings.
	if got := search(2, &searchCriteria{Attribs: []string{"3", "", "999", "", "", "0"}, GameMode: "2"}); got != gid {
		t.Fatalf("same-option searcher got gid %d, want %d", got, gid)
	}
	// Another primary option opens its own room…
	other := search(3, &searchCriteria{Attribs: []string{"1", "", "320", "", "", "1073769413"}, GameMode: "2"})
	if other == gid {
		t.Fatalf("other-option searcher joined gid %d", gid)
	}
	// …which takes its own kind, including ranges.
	if got := search(4, &searchCriteria{Attribs: []string{"0,1", "", "", "", "", ""}, GameMode: "2"}); got != other {
		t.Fatalf("range searcher got gid %d, want %d", got, other)
	}
	// No criteria (pre-param path) matches everything: it joins one of the
	// two open rooms (map order decides which).
	sess := &Session{MaxParticipants: 8}
	autoMatchmake(e, conn(5), request(ProtoMatchmakeExt, 0x03, nil), sess, nil)
	if got := e.Match.sessionsOf(5)[0].GID; got != gid && got != other {
		t.Fatalf("criterialess searcher got gid %d, want %d or %d", got, gid, other)
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
	autoMatchmake(e, conn(1), request(ProtoMatchmakeExt, 0x28, nil), &Session{MaxParticipants: 4}, nil)
	got = map[uint64][]uint64{}
	autoMatchmake(e, conn(2), request(ProtoMatchmakeExt, 0x28, nil), &Session{MaxParticipants: 4}, nil)

	if len(got[1]) != 1 || got[1][0] != 2 {
		t.Fatalf("host heard %v, want [2]", got[1])
	}
	if len(got[2]) != 2 || !(got[2][0] == 2 && got[2][1] == 1 || got[2][0] == 1 && got[2][1] == 2) {
		t.Fatalf("joiner heard %v, want itself and the host", got[2])
	}
}
