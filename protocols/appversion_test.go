package protocols

import (
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// TestVersionsNeverShareAMatch: auto-matchmake, browse and join-by-id keep
// two title versions apart, while an unknown version (no token, or nx-baas's
// non-hex default) still meets everyone.
func TestVersionsNeverShareAMatch(t *testing.T) {
	e := NewEnv("kirbys-dream-buffet", "10.0.0.1", 22380)
	e.Match = NewMatchmakingStore()
	e.ForceOpenParticipation = true
	e.SetAppVersion(1, "00150000")
	e.SetAppVersion(2, "00150000")
	e.SetAppVersion(3, "00140000")
	e.SetAppVersion(4, "0033") // not a version: unknown
	search := func(pid uint64) uint32 {
		sess := &Session{MaxParticipants: 8}
		autoMatchmake(e, conn(pid), request(ProtoMatchmakeExt, 0x28, nil), sess, nil)
		return e.Match.sessionsOf(pid)[0].GID
	}

	newer := search(1)
	if got := search(2); got != newer {
		t.Fatalf("same version: gid %d, want %d", got, newer)
	}
	older := search(3)
	if older == newer {
		t.Fatal("an older version was matched into the newer room")
	}
	if got := search(4); got != newer && got != older {
		t.Fatalf("unknown version made its own room %d", got)
	}

	for pid, want := range map[uint64]uint32{2: newer, 3: older} {
		open := e.sameVersionOpen(pid)
		for _, s := range open {
			if g, _ := e.Match.get(s.GID); g.version != e.AppVersion(pid) {
				t.Errorf("pid %d browses gid %d of version %s", pid, s.GID, g.version)
			}
		}
		if len(open) == 0 || open[0].GID != want && (len(open) < 2 || open[1].GID != want) {
			t.Errorf("pid %d does not see its own version's room %d", pid, want)
		}
	}

	join := func(pid uint64, gid uint32) uint32 {
		w := rmc.NewWriter()
		w.U32(gid)
		w.String("")
		_, _, code := decodeResponse(Dispatch(e, conn(pid), request(ProtoMatchmakeExt, 0x07, w.Bytes())))
		return code
	}
	e.SetAppVersion(5, "00140000")
	if code := join(5, newer); code != rmc.ResultSessionVoid {
		t.Fatalf("older version joined the newer room by id: code %#08x", code)
	}
	if code := join(5, older); code != 0 {
		t.Fatalf("same version refused by id: code %#08x", code)
	}
	if code := join(6, newer); code != 0 { // never logged in with a version
		t.Fatalf("unknown version refused by id: code %#08x", code)
	}
}
