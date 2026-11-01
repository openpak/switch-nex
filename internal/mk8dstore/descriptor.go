package mk8dstore

import (
	"encoding/binary"
	"time"
	"unicode/utf16"

	"github.com/openpak/switch-nex/rmc"
)

// Descriptor is what the site shows of a tournament, read best-effort from a
// SimpleSearchObject (NintendoClients wiki, "Matchmake Extension Protocol
// (MK8D)"): object id, owner, attributes, metadata, community id, code,
// datetime attribute. A descriptor that does not parse leaves the fields empty;
// the raw blob is always stored.
type Descriptor struct {
	Name, Description, Code string
	Attributes              []int64
	Start, End              *time.Time
}

// ParseDescriptor reads the structure contents (no header) of a SimpleSearchObject.
func ParseDescriptor(blob []byte) (d Descriptor) {
	d.Attributes = []int64{}
	r := rmc.NewReader(blob)
	if _, err := r.U32(); err != nil { // object id
		return
	}
	if _, err := r.U64(); err != nil { // owner
		return
	}
	n, err := r.ListHeader()
	if err != nil || n > 64 {
		return
	}
	for range n {
		v, err := r.U32()
		if err != nil {
			return
		}
		d.Attributes = append(d.Attributes, int64(v))
	}
	meta, err := r.QBuffer()
	if err != nil {
		return
	}
	d.Name, d.Description = parseMetadata(meta)
	if _, err = r.U32(); err != nil { // community id
		return
	}
	if d.Code, err = r.String(); err != nil {
		return
	}
	_, dt, err := r.Struct()
	if err != nil {
		return
	}
	for range 4 {
		if _, err = dt.U32(); err != nil {
			return
		}
	}
	if v, err := dt.U64(); err == nil {
		d.Start = unpackDateTime(v)
	}
	if v, err := dt.U64(); err == nil {
		d.End = unpackDateTime(v)
	}
	return
}

func unpackDateTime(v uint64) *time.Time {
	if v == 0 {
		return nil
	}
	y, mo, dd := int(v>>26), int(v>>22&0xF), int(v>>17&0x1F)
	if y < 2000 || y > 2100 || mo < 1 || mo > 12 || dd < 1 {
		return nil
	}
	t := time.Date(y, time.Month(mo), dd, int(v>>12&0x1F), int(v>>6&0x3F), int(v&0x3F), 0, time.UTC)
	return &t
}

// parseMetadata reads the game's chunked metadata blob: big-endian chunks
// (magic 0x5a5a, then id:u8 size:u16 data until id 255), chunk 2 the name and
// chunk 4 the description as UTF-16BE. INFERRED from the Wii U title's layout
// (the Deluxe client has not been captured); a blob that does not fit yields "".
func parseMetadata(data []byte) (name, desc string) {
	if len(data) < 3 || binary.BigEndian.Uint16(data) != 0x5a5a {
		return
	}
	chunks := map[uint8][]byte{}
	p := 2
	for p < len(data) {
		id := data[p]
		p++
		if id == 255 {
			break
		}
		if p+2 > len(data) {
			return
		}
		size := int(binary.BigEndian.Uint16(data[p:]))
		p += 2
		if p+size > len(data) {
			return
		}
		chunks[id] = data[p : p+size]
		p += size
	}
	str := func(b []byte) string {
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			v := binary.BigEndian.Uint16(b[i:])
			if v == 0 {
				break
			}
			u = append(u, v)
		}
		return string(utf16.Decode(u))
	}
	return str(chunks[2]), str(chunks[4])
}
