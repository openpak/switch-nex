package protocols

import (
	"bytes"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

func TestSessionRefreshAfterHostDeparturePreservesMemberKey(t *testing.T) {
	env := testEnv()
	key := bytes.Repeat([]byte{0x5a}, 32)
	s := &Session{OwnerPID: 10, HostPID: 10, MaxParticipants: 8, SessionKey: key, UserPassword: "room-password"}
	gid := env.Match.create(s)
	g, _ := env.Match.get(gid)
	g.participants[10] = true
	g.participants[20] = true
	s.ParticipationCount = 2
	leaveGathering(env, gid, g, 10)
	if s.OwnerPID != 20 || s.HostPID != 20 {
		t.Fatal("remaining participant did not become host")
	}
	for _, pid := range []uint64{20, 10, 30} {
		w := rmc.NewWriter()
		w.U32(gid)
		ok, body, code := decodeResponse(matchmakeExtHandler(env, conn(pid), request(0x6d, 0x31, w.Bytes())))
		if !ok {
			t.Fatalf("session refresh error %#x", code)
		}
		got, err := readSession(rmc.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if pid == 20 {
			if !bytes.Equal(got.SessionKey, key) || got.UserPassword != s.UserPassword {
				t.Fatal("host refresh lost session credentials")
			}
		} else if len(got.SessionKey) != 0 || got.UserPassword != "" {
			t.Fatal("former member or outsider acquired session credentials")
		}
	}
}
