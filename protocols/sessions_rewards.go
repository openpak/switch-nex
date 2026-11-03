package protocols

// Online sessions for rewards: a player counts once they have shared a gathering with at least
// one other player for SessionMin of unbroken time. The title's own matchmaking store is the
// source, so it proves presence in an online lobby, not a finished match. One event per
// (gathering, player); the gathering id plus its creation time is the event id and the distinct
// key, because gathering ids restart with the process.

import (
	"context"
	"fmt"
	"os"
	"time"

	emit "github.com/openpak/rewards-emit"
)

// SessionMin is how long two players must share a gathering before it counts.
func SessionMin() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("REWARDS_SESSION_MIN")); err == nil && d > 0 {
		return d
	}
	return 3 * time.Minute
}

// sessionRooms copies each gathering's participants under the title's lock. The room id is the
// gathering id plus its creation time: gathering ids restart with the process.
func (e *Env) sessionRooms() []emit.Room {
	e.mu.Lock()
	defer e.mu.Unlock()
	var rooms []emit.Room
	if e.Match == nil {
		return rooms
	}
	for _, g := range e.Match.all() {
		r := emit.Room{ID: fmt.Sprintf("%d-%d", g.sess.GID, g.created.Unix())}
		for pid := range g.participants {
			r.Players = append(r.Players, emit.Identity{Namespace: "switch", Subject: fmt.Sprintf("%016x", pid)})
		}
		rooms = append(rooms, r)
	}
	return rooms
}

// WatchSessions reports this title's online sessions to rewards under metric (for example
// "kart_sessions.mk8dx"). The session clock is kept in the rewards-emit database when one is
// configured, so a restart loses no half-served session. It returns a stop function; with
// rewards unconfigured it does nothing.
func (e *Env) WatchSessions(metric string, min, every time.Duration) (stop func()) {
	if !e.Rewards.Enabled() {
		return func() {}
	}
	s := emit.NewSessions(e.Rewards, metric, min)
	e.Rewards.PersistSessions("sessions/"+metric, s)
	ctx, cancel := context.WithCancel(context.Background())
	s.Watch(ctx, every, e.sessionRooms)
	return cancel
}
