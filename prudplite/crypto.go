package prudplite

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rc4"
	"encoding/binary"
	"net"
)

// ConnectionSignature derives the 16-byte connection signature for a SYN/CONNECT
// exchange: HMAC-MD5(key, client ip ‖ port), keyed with the server's PRUDP v1
// connection signature key.
func ConnectionSignature(key []byte, remote net.Addr) ([]byte, error) {
	tcp, ok := remote.(*net.TCPAddr)
	if !ok {
		return nil, ErrTruncated
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(tcp.Port))
	mac := hmac.New(md5.New, key)
	mac.Write(tcp.IP.To4())
	mac.Write(portBytes)
	return mac.Sum(nil), nil
}

// StreamCipher is the per-direction RC4 stream applied to DATA payloads after
// the handshake. Fresh instances per direction; RC4 state advances per packet.
type StreamCipher struct{ c *rc4.Cipher }

// NewStreamCipher builds an RC4 stream cipher from a key.
func NewStreamCipher(key []byte) (*StreamCipher, error) {
	c, err := rc4.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &StreamCipher{c: c}, nil
}

// Apply XORs data in place (RC4 keystream) and returns it.
func (s *StreamCipher) Apply(data []byte) []byte {
	s.c.XORKeyStream(data, data)
	return data
}
