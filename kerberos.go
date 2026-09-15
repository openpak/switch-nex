// Package kerberos implements the Switch-era NEX ticket format (v1): a 32-byte
// random key in the clear followed by the RC4-encrypted internal data. The RC4
// key is MD5(source key ‖ random key). Field layout of the internal data
// follows SPEC.md; the trailing unmapped bytes nex-go emits are an open item.
package switchnex

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// RandomKeyLen is the Switch kerberos source/ticket key size (32 bytes).
const RandomKeyLen = 32

// Internal is the decryptable core of a server ticket.
type Internal struct {
	Timestamp  uint64 // console epoch nanoseconds? — compare with the working capture before shipping
	SourcePID  uint64
	SessionKey []byte // 32 bytes for MK8D
}

// FinalKey derives the ticket body key: MD5(source key ‖ random key).
func FinalKey(sourceKey, randomKey []byte) []byte {
	h := md5.Sum(append(append([]byte{}, sourceKey...), randomKey...))
	return h[:]
}

// EncryptBody RC4-encrypts the internal data under finalKey.
func EncryptBody(finalKey, plaintext []byte) ([]byte, error) {
	c, err := rc4.NewCipher(finalKey)
	if err != nil {
		return nil, fmt.Errorf("rc4: %w", err)
	}
	out := make([]byte, len(plaintext))
	c.XORKeyStream(out, plaintext)
	return out, nil
}

// DecryptBody RC4-decrypts the internal data under finalKey.
func DecryptBody(finalKey, ciphertext []byte) ([]byte, error) {
	return EncryptBody(finalKey, ciphertext) // RC4 is symmetric
}

// InternalBytes serialises Internal in the minimal Switch layout:
// u64 timestamp, u64 source PID, session key bytes.
//
// OPEN ITEM: nex-go's encoder emits 116 encrypted bytes for the same 48-byte
// core, so additional internal fields exist. If the console answers
// 2306-0802 (Token Parse Error) against this layout, extend here and re-test;
// a working capture decides the field set (SPEC.md).
func (i Internal) InternalBytes() []byte {
	out := make([]byte, 0, 8+8+len(i.SessionKey))
	out = binary.BigEndian.AppendUint64(out, i.Timestamp)
	out = binary.BigEndian.AppendUint64(out, i.SourcePID)
	return append(out, i.SessionKey...)
}

// Mint builds a complete ticket buffer for the given source key:
// u32 randomKeyLen, randomKey(32), RC4 body. The session key is generated here
// and returned for the server's own bookkeeping.
func Mint(sourceKey []byte, sourcePID uint64, timestamp uint64) (ticket []byte, sessionKey []byte, err error) {
	randomKey := make([]byte, RandomKeyLen)
	if _, err = rand.Read(randomKey); err != nil {
		return nil, nil, fmt.Errorf("rand: %w", err)
	}
	sessionKey = make([]byte, RandomKeyLen)
	if _, err = rand.Read(sessionKey); err != nil {
		return nil, nil, fmt.Errorf("rand: %w", err)
	}
	i := Internal{Timestamp: timestamp, SourcePID: sourcePID, SessionKey: sessionKey}
	body, err := EncryptBody(FinalKey(sourceKey, randomKey), i.InternalBytes())
	if err != nil {
		return nil, nil, err
	}
	ticket = binary.BigEndian.AppendUint32(nil, uint32(len(randomKey)))
	ticket = append(ticket, randomKey...)
	ticket = append(ticket, body...)
	return ticket, sessionKey, nil
}

// SourceKeyHex renders the source key the way the LoginEx response carries it
// (hex string — verified shape from our own capture).
func SourceKeyHex(key []byte) string { return hex.EncodeToString(key) }
