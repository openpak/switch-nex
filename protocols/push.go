// Server-initiated messages: NotificationService (0x0E) events and the NAT
// probe relay. Both are RMC *requests* the server sends to a client on the
// client's own connection — not responses — so they need a call id of their own
// and a way to find the connection behind a principal id.
//
// NotificationService is push-only. The console's Pia blocks on these after
// matchmaking: without the participation event a joiner sits waiting for a
// pairing that already happened.
package protocols

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
	"github.com/openpak/switch-nex/rmc"
)

// Notification event types (category*1000 + subtype).
const (
	NotifyParticipate           uint32 = 3001   // ParticipationEvent::Participate — the pairing unblock
	NotifyParticipantDisconnect uint32 = 3007   // ParticipationEvent::Disconnected
	NotifyEndParticipation      uint32 = 3008   // ParticipationEvent::EndParticipation
	NotifyOwnershipChanged      uint32 = 4000   // the gathering changed owner
	NotifyGatheringUnregistered uint32 = 109000 // the gathering was torn down
	NotifyHostChanged           uint32 = 110000 // the host migrated
	NotifyRefereeRoundStarted   uint32 = 116000 // an arbitrated round began
	NotifyAddedToGathering      uint32 = 122000 // sent to a participant who did not ask
)

// pusher addresses live connections by principal id and numbers the RMC
// requests the server originates. Call ids start high so they cannot collide
// with a client's own sequence.
type pusher struct {
	mu       sync.Mutex
	endpoint *prudplite.Endpoint
	callID   atomic.Uint32
}

// bind attaches the secure endpoint once it exists.
func (p *pusher) bind(e *prudplite.Endpoint) {
	p.mu.Lock()
	p.endpoint = e
	p.mu.Unlock()
}

func (p *pusher) connection(pid uint64) (*prudplite.Connection, bool) {
	p.mu.Lock()
	e := p.endpoint
	p.mu.Unlock()
	if e == nil {
		return nil, false
	}
	return e.ConnectionByPID(pid)
}

func (p *pusher) nextCallID() uint32 { return 0xFFFE0000 + p.callID.Add(1) }

// notificationEvent is the ProcessNotificationEvent payload: structure version
// 0, all-u64 parameters, no map parameter. Pia titles use this shape; Eagle's
// map-bearing revision-1 handoff is encoded separately (eagle.go).
type notificationEvent struct {
	SourcePID uint64
	Type      uint32
	Param1    uint64
	Param2    uint64
	StrParam  string
	Param3    uint64
}

func (n notificationEvent) bytes() []byte {
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.U64(n.SourcePID)
		w.U32(n.Type)
		w.U64(n.Param1)
		w.U64(n.Param2)
		w.String(n.StrParam)
		w.U64(n.Param3)
	})
	return w.Bytes()
}

// notify pushes one event to a principal, if they are connected. A player who
// has already left is not an error: the event is about a gathering, and the
// gathering outlives any one participant's socket.
func (e *Env) notify(pid uint64, event notificationEvent) {
	if event.Type == NotifyParticipate && e.ParticipateDelay > 0 {
		time.AfterFunc(e.ParticipateDelay, func() { e.send(pid, event) })
		return
	}
	e.send(pid, event)
}

func (e *Env) send(pid uint64, event notificationEvent) {
	if e.sent != nil {
		e.sent(pid, event)
	}
	conn, ok := e.push.connection(pid)
	if !ok {
		return
	}
	req := rmc.EncodeRequest(ProtoNotification, 0x01, e.push.nextCallID(), event.bytes())
	if err := conn.SendRMC(req); err != nil {
		log.Printf("[%s] notify pid=%s type=%d: %v", e.Name, redact.Tag(pid), event.Type, err)
		return
	}
	log.Printf("[%s] notify pid=%s type=%d gid=%d", e.Name, redact.Tag(pid), event.Type, event.Param1)
}

// notifyGathering pushes an event to every participant of a gathering except
// the one that caused it.
func (e *Env) notifyGathering(gid uint32, except uint64, event notificationEvent) {
	g, ok := e.Match.get(gid)
	if !ok {
		return
	}
	for _, pid := range participantPIDs(g) {
		if pid == except {
			continue
		}
		e.notify(pid, event)
	}
}

// natStore remembers each principal's NAT bucket for the lobby-compatibility
// check. In memory: a console re-reports its properties every session.
type natStore struct {
	mu sync.Mutex
	m  map[uint64]natClass
}

func (n *natStore) put(pid uint64, c natClass) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.m == nil {
		n.m = map[uint64]natClass{}
	}
	n.m[pid] = c
}

func (n *natStore) get(pid uint64) natClass {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.m[pid]
}

// compatibleWith reports whether pid can be seated with everyone already in g.
func (n *natStore) compatibleWith(g *gathering, pid uint64) bool {
	mine := n.get(pid)
	if mine == natUnknown {
		return true
	}
	for _, other := range participantPIDs(g) {
		if other == pid {
			continue
		}
		if !natCompatible(mine, n.get(other)) {
			return false
		}
	}
	return true
}

// nowDateTime is the current UTC time as a packed NEX DateTime.
func nowDateTime() uint64 { return switchnex.NowDateTime() }

// diagStations logs the raw address:port pairs a client advertises or is
// handed. Off unless SWITCH_NEX_DIAG_STATIONS lists this title (comma
// separated) or is "all": it prints players' addresses, so enable it per title
// for a test and unset it afterwards.
func diagStations(env *Env, pid uint64, what string, stations []string) {
	if !diagEnabled(env.Name, os.Getenv("SWITCH_NEX_DIAG_STATIONS")) {
		return
	}
	addrs := make([]string, 0, len(stations))
	full := diagEnabled(env.Name, os.Getenv("SWITCH_NEX_DIAG_FULL_URLS"))
	for _, s := range stations {
		if full {
			addrs = append(addrs, s)
			continue
		}
		u := switchnex.Parse(s)
		addrs = append(addrs, u.Get("address")+":"+strconv.FormatUint(u.GetInt("port"), 10)+
			" type="+u.Get("type")+" natm="+u.Get("natm")+" natf="+u.Get("natf"))
	}
	log.Printf("[%s] diag %s pid=%s stations=%q", env.Name, what, redact.Tag(pid), addrs)
}

func diagEnabled(title, list string) bool {
	for _, t := range strings.Split(list, ",") {
		if t = strings.TrimSpace(t); t == "all" || t == title {
			return true
		}
	}
	return false
}

// parseStationPID reads the PID parameter out of a station URL string, or 0.
func parseStationPID(station string) uint64 {
	i := strings.Index(station, ":/")
	if i < 0 {
		return 0
	}
	for _, field := range strings.Split(station[i+2:], ";") {
		if k, v, ok := strings.Cut(field, "="); ok && k == "PID" {
			pid, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0
			}
			return pid
		}
	}
	return 0
}
