package switchnex

import (
	"bytes"
	"encoding/binary"
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
