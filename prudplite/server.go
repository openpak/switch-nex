// Raw PRUDPLite-over-TLS connection state machine.
//
// Switch titles speak raw PRUDPLite frames over a TLS stream: no websocket
// upgrade, no payload encryption (TLS carries confidentiality), no reliable
// retransmission (TCP carries reliability and ordering). The state machine
// mirrors the handshake, acknowledges what asks to be acknowledged,
// reassembles fragments, and dispatches complete RMC messages in order.
package prudplite

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"

	"github.com/openpak/switch-nex/rmc"
)

// Endpoint serves one PRUDP virtual stream on one TLS listener.
type Endpoint struct {
	StreamID uint8
	IsSecure bool
	// Signature keys the connection signature on the SYN-ACK (HMAC-MD5 over
	// the client ip‖port).
	Signature [16]byte
	// RecallPID resolves a ticketless secure CONNECT to a login PID by client
	// IP. Returning false drops the connection.
	RecallPID func(host string) (uint64, bool)
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
	delete(e.conns, c.Host)
	e.mu.Unlock()
	if e.OnDisconnect != nil {
		e.OnDisconnect(c)
	}
}

// Connection is one client's PRUDPLite session.
type Connection struct {
	Endpoint *Endpoint
	Remote   *net.TCPAddr
	Host     string // client IP — the recall key

	PID        uint64
	Signature  []byte
	SessionKey []byte

	State   int
	SendSeq uint16

	fragBuf  []byte
	conn     net.Conn
	writeMu  sync.Mutex
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

func (c *Connection) ack(p *Packet) {
	ack := &Packet{
		Type:                   p.Type,
		Flags:                  FlagAck,
		SequenceID:             p.SequenceID,
		SourceStreamType:       p.DestinationStreamType,
		DestinationStreamType:  p.SourceStreamType,
		SourceVirtualPort:      p.DestinationVirtualPort,
		DestinationVirtualPort: p.SourceVirtualPort,
		FragmentID:             p.FragmentID,
	}
	_ = c.send(ack)
}

func (c *Connection) sendData(payload []byte, fragmentID uint8) error {
	c.SendSeq++
	p := &Packet{
		Type:                   Data,
		Flags:                  FlagReliable | FlagNeedsAck,
		SequenceID:             c.SendSeq,
		SourceStreamType:       StreamState,
		DestinationStreamType:  StreamState,
		SourceVirtualPort:      c.Endpoint.StreamID,
		DestinationVirtualPort: 0x1F,
		FragmentID:             fragmentID,
		Payload:                payload,
	}
	return c.send(p)
}

func (c *Connection) serve() {
	defer c.conn.Close()
	defer c.Endpoint.forget(c)
	head := make([]byte, 12)
	buf := make([]byte, 4096)
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
		total := 12 + optionsLength + payloadLength
		rest := make([]byte, total-12)
		if _, err := io.ReadFull(c.conn, rest); err != nil {
			return
		}
		p, _, err := Decode(head)
		if err != nil {
			return
		}
		p.Options = append(p.Options, rest[12:12+optionsLength]...)
		p.Payload = append(p.Payload, rest[12+optionsLength:]...)
		c.handle(p)
		_ = buf
	}
}

func (c *Connection) handle(p *Packet) {
	switch p.Type {
	case Syn:
		c.handleSyn(p)
	case Connect:
		c.handleConnect(p)
	case Data:
		c.handleData(p)
	case Ping:
		c.ack(p)
	case Disconnect:
		c.ack(p)
		c.Endpoint.forget(c)
		c.Endpoint.OnDisconnect(c)
	}
}

func (c *Connection) handleSyn(p *Packet) {
	c.Signature = random16()
	ack := &Packet{
		Type:                   Syn,
		Flags:                  FlagAck,
		SequenceID:             p.SequenceID,
		SourceStreamType:       p.DestinationStreamType,
		DestinationStreamType:  p.SourceStreamType,
		SourceVirtualPort:      p.DestinationVirtualPort,
		DestinationVirtualPort: p.SourceVirtualPort,
		Options:                append([]byte{OptConnectionSignature, 16}, c.Signature...),
	}
	_ = c.send(ack)
}

func (c *Connection) handleConnect(p *Packet) {
	// Ticketless secure CONNECT (empty payload): recall the login PID by the
	// client IP. Auth-endpoint CONNECTs also arrive empty and just mark the
	// session as connected.
	if c.Endpoint.IsSecure && c.PID == 0 {
		if pid, ok := c.Endpoint.RecallPID(c.Host); ok {
			c.PID = pid
		}
	}
	if c.Endpoint.OnConnect != nil {
		c.Endpoint.OnConnect(c)
	}
	c.State = StateConnected

	ack := &Packet{
		Type:                   Connect,
		Flags:                  FlagAck,
		SequenceID:             p.SequenceID,
		SourceStreamType:       p.DestinationStreamType,
		DestinationStreamType:  p.SourceStreamType,
		SourceVirtualPort:      p.DestinationVirtualPort,
		DestinationVirtualPort: p.SourceVirtualPort,
	}
	_ = c.send(ack)
}

func (c *Connection) handleData(p *Packet) {
	c.ack(p)

	// Fragment reassembly: fragments 1..n accumulate; fragment 0 completes the
	// message. TCP orders the stream, so plain appending is correct.
	var assembled []byte
	if p.FragmentID != 0 {
		c.fragBuf = append(c.fragBuf, p.Payload...)
		return
	}
	assembled = append(c.fragBuf, p.Payload...)
	c.fragBuf = nil

	if len(assembled) < 10 {
		return
	}
	msg, err := rmc.DecodeRequest(assembled)
	if err != nil {
		return
	}
	var response []byte
	if c.Endpoint.OnRMC != nil {
		response = c.Endpoint.OnRMC(c, msg)
	}
	if response == nil {
		return
	}
	c.SendSeq++
	rr := &Packet{
		Type:                   Data,
		Flags:                  FlagReliable | FlagNeedsAck,
		SequenceID:             c.SendSeq,
		SourceStreamType:       StreamState,
		DestinationStreamType:  StreamState,
		SourceVirtualPort:      c.Endpoint.StreamID,
		DestinationVirtualPort: 0x1F,
		Payload:                response,
	}
	_ = c.send(rr)
}

func random16() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}
