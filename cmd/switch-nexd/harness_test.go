package main

// End-to-end: a real console-shaped client against the real server stack, over
// TLS, through the real handshake. Everything below would have passed the unit
// tests while failing a console, which is the whole point of having it.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// testTLS builds a throwaway certificate. The console negotiates legacy TLS, so
// the server floor is 1.0 and the harness has to accept that too.
func testTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "*.s.n.srv.nintendo.net"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS10,
	}
}

// start brings up one title on ephemeral ports with identity resolution off,
// which is the shape of the fleet today.
func start(t *testing.T, name string) (authAddr, secureAddr string, svc *service) {
	return startWith(t, name, newIdentity("", ""))
}

// startWith brings up one title against a given identity resolver.
func startWith(t *testing.T, name string, ids *identity) (authAddr, secureAddr string, svc *service) {
	t.Helper()
	var spec title
	found := false
	for _, ti := range titles {
		if ti.name == name {
			spec, found = ti, true
			break
		}
	}
	if !found {
		t.Fatalf("no such title: %s", name)
	}

	cfg := testTLS(t)
	authLn, err := tls.Listen("tcp", "127.0.0.1:0", cfg.Clone())
	if err != nil {
		t.Fatal(err)
	}
	secureLn, err := tls.Listen("tcp", "127.0.0.1:0", cfg.Clone())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authLn.Close(); secureLn.Close() })

	// Memory-only recall: a test must not inherit another run's state.
	svc = newService(spec, "127.0.0.1", newRecall(time.Hour, ""), ids)
	svc.serve(authLn, secureLn)
	return authLn.Addr().String(), secureLn.Addr().String(), svc
}

// TestLoginThroughToSecureSession is the flow that matters: log in on the auth
// stage, spend the ticket on the secure stage, and get a real answer back. It
// covers both login methods because they return DIFFERENT bodies and a console
// only ever exercises one of them.
func TestLoginThroughToSecureSession(t *testing.T) {
	for _, tc := range []struct {
		name       string
		structured bool
	}{
		{"method 2 (LoginEx)", false},
		{"method 6 (WithParam)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authAddr, secureAddr, _ := start(t, "mario-kart-8-deluxe")
			key := titleAccessKey(t, "mario-kart-8-deluxe")

			c := dial(t, authAddr, key)
			c.login(randomName("player"), tc.structured)
			if c.pid == 0 {
				t.Fatal("login returned PID 0")
			}
			if c.stationURL == "" {
				t.Fatal("login named no secure endpoint")
			}

			// The secure stage: a fresh connection that spends the ticket.
			// This is the assertion that the ticket is real — the server
			// decrypts it with a key the client never saw.
			s := dial(t, secureAddr, key)
			s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
			ack := s.handshake(s.secureConnectPayload(1))
			if len(ack) != 8 {
				t.Fatalf("CONNECT-ACK is %d bytes, want 8", len(ack))
			}
			// The server proves it holds the session key by returning check+1.
			if got := le32(ack[4:]); got != 0x11223344+1 {
				t.Fatalf("check response = %#x, want %#x", got, 0x11223344+1)
			}

			// And it answers a real protocol call on that session.
			resp := s.call(ProtoUtility, 0x01, nil) // AcquireNexUniqueID
			if resp.IsError {
				t.Fatalf("AcquireNexUniqueID: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
			}
			id, err := rmc.NewReader(resp.Body).U64()
			if err != nil {
				t.Fatalf("unique id: %v", err)
			}
			if id != c.pid {
				t.Errorf("unique id = %d, want the caller's PID %d", id, c.pid)
			}
		})
	}
}

// TestRegisterHandsBackTheClientsOwnStation is the P2P precondition. The
// console builds its mesh from the endpoint it is told the world sees for it;
// handed the server's own address it never recognises itself.
func TestRegisterHandsBackTheClientsOwnStation(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "mario-kart-8-deluxe")
	key := titleAccessKey(t, "mario-kart-8-deluxe")

	c := dial(t, authAddr, key)
	c.login(randomName("host"), false)

	s := dial(t, secureAddr, key)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(1))

	// The console reports only its LAN station, which is the common case.
	reported := rmc.NewWriter()
	reported.List(1)
	reported.String("prudp:/address=192.168.1.20;port=50000;sid=1;natf=0")

	for _, method := range []uint32{0x01, 0x04} { // Register and RegisterEx
		resp := s.call(ProtoSecureConnection, method, reported.Bytes())
		if resp.IsError {
			t.Fatalf("method %#x: %#08x", method, resp.ErrorCode|rmc.ErrorCodeBit)
		}
		r := rmc.NewReader(resp.Body)
		qr, err := r.U32()
		if err != nil || qr != rmc.ResultSuccess {
			t.Fatalf("method %#x qresult=%#x (%v) — a bare URL here is the old wrong shape", method, qr, err)
		}
		connID, err := r.U32()
		if err != nil || connID == 0 {
			t.Fatalf("method %#x connection id = %d", method, connID)
		}
		raw, err := r.String()
		if err != nil {
			t.Fatalf("method %#x url: %v", method, err)
		}
		if r.Remaining() != 0 {
			t.Errorf("method %#x left %d trailing bytes", method, r.Remaining())
		}

		u := switchnex.Parse(raw)
		if u.Get("address") == "127.0.0.1" && u.Get("port") == "0" {
			t.Errorf("method %#x handed back a bare station", method)
		}
		if got := u.Get("PID"); got != itoa(c.pid) {
			t.Errorf("method %#x PID=%q, want the caller's %d", method, got, c.pid)
		}
		if u.Get("RVCID") == "" {
			t.Errorf("method %#x station carries no RVCID", method)
		}
		// The synthesised public station keeps what the console set on its
		// LAN one; a station missing those is one Pia will not accept.
		if got := u.Get("natf"); got != "0" {
			t.Errorf("method %#x lost the console's natf: %q", method, got)
		}
	}
}

// TestTwoConsolesGetDistinctConnectionIDs. Every client used to be told it was
// connection 1, which two consoles cannot both be.
func TestTwoConsolesGetDistinctConnectionIDs(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "mario-kart-8-deluxe")
	key := titleAccessKey(t, "mario-kart-8-deluxe")

	ids := map[uint32]bool{}
	for i := range 2 {
		c := dial(t, authAddr, key)
		c.login(randomName("player"), false)

		s := dial(t, secureAddr, key)
		s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
		s.handshake(s.secureConnectPayload(uint32(i + 1)))

		reported := rmc.NewWriter()
		reported.List(1)
		reported.String("prudp:/address=192.168.1.20;port=50000")
		resp := s.call(ProtoSecureConnection, 0x04, reported.Bytes())

		r := rmc.NewReader(resp.Body)
		r.U32()
		id, _ := r.U32()
		if ids[id] {
			t.Fatalf("two connections were both told they are id %d", id)
		}
		ids[id] = true
	}
}

// TestFriendJoinResolvesTheHostsGathering walks the path a friend-join takes:
// the host creates a session, and the joiner asks which gathering the host is
// in. An empty answer here is a join that can never start.
func TestFriendJoinResolvesTheHostsGathering(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "mario-kart-8-deluxe")
	key := titleAccessKey(t, "mario-kart-8-deluxe")

	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")

	// The host opens a session.
	sess := sessionHolderBytes(4, 3, 77)
	if resp := host.call(ProtoMatchmakeExt, 0x06, sess); resp.IsError {
		t.Fatalf("create: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}

	// The joiner asks where its friend is.
	req := rmc.NewWriter()
	req.List(1)
	req.U64(host.pid)
	req.Bool(false)
	resp := joiner.call(ProtoMatchmakeExt, 0x3C, req.Bytes())
	if resp.IsError {
		t.Fatalf("playing session: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	r := rmc.NewReader(resp.Body)
	n, err := r.ListHeader()
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v — the host's gathering was not reported, so the join cannot start", n, err)
	}
	r.StructHeader()
	pid, _ := r.U64()
	gid, _ := r.U32()
	if pid != host.pid || gid == 0 {
		t.Fatalf("pid=%d gid=%d", pid, gid)
	}

	// And the joiner can act on it.
	if resp := joiner.call(ProtoMatchmakeExt, 0x07, u32le(gid)); resp.IsError {
		t.Fatalf("join gid=%d: %#08x", gid, resp.ErrorCode|rmc.ErrorCodeBit)
	}
}

// TestDataStoreNotFoundReachesTheClientIntact checks the error code survives
// the whole stack, not just the handler: the transposed constant produced a
// code the game does not recognise as "nothing saved yet".
func TestDataStoreNotFoundReachesTheClientIntact(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "arms") // a title with DataStore
	key := titleAccessKey(t, "arms")
	s := secureSession(t, authAddr, secureAddr, key, "player")

	resp := s.call(ProtoDataStore, 0x08, nil) // GetMeta
	if !resp.IsError {
		t.Fatal("GetMeta answered success; it must answer NotFound")
	}
	if got := resp.ErrorCode | rmc.ErrorCodeBit; got != 0x80690004 {
		t.Fatalf("wire code = %#08x, want 0x80690004", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// secureSession logs in and returns a connected secure-stage client.
func secureSession(t *testing.T, authAddr, secureAddr, key, who string) *client {
	t.Helper()
	c := dial(t, authAddr, key)
	c.login(randomName(who), false)
	s := dial(t, secureAddr, key)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(1))
	return s
}

// titleAccessKey returns a registered title's access key.
func titleAccessKey(t *testing.T, name string) string {
	t.Helper()
	for _, ti := range titles {
		if ti.name == name {
			return ti.accessKey
		}
	}
	t.Fatalf("no such title: %s", name)
	return ""
}

// sessionHolderBytes frames a CreateMatchmakeSession request.
func sessionHolderBytes(maxParticipants uint16, gameMode, attr0 uint32) []byte {
	inner := rmc.NewWriter()
	// Parent and child travel as siblings, each under its own header — the
	// shape a console sends (SPEC.md), not one nested in the other.
	inner.StructOf(0, func(w *rmc.Writer) { // Gathering base
		w.U32(0) // gid, assigned by the server
		w.U64(0) // owner
		w.U64(0) // host
		w.U16(1) // min
		w.U16(maxParticipants)
		w.U32(0) // participation policy
		w.U32(0) // policy argument
		w.U32(0) // flags
		w.U32(0) // state
		w.String("harness")
	})
	inner.StructOf(0, func(w *rmc.Writer) {
		w.U32(gameMode)
		w.List(1)
		w.U32(attr0)
		w.Bool(true) // open participation
		w.U32(0)     // matchmake system type
		w.Buffer(nil)
		w.U32(0)                                         // participation count
		w.U8(0)                                          // progress score
		w.Buffer(nil)                                    // session key
		w.U32(0)                                         // option
		w.StructOf(0, func(w *rmc.Writer) { w.List(0) }) // matchmake param
		w.U64(0)                                         // started time
		w.String("")                                     // user password
		w.U32(0)                                         // refer gid
		w.Bool(false)
		w.Bool(false)
		w.String("") // codeword
	})
	body := inner.Bytes()

	w := rmc.NewWriter()
	w.String("MatchmakeSession")
	w.U32(uint32(len(body) + 4))
	w.U32(uint32(len(body)))
	w.Raw(body)
	return w.Bytes()
}

func u32le(v uint32) []byte {
	w := rmc.NewWriter()
	w.U32(v)
	return w.Bytes()
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// Protocol ids the harness calls, mirrored here so the test reads as a client
// rather than reaching into the server's package for its own constants.
const (
	ProtoSecureConnection uint16 = 0x0B
	ProtoUtility          uint16 = 0x6E
	ProtoMatchmakeExt     uint16 = 0x6D
	ProtoDataStore        uint16 = 0x73
)

// TestLoginExShapeIsSwitchable covers both candidate answers to TicketGranting
// method 2. Two OpenPak lineages disagree about which one a console accepts and
// both cite console evidence about these same titles, so the server can serve
// either and this pins both to a client that has to read them.
//
// The shapes are genuinely confusable: the flat retval 0x00010001 is the bytes
// 01 00 01 00, which reads convincingly as a structure version byte followed by
// a length — which is how the disagreement arose in the first place.
func TestLoginExShapeIsSwitchable(t *testing.T) {
	for _, structured := range []bool{false, true} {
		name := "flat (this lineage, in production)"
		if structured {
			name = "structured (servers/nex)"
		}
		t.Run(name, func(t *testing.T) {
			*loginExStruct = structured
			t.Cleanup(func() { *loginExStruct = false })

			authAddr, secureAddr, _ := start(t, "mario-kart-8-deluxe")
			key := titleAccessKey(t, "mario-kart-8-deluxe")

			c := dial(t, authAddr, key)
			c.handshake(nil)
			w := rmc.NewWriter()
			w.String(randomName("player"))
			resp := c.call(0x0A, 0x02, w.Bytes())
			if resp.IsError {
				t.Fatalf("login refused: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
			}

			// Read it the way the matching client would, and insist the body
			// is consumed exactly — a shape that parses but leaves bytes over
			// is the wrong shape.
			if structured {
				c.readTicketResult(resp.Body)
			} else {
				c.readLoginExResult(resp.Body)
			}
			c.openTicket()

			// Whichever shape, the ticket has to work on the secure stage.
			s := dial(t, secureAddr, key)
			s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
			s.handshake(s.secureConnectPayload(1))
			if r := s.call(ProtoUtility, 0x01, nil); r.IsError {
				t.Fatalf("secure stage: %#08x", r.ErrorCode|rmc.ErrorCodeBit)
			}
		})
	}
}
