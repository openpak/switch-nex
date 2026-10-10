package main

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// recall pairs a ticketless secure CONNECT with the login it belongs to.
//
// A title whose station URL says "prudp" (not "prudps") opens its secure
// connection with an EMPTY CONNECT payload: no ticket, so nothing in the
// connection itself names the player. The only way to give it a PID is to
// work out which login it follows, and a wrong answer hands one player
// somebody else's account. So the rule is: bind only when exactly one login can
// be meant, and refuse when more than one can. A refused CONNECT costs the
// player a retry; a wrong bind costs them their identity.
//
// Two tables do this.
//
//   - pending: every login, claimable ONCE by a CONNECT for the same title,
//     inside recallByTitleWindow, from the login's own address when that
//     address is known (the auth stage rides Traefik; PROXY protocol on its
//     passthrough router hands us the console's address, proxyproto.go).
//     A login whose address is not known can be claimed from anywhere, which
//     is exactly why more than one candidate is a refusal, not a guess.
//   - seen: which PIDs have played a title from a public address, for a
//     console that re-CONNECTs without logging in again. Only an address
//     that has seen exactly one PID inside the TTL is an answer.
//
// What it can never do is tell apart two consoles behind one address that log
// in to one title in the same second (two Harbor instances on one PC, or one
// household). Those are refused. Only the ticket separates them, which is what
// the "prudps" scheme makes the client send.
//
// The seen table SURVIVES A RESTART (debounced writes to -recall-file, re-read
// at startup), so a console mid-session that re-CONNECTs after a restart is
// not refused for want of a login the server forgot. pending does not: a login
// older than recallByTitleWindow is no use to anyone.
type recall struct {
	mu      sync.Mutex
	pending map[string][]pendingLogin   // title -> logins not yet claimed
	seen    map[string]map[uint64]int64 // title|host -> pid -> unix seconds
	names   map[string]uint64           // login name -> PID
	ttl     time.Duration
	file    string
	dirty   atomic.Bool
	now     func() time.Time // tests move the clock
}

type pendingLogin struct {
	pid     uint64
	host    string // "" when the login's address said nothing
	at      time.Time
	claimed bool
}

// recallEntry is one persisted seen row.
type recallEntry struct {
	PID uint64 `json:"pid"`
	At  int64  `json:"at"` // unix seconds
}

// recallMax bounds the persisted table: a recall table must not grow without end.
const recallMax = 50000

// recallFlushEvery is how often a dirty table reaches the disk.
const recallFlushEvery = 5 * time.Second

// recallByTitleWindow is how long a login stays claimable. The game opens its
// secure connection within a second of the login answer.
const recallByTitleWindow = 30 * time.Second

// bindVerdict is how a ticketless CONNECT was resolved.
type bindVerdict int

const (
	bindNone      bindVerdict = iota // no login to pair it with
	bindLogin                        // the one login waiting for it
	bindReconnect                    // the one PID this address has played as
	bindAmbiguous                    // more than one player it could be: refused
)

func (v bindVerdict) String() string {
	switch v {
	case bindLogin:
		return "its login"
	case bindReconnect:
		return "the only player seen from its address"
	case bindAmbiguous:
		return "ambiguous"
	}
	return "nothing"
}

func newRecall(ttl time.Duration, file string) *recall {
	r := &recall{
		pending: map[string][]pendingLogin{},
		seen:    map[string]map[uint64]int64{},
		names:   map[string]uint64{},
		ttl:     ttl,
		file:    file,
		now:     time.Now,
	}
	r.load()
	if file != "" {
		go r.flushLoop()
	}
	return r
}

// addressSaysNothing reports whether a source address is one a NAT or a proxy
// put there rather than the client's own.
func addressSaysNothing(host string) bool {
	ip := net.ParseIP(host)
	return ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified()
}

func seenKey(title, host string) string { return title + "|" + host }

// noteLogin records a login: claimable once by its own secure CONNECT, and,
// when the address is the console's, remembered for reconnects.
func (r *recall) noteLogin(title, host string, pid uint64) {
	if pid == 0 {
		return
	}
	if addressSaysNothing(host) {
		host = ""
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.pending[title][:0]
	for _, p := range r.pending[title] {
		// The same player logging in again replaces its unclaimed login.
		if now.Sub(p.at) > recallByTitleWindow || (p.pid == pid && !p.claimed) {
			continue
		}
		kept = append(kept, p)
	}
	r.pending[title] = append(kept, pendingLogin{pid: pid, host: host, at: now})
	if host != "" {
		r.markSeen(title, host, pid, now)
	}
}

// bind resolves a ticketless CONNECT from host for title.
func (r *recall) bind(title, host string) (uint64, bindVerdict) {
	if addressSaysNothing(host) {
		host = ""
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. A login waiting for its secure connection.
	var pid uint64
	candidates := map[uint64]bool{}
	for _, p := range r.pending[title] {
		if p.claimed || now.Sub(p.at) > recallByTitleWindow {
			continue
		}
		// Both addresses known and different: not this console's login.
		if p.host != "" && host != "" && p.host != host {
			continue
		}
		candidates[p.pid] = true
		pid = p.pid
	}
	switch len(candidates) {
	case 0:
	case 1:
		for i := range r.pending[title] {
			if r.pending[title][i].pid == pid {
				r.pending[title][i].claimed = true
			}
		}
		if host != "" {
			r.markSeen(title, host, pid, now)
		}
		return pid, bindLogin
	default:
		return 0, bindAmbiguous
	}

	// 2. No login in flight: a reconnect, answerable only by an address that
	// has played as exactly one PID.
	if host == "" {
		return 0, bindNone
	}
	pid, n := 0, 0
	for p, at := range r.seen[seenKey(title, host)] {
		if now.Sub(time.Unix(at, 0)) > r.ttl {
			continue
		}
		pid, n = p, n+1
	}
	switch {
	case n > 1:
		return 0, bindAmbiguous
	case n == 1:
		r.markSeen(title, host, pid, now)
		return pid, bindReconnect
	}
	return 0, bindNone
}

// markSeen records pid under title|host. Caller holds r.mu.
func (r *recall) markSeen(title, host string, pid uint64, now time.Time) {
	k := seenKey(title, host)
	if r.seen[k] == nil {
		r.seen[k] = map[uint64]int64{}
	}
	r.seen[k][pid] = now.Unix()
	r.dirty.Store(true)
}

// rememberName records the login-name-to-PID mapping. Some titles never speak
// our internal PID: they ask for profiles by the name the console logged in
// under, and only the login ever saw both halves, so the mapping cannot be
// recovered later by arithmetic.
//
// ponytail: the table is kept but nothing reads it yet — the profile store it
// feeds does not exist. Serving a profile means emitting the title's 26-field
// user structure, and a partial one shifts every field after it, so the current
// "no profile stored" answer is the correct one until there is real data behind
// it. Wire this up when the store lands, not before.
func (r *recall) rememberName(name string, pid uint64) {
	if name == "" || pid == 0 {
		return
	}
	r.mu.Lock()
	r.names[name] = pid
	r.mu.Unlock()
}

// pidForName resolves a login name to the PID it logged in as.
func (r *recall) pidForName(name string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pid, ok := r.names[name]
	return pid, ok
}

// load restores the seen table. A file in the old one-PID-per-address shape
// does not parse and is ignored: its addresses were Traefik's, never a
// console's, so nothing in it could pair anyone.
func (r *recall) load() {
	if r.file == "" {
		return
	}
	b, err := os.ReadFile(r.file)
	if err != nil {
		return
	}
	var m map[string][]recallEntry
	if json.Unmarshal(b, &m) != nil {
		log.Printf("recall: %s is not in the current shape; starting empty", r.file)
		return
	}
	n := 0
	now := r.now()
	for k, es := range m {
		for _, e := range es {
			if e.PID == 0 || now.Sub(time.Unix(e.At, 0)) > r.ttl {
				continue
			}
			if r.seen[k] == nil {
				r.seen[k] = map[uint64]int64{}
			}
			r.seen[k][e.PID] = e.At
			n++
		}
	}
	log.Printf("recall: restored %d entry(ies) from %s", n, r.file)
}

func (r *recall) flushLoop() {
	t := time.NewTicker(recallFlushEvery)
	defer t.Stop()
	for range t.C {
		if !r.dirty.Swap(false) {
			continue
		}
		r.flush()
	}
}

func (r *recall) flush() {
	now := r.now()
	r.mu.Lock()
	out := map[string][]recallEntry{}
	n := 0
	for k, pids := range r.seen {
		for pid, at := range pids {
			if now.Sub(time.Unix(at, 0)) > r.ttl {
				delete(pids, pid)
				continue
			}
			if n < recallMax {
				out[k] = append(out[k], recallEntry{PID: pid, At: at})
				n++
			}
		}
		if len(pids) == 0 {
			delete(r.seen, k)
		}
	}
	r.mu.Unlock()

	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	tmp := r.file + ".tmp"
	// 0600: this file is a list of which address played as whom.
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, r.file) // atomic replace
	}
}
