package protocols

import (
	"slices"
	"testing"
)

func TestGatheringOfAnswersTheNewestGatheringWithItsMembers(t *testing.T) {
	e := NewEnv("splatoon-2", "127.0.0.1", 22320)
	e.Match = NewMatchmakingStore()

	lobby := e.Match.create(&Session{})
	match := e.Match.create(&Session{})
	other := e.Match.create(&Session{})
	g, _ := e.Match.get(lobby)
	g.participants[1] = true
	g.participants[2] = true
	g, _ = e.Match.get(match)
	g.participants[1] = true
	g.participants[3] = true
	g.participants[4] = false // left: not a member
	g, _ = e.Match.get(other)
	g.participants[9] = true

	v, ok := e.GatheringOf(1)
	if !ok || v.GID != match {
		t.Fatalf("GatheringOf(1) = %+v %v, want gid %d", v, ok, match)
	}
	slices.Sort(v.Participants)
	if !slices.Equal(v.Participants, []uint64{1, 3}) {
		t.Fatalf("participants = %v, want [1 3]", v.Participants)
	}

	if _, ok := e.GatheringOf(5); ok {
		t.Fatal("a pid in no gathering was answered")
	}
	e.Disconnected(9)
	if _, ok := e.GatheringOf(9); ok {
		t.Fatal("a disconnected pid is still in its gathering")
	}
	if _, ok := (&Env{}).GatheringOf(1); ok {
		t.Fatal("a title without matchmaking answered")
	}
}
