// Command nexprobe drives one console-shaped login against a running server, so
// a deployment can be checked without a console.
//
// It speaks the console's half for real: TLS with the title's SNI, the
// PRUDPLite handshake including the access-key signature, and a TicketGranting
// login carrying an AuthenticationInfo holder. What it proves depends on the
// token you give it:
//
//   - no token: the login is accepted on its unproven name (the pre-identity
//     behaviour, and what the fallback still does today)
//   - a syntactically valid but unsigned token: the server must reach nx-baas
//     and be told no, which is the only way to prove the call actually happens
//   - a real token: the PID that comes back is the account's
//
// The server's own log is the other half of the answer — look for "UNPROVEN"
// or "login pid=".
//
//	nexprobe -addr 145.241.199.19:443 -sni g2b309e01-lp1.s.n.srv.nintendo.net -key 09c1c475
//
// The SNI is the title's game-server hostname; the authoritative list is the
// Traefik dynamic.yml on the host, not guesswork — a wrong one reaches the
// catch-all, which answers TLS and then never speaks PRUDP, so the probe hangs
// at SYN rather than failing usefully.
package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

var (
	addr     = flag.String("addr", "", "host:port to dial (the edge on :443, or a secure port directly)")
	sni      = flag.String("sni", "", "TLS server name — the title's game-server hostname")
	key      = flag.String("key", "", "the title's PRUDP access key")
	username = flag.String("username", "nexprobe", "login name (a claim, not proof)")
	token    = flag.String("token", "", "BAAS id token; empty sends no token, \"fake\" sends an unsigned one")
	method   = flag.Uint("method", 2, "TicketGranting method: 2 (LoginEx) or 6 (WithParam)")
)

func main() {
	flag.Parse()
	if *addr == "" || *key == "" {
		log.Fatal("need -addr and -key")
	}

	conn, err := tls.Dial("tcp", *addr, &tls.Config{
		InsecureSkipVerify: true, // the server's cert is for console hostnames
		ServerName:         *sni,
		MinVersion:         tls.VersionTLS10,
	})
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	fmt.Printf("TLS      ok  %s (sni %q)\n", *addr, *sni)

	p := &probe{conn: conn}
	p.handshake(*key)
	p.login(*username, *token, uint32(*method))
}

type probe struct {
	conn net.Conn
	sig  []byte
}

func (p *probe) send(pkt *prudplite.Packet) {
	pkt.SourceStreamType, pkt.SourceVirtualPort = prudplite.StreamState, 0x0F
	pkt.DestinationStreamType, pkt.DestinationVirtualPort = prudplite.StreamState, 0x01
	if _, err := p.conn.Write(pkt.Encode()); err != nil {
		log.Fatalf("write: %v", err)
	}
}

func (p *probe) recv() *prudplite.Packet {
	p.conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	head := make([]byte, 12)
	if _, err := io.ReadFull(p.conn, head); err != nil {
		log.Fatalf("read: %v (a wrong -sni reaches a server that does not speak PRUDP)", err)
	}
	rest := make([]byte, int(head[1])+int(binary.LittleEndian.Uint16(head[2:4])))
	if _, err := io.ReadFull(p.conn, rest); err != nil {
		log.Fatalf("read body: %v", err)
	}
	pkt, _, err := prudplite.Decode(append(head, rest...))
	if err != nil {
		log.Fatalf("decode: %v", err)
	}
	return pkt
}

func (p *probe) handshake(accessKey string) {
	p.send(&prudplite.Packet{Type: prudplite.Syn})
	syn := p.recv()
	if len(syn.ConnectionSig) != 16 {
		log.Fatalf("SYN-ACK carried a %d-byte signature, want 16", len(syn.ConnectionSig))
	}
	p.sig = syn.ConnectionSig
	fmt.Printf("SYN-ACK  ok  %d-byte connection signature\n", len(syn.ConnectionSig))

	// RELIABLE|NEEDS_ACK is required: the Lite signature is only defined for a
	// CONNECT that asks to be acknowledged, so without it there is no
	// access-key proof and the server answers nothing at all.
	c := &prudplite.Packet{Type: prudplite.Connect, Flags: prudplite.FlagReliable | prudplite.FlagNeedsAck, SequenceID: 1}
	c.SourceStreamType, c.SourceVirtualPort = prudplite.StreamState, 0x0F
	c.DestinationStreamType, c.DestinationVirtualPort = prudplite.StreamState, 0x01
	c.Signature = prudplite.LitePacketSignature(accessKey, c, p.sig)
	p.send(c)
	p.recv()
	fmt.Printf("CONNECT  ok  access key accepted\n")
}

func (p *probe) login(username, token string, method uint32) {
	w := rmc.NewWriter()
	if method == 6 {
		inner := rmc.NewWriter()
		inner.U32(3) // PlatformType
		inner.String(username)
		if body := holder(token); body != nil {
			inner.Raw(body)
		}
		w.Struct(1, inner.Bytes())
	} else {
		w.String(username)
		if body := holder(token); body != nil {
			w.Raw(body)
		}
	}

	p.send(&prudplite.Packet{
		Type: prudplite.Data, Flags: prudplite.FlagReliable | prudplite.FlagNeedsAck,
		SequenceID: 1, Payload: rmc.EncodeRequest(0x0A, method, 1, w.Bytes()),
	})

	for range 5 {
		pkt := p.recv()
		if pkt.Type != prudplite.Data || pkt.HasFlag(prudplite.FlagAck) || pkt.HasFlag(prudplite.FlagMultiAck) {
			continue
		}
		msg, err := rmc.Decode(pkt.Payload)
		if err != nil {
			log.Fatalf("undecodable response: %v", err)
		}
		if msg.IsError {
			code := msg.ErrorCode | rmc.ErrorCodeBit
			fmt.Printf("LOGIN    REFUSED  %#08x %s\n", code, explain(msg.ErrorCode))
			return
		}
		// Method 6 wraps its fields; method 2 leads with the retval.
		r := rmc.NewReader(msg.Body)
		if method == 6 {
			r.StructHeader()
		} else {
			r.U32()
		}
		pid, _ := r.U64()
		fmt.Printf("LOGIN    ok   pid=%d\n", pid)
		fmt.Println("         check the server log: \"UNPROVEN\" means it fell back to the name")
		return
	}
	log.Fatal("no login response")
}

// holder frames the AuthenticationInfo DataHolder carrying the token.
func holder(token string) []byte {
	if token == "" {
		return nil
	}
	if token == "fake" {
		enc := base64.RawURLEncoding.EncodeToString
		claims, _ := json.Marshal(map[string]any{"sub": "nexprobe", "typ": "id_token"})
		token = enc([]byte(`{"alg":"RS256"}`)) + "." + enc(claims) + ".not-a-real-signature"
	}
	content := rmc.NewWriter()
	content.String(token)
	content.U32(0) // NGSVersion
	content.U32(0) // TokenType
	content.U32(0) // ServerVersion

	object := rmc.NewWriter()
	object.StructOf(0, func(*rmc.Writer) {}) // the empty Data base
	object.Struct(1, content.Bytes())
	body := object.Bytes()

	w := rmc.NewWriter()
	w.String("AuthenticationInfo")
	w.U32(uint32(len(body) + 4))
	w.U32(uint32(len(body)))
	w.Raw(body)
	return w.Bytes()
}

func explain(code uint32) string {
	switch code {
	case rmc.ResultAuthTokenExpired:
		return "(TokenExpired — the console would fetch a fresh token and retry)"
	case rmc.ResultAuthValidationFail:
		return "(ValidationFailed — no account; the console gives up)"
	case rmc.ResultAuthTokenParse:
		return "(TokenParseError — no usable token in the login)"
	}
	return ""
}
