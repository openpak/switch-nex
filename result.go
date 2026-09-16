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

// appendString writes a NEX String: u16 length including the trailing NUL,
// then the UTF-8 bytes and the NUL.
func appendString(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint16(b, uint16(len(s)+1))
	return append(append(b, s...), 0)
}

// Bytes serialises the response body exactly as the working reference emits
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
