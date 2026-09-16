package prudplite

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

// The 12-byte Lite header, byte-for-byte.
func TestHeaderLayout(t *testing.T) {
	p := &Packet{
		Type:                   Data,
		Flags:                  FlagReliable | FlagNeedsAck,
		SequenceID:             0x0102,
		SourceStreamType:       StreamState,
		DestinationStreamType:  StreamState,
		SourceVirtualPort:      0x11,
		DestinationVirtualPort: 0x1F,
		FragmentID:             3,
		Payload:                []byte{0xAA},
	}
	got := p.Encode()
	want := []byte{
		0x80,       // marker
		0x00,       // options length (DATA carries none)
		0x01, 0x00, // payload length
		0x33,       // (source type 3 << 4) | dest type 3
		0x11, 0x1F, // source / destination virtual port
		0x03,       // fragment id
		0x62, 0x00, // type 0x2 | (flags 0x6 << 4)
		0x02, 0x01, // sequence id LE
		0xAA,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode\n got %x\nwant %x", got, want)
	}
	back, n, err := Decode(got)
	if err != nil || n != len(got) {
		t.Fatalf("Decode: %v (n=%d)", err, n)
	}
	if back.Type != Data || back.Flags != FlagReliable|FlagNeedsAck || back.SequenceID != 0x0102 ||
		back.FragmentID != 3 || back.SourceVirtualPort != 0x11 || back.DestinationVirtualPort != 0x1F {
		t.Fatalf("decoded %+v", back)
	}
}

// SYN-ACK: flags ACK|HAS_SIZE, support option then connection-signature option.
func TestSynAckOptions(t *testing.T) {
	p := &Packet{
		Type:               Syn,
		Flags:              FlagAck | FlagHasSize,
		MinorVersion:       5,
		SupportedFunctions: 0,
		ConnectionSig:      bytes.Repeat([]byte{0x77}, 16),
	}
	got := p.Encode()
	// Header 12 bytes; options: support = 2+4, conn sig = 2+16 → 24 total.
	if got[1] != 24 {
		t.Fatalf("options length %d, want 24", got[1])
	}
	opts := got[12 : 12+24]
	if opts[0] != OptSupportedFunctions || opts[1] != 4 {
		t.Fatalf("support option header %x", opts[0:2])
	}
	if v := binary.LittleEndian.Uint32(opts[2:6]); v != 5 {
		t.Fatalf("support value %d", v)
	}
	if opts[6] != OptConnectionSignature || opts[7] != 16 {
		t.Fatalf("conn-sig option header %x", opts[6:8])
	}
	if !bytes.Equal(opts[8:24], bytes.Repeat([]byte{0x77}, 16)) {
		t.Fatalf("conn-sig value %x", opts[8:24])
	}

	back, _, err := Decode(got)
	if err != nil {
		t.Fatal(err)
	}
	if !back.HasFlag(FlagHasSize) || !back.HasFlag(FlagAck) {
		t.Fatalf("flags %x", back.Flags)
	}
	if back.MinorVersion != 5 || !bytes.Equal(back.ConnectionSig, p.ConnectionSig) {
		t.Fatalf("decoded options %+v", back)
	}
}

// Client CONNECT carries the Lite signature option (0x80).
func TestConnectLiteSignatureOption(t *testing.T) {
	p := &Packet{
		Type:         Connect,
		Flags:        FlagNeedsAck,
		MinorVersion: 5,
		Signature:    bytes.Repeat([]byte{0x99}, 16),
	}
	got := p.Encode()
	if got[1] != 2+16+2+4 {
		t.Fatalf("options length %d", got[1])
	}
	back, _, err := Decode(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Signature, p.Signature) || back.MinorVersion != 5 {
		t.Fatalf("decoded %+v", back)
	}
}

// CONNECT-ACK: ACK|HAS_SIZE, support option only.
func TestConnectAckOptions(t *testing.T) {
	p := &Packet{
		Type:         Connect,
		Flags:        FlagAck | FlagHasSize,
		MinorVersion: 5,
		SequenceID:   1,
		Payload:      []byte{4, 0, 0, 0, 9, 0, 0, 0},
	}
	got := p.Encode()
	if got[1] != 6 {
		t.Fatalf("options length %d", got[1])
	}
	back, _, err := Decode(got)
	if err != nil {
		t.Fatal(err)
	}
	if back.Signature != nil || back.ConnectionSig != nil {
		t.Fatalf("unexpected options on CONNECT-ACK")
	}
}

// Connection signature: HMAC-MD5(constant key, ipv4 ‖ port BE).
func TestConnectionSignatureVector(t *testing.T) {
	remote := &net.TCPAddr{IP: net.IPv4(192, 168, 1, 10), Port: 54321}
	got := ConnectionSignature(remote)
	if len(got) != 16 {
		t.Fatalf("length %d", len(got))
	}
	key := mustHex("26c31f381e46d6eb38e1af6ab70d11")
	data := []byte{192, 168, 1, 10}
	data = binary.BigEndian.AppendUint16(data, 54321)
	h := hmacMD5(key, data)
	if !bytes.Equal(got, h) {
		t.Fatalf("signature %x want %x", got, h)
	}
}

// Lite packet signature: HMAC-MD5(md5(accessKey), md5(accessKey) ‖ connSig),
// only on NEED_ACK CONNECT frames.
func TestLitePacketSignature(t *testing.T) {
	accessKey := "09c1c475"
	connSig := bytes.Repeat([]byte{0x42}, 16)
	connect := &Packet{Type: Connect, Flags: FlagNeedsAck}
	sig := LitePacketSignature(accessKey, connect, connSig)

	key := md5Sum([]byte(accessKey))
	want := hmacMD5(key, append(append([]byte{}, key...), connSig...))
	if !bytes.Equal(sig, want) {
		t.Fatalf("signature %x want %x", sig, want)
	}
	if LitePacketSignature(accessKey, &Packet{Type: Data, Flags: FlagReliable}, connSig) != nil {
		t.Fatal("DATA frames must not be signed")
	}
	if LitePacketSignature(accessKey, &Packet{Type: Connect, Flags: FlagAck}, connSig) != nil {
		t.Fatal("CONNECT-ACK must not be signed")
	}
}
