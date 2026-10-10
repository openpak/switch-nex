package main

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openpak/switch-nex/prudplite"
)

// dialAs connects the way Traefik does once its router speaks PROXY protocol:
// the header naming the console, then the console's own TLS. An empty from
// dials with no header at all.
func dialAs(t *testing.T, addr, accessKey, from string) *client {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if from != "" {
		// The console's own source port: distinct per connection, as behind
		// any NAT. The local one is as good as any.
		_, sport, _ := net.SplitHostPort(raw.LocalAddr().String())
		host, port, _ := net.SplitHostPort(addr)
		if _, err := raw.Write([]byte("PROXY TCP4 " + from + " " + host + " " + sport + " " + port + "\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10})
	t.Cleanup(func() { conn.Close() })
	return &client{t: t, conn: conn, accessKey: accessKey}
}

// connectTicketless does a "prudp"-scheme title's secure handshake: CONNECT
// with an empty payload. It reports whether the server accepted it.
func (c *client) connectTicketless() bool {
	c.t.Helper()
	c.send(&prudplite.Packet{Type: prudplite.Syn})
	synAck := c.recv()
	connect := &prudplite.Packet{
		Type:       prudplite.Connect,
		Flags:      prudplite.FlagReliable | prudplite.FlagNeedsAck,
		SequenceID: 1,
	}
	connect.SourceStreamType, connect.SourceVirtualPort = prudplite.StreamState, 0x0F
	connect.DestinationStreamType, connect.DestinationVirtualPort = prudplite.StreamState, 0x01
	connect.Signature = prudplite.LitePacketSignature(c.accessKey, connect, synAck.ConnectionSig)
	c.send(connect)
	reply := c.recv()
	return reply.Type == prudplite.Connect && reply.HasFlag(prudplite.FlagAck)
}

// recordBinds wraps the secure endpoint's OnConnect (before anyone connects)
// and returns a snapshot of which hosts each PID was bound for.
func recordBinds(svc *service) func() map[uint64][]string {
	var mu sync.Mutex
	binds := map[uint64][]string{}
	inner := svc.secure.OnConnect
	svc.secure.OnConnect = func(c *prudplite.Connection) {
		mu.Lock()
		binds[c.PID] = append(binds[c.PID], c.Host)
		mu.Unlock()
		inner(c)
	}
	return func() map[uint64][]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[uint64][]string{}
		for k, v := range binds {
			out[k] = append([]string(nil), v...)
		}
		return out
	}
}

// The sweep's failure end to end: two consoles behind one public address log
// in to MK8D (a "prudp" title, so its secure CONNECT carries no ticket) and
// both connect before either is settled. Neither may be handed the other's
// PID. They are refused; a ticket-carrying CONNECT from the same pair binds
// each to its own login.
func TestTwoConcurrentLoginsFromOneAddress(t *testing.T) {
	const addr = "203.0.113.7"
	authAddr, secureAddr, svc := start(t, mk8d)
	key := titleAccessKey(t, mk8d)
	binds := recordBinds(svc)

	a, b := dialAs(t, authAddr, key, addr), dialAs(t, authAddr, key, addr)
	a.login(randomName("host"), false)
	b.login(randomName("joiner"), false)
	if a.pid == b.pid {
		t.Fatal("two names logged in as one PID")
	}

	for _, who := range []string{"first", "second"} {
		s := dialAs(t, secureAddr, key, addr)
		if s.connectTicketless() {
			t.Fatalf("%s ticketless CONNECT was bound to a login it cannot prove", who)
		}
	}

	for _, c := range []*client{a, b} {
		s := dialAs(t, secureAddr, key, addr)
		s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
		s.handshake(s.secureConnectPayload(1))
	}
	waitFor(t, func() bool { got := binds(); return len(got[a.pid]) == 1 && len(got[b.pid]) == 1 && len(got) == 2 })
}

// With the console's address on the login, two households logging in in the
// same second each get their own PID on a ticketless CONNECT.
func TestConcurrentLoginsFromTwoAddressesPairByAddress(t *testing.T) {
	authAddr, secureAddr, svc := start(t, mk8d)
	key := titleAccessKey(t, mk8d)
	binds := recordBinds(svc)

	a, b := dialAs(t, authAddr, key, "203.0.113.7"), dialAs(t, authAddr, key, "198.51.100.9")
	a.login(randomName("home"), false)
	b.login(randomName("neighbor"), false)

	// The second household connects first: order must not matter.
	if !dialAs(t, secureAddr, key, "198.51.100.9").connectTicketless() {
		t.Fatal("neighbor's ticketless CONNECT refused")
	}
	if !dialAs(t, secureAddr, key, "203.0.113.7").connectTicketless() {
		t.Fatal("home's ticketless CONNECT refused")
	}
	waitFor(t, func() bool { return len(binds()) == 2 })
	got := binds()
	if len(got[a.pid]) != 1 || got[a.pid][0] != "203.0.113.7" || len(got[b.pid]) != 1 || got[b.pid][0] != "198.51.100.9" {
		t.Fatalf("a household was bound to the other's login: %d/%d", len(got[a.pid]), len(got[b.pid]))
	}
}

// One console at a time, through Traefik as it is today (no header), still
// works: the ticketless CONNECT takes the one login waiting for it.
func TestTicketlessConnectWithoutProxyHeaderStillPairs(t *testing.T) {
	authAddr, secureAddr, svc := start(t, mk8d)
	key := titleAccessKey(t, mk8d)
	binds := recordBinds(svc)

	a := dialAs(t, authAddr, key, "")
	a.login(randomName("solo"), false)
	if !dialAs(t, secureAddr, key, "").connectTicketless() {
		t.Fatal("a lone login's ticketless CONNECT was refused")
	}
	waitFor(t, func() bool { return len(binds()[a.pid]) == 1 })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestReadProxyHeader(t *testing.T) {
	v2 := append([]byte{}, proxyV2Sig...)
	v2 = append(v2, 0x21, 0x11, 0, 12, 203, 0, 113, 7, 10, 0, 0, 1)
	v2 = binary.BigEndian.AppendUint16(v2, 40001)
	v2 = binary.BigEndian.AppendUint16(v2, 22150)

	for name, tc := range map[string]struct {
		in, want string
		rest     string
	}{
		"v1":      {"PROXY TCP4 203.0.113.7 10.0.0.1 40001 22150\r\n\x16\x03", "203.0.113.7:40001", "\x16\x03"},
		"v1-tcp6": {"PROXY TCP6 2001:db8::1 ::1 40001 22150\r\n\x16", "[2001:db8::1]:40001", "\x16"},
		"unknown": {"PROXY UNKNOWN\r\n\x16", "", "\x16"},
		"v2":      {string(v2) + "\x16", "203.0.113.7:40001", "\x16"},
		"none":    {"\x16\x03\x01", "", "\x16\x03\x01"},
	} {
		br := bufio.NewReader(strings.NewReader(tc.in))
		got, err := readProxyHeader(br)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if (got == nil && tc.want != "") || (got != nil && got.String() != tc.want) {
			t.Fatalf("%s: got %v want %q", name, got, tc.want)
		}
		rest := make([]byte, len(tc.rest))
		if _, err := br.Read(rest); err != nil || string(rest) != tc.rest {
			t.Fatalf("%s: stream after the header is %q", name, rest)
		}
	}
	if _, err := readProxyHeader(bufio.NewReader(strings.NewReader("PROXY TCP4 nonsense\r\n"))); err == nil {
		t.Fatal("malformed v1 header accepted")
	}
}
