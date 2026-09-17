package main

// The console half of the wire, enough to drive a real login and a real secure
// session: TLS, the PRUDPLite handshake, kerberos ticket handling and RMC
// framing. Written against the server's own packet codec but never its
// handlers, so a shape the server gets wrong is a shape this cannot read.
//
// This is the only thing in the repository that exercises the stack the way a
// console does. Unit tests assert one answer at a time; they cannot catch a
// login response the client refuses, because nothing in them is a client.

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// client is one console-side connection.
type client struct {
	pushes    []*rmc.RMCMessage // server-initiated requests seen while waiting for answers
	t         *testing.T
	conn      net.Conn
	accessKey string
	connSig   []byte
	seq       uint16
	callID    uint32

	// learned at login
	pid        uint64
	ticket     []byte
	sourceKey  []byte
	sessionKey []byte
	stationURL string
}

func dial(t *testing.T, addr, accessKey string) *client {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return &client{t: t, conn: conn, accessKey: accessKey}
}

func (c *client) send(p *prudplite.Packet) {
	c.t.Helper()
	// The console addresses the server's virtual port and speaks from its own.
	// The server learns both from the SYN and mirrors them onto every reply.
	p.SourceStreamType, p.SourceVirtualPort = prudplite.StreamState, 0x0F
	p.DestinationStreamType, p.DestinationVirtualPort = prudplite.StreamState, 0x01
	if _, err := c.conn.Write(p.Encode()); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// recv reads one frame. The header is fixed-width and carries both lengths, so
// a frame is read exactly rather than guessed at.
func (c *client) recv() *prudplite.Packet {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	head := make([]byte, 12)
	if _, err := io.ReadFull(c.conn, head); err != nil {
		c.t.Fatalf("read header: %v", err)
	}
	rest := make([]byte, int(head[1])+int(binary.LittleEndian.Uint16(head[2:4])))
	if _, err := io.ReadFull(c.conn, rest); err != nil {
		c.t.Fatalf("read body: %v", err)
	}
	p, _, err := prudplite.Decode(append(head, rest...))
	if err != nil {
		c.t.Fatalf("decode: %v", err)
	}
	return p
}

// handshake performs SYN then CONNECT, returning the CONNECT-ACK payload.
// payload is empty for the auth stage and carries the ticket on the secure one.
func (c *client) handshake(payload []byte) []byte {
	c.t.Helper()
	c.send(&prudplite.Packet{Type: prudplite.Syn, SequenceID: 0})
	synAck := c.recv()
	if synAck.Type != prudplite.Syn || !synAck.HasFlag(prudplite.FlagAck) {
		c.t.Fatalf("expected SYN-ACK, got %s", synAck)
	}
	if len(synAck.ConnectionSig) != 16 {
		c.t.Fatalf("connection signature is %d bytes, want 16", len(synAck.ConnectionSig))
	}
	c.connSig = synAck.ConnectionSig

	// RELIABLE|NEEDS_ACK is not decoration: the Lite signature is only defined
	// for a CONNECT that asks to be acknowledged, so an unflagged one carries
	// no proof of the access key and is dropped without a reply.
	connect := &prudplite.Packet{
		Type:       prudplite.Connect,
		Flags:      prudplite.FlagReliable | prudplite.FlagNeedsAck,
		SequenceID: 1,
		Payload:    payload,
	}
	// The Lite signature is the client's proof it knows the access key.
	connect.SourceStreamType, connect.SourceVirtualPort = prudplite.StreamState, 0x0F
	connect.DestinationStreamType, connect.DestinationVirtualPort = prudplite.StreamState, 0x01
	connect.Signature = prudplite.LitePacketSignature(c.accessKey, connect, c.connSig)
	c.send(connect)

	connAck := c.recv()
	if connAck.Type != prudplite.Connect || !connAck.HasFlag(prudplite.FlagAck) {
		c.t.Fatalf("expected CONNECT-ACK, got %s", connAck)
	}
	return connAck.Payload
}

// call sends one RMC request and returns the decoded response, acknowledging
// the server's DATA frame the way a console does.
func (c *client) call(protocol uint16, method uint32, body []byte) *rmc.RMCMessage {
	c.t.Helper()
	c.callID++
	c.seq++
	c.send(&prudplite.Packet{
		Type:       prudplite.Data,
		Flags:      prudplite.FlagReliable | prudplite.FlagNeedsAck,
		SequenceID: c.seq,
		Payload:    rmc.EncodeRequest(protocol, method, c.callID, body),
	})

	for {
		p := c.recv()
		if p.Type != prudplite.Data || p.HasFlag(prudplite.FlagAck) || p.HasFlag(prudplite.FlagMultiAck) {
			continue // our own frame being acknowledged
		}
		if p.HasFlag(prudplite.FlagNeedsAck) {
			ack := &prudplite.Packet{Type: p.Type, Flags: prudplite.FlagAck, SequenceID: p.SequenceID}
			c.send(ack)
		}
		msg, err := rmc.Decode(p.Payload)
		if err != nil {
			c.t.Fatalf("undecodable RMC (%d bytes): %v", len(p.Payload), err)
		}
		if msg.IsRequest {
			c.pushes = append(c.pushes, msg) // server-initiated; not the answer to this call
			continue
		}
		return msg
	}
}

// login runs TicketGranting and keeps what the console keeps. structured
// selects method 6 (the NEX 4.4+ path) over method 2.
func (c *client) login(username string, structured bool) {
	c.t.Helper()
	c.handshake(nil)

	var body []byte
	method := uint32(0x02)
	if structured {
		method = 0x06
		w := rmc.NewWriter()
		w.StructOf(1, func(n *rmc.Writer) {
			n.U32(3) // PlatformType
			n.String(username)
		})
		body = w.Bytes()
	} else {
		w := rmc.NewWriter()
		w.String(username)
		body = w.Bytes()
	}

	resp := c.call(0x0A, method, body)
	if resp.IsError {
		c.t.Fatalf("login method %#x refused: %#08x", method, resp.ErrorCode|rmc.ErrorCodeBit)
	}
	if structured {
		c.readTicketResult(resp.Body)
	} else {
		c.readLoginExResult(resp.Body)
	}

	c.openTicket()
}

// openTicket does what the console does with what it was handed: decrypt the
// outer layer with the source key to recover the session key, keeping the inner
// blob untouched for the secure stage. A ticket that will not open here is a
// login that only looked like it worked.
func (c *client) openTicket() {
	c.t.Helper()
	outer, err := switchnex.DecryptClientTicket(c.ticket, c.sourceKey)
	if err != nil {
		c.t.Fatalf("ticket does not open with the source key we were handed: %v", err)
	}
	c.sessionKey = outer.SessionKey
	c.ticket = outer.Internal
}

// readLoginExResult reads the flat method-2 body and insists it is consumed
// exactly. Trailing bytes mean the layout is wrong even if every field parsed.
func (c *client) readLoginExResult(body []byte) {
	c.t.Helper()
	r := rmc.NewReader(body)
	retval, err := r.U32()
	if err != nil || retval != rmc.ResultSuccess {
		c.t.Fatalf("retval = %#x (%v)", retval, err)
	}
	if c.pid, err = r.U64(); err != nil {
		c.t.Fatalf("SourcePID: %v", err)
	}
	if c.ticket, err = r.Buffer(); err != nil {
		c.t.Fatalf("ticket: %v", err)
	}
	// RVConnectionData is a versioned structure here, unlike method 6.
	if _, _, err := r.StructHeader(); err != nil {
		c.t.Fatalf("RVConnectionData header: %v", err)
	}
	if c.stationURL, err = r.String(); err != nil {
		c.t.Fatalf("station URL: %v", err)
	}
	n, err := r.ListHeader()
	if err != nil {
		c.t.Fatalf("special protocols: %v", err)
	}
	for range n {
		r.U8()
	}
	special, err := r.String()
	if err != nil {
		c.t.Fatalf("special station: %v", err)
	}
	if special != "prudp:/" {
		c.t.Errorf("special station = %q, want %q — the bare scheme word is a different string", special, "prudp:/")
	}
	if _, err := r.U64(); err != nil {
		c.t.Fatalf("server time: %v", err)
	}
	if _, err := r.String(); err != nil {
		c.t.Fatalf("return message: %v", err)
	}
	keyHex, err := r.String()
	if err != nil {
		c.t.Fatalf("source key: %v", err)
	}
	if c.sourceKey, err = hex.DecodeString(keyHex); err != nil {
		c.t.Fatalf("source key is not hex: %v", err)
	}
	if r.Remaining() != 0 {
		c.t.Errorf("%d bytes left after the method-2 body; the layout is wrong", r.Remaining())
	}
}

// readTicketResult reads the method-6 structure. It is NOT the method-2 body
// with a header: no retval, and the endpoint is a bare URL string followed by
// the server time rather than an RVConnectionData structure.
func (c *client) readTicketResult(body []byte) {
	c.t.Helper()
	r := rmc.NewReader(body)
	version, length, err := r.StructHeader()
	if err != nil {
		c.t.Fatalf("structure header: %v", err)
	}
	if version != 1 {
		c.t.Errorf("structure version = %d, want 1", version)
	}
	if int(length) != r.Remaining() {
		c.t.Errorf("header claims %d content bytes, %d follow", length, r.Remaining())
	}
	if c.pid, err = r.U64(); err != nil {
		c.t.Fatalf("SourcePID: %v", err)
	}
	if c.pid == uint64(rmc.ResultSuccess) {
		c.t.Fatal("first field reads as the success retval: this is the method-2 body wrapped in a header")
	}
	if c.ticket, err = r.Buffer(); err != nil {
		c.t.Fatalf("ticket: %v", err)
	}
	if c.stationURL, err = r.String(); err != nil {
		c.t.Fatalf("service node URL: %v", err)
	}
	if _, err := r.U64(); err != nil {
		c.t.Fatalf("server time: %v", err)
	}
	if _, err := r.String(); err != nil {
		c.t.Fatalf("return message: %v", err)
	}
	keyHex, err := r.String()
	if err != nil {
		c.t.Fatalf("source key: %v", err)
	}
	if c.sourceKey, err = hex.DecodeString(keyHex); err != nil {
		c.t.Fatalf("source key is not hex: %v", err)
	}
	if r.Remaining() != 0 {
		c.t.Errorf("%d bytes left after the method-6 structure", r.Remaining())
	}
}

// secureConnectPayload builds what a console sends on the secure stage: the
// ticket it was given, plus check-data encrypted under the session key proving
// it opened the outer layer.
func (c *client) secureConnectPayload(cid uint32) []byte {
	c.t.Helper()
	check := make([]byte, 16)
	binary.LittleEndian.PutUint64(check[0:8], c.pid)
	binary.LittleEndian.PutUint32(check[8:12], cid)
	binary.LittleEndian.PutUint32(check[12:16], 0x11223344)

	out := binary.LittleEndian.AppendUint32(nil, uint32(len(c.ticket)))
	out = append(out, c.ticket...)
	enc := switchnex.EncryptSum(c.sessionKey, check)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(enc)))
	return append(out, enc...)
}

// randomName keeps two clients in one test from colliding on a PID.
func randomName(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}
