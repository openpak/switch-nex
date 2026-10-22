// Packed RMC (Remote Method Call) message framing, as carried in the PRUDP
// DATA payload. Layout pinned to the byte shapes the working Switch-era
// servers emit (SPEC.md, docs/provenance):
//
//	u32le size — bytes following this field
//	u8  protocol | 0x80 request bit (0x7F escapes to a u16le protocol id)
//	request:  u32le callID, u32le methodID, parameters
//	response: u8  success
//	          success → u32le callID, u32le methodID|0x8000, body
//	          error   → u32le code|0x80000000, u32le callID
package rmc

import (
	"encoding/binary"
	"fmt"
)

// Wire constants of the packed framing.
const (
	FlagRequest   byte   = 0x80 // set on the protocol byte of requests
	EscapeProto   byte   = 0x7F // protocol byte value that escapes to u16le
	RespMethodBit uint32 = 0x8000
	ErrorCodeBit  uint32 = 0x80000000
)

// RMCMessage is one decoded request or response.
type RMCMessage struct {
	IsRequest bool
	IsSuccess bool // response carried the success marker
	IsError   bool
	Protocol  uint16
	MethodID  uint32
	CallID    uint32
	ErrorCode uint32 // error responses
	Body      []byte // request parameters / response body
}

// String renders the message summary for logs.
func (m RMCMessage) String() string {
	return fmt.Sprintf("rmc proto=%d method=0x%X call=%d params=%dB", m.Protocol, m.MethodID, m.CallID, len(m.Body))
}

// Decode parses one packed RMC message including its u32le length prefix
// (the whole reassembled DATA payload).
func Decode(payload []byte) (*RMCMessage, error) {
	if len(payload) < 5 {
		return nil, fmt.Errorf("rmc: truncated (%d bytes)", len(payload))
	}
	size := int(binary.LittleEndian.Uint32(payload[0:4]))
	if size != len(payload)-4 {
		return nil, fmt.Errorf("rmc: size mismatch (header=%d, actual=%d)", size, len(payload)-4)
	}
	b := payload[4:]
	proto := b[0]
	m := &RMCMessage{Protocol: uint16(proto &^ FlagRequest)}
	off := 1
	if m.Protocol == uint16(EscapeProto) {
		if len(b) < off+2 {
			return nil, fmt.Errorf("rmc: truncated escaped protocol")
		}
		m.Protocol = binary.LittleEndian.Uint16(b[off:])
		off += 2
	}
	if proto&FlagRequest != 0 {
		m.IsRequest = true
		if len(b) < off+8 {
			return nil, fmt.Errorf("rmc: truncated request header")
		}
		m.CallID = binary.LittleEndian.Uint32(b[off:])
		m.MethodID = binary.LittleEndian.Uint32(b[off+4:])
		m.Body = b[off+8:]
		return m, nil
	}
	if len(b) < off+1 {
		return nil, fmt.Errorf("rmc: truncated response marker")
	}
	if b[off] == 0 {
		m.IsError = true
		if len(b) < off+9 {
			return nil, fmt.Errorf("rmc: truncated error response")
		}
		m.ErrorCode = binary.LittleEndian.Uint32(b[off+1:]) &^ ErrorCodeBit
		m.CallID = binary.LittleEndian.Uint32(b[off+5:])
		return m, nil
	}
	m.IsSuccess = true
	if len(b) < off+9 {
		return nil, fmt.Errorf("rmc: truncated success response")
	}
	m.CallID = binary.LittleEndian.Uint32(b[off+1:])
	m.MethodID = binary.LittleEndian.Uint32(b[off+5:]) &^ RespMethodBit
	m.Body = b[off+9:]
	return m, nil
}

// DecodeRequest parses a packed RMC request from a whole DATA payload; any
// other message kind is an error.
func DecodeRequest(payload []byte) (*RMCMessage, error) {
	m, err := Decode(payload)
	if err != nil {
		return nil, err
	}
	if !m.IsRequest {
		return nil, fmt.Errorf("rmc: not a request")
	}
	return m, nil
}

// EncodeSuccess serialises a success response (with length prefix).
func EncodeSuccess(protocol uint16, callID, method uint32, body []byte) []byte {
	inner := make([]byte, 0, 14+len(body))
	inner = appendProto(inner, protocol, 0)
	inner = append(inner, 1) // success marker
	inner = binary.LittleEndian.AppendUint32(inner, callID)
	inner = binary.LittleEndian.AppendUint32(inner, method|RespMethodBit)
	inner = append(inner, body...)
	return frame(inner)
}

// EncodeError serialises an error response (with length prefix).
func EncodeError(protocol uint16, callID, errorCode uint32) []byte {
	inner := make([]byte, 0, 10)
	inner = appendProto(inner, protocol, 0)
	inner = append(inner, 0) // failure marker
	inner = binary.LittleEndian.AppendUint32(inner, errorCode|ErrorCodeBit)
	inner = binary.LittleEndian.AppendUint32(inner, callID)
	return frame(inner)
}

// ResponseError reports the error code of an encoded response, if it is one.
func ResponseError(resp []byte) (code uint32, isError bool) {
	if len(resp) < 5 {
		return 0, false
	}
	i := 5 // u32 size, protocol byte
	if resp[4]&^0x80 == EscapeProto&^0x80 {
		i += 2
	}
	if len(resp) < i+5 || resp[i] != 0 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(resp[i+1:]) &^ ErrorCodeBit, true
}

// EncodeRequest serialises a request (client-side debugging aid).
func EncodeRequest(protocol uint16, method, callID uint32, params []byte) []byte {
	inner := make([]byte, 0, 9+len(params))
	inner = appendProto(inner, protocol, FlagRequest)
	inner = binary.LittleEndian.AppendUint32(inner, callID)
	inner = binary.LittleEndian.AppendUint32(inner, method)
	inner = append(inner, params...)
	return frame(inner)
}

// appendProto writes the protocol byte, escaping to a u16 for any id that does
// not fit in the seven bits below the request flag. 0x80 and up must escape:
// the high bit is the request marker, so a bare 0x80-0xFF byte would be read
// back as a different protocol with the wrong direction.
func appendProto(b []byte, protocol uint16, flag byte) []byte {
	if protocol < 0x80 {
		return append(b, byte(protocol)|flag)
	}
	b = append(b, EscapeProto|flag)
	return binary.LittleEndian.AppendUint16(b, protocol)
}

// frame prefixes the message with its u32le size.
func frame(inner []byte) []byte {
	out := make([]byte, 4, 4+len(inner))
	binary.LittleEndian.PutUint32(out, uint32(len(inner)))
	return append(out, inner...)
}
