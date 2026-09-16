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

// TestClubhouseJoinEcho pins the join shape: the answer carries the full
// session and the participation count as it was BEFORE the joiner entered —
// the client builds its P2P mesh from that number.
func TestClubhouseJoinEcho(t *testing.T) {
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

	// A new joiner hears count=1 (the host only), while the store moves to 2.
	resp = Dispatch(env, conn(200), request(ProtoMatchmakeExt, 0x07, u32(created.GID)))
	ok, body, errCode := decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("join failed: %#v", resp)
	}
	echoed, err := readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatalf("join body is not a session: %v", err)
	}
	if echoed.ParticipationCount != 1 {
		t.Fatalf("echoed count: %d (want the pre-join 1)", echoed.ParticipationCount)
	}
	g, ok := env.Match.get(created.GID)
	if !ok || g.sess.ParticipationCount != 2 || !g.participants[200] {
		t.Fatalf("store after join: count=%d joined=%v", g.sess.ParticipationCount, g.participants[200])
	}

	// A repeat call from the same participant echoes the current count.
	resp = Dispatch(env, conn(200), request(ProtoMatchmakeExt, 0x1E, u32(created.GID)))
	ok, body, errCode = decodeResponse(resp)
	if errCode != 0 || !ok {
		t.Fatalf("rejoin failed: %#v", resp)
	}
	echoed, err = readSession(rmc.NewReader(body))
	if err != nil {
		t.Fatalf("rejoin body: %v", err)
	}
	if echoed.ParticipationCount != 2 {
		t.Fatalf("rejoin echoed count: %d", echoed.ParticipationCount)
	}
	if g.sess.ParticipationCount != 2 {
		t.Fatalf("store count moved on rejoin: %d", g.sess.ParticipationCount)
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
