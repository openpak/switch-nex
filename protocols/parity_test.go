package protocols

import (
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// TestDataStoreNotFoundGoesOutAsTheMeasuredCode pins the one constant that is
// easy to transpose and impossible to notice: the error bit is added at encode
// time, so the constant is the wire value minus 0x80000000.
func TestDataStoreNotFoundGoesOutAsTheMeasuredCode(t *testing.T) {
	env := testEnv()
	env.DataStore = DSStub
	resp := Dispatch(env, conn(5), request(ProtoDataStore, 0x08, nil)) // GetMeta
	m, err := rmc.Decode(resp)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !m.IsError {
		t.Fatal("GetMeta answered success; it must answer NotFound")
	}
	if got := m.ErrorCode | rmc.ErrorCodeBit; got != 0x80690004 {
		t.Fatalf("wire code = %#08x, want 0x80690004", got)
	}
}

// TestDataStoreShapesArePerTitle: the stub answer is not one shape. A title
// served the wrong one either gets an error where it expected a list, or a body
// where it expected nothing.
func TestDataStoreShapesArePerTitle(t *testing.T) {
	emptyList := func(t *testing.T, body []byte) {
		t.Helper()
		n, err := rmc.NewReader(body).ListHeader()
		if err != nil || n != 0 {
			t.Fatalf("want an empty list, got %d bytes", len(body))
		}
	}

	env := testEnv()
	env.DataStore = DSEmptyList
	_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoDataStore, 0x08, nil)))
	if errCode|rmc.ErrorCodeBit != 0x80690004 {
		t.Fatalf("DSEmptyList GetMeta = %#x, want NotFound", errCode)
	}
	_, body, errCode = decodeResponse(Dispatch(env, conn(5), request(ProtoDataStore, 0x09, nil)))
	if errCode != 0 {
		t.Fatalf("DSEmptyList non-GetMeta errored: %#x", errCode)
	}
	emptyList(t, body)

	// The empty-success title takes a bare body for everything, GetMeta too.
	env.DataStore = DSEmptySuccess
	_, body, errCode = decodeResponse(Dispatch(env, conn(5), request(ProtoDataStore, 0x08, nil)))
	if errCode != 0 || len(body) != 0 {
		t.Fatalf("DSEmptySuccess GetMeta = err %#x / %d bytes, want empty success", errCode, len(body))
	}
}

// TestUniqueIDAnswersAStructureAndIsStable: methods 2 and 5 answer the id AND
// its password, framed. An empty answer to 2 is what a fresh account gets, and
// a counter hands a returning account somebody else's id.
func TestUniqueIDAnswersAStructureAndIsStable(t *testing.T) {
	env := testEnv()
	for _, method := range []uint32{0x02, 0x05} {
		_, body, errCode := decodeResponse(Dispatch(env, conn(4242), request(ProtoUtility, method, nil)))
		if errCode != 0 {
			t.Fatalf("method %#x errored %#x", method, errCode)
		}
		r := rmc.NewReader(body)
		if _, _, err := r.StructHeader(); err != nil {
			t.Fatalf("method %#x is not a structure: %v", method, err)
		}
		id, err := r.U64()
		if err != nil || id != 4242 {
			t.Fatalf("method %#x id=%d want the caller's PID", method, id)
		}
		if _, err := r.U64(); err != nil { // the password rides alongside
			t.Fatalf("method %#x has no password field", method)
		}
	}
	// AcquireNexUniqueID is the same number every time, across connections.
	_, a, _ := decodeResponse(Dispatch(env, conn(4242), request(ProtoUtility, 0x01, nil)))
	_, b, _ := decodeResponse(Dispatch(env, conn(4242), request(ProtoUtility, 0x01, nil)))
	if string(a) != string(b) {
		t.Fatal("AcquireNexUniqueID is not stable across calls")
	}
}

// TestIntegerSettingsKeepKeyOrder: the client reads POSITIONS, so a map ranged
// in Go's randomised order is a different table on every call.
func TestIntegerSettingsKeepKeyOrder(t *testing.T) {
	env := testEnv()
	env.IntegerSettings = func(uint32) (map[uint16]uint32, uint32) {
		return map[uint16]uint32{5: 50, 1: 10, 9: 90, 3: 30, 7: 70, 0: 0, 2: 20, 4: 40, 6: 60, 8: 80}, 0
	}
	var first []byte
	for range 12 { // enough iterations that a randomised order would show
		_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoUtility, 0x07, u32(0))))
		if errCode != 0 {
			t.Fatalf("settings errored %#x", errCode)
		}
		if first == nil {
			first = body
			continue
		}
		if string(body) != string(first) {
			t.Fatal("settings table is not byte-stable between calls")
		}
	}
	r := rmc.NewReader(first)
	n, _ := r.ListHeader()
	for i := range n {
		k, err := r.U16()
		if err != nil || uint32(k) != i {
			t.Fatalf("entry %d has key %d; keys must ascend", i, k)
		}
		r.U32()
	}
}

// TestRanking2CommonDataUsesQBuffers: the mii and binary blobs carry u16
// lengths, and an unnamed player's name is zero-length rather than a lone NUL.
// A board cites players we hold nothing for, so that is the common case.
func TestRanking2CommonDataUsesQBuffers(t *testing.T) {
	env := testEnv()
	env.Ranking2 = true

	put := rmc.NewWriter()
	put.StructOf(1, func(w *rmc.Writer) {
		w.String("Player")
		w.QBuffer([]byte{1, 2, 3, 4})
		w.QBuffer([]byte{9})
	})
	put.U64(1)
	if _, _, errCode := decodeResponse(Dispatch(env, conn(7), request(ProtoRanking2, 0x03, put.Bytes()))); errCode != 0 {
		t.Fatalf("PutCommonData errored %#x", errCode)
	}

	get := rmc.NewWriter()
	get.U32(0)
	get.U64(7)
	get.U64(1)
	_, body, errCode := decodeResponse(Dispatch(env, conn(7), request(ProtoRanking2, 0x02, get.Bytes())))
	if errCode != 0 {
		t.Fatalf("GetCommonData errored %#x", errCode)
	}
	r := rmc.NewReader(body)
	r.StructHeader()
	name, _ := r.String()
	mii, err := r.QBuffer()
	if err != nil || len(mii) != 4 {
		t.Fatalf("mii is not a QBuffer: %v (%d bytes)", err, len(mii))
	}
	bin, err := r.QBuffer()
	if err != nil || len(bin) != 1 {
		t.Fatalf("binary is not a QBuffer: %v", err)
	}
	if name != "Player" || r.Remaining() != 0 {
		t.Fatalf("name=%q trailing=%d", name, r.Remaining())
	}

	// A player we hold nothing for gets an EMPTY STRUCTURE, never an error and
	// never zero bytes: the client extracts a structure either way and
	// overruns its buffer if there is nothing there.
	_, body, errCode = decodeResponse(Dispatch(env, conn(7), request(ProtoRanking2, 0x02, func() []byte {
		w := rmc.NewWriter()
		w.U32(0)
		w.U64(999)
		w.U64(1)
		return w.Bytes()
	}())))
	if errCode != 0 {
		t.Fatalf("unknown player errored %#x; it must answer an empty structure", errCode)
	}
	r = rmc.NewReader(body)
	if _, _, err := r.StructHeader(); err != nil {
		t.Fatalf("unknown player is not a structure: %v", err)
	}
	if n, err := r.U16(); err != nil || n != 0 {
		t.Fatalf("unnamed player's name is %d bytes; want a zero-length string", n)
	}
}

// TestRanking2CategorySettingWidths: the reset month is a u16. As a u32 it
// inserts two bytes and everything after it reads shifted.
func TestRanking2CategorySettingWidths(t *testing.T) {
	env := testEnv()
	env.Ranking2 = true
	_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoRanking2, 0x07, u32(1))))
	if errCode != 0 {
		t.Fatalf("errored %#x", errCode)
	}
	r := rmc.NewReader(body)
	r.StructHeader()
	r.U32() // MinScore
	r.U32() // MaxScore
	if lowest, _ := r.U32(); lowest != ranking2LowestRank {
		t.Fatalf("lowest rank %d must match the board's", lowest)
	}
	if month, err := r.U16(); err != nil || month != 4095 {
		t.Fatalf("reset month = %d (u16 read): %v", month, err)
	}
	r.U8() // day
	r.U8() // hour
	r.U8() // mode
	r.U8() // seasons
	order, err := r.Bool()
	if err != nil || !order {
		t.Fatalf("score order = %v, %v", order, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d trailing bytes: the field widths do not add up", r.Remaining())
	}
}

// TestSimplePlayingSessionFindsAFriendsGathering is the friend-join path: an
// empty answer here is why joining a friend fails, because the joiner never
// learns which gathering to look up.
func TestSimplePlayingSessionFindsAFriendsGathering(t *testing.T) {
	env := testEnv()
	host := conn(100)
	sess := &Session{MaxParticipants: 4, GameMode: 3, Attributes: []uint32{77}, OpenParticipation: true}
	Dispatch(env, host, request(ProtoMatchmakeExt, 0x06, sessionHolder(sess)))

	req := rmc.NewWriter()
	req.List(1)
	req.U64(100)
	req.Bool(false) // exclude the caller's own session

	_, body, errCode := decodeResponse(Dispatch(env, conn(200), request(ProtoMatchmakeExt, 0x3C, req.Bytes())))
	if errCode != 0 {
		t.Fatalf("errored %#x", errCode)
	}
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v — the friend's gathering was not reported", n, err)
	}
	r.StructHeader()
	pid, _ := r.U64()
	gid, _ := r.U32()
	mode, _ := r.U32()
	attr, _ := r.U32()
	if pid != 100 || gid == 0 || mode != 3 || attr != 77 {
		t.Fatalf("pid=%d gid=%d mode=%d attr=%d", pid, gid, mode, attr)
	}
}

// TestOwnershipMigratesWhenTheOwnerLeaves: a session must outlive its creator,
// or everyone still in it is stranded.
func TestOwnershipMigratesWhenTheOwnerLeaves(t *testing.T) {
	env := testEnv()
	owner, joiner := conn(100), conn(200)
	Dispatch(env, owner, request(ProtoMatchmakeExt, 0x06, sessionHolder(&Session{MaxParticipants: 4, OpenParticipation: true})))

	var gid uint32
	for _, s := range env.Match.sessionsOf(100) {
		gid = s.GID
	}
	Dispatch(env, joiner, request(ProtoMatchmakeExt, 0x07, u32(gid)))

	// The owner ends its participation; the session goes to the joiner.
	Dispatch(env, owner, request(ProtoMatchMakingExt, 0x01, u32(gid)))
	g, ok := env.Match.get(gid)
	if !ok {
		t.Fatal("session died with its owner")
	}
	if g.sess.OwnerPID != 200 || g.sess.HostPID != 200 {
		t.Fatalf("owner=%d host=%d, want 200", g.sess.OwnerPID, g.sess.HostPID)
	}

	// The last participant leaving takes the session with it.
	Dispatch(env, joiner, request(ProtoMatchMakingExt, 0x01, u32(gid)))
	if _, ok := env.Match.get(gid); ok {
		t.Fatal("empty session outlived its last participant")
	}
}

// TestRoomCodeRoundTrip: create a code, resolve it back to the gathering.
func TestRoomCodeRoundTrip(t *testing.T) {
	env := testEnv()
	host := conn(100)
	Dispatch(env, host, request(ProtoMatchmakeExt, 0x06, sessionHolder(&Session{MaxParticipants: 4, OpenParticipation: true})))
	var gid uint32
	for _, s := range env.Match.sessionsOf(100) {
		gid = s.GID
	}

	_, body, errCode := decodeResponse(Dispatch(env, host, request(ProtoMatchmakeExt, 0x44, nil)))
	if errCode != 0 {
		t.Fatalf("create errored %#x", errCode)
	}
	// The answer is a structure around (String, DateTime), not the bare pair.
	_, content, err := rmc.NewReader(body).Struct()
	if err != nil {
		t.Fatalf("answer is not a structure: %v", err)
	}
	code, err := content.String()
	if err != nil || len(code) != codeLength {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if _, err := content.U64(); err != nil || content.Remaining() != 0 {
		t.Fatalf("structure does not end after the DateTime: err=%v left=%d", err, content.Remaining())
	}

	req := rmc.NewWriter()
	req.String(code)
	_, body, errCode = decodeResponse(Dispatch(env, conn(200), request(ProtoMatchmakeExt, 0x45, req.Bytes())))
	if errCode != 0 {
		t.Fatalf("resolve errored %#x", errCode)
	}
	if got, _ := rmc.NewReader(body).U32(); got != gid {
		t.Fatalf("resolved to %d, want %d", got, gid)
	}

	// The same code inside a structure header resolves too.
	wrapped := rmc.NewWriter()
	wrapped.StructOf(0, func(w *rmc.Writer) { w.String(code) })
	_, body, errCode = decodeResponse(Dispatch(env, conn(200), request(ProtoMatchmakeExt, 0x45, wrapped.Bytes())))
	if got, _ := rmc.NewReader(body).U32(); errCode != 0 || got != gid {
		t.Fatalf("wrapped code resolved to %d (err %#x), want %d", got, errCode, gid)
	}

	// A code for a gathering that has gone is not a gathering.
	env.Match.delete(gid)
	_, _, errCode = decodeResponse(Dispatch(env, conn(200), request(ProtoMatchmakeExt, 0x45, req.Bytes())))
	if errCode != rmc.ResultSessionVoid {
		t.Fatalf("stale code = %#x, want SessionVoid", errCode)
	}
}

// TestProbeAndTournamentsShareANumber: 0x3A means two different things and the
// request shape is the only thing that separates them.
func TestProbeAndTournamentsShareANumber(t *testing.T) {
	env := testEnv()

	// No body: the pre-matchmake probe, which wants a bare u32 of 2.
	_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoMatchmakeExt, 0x3A, nil)))
	if errCode != 0 {
		t.Fatalf("probe errored %#x", errCode)
	}
	if v, _ := rmc.NewReader(body).U32(); v != 2 {
		t.Fatalf("probe answered %d, want 2", v)
	}

	// A u32 count plus that many u32 ids: a tournament query, which wants a
	// LIST. Answering 2 makes the caller read two entries that are not there.
	req := rmc.NewWriter()
	req.List(2)
	req.U32(11)
	req.U32(22)
	_, body, errCode = decodeResponse(Dispatch(env, conn(5), request(ProtoMatchmakeExt, 0x3A, req.Bytes())))
	if errCode != 0 {
		t.Fatalf("tournament query errored %#x", errCode)
	}
	if n, err := rmc.NewReader(body).ListHeader(); err != nil || n != 0 {
		t.Fatalf("tournament query answered n=%d err=%v, want an empty list", n, err)
	}
}

// TestNATCompatibilityRefusesOnlyThePairsThatFail. Unknown stays optimistic:
// a peer we cannot classify is never isolated on a guess.
func TestNATCompatibilityRefusesOnlyThePairsThatFail(t *testing.T) {
	cases := []struct {
		a, b natClass
		want bool
	}{
		{natOpen, natStrict, true},
		{natModerate, natModerate, true},
		{natUnknown, natStrict, true},
		{natStrict, natStrict, false},
		{natStrict, natModerate, false},
		{natModerate, natStrict, false},
	}
	for _, c := range cases {
		if got := natCompatible(c.a, c.b); got != c.want {
			t.Errorf("natCompatible(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if classifyNAT(0, 0) != natUnknown {
		t.Error("an unreported NAT must classify as unknown, not open")
	}
	if classifyNAT(1, 1) != natOpen || classifyNAT(1, 2) != natModerate || classifyNAT(2, 2) != natStrict {
		t.Error("NAT bucketing is wrong")
	}
}

// TestRelaySignatureKeyIsWellFormed: an empty body here is not "no relay", it
// is an answer the newer Pia cannot parse — its match-start never completes.
func TestRelaySignatureKeyIsWellFormed(t *testing.T) {
	env := testEnv()
	_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoNatTraversal, 0x06, nil)))
	if errCode != 0 {
		t.Fatalf("errored %#x", errCode)
	}
	r := rmc.NewReader(body)
	r.U32() // relayMode
	if when, err := r.U64(); err != nil || when == 0 {
		t.Fatal("no server time")
	}
	addr, err := r.String()
	if err != nil || addr != "" {
		t.Fatalf("relay address = %q, want empty (no relay offered)", addr)
	}
	r.U16() // port
	r.U32() // address type
	r.U32() // game server id
	if r.Remaining() != 0 {
		t.Fatalf("%d trailing bytes", r.Remaining())
	}
}

// TestRankingSubmissionsAnswerOneZeroByte, not an empty body: the client counts.
func TestRankingSubmissionsAnswerOneZeroByte(t *testing.T) {
	env := testEnv()
	env.Ranking = true
	for _, method := range []uint32{0x11, 0x13} {
		_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoRanking, method, nil)))
		if errCode != 0 {
			t.Fatalf("method %#x errored %#x", method, errCode)
		}
		if len(body) != 1 || body[0] != 0 {
			t.Fatalf("method %#x answered %v, want a single zero byte", method, body)
		}
	}
}

// TestRankingUploadScoreAnswersTrue: Mario Golf posts its match points with
// UploadScore (0x01) the moment a ranked match ends. A NotImplemented there
// disconnects the console with 2306-0103 and the points are lost, so the
// upload is acknowledged with a true bool — the NEX UploadScore answer shape.
func TestRankingUploadScoreAnswersTrue(t *testing.T) {
	env := testEnv()
	env.Ranking = true
	upload := rmc.NewWriter()
	upload.U8(0)       // RankingScoreData structure version
	upload.U32(26)     // category (Golf's match-points board)
	upload.U32(1037)   // score
	upload.U8(1)       // orderBy
	upload.U8(0)       // updateMode
	upload.Buffer(nil) // groups
	upload.U64(0)      // param
	upload.U64(1234)   // uniqueID
	_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoRanking, 0x01, upload.Bytes())))
	if errCode != 0 {
		t.Fatalf("errored %#x — refusing loses the player's match points", errCode)
	}
	if len(body) != 1 || body[0] != 1 {
		t.Fatalf("answered %v, want a single true byte", body)
	}
}

// TestCommonDataByPIDsKeepsRequestOrder: the client pairs the nth reply with
// the nth PID it asked about, and an unknown player is an empty entry rather
// than a missing one.
func TestCommonDataByPIDsKeepsRequestOrder(t *testing.T) {
	env := testEnv()
	env.Ranking = true
	upload := rmc.NewWriter()
	upload.Buffer([]byte("profile-of-7"))
	Dispatch(env, conn(7), request(ProtoRanking, 0x04, upload.Bytes()))

	req := rmc.NewWriter()
	req.List(3)
	req.U64(99) // unknown
	req.U64(7)  // known
	req.U64(11) // unknown
	_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoRanking, 0x16, req.Bytes())))
	if errCode != 0 {
		t.Fatalf("errored %#x — this method must never refuse", errCode)
	}
	r := rmc.NewReader(body)
	n, _ := r.ListHeader()
	if n != 3 {
		t.Fatalf("n=%d, want one entry per requested PID", n)
	}
	first, _ := r.Buffer()
	second, _ := r.Buffer()
	third, _ := r.Buffer()
	if len(first) != 0 || string(second) != "profile-of-7" || len(third) != 0 {
		t.Fatalf("entries out of order: %q %q %q", first, second, third)
	}
}

// TestProtocolByteEscapesAboveSeven Bits: 0x80 and up must escape, because the
// high bit of that byte is the request marker.
func TestProtocolByteEscapesAbove7Bits(t *testing.T) {
	for _, proto := range []uint16{0x03, 0x7A, 0x80, 0xFF, 0x1234} {
		round := func(encoded []byte) *rmc.RMCMessage {
			m, err := rmc.Decode(encoded)
			if err != nil {
				t.Fatalf("proto %#x: %v", proto, err)
			}
			return m
		}
		if got := round(rmc.EncodeRequest(proto, 1, 2, nil)); got.Protocol != proto || !got.IsRequest {
			t.Errorf("request proto %#x round-tripped as %#x (request=%v)", proto, got.Protocol, got.IsRequest)
		}
		if got := round(rmc.EncodeSuccess(proto, 2, 1, nil)); got.Protocol != proto || got.IsRequest {
			t.Errorf("response proto %#x round-tripped as %#x (request=%v)", proto, got.Protocol, got.IsRequest)
		}
	}
}

// TestRankingBoardReadIsAnEmptyResult: Mario Golf's first call after CONNECT is
// GetRanking (0x09), and a NotImplemented there makes it hang up — the console
// reports it as a failure to connect. The answer is a RankingResult structure:
// an empty list, a zero total, a sinceTime.
func TestRankingBoardReadIsAnEmptyResult(t *testing.T) {
	env := testEnv()
	env.Ranking = true
	// The request Mario Golf sends: mode 1, category 1280, a window of one.
	req := rmc.NewWriter()
	req.U8(1)
	req.U32(1280)
	req.StructOf(0, func(w *rmc.Writer) {
		w.U8(0)
		w.U8(0xFF)
		w.U8(0)
		w.U8(2)
		w.U32(0)
		w.U8(1)
	})
	req.U64(0) // uniqueID
	req.U64(0) // principalID
	for _, method := range []uint32{0x09, 0x0C, 0x0D} {
		_, body, errCode := decodeResponse(Dispatch(env, conn(5), request(ProtoRanking, method, req.Bytes())))
		if errCode != 0 {
			t.Fatalf("method %#x errored %#x — the game hangs up on this", method, errCode)
		}
		r := rmc.NewReader(body)
		if _, _, err := r.StructHeader(); err != nil {
			t.Fatalf("method %#x: no structure header: %v", method, err)
		}
		if n, err := r.ListHeader(); err != nil || n != 0 {
			t.Fatalf("method %#x: entries n=%d err=%v, want an empty list", method, n, err)
		}
		if total, err := r.U32(); err != nil || total != 0 {
			t.Fatalf("method %#x: total=%d err=%v", method, total, err)
		}
		if _, err := r.U64(); err != nil { // sinceTime
			t.Fatalf("method %#x: no sinceTime: %v", method, err)
		}
		if r.Remaining() != 0 {
			t.Fatalf("method %#x: %d trailing bytes", method, r.Remaining())
		}
	}
}
