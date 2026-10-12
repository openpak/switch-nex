package protocols

import (
	"testing"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func testEnv() *Env {
	e := NewEnv("test", "10.0.0.1", 22150)
	e.Match = NewMatchmakingStore()
	return e
}

func conn(pid uint64) *prudplite.Connection {
	return &prudplite.Connection{PID: pid, Host: "10.0.0.9"}
}

// request frames a client call the way the wire does.
func request(protocol uint16, method uint32, body []byte) *rmc.RMCMessage {
	return &rmc.RMCMessage{IsRequest: true, Protocol: protocol, MethodID: method, CallID: 7, Body: body}
}

func sessionHolder(s *Session) []byte {
	w := rmc.NewWriter()
	w.String("MatchmakeSession")
	n := rmc.NewWriter()
	s.write(n)
	w.U32(uint32(len(n.Bytes()) + 4))
	w.U32(uint32(len(n.Bytes())))
	w.Raw(n.Bytes())
	return w.Bytes()
}

// TestCreateThenFind exercises the flow MPS measured: create a session, find
// it back by gathering ID (sanitized — no session key), and by participant.
func TestCreateThenFind(t *testing.T) {
	env := testEnv()
	env.SanitizeDetailFind = true
	c := conn(1234)

	s := sampleSession()
	s.GID = 0
	resp := Dispatch(env, c, request(ProtoMatchmakeExt, 0x06, sessionHolder(s)))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("create failed: %#v", resp)
	}
	r := rmc.NewReader(body)
	created, err := readSession(r)
	if err != nil {
		t.Fatalf("create body: %v", err)
	}
	if created.GID == 0 {
		t.Fatal("no gid assigned")
	}
	gid := created.GID

	// FindMatchmakeSessionByGatheringIDDetail returns the session sanitized.
	resp = Dispatch(env, c, request(ProtoMatchmakeExt, 0x29, u32(gid)))
	ok, body, errCode = decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("detail find failed: %#v", resp)
	}
	found, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatalf("detail body: %v", err)
	}
	if len(found.SessionKey) != 0 || found.UserPassword != "" {
		t.Fatal("detail find leaked SessionKey/UserPassword (MPS rule)")
	}

	// FindMatchmakeSessionByParticipant returns (PID, session) pairs — the
	// full session, session key included, because a joiner needs the key. Same
	// env: the sanitizer only touches the detail-find path. The parameter is a
	// STRUCTURE, and the method only serves real results where a title asked
	// for them.
	env.FindByParticipant = true
	resp = Dispatch(env, c, request(ProtoMatchmakeExt, 0x33, participantParam(1234)))
	ok, body, errCode = decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("participant find failed: %#v", resp)
	}
	r = rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil || n != 1 {
		t.Fatalf("pairs: n=%d err=%v", n, err)
	}
	if _, _, err := r.StructHeader(); err != nil {
		t.Fatalf("pair header: %v", err)
	}
	pid, err := r.U64()
	if err != nil || pid != 1234 {
		t.Fatalf("pair pid=%d err=%v", pid, err)
	}
	pairSess, err := readSession(r)
	if err != nil {
		t.Fatalf("pair session: %v", err)
	}
	if len(pairSess.SessionKey) == 0 {
		t.Fatal("participant find lost the session key")
	}
	_ = gid
}

// TestJoinMissingSession answers SessionVoid for unknown gatherings — the MPS
// measured join-room behaviour when the host is gone.
func TestJoinMissingSession(t *testing.T) {
	env := testEnv()
	resp := Dispatch(env, conn(1), request(ProtoMatchmakeExt, 0x07, u32(999)))
	_, _, errCode := decodeResponse(resp)
	if errCode != rmc.ResultSessionVoid {
		t.Fatalf("want SessionVoid, got %#x", errCode)
	}
}

// TestUtilitySettingsCoversSMB35: index 0 serves the tuning table, index 10
// an empty map, anything else Core::InvalidArgument.
func TestUtilitySettingsCoversSMB35(t *testing.T) {
	env := testEnv()
	env.IntegerSettings = SMB35IntegerSettings

	w := rmc.NewWriter()
	w.U32(0)
	resp := Dispatch(env, conn(1), request(ProtoUtility, 0x07, w.Bytes()))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("index 0 failed")
	}
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil || n != 17 {
		t.Fatalf("index 0 table: n=%d err=%v", n, err)
	}

	w = rmc.NewWriter()
	w.U32(99)
	resp = Dispatch(env, conn(1), request(ProtoUtility, 0x07, w.Bytes()))
	_, _, errCode = decodeResponse(resp)
	if errCode != rmc.ResultInvalidArgument {
		t.Fatalf("index 99: want InvalidArgument, got %#x", errCode)
	}
}

// TestRefereeRoundLifecycle exercises the non-Eagle referee: start refuses
// non-participants, echoes its own parameters, and closes.
func TestRefereeRoundLifecycle(t *testing.T) {
	env := testEnv()
	env.MatchmakeReferee = true
	c := conn(77)

	// A gathering with pid 77 in it.
	s := sampleSession()
	s.GID = 0
	resp := Dispatch(env, c, request(ProtoMatchmakeExt, 0x06, sessionHolder(s)))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("create failed")
	}
	created, _ := readSession(rmc.NewReader(body))

	// StartRound with a non-participant is refused.
	ref := rmc.NewWriter()
	ref.StructOf(1, func(w *rmc.Writer) {
		w.U32(0) // personalDataCategory
		w.U32(created.GID)
		w.List(1)
		w.U64(666) // stranger
	})
	resp = Dispatch(env, c, request(ProtoMatchmakeReferee, 0x01, ref.Bytes()))
	_, _, errCode = decodeResponse(resp)
	if errCode != rmc.ResultRefereeNotJoined {
		t.Fatalf("want NotParticipatedGathering, got %#x", errCode)
	}

	// StartRound for a real participant returns the round id.
	ref = rmc.NewWriter()
	ref.StructOf(1, func(w *rmc.Writer) {
		w.U32(0)
		w.U32(created.GID)
		w.List(1)
		w.U64(77)
	})
	startBody := ref.Bytes()
	resp = Dispatch(env, c, request(ProtoMatchmakeReferee, 0x01, startBody))
	ok, body, errCode = decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("start failed: %#x", errCode)
	}
	roundID, _ := rmc.NewReader(body).U64()

	// GetStartRoundParam echoes the start body verbatim (Eagle-variant fields ride along).
	w := rmc.NewWriter()
	w.U64(roundID)
	resp = Dispatch(env, c, request(ProtoMatchmakeReferee, 0x02, w.Bytes()))
	ok, body, errCode = decodeResponse(resp)
	if errCode != 0 || !ok || string(body) != string(startBody) {
		t.Fatalf("param echo mismatch (err=%#x)", errCode)
	}

	// EndRoundWithoutReport is method 5, not 4 — 4 is the PARTIAL report and
	// takes a different parameter. A second close answers RoundNotFound.
	w = rmc.NewWriter()
	w.U64(roundID)
	resp = Dispatch(env, c, request(ProtoMatchmakeReferee, 0x05, w.Bytes()))
	_, _, errCode = decodeResponse(resp)
	if errCode != 0 {
		t.Fatalf("close failed: %#x", errCode)
	}
	resp = Dispatch(env, c, request(ProtoMatchmakeReferee, 0x05, w.Bytes()))
	_, _, errCode = decodeResponse(resp)
	if errCode != rmc.ResultRefereeRoundGone {
		t.Fatalf("want RoundNotFound, got %#x", errCode)
	}
}

func TestEagleRefereeAll35PlayersCanReport(t *testing.T) {
	env := testEnv()
	env.Eagle, env.MatchmakeReferee = true, true
	_, body, code := decodeResponse(Dispatch(env, conn(1), request(ProtoMatchmakeExt, 6, sessionHolder(sampleSession()))))
	if code != 0 {
		t.Fatalf("create: %#x", code)
	}
	session, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	join := rmc.NewWriter()
	join.U32(session.GID)
	join.String("")
	join.EmptyList()
	for pid := uint64(2); pid <= 35; pid++ {
		_, _, code = decodeResponse(Dispatch(env, conn(pid), request(ProtoMatchmakeExt, 7, join.Bytes())))
		if code != 0 {
			t.Fatalf("join player %d: %#x", pid, code)
		}
	}
	start := rmc.NewWriter()
	start.StructOf(0, func(w *rmc.Writer) {
		w.U32(0)
		w.U32(session.GID)
		w.List(35)
		for pid := uint64(1); pid <= 35; pid++ {
			w.U64(pid)
		}
		w.U8(0)
		w.U32(0)
	})
	_, body, code = decodeResponse(Dispatch(env, conn(1), request(ProtoMatchmakeReferee, 1, start.Bytes())))
	if code != 0 {
		t.Fatalf("start: %#x", code)
	}
	roundID, err := rmc.NewReader(body).U64()
	if err != nil {
		t.Fatal(err)
	}
	id := rmc.NewWriter()
	id.U64(roundID)
	// All 34 eliminated players report before player 1, the winner.
	for pid := uint64(35); pid > 0; pid-- {
		end := rmc.NewWriter()
		end.StructOf(0, func(w *rmc.Writer) {
			w.U64(roundID)
			w.List(1)
			w.StructOf(0, func(w *rmc.Writer) {
				w.U64(pid)
				w.U32(0) // personal result flag
				w.U32(0) // win/loss
				w.U32(0) // rating change
				w.QBuffer(nil)
				w.U8(0)  // report summary mode
				w.U32(0) // event ID
			})
		})
		for _, method := range []uint32{4, 3, 3} { // partial, final, retry
			_, _, code = decodeResponse(Dispatch(env, conn(pid), request(ProtoMatchmakeReferee, method, end.Bytes())))
			if code != 0 {
				t.Fatalf("player %d report %d: %#x", pid, method, code)
			}
		}
		_, body, code = decodeResponse(Dispatch(env, conn(1), request(ProtoMatchmakeReferee, 2, id.Bytes())))
		if code != 0 || string(body) != string(start.Bytes()) {
			t.Fatalf("player %d removed the shared round", pid)
		}
	}

	// Retention still expires through the existing new-round pruning.
	env.referee.mu.Lock()
	old := env.referee.rounds[roundID]
	old.started = time.Now().Add(-roundLifetime - time.Minute)
	env.referee.rounds[roundID] = old
	env.referee.mu.Unlock()
	_, _, code = decodeResponse(Dispatch(env, conn(1), request(ProtoMatchmakeReferee, 1, start.Bytes())))
	if code != 0 {
		t.Fatalf("next start: %#x", code)
	}
	_, _, code = decodeResponse(Dispatch(env, conn(1), request(ProtoMatchmakeReferee, 2, id.Bytes())))
	if code != rmc.ResultRefereeRoundGone {
		t.Fatalf("expired round was retained: %#x", code)
	}
}

// TestSecureConnectionRegister checks the (QResult, connectionID, URL) shape
// both Register and RegisterEx answer, and that the URL handed back is the
// CLIENT's public station — the endpoint the world sees for it — rather than
// our own secure address, which Pia cannot recognise as itself.
func TestSecureConnectionRegister(t *testing.T) {
	for _, method := range []uint32{0x01, 0x04} {
		env := testEnv()
		c := conn(5)
		c.ID = 9
		// The console reports only its LAN station, which is the common case.
		reported := rmc.NewWriter()
		reported.List(1)
		reported.String("prudp:/address=192.168.1.20;port=50000;sid=1;natf=0")

		resp := Dispatch(env, c, request(ProtoSecureConnection, method, reported.Bytes()))
		ok, body, errCode := decodeResponse(resp)
		if errCode != 0 || !ok {
			t.Fatalf("method %#x failed: %#x", method, errCode)
		}
		r := rmc.NewReader(body)
		qr, _ := r.U32()
		connID, _ := r.U32()
		raw, _ := r.String()
		if qr != rmc.ResultSuccess {
			t.Fatalf("method %#x qresult=%#x", method, qr)
		}
		if connID != c.ID {
			t.Fatalf("method %#x connID=%d want %d", method, connID, c.ID)
		}
		u := switchnex.Parse(raw)
		if got := u.Get("address"); got != c.Host {
			t.Fatalf("method %#x public address=%q want the observed %q", method, got, c.Host)
		}
		if got := u.Get("RVCID"); got != "9" {
			t.Fatalf("method %#x RVCID=%q want 9", method, got)
		}
		if got := u.Get("PID"); got != "5" {
			t.Fatalf("method %#x PID=%q want the caller's 5", method, got)
		}
		// The synthesised public station keeps the parameters the console set
		// on its LAN one; a bare station is one Pia will not accept.
		if got := u.Get("natf"); got != "0" {
			t.Fatalf("method %#x lost the console's natf: %q", method, got)
		}
		// Default (Pia 5.19) shape: type 0x0B and Pa present.
		if got := u.Get("type"); got != "11" {
			t.Fatalf("method %#x type=%q want 11", method, got)
		}
		if !u.Has("Pa") {
			t.Fatalf("method %#x has no Pa", method)
		}
	}
}

// TestSecureConnectionLegacyPiaShape checks the pre-5.19 titles get type 3 and
// no Pa at all — the parameter's absence is part of the shape.
func TestSecureConnectionLegacyPiaShape(t *testing.T) {
	env := testEnv()
	env.Pia = LegacyPia()
	reported := rmc.NewWriter()
	reported.List(1)
	reported.String("prudp:/address=192.168.1.20;port=50000;Pa=192.168.1.20")

	resp := Dispatch(env, conn(5), request(ProtoSecureConnection, 0x01, reported.Bytes()))
	_, body, _ := decodeResponse(resp)
	r := rmc.NewReader(body)
	r.U32()
	r.U32()
	raw, _ := r.String()
	u := switchnex.Parse(raw)
	if got := u.Get("type"); got != "3" {
		t.Fatalf("type=%q want 3", got)
	}
	if u.Has("Pa") {
		t.Fatal("legacy Pia station carries Pa; it must be absent, not empty")
	}
}

// TestPresenceStub checks LM3's 0x79 answers.
func TestPresenceStub(t *testing.T) {
	env := testEnv()
	env.Presence = true
	resp := Dispatch(env, conn(1), request(0x79, 0x0F, nil))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok || len(body) != 4 {
		t.Fatalf("GetStationLocation: %#x %d bytes", errCode, len(body))
	}
	resp = Dispatch(env, conn(1), request(0x79, 0x2A, nil))
	_, _, errCode = decodeResponse(resp)
	if errCode != rmc.ResultNotImplemented {
		t.Fatalf("unknown 0x79 method: %#x", errCode)
	}
}

// helpers

func u32(v uint32) []byte {
	w := rmc.NewWriter()
	w.U32(v)
	return w.Bytes()
}

// participantParam frames a FindMatchmakeSessionByParticipant parameter: the
// structure header the real request carries, then the PID list.
func participantParam(pids ...uint64) []byte {
	w := rmc.NewWriter()
	w.StructOf(1, func(n *rmc.Writer) { n.Raw(pidList(pids...)) })
	return w.Bytes()
}

func pidList(pids ...uint64) []byte {
	w := rmc.NewWriter()
	w.List(len(pids))
	for _, p := range pids {
		w.U64(p)
	}
	return w.Bytes()
}

// decodeResponse unpacks the packed RMC response envelope (u32le size, proto
// byte, success marker, callID, method|0x8000 or error code, body).
func decodeResponse(resp []byte) (ok bool, body []byte, errCode uint32) {
	m, err := rmc.Decode(resp)
	if err != nil {
		return false, nil, 0xFFFFFFFF
	}
	if m.IsError {
		return false, nil, m.ErrorCode
	}
	return m.IsSuccess, m.Body, 0
}
