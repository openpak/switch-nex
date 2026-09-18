package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// fakeBaas stands in for nx-baas. It records what it was asked so the tests can
// assert the request shape, not merely that a reply came back.
type fakeBaas struct {
	*httptest.Server
	gotToken string
	gotOpenPak  string
	gotKey   string
	status   int
	pid      uint64
}

func newFakeBaas(t *testing.T, pid uint64) *fakeBaas {
	t.Helper()
	f := &fakeBaas{status: http.StatusOK, pid: pid}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/switch/identity" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		f.gotKey = r.Header.Get("X-Internal-Key")
		var in struct {
			IDToken string `json:"id_token"`
			OpenPak    string `json:"openpak"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.gotToken, f.gotOpenPak = in.IDToken, in.OpenPak

		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"pid": f.pid, "baas_user_id": "nsa-abc", "nickname": "Player", "country": "GB",
		})
	}))
	t.Cleanup(f.Close)
	return f
}

// jwt builds a token with the given claims payload. Only the payload segment is
// read here — nx-baas is what checks the signature, deliberately not us.
func jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(b) + ".signature"
}

// authInfoHolder frames the DataHolder a login carries: a named object wrapped
// in two length prefixes, holding an AuthenticationInfo.
func authInfoHolder(token string) []byte {
	content := rmc.NewWriter()
	content.String(token)
	content.U32(0) // NGSVersion
	content.U32(0) // TokenType
	content.U32(0) // ServerVersion

	object := rmc.NewWriter()
	object.StructOf(0, func(*rmc.Writer) {}) // the empty Data base
	object.Struct(1, content.Bytes())

	body := object.Bytes()
	w := rmc.NewWriter()
	w.String("AuthenticationInfo")
	w.U32(uint32(len(body) + 4))
	w.U32(uint32(len(body)))
	w.Raw(body)
	return w.Bytes()
}

// TestAuthenticationTokenIsReadFromTheLogin: the token is nested three headers
// deep, and every layer has to be stepped over exactly.
func TestAuthenticationTokenIsReadFromTheLogin(t *testing.T) {
	want := jwt(t, map[string]any{"sub": "user-1"})
	r := rmc.NewReader(authInfoHolder(want))
	got, ok := authenticationToken(r)
	if !ok {
		t.Fatal("token not found in a well-formed holder")
	}
	if got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}

	// A holder naming something else is not an authentication info, and
	// guessing at its contents would read a token out of unrelated bytes.
	other := rmc.NewWriter()
	other.String("SomethingElse")
	other.U32(8)
	other.U32(4)
	other.Raw([]byte{1, 2, 3, 4})
	if _, ok := authenticationToken(rmc.NewReader(other.Bytes())); ok {
		t.Error("a holder of a different class was read as an authentication token")
	}

	// And an empty body is simply no token, not a crash.
	if _, ok := authenticationToken(rmc.NewReader(nil)); ok {
		t.Error("empty body produced a token")
	}
}

// TestOpenPakClaimIsOptional. A retail console sends no claim, which is normal and
// must not be treated as a failure; an emulator build sends one.
func TestOpenPakClaimIsOptional(t *testing.T) {
	if got := openpakClaim(jwt(t, map[string]any{"sub": "user-1"})); got != "" {
		t.Errorf("retail token yielded a claim %q", got)
	}
	if got := openpakClaim(jwt(t, map[string]any{"openpak": "nx2.bound"})); got != "nx2.bound" {
		t.Errorf("claim = %q, want nx2.bound", got)
	}
	// A bare claim passes through as itself.
	if got := openpakClaim("nx2.bare"); got != "nx2.bare" {
		t.Errorf("bare claim = %q", got)
	}
	// Garbage is no claim, not a panic.
	for _, junk := range []string{"", "not-a-jwt", "a.b", "a.!!!.c"} {
		if got := openpakClaim(junk); got != "" {
			t.Errorf("openpakClaim(%q) = %q", junk, got)
		}
	}
}

// TestRefusalCodesMatchConsoleBehaviour. These two are not interchangeable: the
// console refetches on TokenExpired and gives up on ValidationFailed, so
// answering the retryable one for an unknown account is a refresh loop.
func TestRefusalCodesMatchConsoleBehaviour(t *testing.T) {
	if got := loginResultCode(errTokenRejected); got != rmc.ResultAuthTokenExpired {
		t.Errorf("unproven token -> %#x, want TokenExpired %#x", got, rmc.ResultAuthTokenExpired)
	}
	if got := loginResultCode(errNoAccount); got != rmc.ResultAuthValidationFail {
		t.Errorf("unknown account -> %#x, want ValidationFailed %#x", got, rmc.ResultAuthValidationFail)
	}
}

// TestProvenLoginUsesTheAccountPID is the point of all of it: end to end, the
// PID in the ticket is the one nx-baas proved, not one hashed from a name
// anybody could have sent.
func TestProvenLoginUsesTheAccountPID(t *testing.T) {
	const accountPID = 0x1122334455
	baas := newFakeBaas(t, accountPID)
	ids := newIdentity(baas.URL, "secret-key")

	authAddr, secureAddr, _ := startWith(t, "mario-kart-8-deluxe", ids)
	key := titleAccessKey(t, "mario-kart-8-deluxe")
	token := jwt(t, map[string]any{"sub": "user-1", "openpak": "nx2.bound"})

	c := dial(t, authAddr, key)
	c.handshake(nil)
	w := rmc.NewWriter()
	w.String("ignored-name") // the name is a claim; the token is the proof
	w.Raw(authInfoHolder(token))
	resp := c.call(0x0A, 0x02, w.Bytes())
	if resp.IsError {
		t.Fatalf("login refused: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	c.readLoginExResult(resp.Body)
	c.openTicket()

	if c.pid != accountPID {
		t.Fatalf("PID = %#x, want the account's %#x — the login name must not decide identity", c.pid, accountPID)
	}
	if baas.gotToken != token {
		t.Errorf("nx-baas got token %q", baas.gotToken)
	}
	if baas.gotOpenPak != "nx2.bound" {
		t.Errorf("nx-baas got openpak %q, want the claim forwarded", baas.gotOpenPak)
	}
	if baas.gotKey != "secret-key" {
		t.Errorf("internal key = %q", baas.gotKey)
	}

	// And the proven PID is what the secure stage recovers from the ticket —
	// which is what makes the session authenticated rather than asserted.
	s := dial(t, secureAddr, key)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(1))
	idResp := s.call(ProtoUtility, 0x01, nil)
	got, _ := rmc.NewReader(idResp.Body).U64()
	if got != accountPID {
		t.Fatalf("secure stage sees PID %#x, want %#x", got, accountPID)
	}
}

// TestUnprovenLoginFallsBackUntilEnforced. The fallback is what lets the fleet
// migrate without locking everyone out; enforcement is what ends it.
func TestUnprovenLoginFallsBackUntilEnforced(t *testing.T) {
	baas := newFakeBaas(t, 999)
	baas.status = http.StatusUnauthorized // the token will not prove
	ids := newIdentity(baas.URL, "k")

	authAddr, _, _ := startWith(t, "mario-kart-8-deluxe", ids)
	key := titleAccessKey(t, "mario-kart-8-deluxe")
	token := jwt(t, map[string]any{"sub": "nobody"})

	login := func() *rmc.RMCMessage {
		c := dial(t, authAddr, key)
		c.handshake(nil)
		w := rmc.NewWriter()
		w.String("claimed-name")
		w.Raw(authInfoHolder(token))
		return c.call(0x0A, 0x02, w.Bytes())
	}

	// Fallback on: the login still succeeds, on the unproven name.
	*requireIdentity = false
	if resp := login(); resp.IsError {
		t.Fatalf("fallback login refused: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}

	// Enforcing: the same login is refused, with the retryable code, because
	// an unproven token is exactly the case a fresh token can fix.
	*requireIdentity = true
	t.Cleanup(func() { *requireIdentity = false })
	resp := login()
	if !resp.IsError {
		t.Fatal("enforcing, but an unprovable token still logged in")
	}
	if got := resp.ErrorCode; got != rmc.ResultAuthTokenExpired {
		t.Fatalf("refusal = %#x, want TokenExpired %#x", got, rmc.ResultAuthTokenExpired)
	}
}

// TestEnforcingClosesTheTicketlessRecallPath. The by-IP recall lets a secure
// CONNECT with no ticket inherit whoever last logged in from that address.
// While everyone is an unproven name that costs nothing; with real accounts it
// hands a stranger behind the same public address somebody else's session.
func TestEnforcingClosesTheTicketlessRecallPath(t *testing.T) {
	baas := newFakeBaas(t, 0x5150)
	ids := newIdentity(baas.URL, "k")
	authAddr, secureAddr, _ := startWith(t, "mario-kart-8-deluxe", ids)
	key := titleAccessKey(t, "mario-kart-8-deluxe")

	// A genuine login, so the recall table holds a real account's PID.
	c := dial(t, authAddr, key)
	c.handshake(nil)
	w := rmc.NewWriter()
	w.String("victim")
	w.Raw(authInfoHolder(jwt(t, map[string]any{"sub": "victim"})))
	if resp := c.call(0x0A, 0x02, w.Bytes()); resp.IsError {
		t.Fatalf("login: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}

	// Not enforcing: an empty CONNECT from the same address inherits it.
	*requireIdentity = false
	s := dial(t, secureAddr, key)
	s.send(&prudplite.Packet{Type: prudplite.Syn})
	s.connSig = s.recv().ConnectionSig
	if ack := s.handshake(nil); ack != nil && len(ack) != 0 {
		t.Logf("lenient path answered %d bytes", len(ack))
	}

	// Enforcing: the same ticketless CONNECT gets no reply at all, because
	// there is nothing in it that proves who is calling.
	*requireIdentity = true
	t.Cleanup(func() { *requireIdentity = false })

	s2 := dial(t, secureAddr, key)
	s2.send(&prudplite.Packet{Type: prudplite.Syn})
	synAck := s2.recv()
	s2.connSig = synAck.ConnectionSig
	connect := &prudplite.Packet{
		Type:       prudplite.Connect,
		Flags:      prudplite.FlagReliable | prudplite.FlagNeedsAck,
		SequenceID: 1,
	}
	connect.SourceStreamType, connect.SourceVirtualPort = prudplite.StreamState, 0x0F
	connect.DestinationStreamType, connect.DestinationVirtualPort = prudplite.StreamState, 0x01
	connect.Signature = prudplite.LitePacketSignature(key, connect, s2.connSig)
	s2.send(connect)

	s2.conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	if n, err := s2.conn.Read(buf); err == nil && n > 0 {
		// A DISCONNECT is the refusal — immediate, where the old behaviour
		// was thirty seconds of retransmitting into silence. The one answer
		// that would mean the client got in is a Connect-Ack.
		if p, _, err := prudplite.Decode(buf[:n]); err == nil &&
			p.Type == prudplite.Connect && p.HasFlag(prudplite.FlagAck) {
			t.Fatalf("enforcing, but a ticketless CONNECT was admitted: %x", buf[:n])
		}
	}
}
