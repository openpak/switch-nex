package main

import (
	"encoding/binary"

	switchnex "github.com/openpak/switch-nex"
)

// switchnexResult builds the verified ValidateAndRequestTicketResult body for
// a title's LoginEx response (SPEC.md).
func switchnexResult(t title, url string, ticket []byte, now uint64, pid uint64, sourceKey []byte) []byte {
	return switchnex.Result{
		StructureVersion: 1,
		SourcePID:        pid,
		Ticket:           ticket,
		StationURL:       url,
		CurrentUTCTime:   now,
		ReturnMsg:        "OpenPak " + t.name,
		SourceKeyHex:     switchnex.SourceKeyHex(sourceKey),
	}.Bytes()
}

// readString parses a NEX String (u32 length + chars) at the offset.
func readString(b []byte, off int) (string, int) {
	if off+4 > len(b) {
		return "", off
	}
	n := int(binary.LittleEndian.Uint32(b[off : off+4]))
	off += 4
	if n > len(b)-off {
		n = len(b) - off
	}
	return string(b[off : off+n]), off + n
}
