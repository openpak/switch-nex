package rmc

import (
	"bytes"
	"testing"
)

// The packed request framing, byte-for-byte: u32le size, proto byte with the
// request bit, u32le callID, u32le method, parameters.
func TestEncodeRequestGolden(t *testing.T) {
	got := EncodeRequest(0x0A, 0x02, 0x11223344, []byte{0xAA, 0xBB})
	want := []byte{
		0x0B, 0x00, 0x00, 0x00, // size = 11 (1 proto + 4 callID + 4 method + 2 params)
		0x8A,                   // protocol 0x0A | request bit
		0x44, 0x33, 0x22, 0x11, // callID
		0x02, 0x00, 0x00, 0x00, // method
		0xAA, 0xBB,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeRequest\n got %x\nwant %x", got, want)
	}
}

// Success response: u8 1, u32le callID, u32le method|0x8000, body.
func TestEncodeSuccessGolden(t *testing.T) {
	got := EncodeSuccess(0x0A, 7, 0x02, []byte{0x01})
	want := []byte{
		0x0B, 0x00, 0x00, 0x00, // size = 11 (1 proto + 1 marker + 4 callID + 4 method + 1 body)
		0x0A,                   // protocol, no request bit
		0x01,                   // success
		0x07, 0x00, 0x00, 0x00, // callID
		0x02, 0x80, 0x00, 0x00, // method | 0x8000
		0x01,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeSuccess\n got %x\nwant %x", got, want)
	}
}

// Error response: u8 0, u32le code|0x80000000, u32le callID.
func TestEncodeErrorGolden(t *testing.T) {
	got := EncodeError(0x0A, 9, ResultNotImplemented)
	want := []byte{
		0x0A, 0x00, 0x00, 0x00, // size = 10
		0x0A,
		0x00,                   // failure
		0x02, 0x00, 0x01, 0x80, // 0x00010002 | error bit
		0x09, 0x00, 0x00, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeError\n got %x\nwant %x", got, want)
	}
}

func TestDecodeRequest(t *testing.T) {
	payload := EncodeRequest(0x0A, 0x02, 42, []byte{0xDE, 0xAD})
	m, err := DecodeRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsRequest || m.Protocol != 0x0A || m.MethodID != 0x02 || m.CallID != 42 {
		t.Fatalf("decoded %+v", m)
	}
	if !bytes.Equal(m.Body, []byte{0xDE, 0xAD}) {
		t.Fatalf("body %x", m.Body)
	}
}

// Extended protocol ids (>= 0x100) escape through 0x7F + u16le.
func TestEscapeProtocolRoundTrip(t *testing.T) {
	payload := EncodeRequest(0x6F, 0x10, 1, nil)
	m, err := DecodeRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if m.Protocol != 0x6F || m.MethodID != 0x10 {
		t.Fatalf("decoded %+v", m)
	}
}

func TestDecodeResponseRoundTrip(t *testing.T) {
	m, err := Decode(EncodeSuccess(0x0B, 3, 0x05, []byte{0x99}))
	if err != nil {
		t.Fatal(err)
	}
	if m.IsRequest || !m.IsSuccess || m.Protocol != 0x0B || m.MethodID != 0x05 || m.CallID != 3 {
		t.Fatalf("decoded %+v", m)
	}
	if !bytes.Equal(m.Body, []byte{0x99}) {
		t.Fatalf("body %x", m.Body)
	}
}

func TestDecodeRejectsSizeMismatch(t *testing.T) {
	payload := EncodeSuccess(0x0A, 1, 1, nil)
	payload[0] ^= 0xFF
	if _, err := DecodeRequest(payload); err == nil {
		t.Fatal("expected size mismatch error")
	}
}

// The NEX String is u16le length INCLUDING the trailing NUL, then bytes+NUL.
func TestStringWidth(t *testing.T) {
	w := NewWriter()
	w.String("MK8")
	want := []byte{0x04, 0x00, 'M', 'K', '8', 0x00}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("String\n got %x\nwant %x", w.Bytes(), want)
	}
	s, err := NewReader(w.Bytes()).String()
	if err != nil || s != "MK8" {
		t.Fatalf("read back %q err=%v", s, err)
	}
}

// The NEX boolean is one byte.
func TestBoolWidth(t *testing.T) {
	w := NewWriter()
	w.Bool(true)
	w.Bool(false)
	if !bytes.Equal(w.Bytes(), []byte{1, 0}) {
		t.Fatalf("Bool got %x", w.Bytes())
	}
}

func TestResponseError(t *testing.T) {
	for _, proto := range []uint16{0x0A, 0x100} { // plain and escaped ids
		if code, isErr := ResponseError(EncodeError(proto, 7, ResultNotImplemented)); !isErr || code != ResultNotImplemented&^ErrorCodeBit {
			t.Fatalf("proto 0x%X: code=0x%X isErr=%v", proto, code, isErr)
		}
		if _, isErr := ResponseError(EncodeSuccess(proto, 7, 1, []byte{0, 0, 0, 0, 0})); isErr {
			t.Fatalf("proto 0x%X: success read as error", proto)
		}
	}
	if _, isErr := ResponseError(nil); isErr {
		t.Fatal("nil read as error")
	}
}
