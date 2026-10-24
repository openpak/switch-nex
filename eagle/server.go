// SPDX-License-Identifier: AGPL-3.0-only
// Package eagle serves the Eagle v2 gameplay relay of the NSO battle-royale
// titles (SMB35, PAC-MAN 99): authenticated, one room per NEX gathering. Game RPC payloads
// stay opaque; the clients run the match. Only the shared counter RPCs terminate
// on the server. See provenance.md for wire facts and compatible reference inputs.
package eagle

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Server keeps the relay's rooms in the same lifetime as NEX's live gatherings.
// Its random signing key never leaves this process. Only NEX's membership hook
// can mint a ticket; the public listener cannot create rooms or admit players.
type Server struct {
	mu      sync.Mutex
	rooms   map[uint32]*room
	key     []byte
	started time.Time
	profile Profile
}

// Profile is what differs between the Eagle v2 titles: the libeagle version
// string the client sends in its first login phase, the room size, and
// whether the remaining login fields are known well enough to enforce.
type Profile struct {
	Name    string // health line and logs
	Version string // libeagle version string, measured in each client binary
	Players int    // NEX room capacity; the relay admits no more
	// Strict enforces the version, protocol=3, app protocol 0 and DDL hash 0
	// (measured for SMB35). Unmeasured titles accept any values and log them
	// once per login, so the first boot records what the client sends; the
	// ticket still gates admission.
	Strict bool
}

// SMB35 is Super Mario Bros. 35: libeagle 2.0.4, 35 players.
var SMB35 = Profile{Name: "smb35", Version: "2.0.4", Players: 35, Strict: true}

// PacMan99 is PAC-MAN 99: libeagle 2.0.5 (string in its update v131072
// main, same position as SMB35's), 99 players. Login fields unmeasured.
var PacMan99 = Profile{Name: "pac-man-99", Version: "2.0.5", Players: 99}

type room struct {
	mu       sync.Mutex
	gid      uint32
	allowed  map[uint64]bool
	peers    map[uint16]*peer
	counters [32]uint64
	closed   bool
}

type peer struct {
	room   *room
	ws     *websocket.Conn
	node   uint16
	pid    uint64
	ready  bool // room.mu guards membership, pid and ready
	cancel context.CancelFunc
	out    chan []byte
	token  []byte
	phase  int
	clock  func() uint64
}

// NewServer is the SMB35 relay.
func NewServer() (*Server, error) { return NewServerProfile(SMB35) }

func NewServerProfile(p Profile) (*Server, error) {
	if p.Version == "" || p.Players < 2 || p.Players > nodeLimit/4 {
		return nil, errors.New("invalid Eagle profile")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &Server{rooms: make(map[uint32]*room), key: key, started: time.Now(), profile: p}, nil
}

// Ticket admits one NEX participant to one gathering. Reissuing it is safe for
// secure-stage reconnects; the relay permits one live connection per player.
func (s *Server) Ticket(gid uint32, pid uint64) (string, error) {
	if gid == 0 || pid == 0 {
		return "", errTicket
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[gid]
	if r == nil {
		if len(s.rooms) >= 1024 {
			return "", errors.New("Eagle room limit reached")
		}
		r = &room{gid: gid, allowed: make(map[uint64]bool), peers: make(map[uint16]*peer)}
		s.rooms[gid] = r
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || (!r.allowed[pid] && len(r.allowed) >= s.profile.Players) {
		return "", errTicket
	}
	r.allowed[pid] = true
	return signTicket(s.key, gid, pid, time.Now().Add(3*time.Hour)), nil
}

// Leave revokes admission immediately, even while a ticket is unexpired.
func (s *Server) Leave(gid uint32, pid uint64) {
	s.mu.Lock()
	r := s.rooms[gid]
	s.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.allowed, pid)
	for _, p := range r.peers {
		if p.pid == pid {
			p.cancel()
		}
	}
	r.mu.Unlock()
}

func (s *Server) CloseRoom(gid uint32) {
	s.mu.Lock()
	r := s.rooms[gid]
	delete(s.rooms, gid)
	s.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	for _, p := range r.peers {
		p.cancel()
	}
	r.mu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/healthz" && req.Method == http.MethodGet {
		fmt.Fprintln(w, "ok eagle "+s.profile.Name)
		return
	}
	path := strings.TrimPrefix(req.URL.Path, "/")
	id, err := strconv.ParseUint(path, 10, 32)
	if err != nil || id == 0 || req.Method != http.MethodGet {
		http.NotFound(w, req)
		return
	}
	s.mu.Lock()
	r := s.rooms[uint32(id)]
	s.mu.Unlock()
	if r == nil {
		http.NotFound(w, req)
		return
	}
	// Console clients send no Origin; a browser origin is denied by the
	// WebSocket library unless it matches the host. The ticket is mandatory.
	options := &websocket.AcceptOptions{}
	if offered := strings.TrimSpace(strings.Split(req.Header.Get("Sec-WebSocket-Protocol"), ",")[0]); offered != "" {
		options.Subprotocols = []string{offered}
	}
	ws, err := websocket.Accept(w, req, options)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(maxPacket)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &peer{room: r, ws: ws, cancel: cancel, out: make(chan []byte, 64), clock: s.clock}
	r.mu.Lock()
	if r.closed || len(r.peers) >= 2*s.profile.Players {
		r.mu.Unlock()
		return
	}
	for i := uint16(1); i < nodeLimit; i++ {
		if r.peers[i] == nil {
			p.node = i
			break
		}
	}
	r.peers[p.node] = p
	r.mu.Unlock()
	defer p.depart()
	go p.write(ctx)
	accepted := make([]byte, 10)
	binary.BigEndian.PutUint16(accepted, p.node)
	binary.BigEndian.PutUint64(accepted[2:], s.clock())
	p.send(0, 0, accepted)
	log.Printf("[eagle] connected gid=%d node=%d", r.gid, p.node)
	loginDeadline := time.Now().Add(10 * time.Second)
	for {
		timeout := time.Until(loginDeadline)
		if p.phase == 3 {
			timeout = 90 * time.Second
		}
		readCtx, readCancel := context.WithTimeout(ctx, timeout)
		kind, data, err := ws.Read(readCtx)
		readCancel()
		if err != nil {
			return
		}
		if kind != websocket.MessageBinary {
			return
		}
		pkt, err := parsePacket(data)
		if err != nil {
			return
		}
		if err = s.process(p, pkt); err != nil {
			// Never log a token, payload, PID, nickname or client address.
			log.Printf("[eagle] rejected gid=%d node=%d op=%d: %v", r.gid, p.node, pkt.op, err)
			return
		}
	}
}

func (s *Server) clock() uint64 { return uint64(time.Since(s.started).Milliseconds()) }

func (p *peer) send(op uint8, source uint16, payload []byte) {
	select {
	case p.out <- encodePacket(op, source, payload):
	default:
		p.cancel() // a slow receiver cannot block the room or grow an unbounded queue
	}
}

func (p *peer) write(ctx context.Context) {
	defer p.ws.CloseNow()
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-p.out:
			writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := p.ws.Write(writeCtx, websocket.MessageBinary, data)
			cancel()
			if err != nil {
				p.cancel()
				return
			}
		}
	}
}

func (p *peer) depart() {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, p.node)
	if p.ready {
		payload := make([]byte, 11)
		payload[0] = 3 // node removed
		binary.BigEndian.PutUint16(payload[1:], p.node)
		binary.BigEndian.PutUint64(payload[3:], p.clock())
		for _, other := range r.peers {
			if other.ready {
				other.send(8, 0, payload)
			}
		}
	}
	log.Printf("[eagle] disconnected gid=%d node=%d", r.gid, p.node)
}

func (s *Server) process(p *peer, pkt packet) error {
	data := pkt.payload
	switch pkt.op {
	case 1:
		return s.login(p, data)
	case 3:
		if p.phase != 2 || len(data) != 0 {
			return errPacket
		}
		return s.ready(p)
	case 4:
		if len(data) != 8 {
			return errPacket
		}
		payload := make([]byte, 16)
		binary.BigEndian.PutUint64(payload, s.clock())
		copy(payload[8:], data)
		p.send(5, 0, payload)
		return nil
	case 9:
		p.cancel()
		return nil
	}
	if pkt.op < 16 || p.phase != 3 || len(data) < 8 {
		return errPacket
	}
	return s.route(p, pkt.op, pkt.targets, data[8:])
}

func (s *Server) login(p *peer, data []byte) error {
	if len(data) < 2 {
		return errPacket
	}
	phase, last := data[0]>>1, data[0]&1 != 0
	if phase == 0 {
		if p.phase != 0 || len(data) < 19 || int(data[18]) != len(data)-19 {
			return errPacket
		}
		proto, app, ddl := binary.BigEndian.Uint32(data[2:]), binary.BigEndian.Uint64(data[6:]), binary.BigEndian.Uint32(data[14:])
		version := string(data[19:])
		if s.profile.Strict && (data[1] != 0 || proto != 3 || app != 0 || ddl != 0 || version != s.profile.Version) {
			return errPacket
		}
		if !s.profile.Strict {
			log.Printf("[eagle] login fields gid=%d node=%d unknown=%d protocol=%d app=%d ddl=%#x version=%q expected=%q",
				p.room.gid, p.node, data[1], proto, app, ddl, version, s.profile.Version)
		}
		p.phase = 1
		return nil
	}
	if phase != 1 || p.phase != 1 || int(data[1]) != len(data)-2 || len(p.token)+len(data)-2 > 8192 {
		return errPacket
	}
	p.token = append(p.token, data[2:]...)
	if !last {
		return nil
	}
	pid, err := verifyTicket(s.key, strings.TrimSuffix(string(p.token), "\x00"), p.room.gid, time.Now())
	p.token = nil
	if err != nil {
		return err
	}
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.allowed[pid] {
		return errTicket
	}
	for _, old := range r.peers {
		if old != p && old.pid == pid {
			old.cancel()
		}
	}
	p.pid = pid
	p.phase = 2
	identity := fmt.Sprintf("%016x\x00", pid)
	payload := make([]byte, 7+len(identity))
	binary.BigEndian.PutUint32(payload, 1) // Eagle login success is 1, not 0
	binary.BigEndian.PutUint16(payload[5:], uint16(len(identity)))
	copy(payload[7:], identity)
	p.send(2, 0, payload)
	log.Printf("[eagle] authenticated gid=%d node=%d", r.gid, p.node)
	return nil
}

func (s *Server) ready(p *peer) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.allowed[p.pid] {
		return errTicket
	}
	p.ready = true
	p.phase = 3
	all := make([]byte, 1+nodeLimit/8+8)
	all[0] = 4
	for id, member := range r.peers {
		if member.ready {
			all[1+int(id)/8] |= 1 << uint(7-id%8)
		}
	}
	binary.BigEndian.PutUint64(all[1+nodeLimit/8:], s.clock())
	p.send(8, 0, all)
	added := make([]byte, 11)
	added[0] = 0
	binary.BigEndian.PutUint16(added[1:], p.node)
	binary.BigEndian.PutUint64(added[3:], s.clock())
	for _, other := range r.peers {
		if other != p && other.ready {
			other.send(8, 0, added)
		}
	}
	log.Printf("[eagle] ready gid=%d node=%d", r.gid, p.node)
	return nil
}

func (s *Server) route(p *peer, op uint8, targets []uint16, body []byte) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.allowed[p.pid] {
		return errTicket
	}
	if len(targets) == 1 && (targets[0] == nodeLimit || targets[0] == nodeLimit+1) {
		broadcastSelf := targets[0] == nodeLimit+1
		targets = nil
		for id, other := range r.peers {
			if other.ready && (other != p || broadcastSelf) {
				targets = append(targets, id)
			}
		}
	}
	for _, target := range targets {
		if target == 0 {
			if err := s.counter(p, op, body); err != nil {
				return err
			}
		} else if other := r.peers[target]; other != nil && other.ready {
			payload := make([]byte, 8+len(body))
			binary.BigEndian.PutUint64(payload, s.clock())
			copy(payload[8:], body)
			other.send(op, p.node, payload)
		}
	}
	if op == 26 || op == 27 || op == 28 || op == 38 {
		log.Printf("[eagle] game RPC gid=%d node=%d op=%d targets=%d", r.gid, p.node, op, len(targets))
	}
	return nil
}

// counter runs with room.mu held, so increment and the reply form one operation.
func (s *Server) counter(p *peer, op uint8, body []byte) error {
	if op < 17 || op > 19 {
		log.Printf("[eagle] unsupported server RPC gid=%d op=%d", p.room.gid, op)
		return nil // documented game RPCs are relayed; no invented server reply
	}
	if len(body) < 1 || body[0] >= 32 || (op != 18 && len(body) < 9) {
		return errPacket
	}
	index := body[0]
	if op == 17 {
		p.room.counters[index] = binary.BigEndian.Uint64(body[1:])
	} else if op == 19 {
		p.room.counters[index] += binary.BigEndian.Uint64(body[1:])
	}
	payload := make([]byte, 8+11)
	binary.BigEndian.PutUint64(payload, s.clock())
	payload[8] = index
	binary.BigEndian.PutUint64(payload[9:], p.room.counters[index])
	p.send(16, 0, payload)
	return nil
}
