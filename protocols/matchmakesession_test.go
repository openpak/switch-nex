package protocols

import (
	"bytes"
	"encoding/hex"
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

// TestSessionFramesAsSiblings proves both header lengths are truthful and that
// the two structures sit side by side: skipping Gathering by its length, then
// MatchmakeSession by its length, lands exactly at the end.
func TestSessionFramesAsSiblings(t *testing.T) {
	s := sampleSession()
	w := rmc.NewWriter()
	s.write(w)

	r := rmc.NewReader(w.Bytes())
	for _, name := range []string{"Gathering", "MatchmakeSession"} {
		_, contentLen, err := r.StructHeader()
		if err != nil {
			t.Fatalf("%s header: %v", name, err)
		}
		if err := r.Fwd(int(contentLen)); err != nil {
			t.Fatalf("%s skip: %v", name, err)
		}
	}
	if r.Remaining() != 0 {
		t.Fatalf("the two structures do not frame the session (leftover %d)", r.Remaining())
	}
}

// TestSessionFromAConsole parses the opening of a request MK8D 3.0.5 actually
// sent (AutoMatchmakeWithParam_Postpone, 2026-09-17): Gathering under
// `00 2b000000`, then MatchmakeSession under its own header. The capture was
// truncated after the attributes, so the tail is synthesised — with five bytes
// this parser does not know appended, as the console's 0x53-byte structure had.
func TestSessionFromAConsole(t *testing.T) {
	captured, _ := hex.DecodeString("002b000000" +
		"00000000" + "0000000000000000" + "0000000000000000" + "0200" + "0c00" +
		"5f000000" + "00000000" + "100a0000" + "00000000" + "010000")
	w := rmc.NewWriter()
	w.Raw(captured)
	w.StructOf(0, func(w *rmc.Writer) {
		w.U32(2)
		w.List(6)
		for _, a := range []uint32{0, 1000, 0x1011112c, 0, 0x17, 0} {
			w.U32(a)
		}
		w.Bool(true)
		w.U32(1)
		w.Buffer(nil)
		w.U32(0)
		w.U8(0)
		w.Buffer(nil)
		w.U32(0)
		w.List(0)
		w.U64(0)
		w.String("")
		w.U32(0)
		w.Bool(false)
		w.Bool(false)
		w.String("")
		w.Raw([]byte{1, 2, 3, 4, 5}) // fields newer than this parser
	})
	w.U32(0xCAFE) // whatever follows the session in the request

	r := rmc.NewReader(w.Bytes())
	got, err := readSession(r)
	if err != nil {
		t.Fatalf("a console's session did not parse: %v", err)
	}
	if got.MaxParticipants != 12 || got.ParticipationPolicy != 95 || got.GameMode != 2 ||
		len(got.Attributes) != 6 || got.Attributes[1] != 1000 || !got.OpenParticipation {
		t.Fatalf("misparsed: %+v", got)
	}
	if next, _ := r.U32(); next != 0xCAFE {
		t.Fatalf("reader left at the wrong place: next=%#x", next)
	}
}
