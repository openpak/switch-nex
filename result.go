package switchnex

import (
	"encoding/binary"
)

// Result is the ValidateAndRequestTicketResult response body for
// TicketGranting method 0x02, Switch byte-stream profile (8-byte PIDs,
// structure headers). Shape verified byte-for-byte against our own wire
// capture (348-byte body, SPEC.md).
type Result struct {
	StructureVersion uint8
	SourcePID        uint64
	Ticket           []byte
	StationURL       string // full "prudp:/…" string, length-prefixed on the wire
	CurrentUTCTime   uint64
	ReturnMsg        string
	SourceKeyHex     string
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s))+1) // trailing NUL, nex convention
	return append(append(b, s...), 0)
}

// Bytes serialises the response body exactly as observed:
//
//	u8 version=1, u32 content length,
//	u64 SourcePID, u32 len+ticket, u32 len+StationURL,
//	u64 CurrentUTCTime, u32 len+ReturnMsg, u32 len+SourceKey
func (r Result) Bytes() []byte {
	content := make([]byte, 0, 256)
	content = binary.BigEndian.AppendUint64(content, r.SourcePID)
	content = binary.BigEndian.AppendUint32(content, uint32(len(r.Ticket)))
	content = append(content, r.Ticket...)
	content = binary.BigEndian.AppendUint32(content, uint32(len(r.StationURL)))
	content = append(content, r.StationURL...)
	content = binary.BigEndian.AppendUint64(content, r.CurrentUTCTime)
	content = appendString(content, r.ReturnMsg)
	content = appendString(content, r.SourceKeyHex)

	out := binary.BigEndian.AppendUint32(nil, uint32(len(content)))
	out = append(out, r.StructureVersion)
	out = append(out, content...)
	return out
}

// QResult returns the RMC success result word sent ahead of the body
// (observed: success with the "unknown" reason nibble).
func QResult() uint32 { return 0x00010001 }
