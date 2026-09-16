package main

import (
	"encoding/binary"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// switchnexResult builds the flat ValidateAndRequestTicketResult body for a
// title's LoginEx (0x02) response. Method 0x06 is NOT this body with a header
// on it — it is a different field list; see switchnexTicketResult.
func switchnexResult(name, url string, ticket []byte, now uint64, pid uint64, sourceKey []byte) []byte {
	return switchnex.Result{
		RetVal:     rmc.ResultSuccess,
		SourcePID:  pid,
		Ticket:     ticket,
		StationURL: url,
		// The special station is always the plain "prudp" scheme, even for a
		// title whose main station is "prudps" — it is a placeholder for a
		// facility we do not offer, not a second address of ours. It renders
		// with its separator; the bare word is a different string.
		SpecialStation: switchnex.EmptyString("prudp"),
		CurrentUTCTime: now,
		ReturnMsg:      "OpenPak " + name,
		SourceKeyHex:   switchnex.SourceKeyHex(sourceKey),
	}.Bytes()
}

// switchnexTicketResult builds the method-0x06 body: no retval, and the secure
// endpoint is a bare station URL string followed by the server time rather than
// a whole RVConnectionData structure.
func switchnexTicketResult(name, url string, ticket []byte, now uint64, pid uint64, sourceKey []byte) []byte {
	return switchnex.TicketResult{
		SourcePID:      pid,
		Ticket:         ticket,
		StationURL:     url,
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
