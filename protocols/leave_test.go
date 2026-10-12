package protocols

import (
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// leaveEnv wires the behaviour set the first-batch profiles share for the
// session lifecycle: normalized creation plus recapped joins.
func leaveEnv() *Env {
	e := NewEnv("leave-test", "10.0.0.1", 23080)
	e.Match = NewMatchmakingStore()
	e.FinalizeCreateSession = true
	e.JoinEchoSession = true
	e.JoinRecapParticipate = true
	return e
}

func leaveSession() *Session {
	return &Session{
		MaxParticipants:     2,
		ParticipationPolicy: 3,
		Description:         "leave test lobby",
		GameMode:            77,
		MatchmakeSystemType: 1,
	}
}

func mustCreate(t *testing.T, env *Env, pid uint64) uint32 {
	t.Helper()
	resp := Dispatch(env, conn(pid), request(ProtoMatchmakeExt, 0x06, sessionHolder(leaveSession())))
	_, body, errCode := decodeResponse(resp)
	if errCode != 0 {
		t.Fatalf("create failed: %#x", errCode)
	}
	created, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatalf("create body: %v", err)
	}
	return created.GID
}

func mustJoin(t *testing.T, env *Env, pid uint64, gid uint32) {
	t.Helper()
	resp := Dispatch(env, conn(pid), request(ProtoMatchmakeExt, 0x07, u32(gid)))
	_, _, errCode := decodeResponse(resp)
	if errCode != 0 {
		t.Fatalf("join failed: %#x", errCode)
	}
}

// TestCancelParticipationKeepsBookkeeping pins the leave contract on the
// Participate/CancelParticipation pair (0x15.0x0B/0x0C): Cancel used to drop
// only the map entry — the count stayed up, nobody was told, ownership never
// moved — so rooms stayed browsable after everyone had gone. (Note: the
// 0x6D.0x0C in the PICO PARK 2026-10-12 trace is a different method,
// UpdateMatchmakeSessionAttribute, not a leave.)
func TestCancelParticipationKeepsBookkeeping(t *testing.T) {
	env := leaveEnv()
	gid := mustCreate(t, env, 100)
	mustJoin(t, env, 200, gid)

	// Host cancels its participation, then sends the trailing EndParticipation
	// the console emits on lobby exit: the first call does the whole job, the
	// second is a no-op rather than a double decrement.
	Dispatch(env, conn(100), request(ProtoMatchMaking, 0x0C, u32(gid)))
	g, ok := env.Match.get(gid)
	if !ok {
		t.Fatal("gathering gone after CancelParticipation with one player still in it")
	}
	if g.sess.ParticipationCount != 1 {
		t.Fatalf("count=%d after host CancelParticipation, want 1", g.sess.ParticipationCount)
	}
	if g.participants[100] {
		t.Fatal("host still listed after CancelParticipation")
	}
	if g.sess.OwnerPID != 200 {
		t.Fatalf("owner=%d after host leave, want the remaining joiner 200", g.sess.OwnerPID)
	}
	Dispatch(env, conn(100), request(ProtoMatchMakingExt, 0x01, u32(gid)))
	g, ok = env.Match.get(gid)
	if !ok {
		t.Fatal("gathering gone while the joiner is still in it")
	}
	if g.sess.ParticipationCount != 1 {
		t.Fatalf("count=%d after duplicate leave, want 1", g.sess.ParticipationCount)
	}
	Dispatch(env, conn(100), request(ProtoMatchMakingExt, 0x01, u32(gid)))

	// The trailing EndParticipation is a no-op for the already-gone host, not
	// a second decrement.
	g, ok = env.Match.get(gid)
	if !ok {
		t.Fatal("gathering gone while the joiner is still in it")
	}
	if g.sess.ParticipationCount != 1 {
		t.Fatalf("count=%d after duplicate leave, want 1", g.sess.ParticipationCount)
	}

	// The last one out tears the room down: nothing left to browse.
	Dispatch(env, conn(200), request(ProtoMatchMakingExt, 0x01, u32(gid)))
	if _, ok := env.Match.get(gid); ok {
		t.Fatal("gathering survives after the last participant left")
	}
}

// TestDisconnectEmptiesGathering covers the quiet path: both consoles go away
// without an explicit leave and the room must not outlive them.
func TestDisconnectEmptiesGathering(t *testing.T) {
	env := leaveEnv()
	gid := mustCreate(t, env, 100)
	mustJoin(t, env, 200, gid)

	env.Disconnected(100)
	if _, ok := env.Match.get(gid); !ok {
		t.Fatal("gathering gone while the joiner is still connected")
	}
	env.Disconnected(200)
	if _, ok := env.Match.get(gid); ok {
		t.Fatal("gathering survives after every participant disconnected")
	}
}
