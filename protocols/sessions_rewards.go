package protocols

// Online sessions for rewards: a player counts once they have shared a gathering with at least
// one other player for SessionMin of unbroken time. The title's own matchmaking store is the
// source, so it proves presence in an online lobby, not a finished match. One event per
// (gathering, player); the gathering id plus its creation time is the event id and the distinct
// key, because gathering ids restart with the process.

import (
	"fmt"
	"os"
	"sort"
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

type sessionPair struct {
	gid     string
	pid     uint64
	created time.Time
}

type sessionClock struct {
	min     time.Duration
	since   map[string]time.Time
	emitted map[string]bool
}

func newSessionClock(min time.Duration) *sessionClock {
	return &sessionClock{min: min, since: map[string]time.Time{}, emitted: map[string]bool{}}
}

// tick takes the gatherings now (key -> participants) and returns the pairs that just crossed
// the minimum. A gathering with fewer than two players resets its players.
func (c *sessionClock) tick(now time.Time, gs map[string][]uint64, created map[string]time.Time) (done []sessionPair) {
	seen := map[string]bool{}
	for g, pids := range gs {
		if len(pids) < 2 {
			continue
		}
		for _, pid := range pids {
			k := fmt.Sprintf("%s/%d", g, pid)
			seen[k] = true
			if _, ok := c.since[k]; !ok {
				c.since[k] = now
			}
			if !c.emitted[k] && now.Sub(c.since[k]) >= c.min {
				c.emitted[k] = true
				done = append(done, sessionPair{g, pid, created[g]})
			}
		}
	}
	for k := range c.since {
		if !seen[k] {
			delete(c.since, k)
		}
	}
	for k := range c.emitted {
		if !seen[k] {
			delete(c.emitted, k)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].gid+fmt.Sprint(done[i].pid) < done[j].gid+fmt.Sprint(done[j].pid) })
	return done
}

// snapshot copies each gathering's participants under the title's lock.
func (e *Env) sessionSnapshot() (map[string][]uint64, map[string]time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	gs := map[string][]uint64{}
	created := map[string]time.Time{}
	if e.Match == nil {
		return gs, created
	}
	for _, g := range e.Match.all() {
		k := fmt.Sprintf("%d-%d", g.sess.GID, g.created.Unix())
		for pid := range g.participants {
			gs[k] = append(gs[k], pid)
		}
		created[k] = g.created
	}
	return gs, created
}

// WatchSessions reports this title's online sessions to rewards under metric (for example
// "kart_sessions.mk8dx"). It returns a stop function; with rewards unconfigured it does nothing.
func (e *Env) WatchSessions(metric string, min, every time.Duration) (stop func()) {
	if !e.Rewards.Enabled() {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		clock := newSessionClock(min)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			gs, created := e.sessionSnapshot()
			for _, p := range clock.tick(time.Now(), gs, created) {
				e.Rewards.Event(emit.Event{
					ID: fmt.Sprintf("%s:%s:%d", metric, p.gid, p.pid), Metric: metric,
					Player: &emit.Identity{Namespace: "switch", Subject: fmt.Sprintf("%016x", p.pid)}, Keys: []string{p.gid},
				})
			}
			select {
			case <-t.C:
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
