package main

import (
	"encoding/binary"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// switchnexResult builds the flat ValidateAndRequestTicketResult body for a
// title's LoginEx response (result.go — the method-0x02 shape; the
// version+length header belongs to method 0x06 and is added by the caller).
func switchnexResult(name, url string, ticket []byte, now uint64, pid uint64, sourceKey []byte) []byte {
	return switchnex.Result{
		RetVal:         rmc.ResultSuccess,
		SourcePID:      pid,
		Ticket:         ticket,
		StationURL:     url,
		SpecialStation: "prudp",
		CurrentUTCTime: now,
		ReturnMsg:      "OpenPak " + name,
		SourceKeyHex:   switchnex.SourceKeyHex(sourceKey),
	}.Bytes()
}

// readString parses a NEX String (u16 length including the trailing NUL).
func readString(b []byte, off int) (string, int) {
	if off+2 > len(b) {
		return "", off
	}
	n := int(binary.LittleEndian.Uint16(b[off : off+2])) // includes the NUL
	off += 2
	if n > len(b)-off {
		n = len(b) - off
	}
	s := b[off : off+n]
	for len(s) > 0 && s[len(s)-1] == 0 {
		s = s[:len(s)-1]
	}
	return string(s), off + n
}
