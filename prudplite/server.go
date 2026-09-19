// Raw PRUDPLite-over-TLS connection state machine.
//
// Switch titles speak raw PRUDPLite frames over a TLS stream: no websocket
// upgrade, no payload encryption (TLS carries confidentiality), no reliable
// retransmission (TCP carries reliability and ordering). The state machine
// mirrors the handshake shapes of the working reference server:
//
//   - SYN-ACK: flags ACK|HAS_SIZE, support option, connection signature.
//   - CONNECT: the client's Lite signature proves the access key; a secure
//     endpoint's payload carries [buffer ticket][buffer check-data] and the
//     CONNECT-ACK answers [u32 4][u32 check+1], proving we hold the session
//     key. An empty payload falls back to the by-IP PID recall (leniency
//     path, same as the reference).
//   - DATA: RELIABLE|NEEDS_ACK client frames are acked; our responses go out
//     as RELIABLE|NEEDS_ACK without HAS_SIZE; acks/multi-acks are consumed,
//     never parsed as RMC.
package prudplite

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// Endpoint serves one PRUDP virtual stream on one TLS listener.
type Endpoint struct {
	// Name labels this endpoint's log lines ("mario-kart-8-deluxe/secure").
	Name     string
	StreamID uint8
	IsSecure bool
	// AccessKey is the title's PRUDP access key. When set, CONNECT frames
	// are required to carry a valid Lite signature (HMAC over the
	// connection signature keyed with md5(accessKey)).
	AccessKey string
	// KerberosKey is the secure server account's derived key; it decrypts
	// the ticket inside a secure CONNECT payload. Required when IsSecure.
	KerberosKey []byte
	// RecallPID resolves a ticketless secure CONNECT to a login PID by
	// client IP. Returning false drops the connection.
	RecallPID    func(host string) (uint64, bool)
	OnConnect    func(c *Connection)
	OnRMC        func(c *Connection, msg *rmc.RMCMessage) (response []byte)
	OnDisconnect func(c *Connection)

	ln     net.Listener
	mu     sync.Mutex
	conns  map[string]*Connection
	quit   chan struct{}
	nextID atomic.Uint32
}

// ConnectionByPID returns the live connection for a principal, if it has one.
// Server-initiated pushes address a player, not a socket.
func (e *Endpoint) ConnectionByPID(pid uint64) (*Connection, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.conns {
		if c.PID == pid && c.State == StateConnected {
			return c, true
		}
	}
	return nil, false
}

// Listen starts serving the endpoint on an already-open TLS listener.
func (e *Endpoint) Listen(ln net.Listener) {
	e.mu.Lock()
	e.conns = map[string]*Connection{}
	e.mu.Unlock()
	e.quit = make(chan struct{})
	e.ln = ln
	go e.acceptLoop()
}

// Close stops the listener.
func (e *Endpoint) Close() {
	select {
	case <-e.quit:
	default:
		close(e.quit)
	}
	if e.ln != nil {
		e.ln.Close()
	}
}

func (e *Endpoint) acceptLoop() {
	for {
		conn, err := e.ln.Accept()
		if err != nil {
			select {
			case <-e.quit:
				return
			default:
				continue
			}
		}
		tcp, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok {
			conn.Close()
			continue
		}
		c := &Connection{
			Endpoint: e,
			Remote:   tcp,
			Host:     tcp.IP.String(),
			conn:     conn,
			// The connection id the client is told to call itself (RVCID).
			// Per connection, never a constant: two consoles handed the same
			// id cannot address each other in the P2P mesh.
			ID:       e.nextID.Add(1),
			opened:   time.Now(),
			carriage: "bare",
		}
		e.mu.Lock()
		// Keyed by address AND port. Keyed by address alone, two players behind
		// one NAT — or every player, behind a forwarder that rewrites the source
		// — shared a slot: the newer connection evicted the older from the
		// table, pushes to the evicted player went nowhere, and whichever closed
		// first deleted the other's entry.
		e.conns[c.Remote.String()] = c
		e.mu.Unlock()
		go c.serve()
	}
}

func (e *Endpoint) forget(c *Connection) {
	e.mu.Lock()
	_, had := e.conns[c.Remote.String()]
	delete(e.conns, c.Remote.String())
	e.mu.Unlock()
	if had && e.OnDisconnect != nil {
		e.OnDisconnect(c)
	}
}

// Connection is one client's PRUDPLite session.
type Connection struct {
	Endpoint *Endpoint
	Remote   *net.TCPAddr
	Host     string // client IP — the recall key

	PID        uint64
	ID         uint32 // the connection id we assign (RVCID)
	CID        uint32 // the connection id the client claimed in its CONNECT
	Signature  []byte // our connection signature (SYN-ACK)
	SessionKey []byte

	// stations are the client's station URLs as last reported: its LAN
	// address and the public one we observed or it told us. ReplaceURL
	// rewrites them mid-session, and the NAT probe path reads them.
	stationMu sync.Mutex
	stations  []string

	// Learned virtual ports (from the client's SYN): we answer AS the
	// port the client targeted and address the client's source port.
	srcType, dstType StreamType
	srcPort, dstPort uint8

	State   int
	SendSeq uint16

	fragBuf []byte
	conn    net.Conn
	writeMu sync.Mutex

	// What the closing line reports: a session that ends badly is described
	// by how far it got and the last thing it asked.
	opened   time.Time
	frames   int
	calls    int
	lastCall string
	carriage string

	// Frame summaries received before CONNECT — the failing handshake, in the
	// client's own words, one entry per frame (bounded, newest kept).
	preConnect []string
}

// frameSummary names one received frame compactly: type, flags, sequence and
// payload length — enough to recognise a handshake retry pattern in a log.
func frameSummary(p *Packet) string {
	return fmt.Sprintf("%s/flags%#x/seq%d/%dB", typeName(p.Type), p.Flags, p.SequenceID, len(p.Payload))
}

// typeName spells a PRUDPLite packet type for logs.
func typeName(t PacketType) string {
	switch t {
	case Syn:
		return "SYN"
	case Connect:
		return "CONNECT"
	case Data:
		return "DATA"
	case Disconnect:
		return "DISCONNECT"
	case Ping:
		return "PING"
	}
	return fmt.Sprintf("type0x%X", uint8(t))
}

// SetStations replaces the connection's station list.
func (c *Connection) SetStations(urls []string) {
	c.stationMu.Lock()
	c.stations = append([]string(nil), urls...)
	c.stationMu.Unlock()
}

// Stations returns the connection's station list.
func (c *Connection) Stations() []string {
	c.stationMu.Lock()
	defer c.stationMu.Unlock()
	return append([]string(nil), c.stations...)
}

// SendRMC pushes a serialised RMC message to the client on its own reply path.
// The virtual-port routing is the one learned from the client's SYN, which is
// what the console requires — a hardcoded source port is dropped silently.
func (c *Connection) SendRMC(payload []byte) error {
	if c.State != StateConnected {
		return fmt.Errorf("prudplite: push to a connection that is not connected")
	}
	return c.sendData(payload, 0)
}

// Connection states.
const (
	StateNew       = 0
	StateConnected = 1
)

func (c *Connection) send(p *Packet) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.conn.Write(p.Encode())
	return err
}

// mirror copies the virtual port routing of a received frame onto a reply.
func (c *Connection) mirror(p *Packet) {
	p.SourceStreamType = c.srcType
	p.DestinationStreamType = c.dstType
	p.SourceVirtualPort = c.srcPort
	p.DestinationVirtualPort = c.dstPort
}

// ack replies to p with a bare ACK (same type, mirrored routing, carrying the
// acknowledged sequence id).
func (c *Connection) ack(p *Packet) {
	ack := &Packet{Type: p.Type, Flags: FlagAck, SequenceID: p.SequenceID, FragmentID: p.FragmentID}
	c.mirror(ack)
	_ = c.send(ack)
}

// sendData sends one reliable DATA frame on the learned virtual ports. The
// sequence number is taken under the write lock: a push from another
// connection's handler and this connection's own reply can race here, and two
// frames must never share a sequence or go out of order.
func (c *Connection) sendData(payload []byte, fragmentID uint8) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.SendSeq++
	p := &Packet{
		Type:       Data,
		Flags:      FlagReliable | FlagNeedsAck,
		SequenceID: c.SendSeq,
		FragmentID: fragmentID,
		Payload:    payload,
	}
	c.mirror(p)
	_, err := c.conn.Write(p.Encode())
	return err
}

func (c *Connection) serve() {
	// The catch-all: every connection says how it ended. A title that gives
	// up never tells the server why, so the server records what it can see —
	// who hung up, at which stage, after which call. Nothing here is
	// title-specific; it is the first thing to read for any title.
	reason := "unknown"
	defer func() {
		if r := recover(); r != nil {
			reason = fmt.Sprintf("PANIC in handler: %v\n%s", r, debug.Stack())
		}
		state := "before CONNECT"
		if c.State == StateConnected {
			state = "connected"
		}
		log.Printf("[%s] closed %s pid=%d %s carriage=%s age=%s frames=%d calls=%d last=%s saw=%v reason: %s",
			c.Endpoint.Name, c.Remote, c.PID, state, c.carriage,
			time.Since(c.opened).Round(time.Millisecond), c.frames, c.calls, c.lastCall, c.preConnect, reason)
		c.conn.Close()
		c.Endpoint.forget(c)
	}()
	// The client chooses the carriage: bare frames or a websocket upgrade.
	c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	transport, err := sniffTransport(c.conn)
	if err != nil {
		reason = fmt.Sprintf("dropped before its first frame: %v", err)
		return
	}
	if _, ok := transport.(*wsConn); ok {
		c.carriage = "websocket"
	}
	c.writeMu.Lock()
	c.conn = transport
	c.writeMu.Unlock()
	head := make([]byte, 12)
	for {
		c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		if _, err := io.ReadFull(c.conn, head); err != nil {
			if reason == "unknown" {
				reason = describeReadError(err)
			}
			return
		}
		if head[0] != 0x80 {
			reason = fmt.Sprintf("not a PRUDPLite frame, header %x", head)
			return
		}
		optionsLength := int(head[1])
		payloadLength := int(binary.LittleEndian.Uint16(head[2:4]))
		rest := make([]byte, optionsLength+payloadLength)
		if _, err := io.ReadFull(c.conn, rest); err != nil {
			reason = "mid-frame: " + describeReadError(err)
			return
		}
		frame := append(append([]byte{}, head...), rest...)
		p, _, err := Decode(frame)
		if err != nil {
			reason = fmt.Sprintf("undecodable frame %x: %v", head, err)
			return
		}
		c.frames++
		if c.State != StateConnected {
			// A connection that never reaches CONNECT is the one whose
			// handshake is failing; record what it actually said so the
			// close line answers "which stage" without a capture.
			c.preConnect = append(c.preConnect, frameSummary(p))
			if len(c.preConnect) > 32 {
				c.preConnect = append(c.preConnect[:0:0], c.preConnect[1:]...)
			}
		}
		if p.Type == Disconnect && !p.HasFlag(FlagAck) {
			reason = "client sent DISCONNECT"
		}
		c.handle(p)
	}
}

// describeReadError says who ended the stream, in words a log reader can act on.
func describeReadError(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "client closed the connection"
	case errors.As(err, &ne) && ne.Timeout():
		return "idle timeout: nothing from the client"
	case errors.Is(err, net.ErrClosed):
		return "closed by the server"
	}
	return "read: " + err.Error()
}

func (c *Connection) handle(p *Packet) {
	switch p.Type {
	case Syn:
		if !p.HasFlag(FlagAck) {
			c.handleSyn(p)
		}
	case Connect:
		if !p.HasFlag(FlagAck) {
			c.handleConnect(p)
		}
	case Data:
		c.handleData(p)
	case Ping:
		if p.HasFlag(FlagNeedsAck) {
			c.ack(p)
		}
	case Disconnect:
		if p.HasFlag(FlagAck) {
			return
		}
		if p.HasFlag(FlagNeedsAck) {
			c.ack(p)
		}
		c.Endpoint.forget(c)
		c.conn.Close()
	}
}

func (c *Connection) handleSyn(p *Packet) {
	// Learn the virtual ports: respond AS the port the client targeted.
	c.srcType, c.srcPort = p.DestinationStreamType, p.DestinationVirtualPort
	c.dstType, c.dstPort = p.SourceStreamType, p.SourceVirtualPort

	c.Signature = ConnectionSignature(c.Remote)
	if c.Signature == nil {
		c.Signature = random16()
	}
	ack := &Packet{
		Type:               Syn,
		Flags:              FlagAck | FlagHasSize,
		SequenceID:         p.SequenceID,
		ConnectionSig:      c.Signature,
		MinorVersion:       minorVersion(p.MinorVersion),
		SupportedFunctions: 0,
	}
	c.mirror(ack)
	_ = c.send(ack)
}

func (c *Connection) handleConnect(p *Packet) {
	// The client proves it knows the access key by signing our connection
	// signature. Only enforced when the endpoint knows its access key.
	if c.Endpoint.AccessKey != "" {
		expected := LitePacketSignature(c.Endpoint.AccessKey, p, c.Signature)
		if expected == nil || !equalBytes(expected, p.Signature) {
			log.Printf("prudplite: CONNECT bad signature from %s got=%x want=%x", c.Remote, p.Signature, expected)
			c.rejectConnect(p)
			return
		}
	}

	response, err := c.processConnectPayload(p.Payload)
	if err != nil {
		log.Printf("prudplite: CONNECT rejected from %s: %v", c.Remote, err)
		c.rejectConnect(p)
		return
	}

	ack := &Packet{
		Type:       Connect,
		Flags:      FlagAck | FlagHasSize,
		SequenceID: 1,
		Payload:    response,
	}
	c.mirror(ack)
	_ = c.send(ack)
	c.State = StateConnected
	if c.Endpoint.OnConnect != nil {
		c.Endpoint.OnConnect(c)
	}
}

// rejectConnect ends a failed CONNECT immediately: DISCONNECT, then close the
// stream. The old behaviour was silence — the TCP socket stayed open while
// PRUDP said nothing, so the client retransmitted the same CONNECT every ~2 s
// for ~30 s before giving up, 2306-0502 on screen the whole time. No ACK
// here: a bare Connect-Ack frame is indistinguishable on the wire from
// success, and the harness proves clients read it as one.
func (c *Connection) rejectConnect(p *Packet) {
	d := &Packet{Type: Disconnect, Flags: FlagNeedsAck, SequenceID: c.SendSeq}
	c.mirror(d)
	_ = c.send(d)
	c.Endpoint.forget(c)
	c.conn.Close()
}

// processConnectPayload validates a CONNECT payload. On the secure endpoint
// it is [buffer ticket][buffer check-data]: the ticket decrypts with our
// secure key to the session, the check-data decrypts with the session key to
// (PID, CID, check); the reply proves we hold the session key. Returns nil
// for an empty response (auth endpoint, or ticketless recall).
func (c *Connection) processConnectPayload(payload []byte) ([]byte, error) {
	if !c.Endpoint.IsSecure {
		c.PID = 0
		return nil, nil
	}
	if len(payload) == 0 {
		if c.Endpoint.RecallPID == nil {
			return nil, fmt.Errorf("ticketless CONNECT without a recall hook")
		}
		pid, ok := c.Endpoint.RecallPID(c.Host)
		if !ok {
			return nil, fmt.Errorf("ticketless CONNECT with no recent login from %s", c.Host)
		}
		c.PID = pid
		return nil, nil
	}
	if len(c.Endpoint.KerberosKey) == 0 {
		return nil, fmt.Errorf("secure CONNECT but the endpoint has no kerberos key")
	}
	in := &blobReader{b: payload}
	ticketData, err := in.Buffer()
	if err != nil {
		return nil, err
	}
	checkData, err := in.Buffer()
	if err != nil {
		return nil, err
	}
	st, err := switchnex.DecryptServerTicket(ticketData, c.Endpoint.KerberosKey)
	if err != nil {
		return nil, err
	}
	dec, err := switchnex.DecryptSum(st.SessionKey, checkData)
	if err != nil {
		return nil, err
	}
	if len(dec) != 16 { // u64 PID + u32 CID + u32 check
		return nil, fmt.Errorf("bad check-data size %d", len(dec))
	}
	pid := binary.LittleEndian.Uint64(dec[0:8])
	if pid != st.SourcePID {
		return nil, fmt.Errorf("pid %d != ticket source %d", pid, st.SourcePID)
	}
	c.PID = pid
	c.CID = binary.LittleEndian.Uint32(dec[8:12])
	c.SessionKey = st.SessionKey

	check := binary.LittleEndian.Uint32(dec[12:16])
	out := make([]byte, 0, 8)
	out = binary.LittleEndian.AppendUint32(out, 4)
	out = binary.LittleEndian.AppendUint32(out, check+1)
	return out, nil
}

func (c *Connection) handleData(p *Packet) {
	// Acknowledgements of OUR data — never parsed as RMC, never re-acked.
	if p.HasFlag(FlagAck) {
		return
	}
	// Aggregate ack: payload is [substream u8][count u8][base u16][extra u16]*.
	if p.HasFlag(FlagMultiAck) {
		return
	}
	if p.HasFlag(FlagNeedsAck) {
		c.ack(p)
	}

	// Fragment reassembly: fragments 1..n accumulate; fragment 0 completes the
	// message. TCP orders the stream, so plain appending is correct.
	if p.FragmentID != 0 {
		c.fragBuf = append(c.fragBuf, p.Payload...)
		return
	}
	assembled := append(c.fragBuf, p.Payload...)
	c.fragBuf = nil

	if len(assembled) < 5 {
		return
	}
	msg, err := rmc.DecodeRequest(assembled)
	if err != nil {
		log.Printf("prudplite: undecodable RMC from %s (%d bytes): %v", c.Remote, len(assembled), err)
		return
	}
	c.calls++
	c.lastCall = fmt.Sprintf("proto=0x%X/method=0x%X", msg.Protocol, msg.MethodID)
	var response []byte
	if c.Endpoint.OnRMC != nil {
		response = c.Endpoint.OnRMC(c, msg)
	}
	// Every call and its answer, heads only, with the two outcomes that stall
	// a title spelled out: an error answer, and no answer at all.
	outcome := "ok"
	if code, isErr := rmc.ResponseError(response); isErr {
		outcome = fmt.Sprintf("ERROR 0x%08X", code)
	} else if response == nil {
		outcome = "NO ANSWER SENT"
	}
	log.Printf("[%s] rmc pid=%d proto=0x%X method=0x%X call=%d %s req(%dB)=%x resp(%dB)=%x",
		c.Endpoint.Name, c.PID, msg.Protocol, msg.MethodID, msg.CallID, outcome,
		len(msg.Body), headBytes(msg.Body, 96), len(response), headBytes(response, 48))
	if outcome != "ok" && len(msg.Body) > 96 {
		// The head is enough for a call that worked. One that did not is worth
		// every byte: the full request is what the fix is written from.
		log.Printf("[%s] rmc pid=%d call=%d full request: %x", c.Endpoint.Name, c.PID, msg.CallID, msg.Body)
	}
	if response == nil {
		return
	}
	if err := c.sendData(response, 0); err != nil {
		log.Printf("[%s] rmc pid=%d call=%d answer not delivered: %v", c.Endpoint.Name, c.PID, msg.CallID, err)
	}
}

func headBytes(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// minorVersion is the server's Lite minor version, clamped to the client's.
const serverMinorVersion = 5

func minorVersion(client uint8) uint8 {
	if client < serverMinorVersion {
		return client
	}
	return serverMinorVersion
}

func random16() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}

// blobReader is a minimal u32le-length-prefixed blob reader (CONNECT payload).
type blobReader struct {
	b []byte
	o int
}

func (r *blobReader) Buffer() ([]byte, error) {
	if len(r.b)-r.o < 4 {
		return nil, fmt.Errorf("prudplite: truncated blob length")
	}
	n := int(binary.LittleEndian.Uint32(r.b[r.o:]))
	r.o += 4
	if n < 0 || len(r.b)-r.o < n {
		return nil, fmt.Errorf("prudplite: truncated blob (%d < %d)", len(r.b)-r.o, n)
	}
	v := append([]byte{}, r.b[r.o:r.o+n]...)
	r.o += n
	return v, nil
}
