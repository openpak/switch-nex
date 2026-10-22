package switchnex

import (
	"encoding/binary"
)

// Result is the ValidateAndRequestTicketResult body of a successful
// TicketGranting LoginEx (0x02) response, Switch byte-stream profile
// (little-endian, 8-byte PIDs, NEX strings, RVConnectionData as a versioned
// structure). Flat layout — the version+length header belongs to method 0x06
// (ValidateAndRequestTicketWithParam), not to LoginEx.
//
// Wire shape (all little-endian):
//
//	u32 retval
//	u64 SourcePID
//	u32 len + Ticket (the encrypted two-layer kerberos ticket)
//	structure v1: u8 version=1, u32 len, RVConnectionData {
//	    String StationURL, u32 count + SpecialProtocols,
//	    String SpecialStation, u64 ServerTime
//	}
//	String ReturnMsg
//	String SourceKey (hex — the client decrypts the ticket with it)
type Result struct {
	RetVal           uint32 // rmc.ResultSuccess on the happy path
	SourcePID        uint64
	Ticket           []byte
	StationURL       string // full "prudp:/…" string
	SpecialProtocols []uint8
	SpecialStation   string // bare "prudp" when unused
	CurrentUTCTime   uint64 // packed NEX DateTime
	ReturnMsg        string
	SourceKeyHex     string
}

// TicketResult is the ValidateAndRequestTicketResult body of a successful
// TicketGranting ValidateAndRequestTicketWithParam (0x06) response — the NEX
// 4.4+ login path. It is NOT the LoginEx body wrapped in a header: there is no
// leading retval, and the secure endpoint is a bare StationURL string followed
// by the server time, where LoginEx carries a whole RVConnectionData structure.
//
// Wire shape, all inside one structure (u8 version=1, u32 content length):
//
//	u64 SourcePID
//	u32 len + Ticket
//	String ServiceNodeURL
//	u64 CurrentUTCTime
//	String ReturnMsg
//	String SourceKey (hex)
type TicketResult struct {
	SourcePID      uint64
	Ticket         []byte
	StationURL     string
	CurrentUTCTime uint64
	ReturnMsg      string
	SourceKeyHex   string
}

// Bytes serialises the structure-wrapped method-0x06 response body.
func (r TicketResult) Bytes() []byte {
	c := binary.LittleEndian.AppendUint64(nil, r.SourcePID)
	c = binary.LittleEndian.AppendUint32(c, uint32(len(r.Ticket)))
	c = append(c, r.Ticket...)
	c = appendString(c, r.StationURL)
	c = binary.LittleEndian.AppendUint64(c, r.CurrentUTCTime)
	c = appendString(c, r.ReturnMsg)
	c = appendString(c, r.SourceKeyHex)

	out := append([]byte{1}, 0, 0, 0, 0) // structure version 1 + content length
	binary.LittleEndian.PutUint32(out[1:], uint32(len(c)))
	return append(out, c...)
}

// appendString writes a NEX String: u16 length including the trailing NUL,
// then the UTF-8 bytes and the NUL.
func appendString(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint16(b, uint16(len(s)+1))
	return append(append(b, s...), 0)
}

// Bytes serialises the response body exactly as a working server emits
// it for LoginEx.
func (r Result) Bytes() []byte {
	cd := appendString(nil, r.StationURL)
	cd = binary.LittleEndian.AppendUint32(cd, uint32(len(r.SpecialProtocols)))
	for _, p := range r.SpecialProtocols {
		cd = append(cd, p)
	}
	cd = appendString(cd, r.SpecialStation)
	cd = binary.LittleEndian.AppendUint64(cd, r.CurrentUTCTime)

	rvcd := append([]byte{1}, 0, 0, 0, 0) // structure version 1 + u32 content length placeholder
	binary.LittleEndian.PutUint32(rvcd[1:], uint32(len(cd)))
	rvcd = append(rvcd, cd...)

	out := binary.LittleEndian.AppendUint32(nil, r.RetVal)
	out = binary.LittleEndian.AppendUint64(out, r.SourcePID)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(r.Ticket)))
	out = append(out, r.Ticket...)
	out = append(out, rvcd...)
	out = appendString(out, r.ReturnMsg)
	out = appendString(out, r.SourceKeyHex)
	return out
}
