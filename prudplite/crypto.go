package prudplite

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"net"
)

// liteConnSigKey keys the connection-signature HMAC: a fixed protocol
// constant of the PRUDP Lite profile (documented in the NintendoClients
// protocol notes).
var liteConnSigKey = mustHex("26c31f381e46d6eb38e1af6ab70d11")

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// ConnectionSignature derives the server connection signature for a peer
// address: HMAC-MD5(liteConnSigKey, ipv4 ‖ port BE). The client computes the
// same value from the address it dialed.
func ConnectionSignature(remote net.Addr) []byte {
	if tcp, ok := remote.(*net.TCPAddr); ok {
		if ip := tcp.IP.To4(); ip != nil {
			data := make([]byte, 6)
			copy(data, ip)
			binary.BigEndian.PutUint16(data[4:], uint16(tcp.Port))
			return hmacMD5(liteConnSigKey, data)
		}
	}
	return nil
}

// LitePacketSignature returns the packet signature for a Lite packet: only a
// CONNECT carrying NEED_ACK is signed — it proves the sender knows the
// title's access key — as
// HMAC-MD5(md5(accessKey), md5(accessKey) ‖ connectionSig).
// Every other Lite packet is unsigned (nil).
func LitePacketSignature(accessKey string, p *Packet, connectionSig []byte) []byte {
	if p.Type != Connect || !p.HasFlag(FlagNeedsAck) {
		return nil
	}
	key := md5Sum([]byte(accessKey))
	return hmacMD5(key, append(append([]byte{}, key...), connectionSig...))
}

func hmacMD5(key, data []byte) []byte {
	h := hmac.New(md5.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func md5Sum(b []byte) []byte {
	s := md5.Sum(b)
	return s[:]
}
