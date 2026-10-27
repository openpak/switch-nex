package main

// NEX SNI honeypot: a handshake-logging endpoint for game servers whose
// access key is unknown.
//
// Traefik routes every g<hex>-lp1.s.n.srv.nintendo.net SNI that names no
// known title here (docs/honeypot.md). The endpoint completes the PRUDPLite
// handshake far enough to capture the client's CONNECT — its Lite signature
// is HMAC-MD5(md5(key), md5(key) ‖ connSig), so one captured CONNECT makes an
// 8-hex access key brute-forceable offline over 2^32 — then answers nothing
// playable: the secure stage rejects every CONNECT and the auth stage answers
// every RMC with NotImplemented.
//
// Nothing here touches the title path: the only shared change is the
// nil-guarded prudplite OnHandshake hook, and the honeypot serves only when
// -honeypot-base is set, which no title quadlet sets.

import (
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/openpak/switch-nex/protocols"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
	"github.com/openpak/switch-nex/rmc"
)

// honeypotLog is the append-only JSONL capture file: one object per TLS
// hello (type=tls) and per CONNECT frame (type=connect). The tls lines are
// the routing proof — a bogus SNI answered here and never at nx-baas — and
// the connect lines carry the offline brute-force inputs: the connection
// signature the SYN-ACK carried (connsig, the signed value), the client's
// Lite proof (litesig), and the whole CONNECT frame (raw).
type honeypotLog struct {
	mu sync.Mutex
	f  *os.File
}

func openHoneypotLog(path string) (*honeypotLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &honeypotLog{f: f}, nil
}

func (l *honeypotLog) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.f.Write(b)
}

// honeypot serves the two capture endpoints and the SNI table that ties a
// CONNECT back to the ClientHello name the peer asked for.
type honeypot struct {
	log *honeypotLog

	mu   sync.Mutex
	snis map[string]sniEntry // peer address -> ClientHello SNI
}

type sniEntry struct {
	sni string
	at  time.Time
}

// honeypotSNIMax bounds the SNI table: entries are deleted when their CONNECT
// is captured, and a peer that never sends one (a port scan) must not pin
// memory. Past the bound, entries older than an hour go.
const honeypotSNIMax = 4096

func newHoneypot(logPath string) (*honeypot, error) {
	l, err := openHoneypotLog(logPath)
	if err != nil {
		return nil, err
	}
	return &honeypot{log: l, snis: map[string]sniEntry{}}, nil
}

// tlsConfig clones cfg with a GetCertificate that accepts any SNI — the
// static wildcard answers every name — while recording which name each peer
// asked for, keyed by the address the CONNECT logger will see for it.
func (h *honeypot) tlsConfig(cfg *tls.Config) *tls.Config {
	out := cfg.Clone()
	out.GetCertificate = func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
		remote := ""
		if hi.Conn != nil {
			remote = hi.Conn.RemoteAddr().String()
		}
		h.noteSNI(remote, hi.ServerName)
		return &out.Certificates[0], nil
	}
	return out
}

func (h *honeypot) noteSNI(remote, sni string) {
	h.mu.Lock()
	h.snis[remote] = sniEntry{sni: sni, at: time.Now()}
	if len(h.snis) > honeypotSNIMax {
		cutoff := time.Now().Add(-time.Hour)
		for k, e := range h.snis {
			if e.at.Before(cutoff) {
				delete(h.snis, k)
			}
		}
	}
	h.mu.Unlock()
	h.log.write(map[string]any{
		"ts":     time.Now().UTC().Format(time.RFC3339Nano),
		"type":   "tls",
		"remote": remote,
		"sni":    sni,
	})
}

// onHandshake captures one CONNECT before any check: the endpoint knows no
// access key, so there is nothing to check against, and the capture has to
// happen whether the CONNECT that follows is answered or rejected.
func (h *honeypot) onHandshake(c *prudplite.Connection, raw []byte, p *prudplite.Packet) {
	h.mu.Lock()
	sni := "unknown"
	if e, ok := h.snis[c.Remote.String()]; ok {
		sni = e.sni
		delete(h.snis, c.Remote.String())
	}
	h.mu.Unlock()
	h.log.write(map[string]any{
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
		"type":     "connect",
		"endpoint": c.Endpoint.Name,
		"remote":   c.Remote.String(),
		"sni":      sni,
		"connsig":  hex.EncodeToString(c.Signature),
		"litesig":  hex.EncodeToString(p.Signature),
		"raw":      hex.EncodeToString(raw),
	})
	// The capture file carries the real address (0600, forensics); the
	// stdout log carries the tag, like every other connection line.
	log.Printf("[honeypot] CONNECT captured endpoint=%s sni=%q from=%s litesig=%dB raw=%dB",
		c.Endpoint.Name, sni, redact.Tag(c.Remote), len(p.Signature), len(raw))
}

// authEndpoint answers the handshake and then nothing playable: CONNECTs are
// acked (the capture is already taken) and every RMC is NotImplemented.
func (h *honeypot) authEndpoint() *prudplite.Endpoint {
	return &prudplite.Endpoint{
		Name:        "honeypot/auth",
		StreamID:    1,
		AccessKey:   "",
		OnHandshake: h.onHandshake,
		OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
			return rmc.EncodeError(msg.Protocol, msg.CallID, rmc.ResultNotImplemented)
		},
	}
}

// secureEndpoint rejects every CONNECT after capturing it: no kerberos key
// and RequireTicket means an empty payload and a ticketed one both fail.
func (h *honeypot) secureEndpoint() *prudplite.Endpoint {
	return &prudplite.Endpoint{
		Name:          "honeypot/secure",
		StreamID:      1,
		IsSecure:      true,
		AccessKey:     "",
		RequireTicket: true,
		OnHandshake:   h.onHandshake,
	}
}

// serveHoneypot binds auth=base, secure=base+1 and healthz=base+2 and serves
// the capture endpoints. Healthz answers the same shape as a title's, so the
// status page and the drain script treat it like one.
func serveHoneypot(base int, logPath string, tlsCfg *tls.Config, stationAddress string, voice *voiceIndex) error {
	h, err := newHoneypot(logPath)
	if err != nil {
		return fmt.Errorf("honeypot log: %w", err)
	}
	svc := &service{env: protocols.NewEnv("honeypot", stationAddress, base), base: base}
	serveHealth(fmt.Sprintf(":%d", base+2), svc, voice)

	authLn, err := listenTitle(fmt.Sprintf(":%d", base), h.tlsConfig(tlsCfg))
	if err != nil {
		return fmt.Errorf("honeypot auth listen :%d: %w", base, err)
	}
	secureLn, err := listenTitle(fmt.Sprintf(":%d", base+1), h.tlsConfig(tlsCfg))
	if err != nil {
		authLn.Close()
		return fmt.Errorf("honeypot secure listen :%d: %w", base+1, err)
	}
	log.Printf("[honeypot] serving auth :%d secure :%d healthz :%d log=%s", base, base+1, base+2, logPath)
	h.authEndpoint().Listen(authLn)
	h.secureEndpoint().Listen(secureLn)
	svc.authUp.Store(true)
	svc.secureUp.Store(true)
	return nil
}

// honeypotListener wraps an already-open TCP listener for tests, which bind
// ephemeral ports rather than a fixed base.
func (h *honeypot) honeypotListener(ln net.Listener, tlsCfg *tls.Config) net.Listener {
	return tls.NewListener(ln, h.tlsConfig(tlsCfg))
}
