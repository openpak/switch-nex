package switchnex

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"testing"
	"time"
)

var (
	testSourceKey = bytes.Repeat([]byte{0x11}, 32)
	testTargetKey = DeriveKey([]byte("securepassword"), 2)
)

// The v1 inner ticket: u32le 16 + ticket key, u32le len + (48-byte body+16-byte
// HMAC). With a 32-byte session key the encrypted section is 64 bytes, so the
// whole inner blob is 4+16+4+64 = 88 bytes.
func TestServerTicketLayout(t *testing.T) {
	st := ServerTicket{
		Timestamp:  MakeDateTime(time.Date(2026, 9, 16, 12, 34, 56, 0, time.UTC)),
		SourcePID:  1900000001,
		SessionKey: bytes.Repeat([]byte{0x22}, 32),
	}
	out, err := st.Encrypt(testTargetKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4+16+4+48+16 {
		t.Fatalf("inner ticket length %d, want 88", len(out))
	}
	if got := binary.LittleEndian.Uint32(out[0:4]); got != 16 {
		t.Fatalf("ticket-key length %d, want 16", got)
	}
	if got := binary.LittleEndian.Uint32(out[20:24]); got != 48+16 {
		t.Fatalf("encrypted length %d, want 64", got)
	}
	// The HMAC verifies only under MD5(targetKey ‖ ticketKey).
	ticketKey := out[4:20]
	h := md5.Sum(append(append([]byte{}, testTargetKey...), ticketKey...))
	if _, err := DecryptSum(h[:], out[24:]); err != nil {
		t.Fatalf("inner checksum invalid under derived final key: %v", err)
	}
	// It must NOT verify under the raw target key.
	if _, err := DecryptSum(testTargetKey, out[24:]); err == nil {
		t.Fatal("inner checksum verified under the wrong key")
	}
}

func TestServerTicketRoundTrip(t *testing.T) {
	st := ServerTicket{Timestamp: 0x1234567890, SourcePID: 77, SessionKey: bytes.Repeat([]byte{0x33}, 32)}
	out, err := st.Encrypt(testTargetKey)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecryptServerTicket(out, testTargetKey)
	if err != nil {
		t.Fatal(err)
	}
	if back.Timestamp != st.Timestamp || back.SourcePID != st.SourcePID || !bytes.Equal(back.SessionKey, st.SessionKey) {
		t.Fatalf("round trip mismatch: %+v", back)
	}
}

// The outer ticket: session key (32) + u64 target PID + u32le len + inner,
// RC4'd with the source key and a 16-byte HMAC appended:
// 32+8+4+88 = 132 plaintext, 148 bytes on the wire.
func TestClientTicketLayout(t *testing.T) {
	internal := bytes.Repeat([]byte{0x44}, 88)
	ct := ClientTicket{SessionKey: bytes.Repeat([]byte{0x55}, 32), TargetPID: 2, Internal: internal}
	out, err := ct.Encrypt(testSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 32+8+4+88+16 {
		t.Fatalf("outer ticket length %d, want 148", len(out))
	}
	if _, err := DecryptSum(testSourceKey, out); err != nil {
		t.Fatalf("outer checksum invalid under source key: %v", err)
	}
}

func TestClientTicketRoundTrip(t *testing.T) {
	internal := bytes.Repeat([]byte{0x66}, 88)
	ct := ClientTicket{SessionKey: bytes.Repeat([]byte{0x77}, 32), TargetPID: 1234, Internal: internal}
	out, err := ct.Encrypt(testSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecryptClientTicket(out, testSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.SessionKey, ct.SessionKey) || back.TargetPID != ct.TargetPID || !bytes.Equal(back.Internal, ct.Internal) {
		t.Fatalf("round trip mismatch: %+v", back)
	}
}

// Mint produces a ticket the secure server can fully unwrap with only its own
// key and the client can unwrap with only the source key.
func TestMintFullUnwrap(t *testing.T) {
	ticket, sessionKey, err := Mint(testSourceKey, 1900000001, 2, testTargetKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticket) != 148 {
		t.Fatalf("ticket length %d, want 148", len(ticket))
	}
	if len(sessionKey) != 32 {
		t.Fatalf("session key length %d", len(sessionKey))
	}

	client, err := DecryptClientTicket(ticket, testSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(client.SessionKey, sessionKey) || client.TargetPID != 2 {
		t.Fatalf("client unwrap mismatch: target=%d session=%x", client.TargetPID, client.SessionKey)
	}

	server, err := DecryptServerTicket(client.Internal, testTargetKey)
	if err != nil {
		t.Fatal(err)
	}
	if server.SourcePID != 1900000001 || !bytes.Equal(server.SessionKey, sessionKey) {
		t.Fatalf("server unwrap mismatch: %+v", server)
	}
	if NowDateTime() < server.Timestamp {
		t.Fatalf("ticket timestamp %d in the future", server.Timestamp)
	}
}

// The Switch-era key derivation: md5(password) once, append PID u64le, md5 once.
func TestDeriveKeyVector(t *testing.T) {
	key := DeriveKey([]byte("securepassword"), 2)
	if len(key) != 16 {
		t.Fatalf("key length %d", len(key))
	}
	// Independent recomputation.
	h1 := md5.Sum([]byte("securepassword"))
	buf := append(append([]byte{}, h1[:]...), 2, 0, 0, 0, 0, 0, 0, 0)
	h2 := md5.Sum(buf)
	if !bytes.Equal(key, h2[:]) {
		t.Fatalf("DeriveKey\n got %x\nwant %x", key, h2)
	}
}

func TestDateTimePacking(t *testing.T) {
	got := MakeDateTime(time.Date(2026, 9, 16, 12, 34, 56, 0, time.UTC))
	want := uint64(56) | 34<<6 | 12<<12 | 16<<17 | 9<<22 | 2026<<26
	if got != want {
		t.Fatalf("DateTime %d, want %d", got, want)
	}
}
