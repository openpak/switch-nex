// Package prudplite implements the PRUDPLite wire format as spoken by
// Switch-era NEX titles (verified: MK8D 3.0.5 raw PRUDPLite over TLS-TCP,
// OpenPak captures 2026-09-14; handshake shapes matched to the working
// server).
//
// Frame layout (little-endian):
//
//	u8   0x80                marker (Lite signature byte)
//	u8   options length
//	u16  payload length
//	u8   (source stream type << 4) | destination stream type
//	u8   source virtual port stream id
//	u8   destination virtual port stream id
//	u8   fragment id
//	u16  packet type | (flags << 4)
//	u16  sequence id
//	[options]
//	[payload]
//
// Packet types: 0x00 SYN, 0x01 CONNECT, 0x02 DATA, 0x03 DISCONNECT, 0x04 PING.
// Flags: 0x1 ACK, 0x2 RELIABLE, 0x4 NEEDS_ACK, 0x8 HAS_SIZE, 0x200 MULTIACK.
//
// Measured handshake facts the client depends on:
//   - SYN-ACK and CONNECT-ACK carry ACK | HAS_SIZE. Without HAS_SIZE the
//     client completes the handshake but sends an EMPTY CONNECT payload (no
//     kerberos ticket) — the "ticketless CONNECT" symptom.
//   - DATA frames carry RELIABLE | NEEDS_ACK (0x6), never HAS_SIZE: with
//     HAS_SIZE the client reads the payload length per fragment instead of
//     the frame length, corrupting reassembly of large payloads.
//   - SYN/CONNECT frames carry the support option: u32le
//     minorVersion | supportedFunctions<<8.
//   - A SYN-ACK carries the connection-signature option (id 1); a client
//     CONNECT carries the Lite-signature option (id 0x80) proving knowledge
//     of the title's access key.
package prudplite

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// PacketType is the PRUDPLite packet type identifier.
type PacketType uint8

// PRUDPLite packet types.
const (
	Syn        PacketType = 0x0
	Connect    PacketType = 0x1
	Data       PacketType = 0x2
	Disconnect PacketType = 0x3
	Ping       PacketType = 0x4
)

// Flag bits carried in the high nibble of the type word.
const (
	FlagAck      uint16 = 0x1
	FlagReliable uint16 = 0x2
	FlagNeedsAck uint16 = 0x4
	FlagHasSize  uint16 = 0x8
	FlagMultiAck uint16 = 0x200
)

// StreamType is the rdv::Stream::Type carried in the virtual port nibbles.
type StreamType uint8

// Stream types (rdv::Stream::Type enum order).
const (
	StreamDO       StreamType = 1
	StreamTransfer StreamType = 2
	StreamState    StreamType = 3
	StreamNAT      StreamType = 4
	StreamSession  StreamType = 5
)

// Packet is one decoded PRUDPLite frame.
type Packet struct {
	SourceStreamType       StreamType
	DestinationStreamType  StreamType
	SourceVirtualPort      uint8
	DestinationVirtualPort uint8
	FragmentID             uint8
	Type                   PacketType
	Flags                  uint16
	SequenceID             uint16
	Options                []byte
	Payload                []byte

	// Decoded options.
	ConnectionSig      []byte // id 1 on SYN-ACK: the server connection signature
	Signature          []byte // id 0x80 on CONNECT: the access-key proof
	MinorVersion       uint8  // support option
	SupportedFunctions uint32 // support option, upper 24 bits
}

// HasFlag reports whether the flag bit is set.
func (p *Packet) HasFlag(f uint16) bool { return p.Flags&f != 0 }

// Option ids seen on SYN/CONNECT/DATA frames.
const (
	OptSupportedFunctions  uint8 = 0
	OptConnectionSignature uint8 = 1
	OptFragmentID          uint8 = 2
	OptInitialUnreliableID uint8 = 3
	OptMaximumSubstreamID  uint8 = 4
	OptLiteSignature       uint8 = 0x80
)

// ErrTruncated is returned when a frame is shorter than its headers claim.
var ErrTruncated = errors.New("prudplite: truncated frame")

// Decode parses one PRUDPLite frame from the head of b and returns the packet
// plus the number of bytes consumed.
func Decode(b []byte) (*Packet, int, error) {
	if len(b) < 12 {
		return nil, 0, fmt.Errorf("%w: header", ErrTruncated)
	}
	if b[0] != 0x80 {
		return nil, 0, fmt.Errorf("prudplite: bad marker 0x%02X", b[0])
	}
	p := &Packet{}
	optionsLength := int(b[1])
	payloadLength := int(binary.LittleEndian.Uint16(b[2:4]))
	p.SourceStreamType = StreamType(b[4] >> 4)
	p.DestinationStreamType = StreamType(b[4] & 0xF)
	p.SourceVirtualPort = b[5]
	p.DestinationVirtualPort = b[6]
	p.FragmentID = b[7]
	typeWord := binary.LittleEndian.Uint16(b[8:10])
	p.Flags = typeWord >> 4
	p.Type = PacketType(typeWord & 0xF)
	p.SequenceID = binary.LittleEndian.Uint16(b[10:12])

	off := 12
	if off+optionsLength+payloadLength > len(b) {
		return nil, 0, fmt.Errorf("%w: options+payload", ErrTruncated)
	}
	if err := p.decodeOptions(b[off : off+optionsLength]); err != nil {
		return nil, 0, err
	}
	off += optionsLength
	p.Payload = append([]byte{}, b[off:off+payloadLength]...)
	off += payloadLength

	return p, off, nil
}

// Encode serialises the packet (header + options + payload).
func (p *Packet) Encode() []byte {
	options := p.EncodeOptions()
	out := make([]byte, 0, 12+len(options)+len(p.Payload))
	out = append(out, 0x80)
	out = append(out, uint8(len(options)))
	out = binary.LittleEndian.AppendUint16(out, uint16(len(p.Payload)))
	out = append(out, uint8(p.SourceStreamType)<<4|uint8(p.DestinationStreamType))
	out = append(out, p.SourceVirtualPort, p.DestinationVirtualPort, p.FragmentID)
	typeWord := uint16(p.Type)&0xF | (p.Flags << 4)
	out = binary.LittleEndian.AppendUint16(out, typeWord)
	out = binary.LittleEndian.AppendUint16(out, p.SequenceID)
	out = append(out, options...)
	return append(out, p.Payload...)
}

// Option is one TLV option from the options block.
type Option struct {
	ID    uint8
	Value []byte
}

// DecodeOptions splits the options block into TLV options.
func DecodeOptions(b []byte) []Option {
	var opts []Option
	for len(b) >= 2 {
		size := int(b[1])
		if len(b) < 2+size {
			break
		}
		opts = append(opts, Option{ID: b[0], Value: append([]byte{}, b[2:2+size]...)})
		b = b[2+size:]
	}
	return opts
}

// decodeOptions interprets the typed options this profile uses.
func (p *Packet) decodeOptions(b []byte) error {
	for _, o := range DecodeOptions(b) {
		switch o.ID {
		case OptSupportedFunctions:
			if len(o.Value) == 4 {
				v := binary.LittleEndian.Uint32(o.Value)
				p.MinorVersion = uint8(v & 0xFF)
				p.SupportedFunctions = v >> 8
			}
		case OptConnectionSignature:
			p.ConnectionSig = o.Value
		case OptLiteSignature:
			p.Signature = o.Value
		}
	}
	return nil
}

// EncodeOptions serialises the options block (TLV: id, size, value) with the
// options each frame type carries:
//
//	SYN, CONNECT        → support option (minor version | supported functions)
//	SYN + ACK           → connection signature
//	CONNECT without ACK → Lite signature (access-key proof)
func (p *Packet) EncodeOptions() []byte {
	var out []byte
	put := func(id uint8, val []byte) {
		out = append(out, id, uint8(len(val)))
		out = append(out, val...)
	}
	if p.Type == Syn || p.Type == Connect {
		support := make([]byte, 4)
		binary.LittleEndian.PutUint32(support, uint32(p.MinorVersion)|(p.SupportedFunctions<<8))
		put(OptSupportedFunctions, support)
	}
	if p.Type == Syn && p.HasFlag(FlagAck) {
		put(OptConnectionSignature, p.ConnectionSig)
	}
	if p.Type == Connect && !p.HasFlag(FlagAck) {
		put(OptLiteSignature, p.Signature)
	}
	return out
}

// FragmentOption returns the fragment id option from a DATA frame, if present.
func (p *Packet) FragmentOption() (uint8, bool) {
	for _, o := range DecodeOptions(p.Options) {
		if o.ID == OptFragmentID && len(o.Value) >= 1 {
			return o.Value[0], true
		}
	}
	return 0, false
}

// String renders the frame summary for logs.
func (p *Packet) String() string {
	return fmt.Sprintf("prudplite type=%d flags=0x%04X seq=%d dst-stream=%d len=%d",
		p.Type, p.Flags, p.SequenceID, p.DestinationVirtualPort, len(p.Payload))
}
