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
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// Endpoint serves one PRUDP virtual stream on one TLS listener.
type Endpoint struct {
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

	ln    net.Listener
	mu    sync.Mutex
	conns map[string]*Connection
	quit  chan struct{}
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
		}
		e.mu.Lock()
		e.conns[c.Host] = c
		e.mu.Unlock()
		go c.serve()
	}
}

func (e *Endpoint) forget(c *Connection) {
	e.mu.Lock()
	_, had := e.conns[c.Host]
	delete(e.conns, c.Host)
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
	CID        uint32
	Signature  []byte // our connection signature (SYN-ACK)
	SessionKey []byte

	// Learned virtual ports (from the client's SYN): we answer AS the
	// port the client targeted and address the client's source port.
	srcType, dstType StreamType
	srcPort, dstPort uint8

	State   int
	SendSeq uint16

	fragBuf []byte
	conn    net.Conn
	writeMu sync.Mutex
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

// sendData sends one reliable DATA frame on the learned virtual ports.
func (c *Connection) sendData(payload []byte, fragmentID uint8) error {
	c.SendSeq++
	p := &Packet{
		Type:       Data,
		Flags:      FlagReliable | FlagNeedsAck,
		SequenceID: c.SendSeq,
		FragmentID: fragmentID,
		Payload:    payload,
	}
	c.mirror(p)
	return c.send(p)
}

func (c *Connection) serve() {
	defer c.conn.Close()
	defer c.Endpoint.forget(c)
	head := make([]byte, 12)
	for {
		c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		if _, err := io.ReadFull(c.conn, head); err != nil {
			return
		}
		if head[0] != 0x80 {
			return
		}
		optionsLength := int(head[1])
		payloadLength := int(binary.LittleEndian.Uint16(head[2:4]))
		rest := make([]byte, optionsLength+payloadLength)
		if _, err := io.ReadFull(c.conn, rest); err != nil {
			return
		}
		frame := append(append([]byte{}, head...), rest...)
		p, _, err := Decode(frame)
		if err != nil {
			return
		}
		c.handle(p)
	}
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
			return
		}
	}

	response, err := c.processConnectPayload(p.Payload)
	if err != nil {
		log.Printf("prudplite: CONNECT rejected from %s: %v", c.Remote, err)
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
	var response []byte
	if c.Endpoint.OnRMC != nil {
		response = c.Endpoint.OnRMC(c, msg)
	}
	if response == nil {
		return
	}
	_ = c.sendData(response, 0)
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
