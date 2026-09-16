package switchnex

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// The flat LoginEx (0x02) result body, byte-for-byte. TheRVConnectionData is
// the only framed structure; the top level is flat. All little-endian.
func TestResultBytesGolden(t *testing.T) {
	ticket := bytes.Repeat([]byte{0xAB}, 148)
	r := Result{
		RetVal:         0x00010001,
		SourcePID:      1900000001,
		Ticket:         ticket,
		StationURL:     "prudp:/address=145.241.199.19;port=22151;CID=1;PID=2;sid=1;stream=10;type=2",
		SpecialStation: "prudp",
		CurrentUTCTime: 0x1234567890,
		ReturnMsg:      "OpenPak mario-kart-8-deluxe",
		SourceKeyHex:   "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
	}
	got := r.Bytes()

	want := []byte{}
	want = binary.LittleEndian.AppendUint32(want, 0x00010001)          // retval
	want = binary.LittleEndian.AppendUint64(want, 1900000001)          // SourcePID
	want = binary.LittleEndian.AppendUint32(want, uint32(len(ticket))) // ticket buffer
	want = append(want, ticket...)

	// RVConnectionData structure: u8 version 1, u32le content length, content.
	cd := []byte{}
	cd = appendString(cd, r.StationURL)
	cd = binary.LittleEndian.AppendUint32(cd, 0) // SpecialProtocols: empty list
	cd = appendString(cd, "prudp")
	cd = binary.LittleEndian.AppendUint64(cd, 0x1234567890)
	want = append(want, 1)
	want = binary.LittleEndian.AppendUint32(want, uint32(len(cd)))
	want = append(want, cd...)

	want = appendString(want, "OpenPak mario-kart-8-deluxe")
	want = appendString(want, r.SourceKeyHex)

	if !bytes.Equal(got, want) {
		t.Fatalf("Result.Bytes\n got %x\nwant %x", got, want)
	}
}

// With special protocols present, each element is one u8 after the u32 count.
func TestResultSpecialProtocols(t *testing.T) {
	r := Result{
		StationURL:       "prudp:/address=x",
		SpecialProtocols: []uint8{3, 5},
		SpecialStation:   "prudp",
	}
	got := r.Bytes()
	// Walk to the structure: 4 retval + 8 pid + 4 ticket len + 0 ticket.
	off := 16
	if got[off] != 1 {
		t.Fatalf("structure version %d", got[off])
	}
	contentLen := binary.LittleEndian.Uint32(got[off+1 : off+5])
	content := got[off+5 : off+5+int(contentLen)]
	// content: string stationURL (u16 len incl NUL + bytes), u32 count=2,
	// u8 3, u8 5, string "prudp", u64 time(0)
	urlLen := int(binary.LittleEndian.Uint16(content[0:2]))
	countOff := 2 + urlLen
	if binary.LittleEndian.Uint32(content[countOff:countOff+4]) != 2 {
		t.Fatalf("special protocol count %x", content[countOff:countOff+4])
	}
	if content[countOff+4] != 3 || content[countOff+5] != 5 {
		t.Fatalf("special protocol values %x", content[countOff+4:countOff+6])
	}
}

// TestTicketResultIsNotLoginExWithAHeader pins the difference between the two
// login methods. LoginEx (0x02) is the flat layout led by a retval and carrying
// a whole RVConnectionData structure; ValidateAndRequestTicketWithParam (0x06)
// has NO retval and carries the secure endpoint as a plain URL string followed
// by the server time. Wrapping the first in a header does not produce the
// second, and the client rejects it as an invalid argument.
func TestTicketResultIsNotLoginExWithAHeader(t *testing.T) {
	const url = "prudp:/address=10.0.0.1;port=22151"
	ticket := []byte{1, 2, 3, 4}

	body := TicketResult{
		SourcePID:      4242,
		Ticket:         ticket,
		StationURL:     url,
		CurrentUTCTime: 0x1234,
		ReturnMsg:      "OpenPak test",
		SourceKeyHex:   "abcd",
	}.Bytes()

	if body[0] != 1 {
		t.Fatalf("structure version = %d, want 1", body[0])
	}
	if got := binary.LittleEndian.Uint32(body[1:5]); int(got) != len(body)-5 {
		t.Fatalf("content length = %d, but %d bytes follow", got, len(body)-5)
	}

	// The first field is the PID. If a retval had crept in, this would read
	// 0x00010001 instead.
	rest := body[5:]
	if pid := binary.LittleEndian.Uint64(rest[0:8]); pid != 4242 {
		t.Fatalf("first field = %#x; a leading retval would show up exactly here", pid)
	}
	off := 8
	if n := binary.LittleEndian.Uint32(rest[off:]); int(n) != len(ticket) {
		t.Fatalf("ticket length = %d, want %d", n, len(ticket))
	}
	off += 4 + len(ticket)

	// A bare String, not an RVConnectionData structure: the next two bytes are
	// the string length, not a version byte and a length prefix.
	gotURL, off := readNEXString(t, rest, off)
	if gotURL != url {
		t.Fatalf("station URL = %q, want %q", gotURL, url)
	}
	if when := binary.LittleEndian.Uint64(rest[off:]); when != 0x1234 {
		t.Fatalf("server time = %#x, want 0x1234", when)
	}
	off += 8
	if msg, next := readNEXString(t, rest, off); msg != "OpenPak test" {
		t.Fatalf("message = %q", msg)
	} else {
		off = next
	}
	if key, next := readNEXString(t, rest, off); key != "abcd" {
		t.Fatalf("source key = %q", key)
	} else if next != len(rest) {
		t.Fatalf("%d trailing bytes", len(rest)-next)
	}
}

// readNEXString reads a u16 length (including the trailing NUL) and its bytes.
func readNEXString(t *testing.T, b []byte, off int) (string, int) {
	t.Helper()
	n := int(binary.LittleEndian.Uint16(b[off:]))
	off += 2
	s := b[off : off+n]
	for len(s) > 0 && s[len(s)-1] == 0 {
		s = s[:len(s)-1]
	}
	return string(s), off + n
}

// TestEmptyStationRendersWithItsSeparator: the special-station field carries a
// scheme-only URL, which is "prudp:/" — the bare word is a different string.
func TestEmptyStationRendersWithItsSeparator(t *testing.T) {
	if got := EmptyString("prudp"); got != "prudp:/" {
		t.Fatalf("EmptyString = %q, want %q", got, "prudp:/")
	}
	if got := EmptyString(""); got != "prudp:/" {
		t.Fatalf("default scheme = %q", got)
	}
}

// TestStationURLRoundTrip: what we render, we can read back.
func TestStationURLRoundTrip(t *testing.T) {
	u := NewScheme("prudps", "10.0.0.1", 22151)
	u.SetInt("RVCID", 9)
	back := Parse(u.String())
	if back.Scheme != "prudps" {
		t.Fatalf("scheme = %q", back.Scheme)
	}
	if back.Get("address") != "10.0.0.1" || back.Get("port") != "22151" || back.Get("RVCID") != "9" {
		t.Fatalf("round trip lost parameters: %s", back)
	}
	// Remove takes the key out of the ORDER too; left behind it re-emits as
	// "k=", which is not the same as absent.
	back.Remove("RVCID")
	if back.Has("RVCID") || strings.Contains(back.String(), "RVCID") {
		t.Fatalf("RVCID survived removal: %s", back)
	}
}
