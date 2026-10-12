package protocols

import (
	"bytes"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// Minecraft asks for a friend's advertised gathering before joining it.
// Lookup must return the latest world data in a bool + GatheringHolder,
// without registering the reader as a participant.
func TestFindBySingleIDReturnsTheLiveWorld(t *testing.T) {
	env := testEnv()
	host, joiner := conn(100), conn(200)
	_, body, code := decodeResponse(Dispatch(env, host,
		request(ProtoMatchmakeExt, 0x06, sessionHolder(sampleSession()))))
	if code != 0 {
		t.Fatalf("create: %#x", code)
	}
	created, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	worldData := []byte{1, 2, 3, 4}
	update := rmc.NewWriter()
	update.U32(created.GID)
	update.Buffer(worldData)
	Dispatch(env, host, request(ProtoMatchmakeExt, 0x0B, update.Bytes()))

	_, body, code = decodeResponse(Dispatch(env, joiner,
		request(ProtoMatchMaking, 0x15, u32(created.GID))))
	if code != 0 {
		t.Fatalf("find existing world: %#x", code)
	}
	r := rmc.NewReader(body)
	found, err := r.Bool()
	if err != nil || !found {
		t.Fatalf("lookup result: %v, %v", found, err)
	}
	class, err := r.String()
	if err != nil || class != "MatchmakeSession" {
		t.Fatalf("holder class: %q, %v", class, err)
	}
	size, err := r.U32()
	if err != nil || int(size) != r.Remaining() {
		t.Fatalf("holder size: %d, remaining=%d, %v", size, r.Remaining(), err)
	}
	payload, err := r.Buffer()
	if err != nil || r.Remaining() != 0 {
		t.Fatalf("holder payload: %v, trailing=%d", err, r.Remaining())
	}
	foundSession, err := readSession(rmc.NewReader(payload))
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if foundSession.GID != created.GID || foundSession.HostPID != host.PID ||
		!bytes.Equal(foundSession.ApplicationBuffer, worldData) {
		t.Fatal("lookup lost the world's identity or its latest application data")
	}
	g, _ := env.Match.get(created.GID)
	if g.participants[joiner.PID] {
		t.Fatal("lookup joined the reader to the world")
	}

	// The same privacy policy applies as on the detail-find path, and the
	// original session stays intact for a subsequent join.
	env.SanitizeDetailFind = true
	_, body, code = decodeResponse(Dispatch(env, joiner,
		request(ProtoMatchMaking, 0x15, u32(created.GID))))
	if code != 0 {
		t.Fatalf("sanitized lookup: %#x", code)
	}
	sanitized, err := readSessionFromHolder(rmc.NewReader(body[1:]))
	if err != nil || len(sanitized.SessionKey) != 0 || sanitized.UserPassword != "" {
		t.Fatalf("sanitized lookup: %v", err)
	}
	if !bytes.Equal(g.sess.SessionKey, created.SessionKey) || g.sess.UserPassword != created.UserPassword {
		t.Fatal("sanitizing a lookup changed the live world")
	}

	env.Match.delete(created.GID)
	_, _, code = decodeResponse(Dispatch(env, joiner,
		request(ProtoMatchMaking, 0x15, u32(created.GID))))
	if code != rmc.ResultSessionVoid {
		t.Fatalf("deleted world: %#x, want SessionVoid", code)
	}
	_, _, code = decodeResponse(Dispatch(env, joiner,
		request(ProtoMatchMaking, 0x15, []byte{1, 2, 3})))
	if code != rmc.ResultInvalidArgument {
		t.Fatalf("truncated lookup: %#x, want InvalidArgument", code)
	}
}
