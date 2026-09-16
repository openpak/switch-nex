package protocols

import (
	"bytes"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// sampleSession covers every NEX4 MatchmakeSession field.
func sampleSession() *Session {
	return &Session{
		GID:                   42,
		OwnerPID:              1812345678,
		HostPID:               1812345678,
		MinParticipants:       1,
		MaxParticipants:       35,
		ParticipationPolicy:   3,
		PolicyArgument:        0,
		Flags:                 0x4000, // codeword present (ACNH shape)
		State:                 1,
		Description:           "test lobby",
		GameMode:              7,
		Attributes:            []uint32{1, 2, 3, 4, 5, 6},
		OpenParticipation:     true,
		MatchmakeSystemType:   1,
		ApplicationBuffer:     []byte{0xde, 0xad, 0xbe, 0xef},
		ParticipationCount:    2,
		ProgressScore:         9,
		SessionKey:            bytes.Repeat([]byte{0xAB}, 32),
		Option0:               0x55,
		MatchmakeParam:        map[string]rmc.Variant{"param": {TypeID: 4, Str: "hello"}, "num": {TypeID: 1, I64: -5}},
		StartedTime:           1718000000000000000,
		UserPassword:          "secret",
		ReferGID:              0,
		UserPasswordEnabled:   false,
		SystemPasswordEnabled: true,
		CodeWord:              "ABCD1",
	}
}

// TestSessionRoundTrip is the byte-fidelity contract: parse(write(session))
// reproduces every field, and the re-encoded bytes are identical — echoing a
// stored session must reproduce the client's own wire bytes.
func TestSessionRoundTrip(t *testing.T) {
	s := sampleSession()

	w := rmc.NewWriter()
	s.write(w)
	wire := w.Bytes()

	r := rmc.NewReader(wire)
	got, err := readSession(r)
	if err != nil {
		t.Fatalf("readSession: %v", err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing bytes: %d", r.Remaining())
	}

	if got.GID != s.GID || got.OwnerPID != s.OwnerPID || got.HostPID != s.HostPID {
		t.Fatalf("gathering base: %+v", got)
	}
	if got.MinParticipants != s.MinParticipants || got.MaxParticipants != s.MaxParticipants {
		t.Fatalf("participants: %d..%d", got.MinParticipants, got.MaxParticipants)
	}
	if got.Description != s.Description || got.CodeWord != s.CodeWord || got.UserPassword != s.UserPassword {
		t.Fatalf("strings: %q %q %q", got.Description, got.CodeWord, got.UserPassword)
	}
	if got.OpenParticipation != s.OpenParticipation || got.SystemPasswordEnabled != s.SystemPasswordEnabled {
		t.Fatalf("bools: %v %v", got.OpenParticipation, got.SystemPasswordEnabled)
	}
	if !bytes.Equal(got.SessionKey, s.SessionKey) {
		t.Fatalf("session key mismatch")
	}
	if !bytes.Equal(got.ApplicationBuffer, s.ApplicationBuffer) {
		t.Fatalf("application buffer mismatch")
	}
	if got.MatchmakeParam["param"].Str != "hello" || got.MatchmakeParam["num"].I64 != -5 {
		t.Fatalf("matchmake param: %+v", got.MatchmakeParam)
	}
	if got.StartedTime != s.StartedTime || got.ReferGID != s.ReferGID || got.Option0 != s.Option0 {
		t.Fatalf("trailing fields: %d %d %d", got.StartedTime, got.ReferGID, got.Option0)
	}

	w2 := rmc.NewWriter()
	got.write(w2)
	if !bytes.Equal(wire, w2.Bytes()) {
		t.Fatalf("re-encode differs")
	}
}

// TestSessionSkipByHeader proves the header length is truthful: a reader that
// skips the framed structure by its content length lands exactly at the end.
func TestSessionSkipByHeader(t *testing.T) {
	s := sampleSession()
	w := rmc.NewWriter()
	s.write(w)

	r := rmc.NewReader(w.Bytes())
	_, contentLen, err := r.StructHeader()
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	if err := r.Fwd(int(contentLen)); err != nil {
		t.Fatalf("skip: %v", err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("content length %d does not frame the structure (leftover %d)", contentLen, r.Remaining())
	}
}
