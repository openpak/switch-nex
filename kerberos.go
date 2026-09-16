// Package kerberos — the Switch-era NEX ticket, version 1. Two layers, each
// RC4-encrypted with a 16-byte HMAC-MD5 of the ciphertext appended:
//
//	ServerTicket (inner): u64le DateTime, u64le source PID, session key —
//	    wrapped with a 16-byte random per-ticket key, RC4 key
//	    MD5(target key ‖ ticket key), both blobs u32le-length-prefixed.
//	    Encrypted with the SECURE SERVER account key, so only the secure
//	    endpoint can recover the session.
//	ClientTicket (outer): session key, u64le target PID, inner (as a
//	    u32le-length-prefixed buffer) — encrypted with the SOURCE key, which
//	    the client knows (it is returned hex in the LoginEx response), so the
//	    client recovers its session key here.
//
// All integers little-endian; timestamps are packed NEX DateTimes
// (year<<26 | month<<22 | day<<17 | hour<<12 | minute<<6 | second).
package switchnex

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

// RandomKeyLen is the Switch kerberos key size (source key, session key).
const RandomKeyLen = 32

// ticketKeyLen is the v1 per-ticket random key size mixed into the target key.
const ticketKeyLen = 16

// checksumLen is the trailing HMAC-MD5 size on every encrypted blob.
const checksumLen = 16

// HMACMD5 computes HMAC-MD5(key, data).
func HMACMD5(key, data []byte) []byte {
	h := hmac.New(md5.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// EncryptSum RC4-encrypts plaintext under key and appends the HMAC-MD5 of the
// ciphertext (keyed with the same key).
func EncryptSum(key, plaintext []byte) []byte {
	c, _ := rc4.NewCipher(key)
	enc := make([]byte, len(plaintext))
	c.XORKeyStream(enc, plaintext)
	return append(enc, HMACMD5(key, enc)...)
}

// DecryptSum verifies the trailing HMAC-MD5 checksum, then RC4-decrypts.
func DecryptSum(key, buffer []byte) ([]byte, error) {
	if len(buffer) < checksumLen {
		return nil, fmt.Errorf("kerberos: buffer too short (%d)", len(buffer))
	}
	data := buffer[:len(buffer)-checksumLen]
	checksum := buffer[len(buffer)-checksumLen:]
	if !hmac.Equal(checksum, HMACMD5(key, data)) {
		return nil, fmt.Errorf("kerberos: invalid checksum (wrong key)")
	}
	c, _ := rc4.NewCipher(key)
	dec := make([]byte, len(data))
	c.XORKeyStream(dec, data)
	return dec, nil
}

// DeriveKey derives an account's kerberos key from its password and PID
// (the Switch-era derivation): md5(password) once, append the PID as u64
// little-endian, md5 once more.
func DeriveKey(password []byte, pid uint64) []byte {
	sum := md5.Sum(password)
	buf := make([]byte, 0, md5.Size+8)
	buf = append(buf, sum[:]...)
	buf = binary.LittleEndian.AppendUint64(buf, pid)
	out := md5.Sum(buf)
	return out[:]
}

// MakeDateTime packs a time as a NEX DateTime (UTC calendar fields).
func MakeDateTime(t time.Time) uint64 {
	t = t.UTC()
	return uint64(t.Second()) | uint64(t.Minute())<<6 | uint64(t.Hour())<<12 |
		uint64(t.Day())<<17 | uint64(int(t.Month()))<<22 | uint64(t.Year())<<26
}

// NowDateTime returns the current UTC time as a packed NEX DateTime.
func NowDateTime() uint64 { return MakeDateTime(time.Now()) }

// ServerTicket is the inner blob the secure endpoint decrypts with its own
// account key to recover the session.
type ServerTicket struct {
	Timestamp  uint64 // packed NEX DateTime of issue
	SourcePID  uint64
	SessionKey []byte
}

// Encrypt serialises and encrypts the inner ticket with the secure server's
// key (v1 wrap: random 16-byte ticket key, RC4 key MD5(key ‖ ticketKey)).
func (t ServerTicket) Encrypt(targetKey []byte) ([]byte, error) {
	if len(t.SessionKey) != RandomKeyLen {
		return nil, fmt.Errorf("kerberos: session key must be %d bytes, got %d", RandomKeyLen, len(t.SessionKey))
	}
	body := binary.LittleEndian.AppendUint64(nil, t.Timestamp)
	body = binary.LittleEndian.AppendUint64(body, t.SourcePID)
	body = append(body, t.SessionKey...)

	ticketKey := make([]byte, ticketKeyLen)
	if _, err := rand.Read(ticketKey); err != nil {
		return nil, fmt.Errorf("kerberos: rand: %w", err)
	}
	sum := md5.Sum(append(append([]byte{}, targetKey...), ticketKey...))
	enc := EncryptSum(sum[:], body)

	out := binary.LittleEndian.AppendUint32(nil, ticketKeyLen)
	out = append(out, ticketKey...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(enc)))
	return append(out, enc...), nil
}

// DecryptServerTicket decrypts an inner ticket with the secure server's key.
func DecryptServerTicket(data, targetKey []byte) (*ServerTicket, error) {
	in := &keybuf{b: data}
	ticketKey, err := in.Buffer()
	if err != nil {
		return nil, err
	}
	enc, err := in.Buffer()
	if err != nil {
		return nil, err
	}
	sum := md5.Sum(append(append([]byte{}, targetKey...), ticketKey...))
	body, err := DecryptSum(sum[:], enc)
	if err != nil {
		return nil, err
	}
	if len(body) < 16+RandomKeyLen {
		return nil, fmt.Errorf("kerberos: internal data too short (%d)", len(body))
	}
	t := &ServerTicket{
		Timestamp:  binary.LittleEndian.Uint64(body[0:8]),
		SourcePID:  binary.LittleEndian.Uint64(body[8:16]),
		SessionKey: append([]byte{}, body[16:16+RandomKeyLen]...),
	}
	return t, nil
}

// ClientTicket is the outer blob the client decrypts with its source key to
// recover the session key, and hands the untouched inner blob to the secure
// endpoint.
type ClientTicket struct {
	SessionKey []byte
	TargetPID  uint64
	Internal   []byte // encrypted ServerTicket
}

// Encrypt serialises and encrypts the outer ticket with the source key.
func (t ClientTicket) Encrypt(sourceKey []byte) ([]byte, error) {
	if len(t.SessionKey) != RandomKeyLen {
		return nil, fmt.Errorf("kerberos: session key must be %d bytes, got %d", RandomKeyLen, len(t.SessionKey))
	}
	plain := append(append([]byte{}, t.SessionKey...), 0, 0, 0, 0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint64(plain[len(t.SessionKey):], t.TargetPID)
	plain = binary.LittleEndian.AppendUint32(plain, uint32(len(t.Internal)))
	plain = append(plain, t.Internal...)
	return EncryptSum(sourceKey, plain), nil
}

// DecryptClientTicket decrypts an outer ticket with the source key.
func DecryptClientTicket(data, sourceKey []byte) (*ClientTicket, error) {
	plain, err := DecryptSum(sourceKey, data)
	if err != nil {
		return nil, err
	}
	if len(plain) < RandomKeyLen+8+4 {
		return nil, fmt.Errorf("kerberos: client ticket too short (%d)", len(plain))
	}
	internalLen := int(binary.LittleEndian.Uint32(plain[RandomKeyLen+8:]))
	if len(plain) < RandomKeyLen+12+internalLen {
		return nil, fmt.Errorf("kerberos: client ticket internal length mismatch")
	}
	return &ClientTicket{
		SessionKey: append([]byte{}, plain[:RandomKeyLen]...),
		TargetPID:  binary.LittleEndian.Uint64(plain[RandomKeyLen : RandomKeyLen+8]),
		Internal:   append([]byte{}, plain[RandomKeyLen+12:RandomKeyLen+12+internalLen]...),
	}, nil
}

// Mint builds a complete two-layer ticket for one login: fresh 32-byte
// session key, inner ServerTicket under the secure server's key, outer
// ClientTicket under the client's source key. Returns the wire ticket and
// the session key (for the server's own bookkeeping).
func Mint(sourceKey []byte, sourcePID, targetPID uint64, targetKey []byte) (ticket []byte, sessionKey []byte, err error) {
	sessionKey = make([]byte, RandomKeyLen)
	if _, err = rand.Read(sessionKey); err != nil {
		return nil, nil, fmt.Errorf("kerberos: rand: %w", err)
	}
	internal, err := ServerTicket{Timestamp: NowDateTime(), SourcePID: sourcePID, SessionKey: sessionKey}.Encrypt(targetKey)
	if err != nil {
		return nil, nil, err
	}
	ticket, err = ClientTicket{SessionKey: sessionKey, TargetPID: targetPID, Internal: internal}.Encrypt(sourceKey)
	if err != nil {
		return nil, nil, err
	}
	return ticket, sessionKey, nil
}

// SourceKeyHex renders the source key the way the LoginEx response carries it
// (hex string — verified shape from our own capture).
func SourceKeyHex(key []byte) string { return hex.EncodeToString(key) }

// keybuf is a minimal u32le-length-prefixed blob reader for ticket internals.
type keybuf struct {
	b []byte
	o int
}

func (k *keybuf) Buffer() ([]byte, error) {
	if len(k.b)-k.o < 4 {
		return nil, fmt.Errorf("kerberos: truncated blob length")
	}
	n := int(binary.LittleEndian.Uint32(k.b[k.o:]))
	k.o += 4
	if n < 0 || len(k.b)-k.o < n {
		return nil, fmt.Errorf("kerberos: truncated blob (%d < %d)", len(k.b)-k.o, n)
	}
	v := append([]byte{}, k.b[k.o:k.o+n]...)
	k.o += n
	return v, nil
}
