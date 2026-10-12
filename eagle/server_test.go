package eagle

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type testClient struct {
	t    *testing.T
	ws   *websocket.Conn
	node uint16
}

func (c *testClient) receive(op byte) []byte {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	kind, data, err := c.ws.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || len(data) < 3 {
		c.t.Fatalf("receive op=%d: kind=%d len=%d err=%v", op, kind, len(data), err)
	}
	header := uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
	if header>>22 != 0 || byte(header>>14) != op || header&7 != 0 {
		c.t.Fatalf("unexpected server header %06x, wanted op %d", header, op)
	}
	return data
}

func (c *testClient) write(data []byte) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageBinary, data); err != nil {
		c.t.Fatal(err)
	}
}

func openClient(t *testing.T, base, path string) *testClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+path,
		&websocket.DialOptions{Subprotocols: []string{"eagle"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	if ws.Subprotocol() != "eagle" {
		t.Fatal("the offered Eagle subprotocol was not negotiated")
	}
	c := &testClient{t: t, ws: ws}
	accepted := c.receive(0)
	if len(accepted) != 13 {
		t.Fatalf("Accepted length=%d, want 3+2+8", len(accepted))
	}
	c.node = binary.BigEndian.Uint16(accepted[3:])
	return c
}

func (c *testClient) authenticate(token string) {
	c.t.Helper()
	// Independent wire fixture: relay=0, payload=LoginRequest, source=0;
	// phase=0, last=true, connection check=0, P=3, app=0, DDL=0, "2.0.4".
	phase0, _ := hex.DecodeString("00400001000000000300000000000000000000000005322e302e34")
	c.write(phase0)
	token += "\x00"
	for len(token) > 0 {
		n := min(250, len(token))
		last := n == len(token)
		flag := byte(2)
		if last {
			flag++
		}
		data := append([]byte{0, 0x40, 0, flag, byte(n)}, token[:n]...)
		c.write(data)
		token = token[n:]
	}
}

func readyClient(t *testing.T, base, path, token string) *testClient {
	t.Helper()
	c := openClient(t, base, path)
	c.authenticate(token)
	result := c.receive(2)
	if len(result) != 27 || binary.BigEndian.Uint32(result[3:]) != 1 ||
		binary.BigEndian.Uint16(result[8:]) != 17 {
		t.Fatalf("invalid login result (%dB)", len(result))
	}
	c.write([]byte{0, 0xc0, 0}) // ClientReady, no payload
	all := c.receive(8)
	if len(all) != 140 || all[3] != 4 || all[4+int(c.node)/8]&(1<<uint(7-c.node%8)) == 0 {
		t.Fatal("readiness did not deliver the active-node bitmap")
	}
	return c
}

// Single-destination wire header uses exactly 32 bits. Forge the source in
// tests: a received client RPC must still identify its assigned source node.
func (c *testClient) rpc(op byte, target uint16, body []byte) {
	data := make([]byte, 4+8+len(body))
	binary.BigEndian.PutUint32(data, 1<<30|uint32(op)<<22|777<<11|uint32(target))
	copy(data[12:], body)
	c.write(data)
}

func TestTwoPlayersLoginRouteStartAndShareCounters(t *testing.T) {
	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(s)
	defer web.Close()
	oneToken, _ := s.Ticket(42, 100)
	twoToken, _ := s.Ticket(42, 200)
	one := readyClient(t, web.URL, "/42", oneToken)
	two := readyClient(t, web.URL, "/42", twoToken)
	added := one.receive(8)
	if added[3] != 0 || binary.BigEndian.Uint16(added[4:]) != two.node {
		t.Fatal("first player did not learn the second player's node")
	}

	// A counter increment/read is the game-side synchronization primitive.
	inc := make([]byte, 9)
	inc[0] = 3
	binary.BigEndian.PutUint64(inc[1:], 2)
	one.rpc(19, 0, inc)
	count := one.receive(16)
	if count[11] != 3 || binary.BigEndian.Uint64(count[12:]) != 2 {
		t.Fatal("counter increment did not return 2")
	}
	two.rpc(18, 0, []byte{3})
	count = two.receive(16)
	if binary.BigEndian.Uint64(count[12:]) != 2 {
		t.Fatal("counter is not shared across this room")
	}

	// SMB35 JoinMatch / StartMatch and game updates remain bit-identical.
	for _, op := range []byte{26, 28, 31, 38} {
		body := []byte{0xa7, 0x81, 0, 3, 0xff}
		one.rpc(op, two.node, body)
		got := two.receive(op)
		header := uint32(got[0])<<16 | uint32(got[1])<<8 | uint32(got[2])
		if uint16((header>>3)&2047) != one.node || !bytes.Equal(got[11:], body) {
			t.Fatal("relay changed the RPC payload or trusted a forged source node")
		}
	}
	one.rpc(28, nodeLimit+1, []byte{7}) // broadcast including source
	if !bytes.Equal(one.receive(28)[11:], []byte{7}) || !bytes.Equal(two.receive(28)[11:], []byte{7}) {
		t.Fatal("inclusive broadcast did not reach both participants")
	}

	// Another lobby has independent counters and receives none of this lobby's RPCs.
	otherToken, _ := s.Ticket(43, 300)
	other := readyClient(t, web.URL, "/43", otherToken)
	one.rpc(28, nodeLimit, []byte{8})
	two.receive(28)
	other.rpc(18, 0, []byte{3})
	if binary.BigEndian.Uint64(other.receive(16)[12:]) != 0 {
		t.Fatal("rooms share counters or route across lobby boundaries")
	}

	// Leaving NEX closes the matching relay peer and announces its departure.
	s.Leave(42, 200)
	removed := one.receive(8)
	if removed[3] != 3 || binary.BigEndian.Uint16(removed[4:]) != two.node {
		t.Fatal("departure was not announced")
	}
	s.CloseRoom(42)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := one.ws.Read(ctx); err == nil {
		t.Fatal("destroying a gathering left its relay connected")
	}
}

func TestTicketsRejectTamperingExpiryOtherRoomsAndRevokedPlayers(t *testing.T) {
	s, _ := NewServer()
	web := httptest.NewServer(s)
	defer web.Close()
	valid, _ := s.Ticket(42, 100)
	if _, err := verifyTicket(s.key, valid, 42, time.Now()); err != nil {
		t.Fatal("issued ticket is invalid")
	}
	for _, tc := range []struct {
		name, token string
	}{
		{"tampered", valid[:len(valid)-4] + "AAAA"},
		{"expired", signTicket(s.key, 42, 100, time.Now().Add(-time.Second))},
		{"other-room", signTicket(s.key, 43, 100, time.Now().Add(time.Hour))},
		{"unadmitted-player", signTicket(s.key, 42, 200, time.Now().Add(time.Hour))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := openClient(t, web.URL, "/42")
			c.authenticate(tc.token)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, _, err := c.ws.Read(ctx); err == nil {
				t.Fatal("invalid admission received a login result")
			}
		})
	}
	s.Leave(42, 100)
	c := openClient(t, web.URL, "/42")
	c.authenticate(valid)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := c.ws.Read(ctx); err == nil {
		t.Fatal("revoked participant could reconnect with an unexpired token")
	}
}

func TestPacketGoldenAndMalformedInputs(t *testing.T) {
	// Independent big-endian fixture for a single destination: relay=1,
	// RPC=28, source=1, target=1025 (broadcast including self).
	golden, _ := hex.DecodeString("47000c010000000000000001abcd")
	p, err := parsePacket(golden)
	if err != nil || p.op != 28 || len(p.targets) != 1 || p.targets[0] != 1025 ||
		!bytes.Equal(p.payload, golden[4:]) {
		t.Fatalf("golden packet: %+v, %v", p, err)
	}
	if !bytes.Equal(encodePacket(28, 1, []byte{0xaa}), []byte{7, 0, 8, 0xaa}) {
		t.Fatal("outgoing header differs from the independent wire fixture")
	}
	for _, data := range [][]byte{nil, {0}, {0, 0}, {0x80, 0, 0}, {0xc0, 0, 0}} {
		if _, err := parsePacket(data); err == nil {
			t.Fatalf("accepted malformed packet %x", data)
		}
	}
}
