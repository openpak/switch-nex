package protocols

import (
	"bytes"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// clubhouseEnv wires the Clubhouse Games behaviour set (the registry entry in
// cmd/switch-nexd).
func clubhouseEnv() *Env {
	e := NewEnv("clubhouse-games-51", "10.0.0.1", 22870)
	e.Match = NewMatchmakingStore()
	e.Ranking = true
	e.DataStore = DSClubhouse
	e.ForceOpenParticipation = true
	e.FinalizeCreateSession = true
	e.FinalizeParams = true
	e.JoinEchoSession = true
	return e
}

// clubhouseSession is the bare session a Clubhouse client proposes at create:
// unbounded, keyless, closed, inactive.
func clubhouseSession() *Session {
	return &Session{
		MinParticipants:       2,
		ParticipationPolicy:   3,
		Description:           "clubhouse lobby",
		GameMode:              92000,
		MatchmakeSystemType:   1,
		UserPasswordEnabled:   true,
		SystemPasswordEnabled: true,
	}
}

// TestClubhouseCreateNormalisation pins the create-time normalisation: the
// game proposes an unbounded, keyless, closed session; the measured working
// answer is a bounded, host-occupied, keyed, open and active one.
func TestClubhouseCreateNormalisation(t *testing.T) {
	env := clubhouseEnv()
	resp := Dispatch(env, conn(100), request(ProtoMatchmakeExt, 0x06, sessionHolder(clubhouseSession())))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("create failed: %#v", resp)
	}
	created, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatalf("create body: %v", err)
	}
	if created.MaxParticipants != 12 {
		t.Fatalf("max participants: %d", created.MaxParticipants)
	}
	if created.ParticipationCount != 1 {
		t.Fatalf("participation count: %d", created.ParticipationCount)
	}
	if len(created.SessionKey) != 32 {
		t.Fatalf("session key: %d bytes", len(created.SessionKey))
	}
	if !created.OpenParticipation || created.State != 1 {
		t.Fatalf("open=%v state=%d (searchers would never match)", created.OpenParticipation, created.State)
	}
	if created.UserPasswordEnabled || created.SystemPasswordEnabled {
		t.Fatal("password flags left enabled")
	}
	if created.StartedTime == 0 {
		t.Fatal("no start time")
	}
	if len(created.Attributes) < 6 {
		t.Fatalf("attributes: %d", len(created.Attributes))
	}
	if !created.MatchmakeParam["@SR"].Bool || created.MatchmakeParam["@GIR"].I64 != 3 {
		t.Fatalf("matchmake param: %+v", created.MatchmakeParam)
	}

	// A second, different client must find the session by browsing even
	// though the client proposed it closed.
	resp = Dispatch(env, conn(200), request(ProtoMatchmakeExt, 0x04, nil))
	ok, body, errCode = decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("browse failed: %#v", resp)
	}
	r := rmc.NewReader(body)
	if _, n, err := structThenCount(r); err != nil || n != 1 {
		t.Fatalf("browse found %d sessions (err=%v)", n, err)
	}
}

// TestClubhouseJoinKeyBuffer pins the plain/Ex join shape: the answer is the
// session key as a Buffer (NEX IDL), not a bool and not a session. The
// WithParam join (0x27) is the one that echoes the session.
func TestClubhouseJoinKeyBuffer(t *testing.T) {
	env := clubhouseEnv()
	resp := Dispatch(env, conn(100), request(ProtoMatchmakeExt, 0x06, sessionHolder(clubhouseSession())))
	_, body, errCode := decodeResponse(resp)
	if errCode != 0 {
		t.Fatalf("create failed: %#x", errCode)
	}
	created, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatalf("create body: %v", err)
	}
	if len(created.SessionKey) != 32 {
		t.Fatalf("created session key=%d bytes", len(created.SessionKey))
	}

	// The plain and Ex joins answer the session key as a Buffer (NEX IDL),
	// while the store moves to 2.
	for _, method := range []uint32{0x07, 0x1E} {
		resp = Dispatch(env, conn(200), request(ProtoMatchmakeExt, method, u32(created.GID)))
		ok, body, errCode := decodeResponse(resp)
		if errCode != 0 || !ok {
			t.Fatalf("join 0x%x failed: %#v", method, resp)
		}
		key, err := rmc.NewReader(body).Buffer()
		if err != nil || len(key) != 32 || string(key) != string(created.SessionKey) {
			t.Fatalf("join 0x%x key=%d bytes, want the session key: %v", method, len(key), err)
		}
	}
	g, ok := env.Match.get(created.GID)
	if !ok || g.sess.ParticipationCount != 2 || !g.participants[200] {
		t.Fatalf("store after join: count=%d joined=%v", g.sess.ParticipationCount, g.participants[200])
	}
}

// TestClubhouseDataStore pins the account-setup answers: GetPersistenceInfos
// is "nothing saved yet" (empty infos, one NotFound per requested slot),
// GetMeta is NotFound, and everything else — including PostMetaBinary — is a
// bare empty list (the measured working answers; this game never posts).
func TestClubhouseDataStore(t *testing.T) {
	env := clubhouseEnv()

	// GetPersistenceInfos: u64 ownerId + list<u16> slots (3 requested).
	w := rmc.NewWriter()
	w.U64(5000)
	w.List(3)
	w.U16(0)
	w.U16(1)
	w.U16(2)
	resp := Dispatch(env, conn(100), request(ProtoDataStore, 0x1E, w.Bytes()))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("GetPersistenceInfos failed: %#v", resp)
	}
	r := rmc.NewReader(body)
	infos, err := r.ListHeader()
	if err != nil || infos != 0 {
		t.Fatalf("infos: %d err=%v", infos, err)
	}
	results, err := r.ListHeader()
	if err != nil || results != 3 {
		t.Fatalf("results: %d err=%v", results, err)
	}
	for i := 0; i < 3; i++ {
		code, _ := r.U32()
		if code != rmc.ResultDataStoreNotFound {
			t.Fatalf("slot %d result: %#x", i, code)
		}
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing bytes: %d", r.Remaining())
	}

	// GetMeta -> DataStore::NotFound.
	resp = Dispatch(env, conn(100), request(ProtoDataStore, 0x08, nil))
	_, _, errCode = decodeResponse(resp)
	if errCode != rmc.ResultDataStoreNotFound {
		t.Fatalf("GetMeta: %#x", errCode)
	}

	// Everything else: a bare u32 count of 0.
	resp = Dispatch(env, conn(100), request(ProtoDataStore, 0x15, nil))
	ok, body, errCode = decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("default method failed: %#v", resp)
	}
	if !bytes.Equal(body, []byte{0, 0, 0, 0}) {
		t.Fatalf("default body: %x", body)
	}
}

// structThenCount reads a ResultRange-style (struct { u32, u32 }, list count).
func structThenCount(r *rmc.Reader) (uint32, uint32, error) {
	_, _, err := r.StructHeader()
	if err != nil {
		return 0, 0, err
	}
	if _, err := r.U32(); err != nil {
		return 0, 0, err
	}
	total, err := r.U32()
	if err != nil {
		return 0, 0, err
	}
	n, err := r.ListHeader()
	return total, n, err
}

// A title with FinalizeCreateSession but without FinalizeParams (ACNH)
// keeps exactly the parameters its client sent, plus key/count/time: no
// @GIR foreign key, no attribute padding. ACNH's binary never heard of
// @GIR and its presence breaks the client's parse (2306-0116, 2026-09-21).
func TestFinalizeWithoutParamsKeepsClientShape(t *testing.T) {
	env := NewEnv("animal-crossing-new-horizons", "10.0.0.1", 22180)
	env.Match = NewMatchmakingStore()
	env.FinalizeCreateSession = true
	env.JoinEchoSession = true
	sess := &Session{MaxParticipants: 8, Attributes: []uint32{1, 2}}
	resp := Dispatch(env, conn(100), request(ProtoMatchmakeExt, 0x06, sessionHolder(sess)))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("create failed: %#v", resp)
	}
	created, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatalf("create body: %v", err)
	}
	if len(created.SessionKey) != 32 {
		t.Fatalf("session key: %d bytes", len(created.SessionKey))
	}
	if created.ParticipationCount != 1 {
		t.Fatalf("participation: %d", created.ParticipationCount)
	}
	if _, ok := created.MatchmakeParam["@GIR"]; ok {
		t.Fatalf("@GIR injected into a title that never heard of it")
	}
	if _, ok := created.MatchmakeParam["@SR"]; ok {
		t.Fatalf("@SR injected although the client sent none")
	}
	if len(created.Attributes) != 2 {
		t.Fatalf("attributes padded to %d", len(created.Attributes))
	}
}
