// NEX data-type readers and writers for RMC bodies (Switch byte-stream
// profile: little-endian, 2-byte string lengths (including the trailing NUL),
// u8 booleans, u64 PIDs, structure headers on every custom structure —
// SPEC.md / MIGRATION.md).
//
// Structure header: u8 version, u32 content length. The length lets readers
// skip structures they do not understand and writers frame content without
// knowing the enclosing context. Every Switch title runs NEX >= 4.0.0, so
// headers are always present.
package rmc

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Reader walks an RMC body.
type Reader struct {
	b []byte
	o int
}

// NewReader wraps a body.
func NewReader(b []byte) *Reader { return &Reader{b: b} }

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int { return len(r.b) - r.o }

func (r *Reader) need(n int) error {
	if r.Remaining() < n {
		return fmt.Errorf("rmc: short read (%d < %d) at offset %d", r.Remaining(), n, r.o)
	}
	return nil
}

// U8 reads one byte.
func (r *Reader) U8() (uint8, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	v := r.b[r.o]
	r.o++
	return v, nil
}

// U16 reads a little-endian u16.
func (r *Reader) U16() (uint16, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint16(r.b[r.o:])
	r.o += 2
	return v, nil
}

// U32 reads a little-endian u32.
func (r *Reader) U32() (uint32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(r.b[r.o:])
	r.o += 4
	return v, nil
}

// U64 reads a little-endian u64.
func (r *Reader) U64() (uint64, error) {
	if err := r.need(8); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint64(r.b[r.o:])
	r.o += 8
	return v, nil
}

// Bool reads a NEX boolean (u8).
func (r *Reader) Bool() (bool, error) {
	v, err := r.U8()
	return v != 0, err
}

// String reads a NEX string: u16 length (including the trailing NUL) + bytes.
func (r *Reader) String() (string, error) {
	n, err := r.U16()
	if err != nil {
		return "", err
	}
	if err := r.need(int(n)); err != nil {
		return "", err
	}
	s := string(r.b[r.o : r.o+int(n)])
	r.o += int(n)
	for len(s) > 0 && s[len(s)-1] == 0 {
		s = s[:len(s)-1]
	}
	return s, nil
}

// Buffer reads a length-prefixed byte block (NEX Buffer / QBuffer).
func (r *Reader) Buffer() ([]byte, error) {
	n, err := r.U32()
	if err != nil {
		return nil, err
	}
	if err := r.need(int(n)); err != nil {
		return nil, err
	}
	v := make([]byte, n)
	copy(v, r.b[r.o:r.o+int(n)])
	r.o += int(n)
	return v, nil
}

// StructHeader reads a structure header (u8 version, u32 content length).
// The returned length is the content length that follows.
func (r *Reader) StructHeader() (version uint8, contentLen uint32, err error) {
	version, err = r.U8()
	if err != nil {
		return 0, 0, err
	}
	contentLen, err = r.U32()
	return version, contentLen, err
}

// ListHeader reads a list count. Callers loop that many elements.
func (r *Reader) ListHeader() (uint32, error) { return r.U32() }

// Fwd skips n bytes.
func (r *Reader) Fwd(n int) error {
	if err := r.need(n); err != nil {
		return err
	}
	r.o += n
	return nil
}

// Writer builds an RMC body.
type Writer struct {
	b []byte
}

// NewWriter returns an empty writer.
func NewWriter() *Writer { return &Writer{} }

// Bytes returns the accumulated body.
func (w *Writer) Bytes() []byte { return w.b }

// U8 appends one byte.
func (w *Writer) U8(v uint8) { w.b = append(w.b, v) }

// U16 appends a little-endian u16.
func (w *Writer) U16(v uint16) { w.b = binary.LittleEndian.AppendUint16(w.b, v) }

// U32 appends a little-endian u32.
func (w *Writer) U32(v uint32) { w.b = binary.LittleEndian.AppendUint32(w.b, v) }

// U64 appends a little-endian u64.
func (w *Writer) U64(v uint64) { w.b = binary.LittleEndian.AppendUint64(w.b, v) }

// Bool appends a NEX boolean (u8).
func (w *Writer) Bool(v bool) {
	if v {
		w.U8(1)
		return
	}
	w.U8(0)
}

// String appends a NEX string (u16 length including the trailing NUL, then
// the bytes and the NUL).
func (w *Writer) String(s string) {
	w.U16(uint16(len(s) + 1))
	w.b = append(w.b, s...)
	w.b = append(w.b, 0)
}

// Buffer appends a length-prefixed byte block.
func (w *Writer) Buffer(b []byte) {
	w.U32(uint32(len(b)))
	w.b = append(w.b, b...)
}

// Raw appends bytes verbatim.
func (w *Writer) Raw(b []byte) { w.b = append(w.b, b...) }

// Struct appends a structure header framing content, then the content.
func (w *Writer) Struct(version uint8, content []byte) {
	w.U8(version)
	w.U32(uint32(len(content)))
	w.b = append(w.b, content...)
}

// StructOf builds content through a nested writer and appends it framed.
func (w *Writer) StructOf(version uint8, build func(nested *Writer)) {
	n := NewWriter()
	build(n)
	w.Struct(version, n.Bytes())
}

// List appends a list count; the caller then appends count elements.
func (w *Writer) List(count int) { w.U32(uint32(count)) }

// EmptyList appends a zero-count list.
func (w *Writer) EmptyList() { w.U32(0) }

// Variant is a tagged NEX value (rdv::Variant). Type IDs follow the
// reference encoder's registry: 1 Int64, 2 Double, 3 Bool, 4 String,
// 5 DateTime, 6 UInt64; 0 carries no data.
type Variant struct {
	TypeID uint8
	I64    int64
	F64    float64
	Bool   bool
	Str    string
	U64    uint64
}

// ReadVariant reads one Variant.
func (r *Reader) ReadVariant() (Variant, error) {
	id, err := r.U8()
	if err != nil {
		return Variant{}, err
	}
	v := Variant{TypeID: id}
	switch id {
	case 0:
	case 1:
		v.I64, err = r.I64()
	case 2:
		v.F64, err = r.F64()
	case 3:
		v.Bool, err = r.Bool()
	case 4:
		v.Str, err = r.String()
	case 5, 6:
		v.U64, err = r.U64()
	default:
		err = fmt.Errorf("rmc: unknown variant type %d", id)
	}
	return v, err
}

// I64 reads a little-endian i64.
func (r *Reader) I64() (int64, error) {
	v, err := r.U64()
	return int64(v), err
}

// F64 reads a little-endian f64.
func (r *Reader) F64() (float64, error) {
	v, err := r.U64()
	return math.Float64frombits(v), err
}

// WriteVariant appends one Variant.
func (w *Writer) WriteVariant(v Variant) {
	w.U8(v.TypeID)
	switch v.TypeID {
	case 1:
		w.U64(uint64(v.I64))
	case 2:
		w.U64(math.Float64bits(v.F64))
	case 3:
		w.Bool(v.Bool)
	case 4:
		w.String(v.Str)
	case 5, 6:
		w.U64(v.U64)
	}
}

// Result codes (pre-error-bit). The error bit 0x80000000 is added by
// EncodeError; DataStore::NotFound therefore goes on the wire as 0x80690004,
// the value measured against the working reference servers.
const (
	ResultSuccess            uint32 = 0x00010001 // Core::Unknown — the observed success marker
	ResultNotImplemented     uint32 = 0x00010002 // Core::NotImplemented
	ResultInvalidArgument    uint32 = 0x0001000A // Core::InvalidArgument
	ResultSessionVoid        uint32 = 0x00030073 // RendezVous::SessionVoid
	ResultDataStoreNotFound  uint32 = 0x00069004 // DataStore::NotFound
	ResultRanking2InvalidArg uint32 = 0x00710002 // Ranking2::InvalidArgument
	ResultRefereeInvalidArg  uint32 = 0x006F0002 // MatchmakeReferee::InvalidArgument
	ResultRefereeNotJoined   uint32 = 0x006F0004 // MatchmakeReferee::NotParticipatedGathering
	ResultRefereeRoundGone   uint32 = 0x006F0007 // MatchmakeReferee::RoundNotFound
)
