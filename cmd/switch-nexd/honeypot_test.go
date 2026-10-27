package main

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// dialSNI is dial with a ClientHello server name, the piece the honeypot
// captures the routing decision from.
func dialSNI(t *testing.T, addr, accessKey, sni string) *client {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         sni,
		MinVersion:         tls.VersionTLS10,
	})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return &client{t: t, conn: conn, accessKey: accessKey}
}

// TestHoneypotCapturesHandshake drives both capture endpoints the way an
// unknown-title console would: TLS with a bogus game-server SNI, then the
// PRUDPLite handshake signed with a key the server does not know. The auth
// stage must ack the CONNECT (the capture is already taken) and refuse every
// RMC; the secure stage must reject the CONNECT outright. Both CONNECTs and
// both TLS hellos must land in the capture file with the SNI attached.
func TestHoneypotCapturesHandshake(t *testing.T) {
	const sni = "gdeadbeef-lp1.s.n.srv.nintendo.net"
	const key = "ae8528ab" // speedrunners' key: known, so the proof recomputes

	logPath := filepath.Join(t.TempDir(), "capture.jsonl")
	h, err := newHoneypot(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testTLS(t)
	authLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secureLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authLn.Close(); secureLn.Close() })
	h.authEndpoint().Listen(h.honeypotListener(authLn, cfg))
	h.secureEndpoint().Listen(h.honeypotListener(secureLn, cfg))

	// Auth: handshake accepted, login refused, nothing playable.
	a := dialSNI(t, authLn.Addr().String(), key, sni)
	a.handshake(nil)
	authSig := append([]byte{}, a.connSig...)
	resp := a.call(0x0A, 0x02, rmc.NewWriter().Bytes())
	if !resp.IsError || resp.ErrorCode != rmc.ResultNotImplemented {
		t.Fatalf("auth RMC answered %#08x, want NotImplemented", resp.ErrorCode|rmc.ErrorCodeBit)
	}

	// Secure: CONNECT rejected after capture.
	s := dialSNI(t, secureLn.Addr().String(), key, sni)
	s.send(&prudplite.Packet{Type: prudplite.Syn, SequenceID: 0})
	synAck := s.recv()
	secureSig := append([]byte{}, synAck.ConnectionSig...)
	connect := &prudplite.Packet{Type: prudplite.Connect, Flags: prudplite.FlagReliable | prudplite.FlagNeedsAck, SequenceID: 1}
	connect.SourceStreamType, connect.SourceVirtualPort = prudplite.StreamState, 0x0F
	connect.DestinationStreamType, connect.DestinationVirtualPort = prudplite.StreamState, 0x01
	connect.Signature = prudplite.LitePacketSignature(key, connect, secureSig)
	s.send(connect)
	rej := s.recv()
	if rej.Type != prudplite.Disconnect {
		t.Fatalf("secure CONNECT answered %s, want DISCONNECT", rej)
	}

	recs := readHoneypotLog(t, logPath)
	var tlsRecs, connectRecs []map[string]any
	for _, r := range recs {
		switch r["type"] {
		case "tls":
			tlsRecs = append(tlsRecs, r)
		case "connect":
			connectRecs = append(connectRecs, r)
		default:
			t.Fatalf("unknown record type %q", r["type"])
		}
	}
	if len(tlsRecs) != 2 || len(connectRecs) != 2 {
		t.Fatalf("want 2 tls + 2 connect records, got %d + %d:\n%v", len(tlsRecs), len(connectRecs), recs)
	}
	for _, r := range recs {
		if r["sni"] != sni {
			t.Errorf("record carries sni %q, want %q", r["sni"], sni)
		}
		if r["ts"] == "" {
			t.Error("record carries no timestamp")
		}
	}

	// Each connect record must carry everything the offline brute-forcer
	// needs: the SYN-ACK signature the client signed, the Lite proof, and
	// the raw frame those decode from — with the proof recomputed here from
	// the documented transform, not from the server's own helper.
	sigs := map[string][]byte{"honeypot/auth": authSig, "honeypot/secure": secureSig}
	for _, r := range connectRecs {
		ep, _ := r["endpoint"].(string)
		wantSig, ok := sigs[ep]
		if !ok {
			t.Errorf("connect record for unknown endpoint %q", ep)
			continue
		}
		if r["connsig"] != hex.EncodeToString(wantSig) {
			t.Errorf("%s: connsig %q is not the SYN-ACK signature", ep, r["connsig"])
		}
		mk := md5.Sum([]byte(key))
		mac := hmac.New(md5.New, mk[:])
		mac.Write(mk[:])
		mac.Write(wantSig)
		if r["litesig"] != hex.EncodeToString(mac.Sum(nil)) {
			t.Errorf("%s: litesig %q does not match HMAC-MD5(md5(key), md5(key)||connsig)", ep, r["litesig"])
		}
		rawHex, _ := r["raw"].(string)
		raw, derr := hex.DecodeString(rawHex)
		if derr != nil || len(raw) == 0 {
			t.Errorf("%s: raw frame is not hex: %q", ep, rawHex)
			continue
		}
		p, _, err := prudplite.Decode(raw)
		if err != nil {
			t.Errorf("%s: raw frame does not decode: %v", ep, err)
			continue
		}
		if p.Type != prudplite.Connect || hex.EncodeToString(p.Signature) != r["litesig"] {
			t.Errorf("%s: raw frame is not the captured CONNECT", ep)
		}
	}
}

func readHoneypotLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var recs []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var r map[string]any
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				t.Fatalf("capture line is not JSON: %q", line)
			}
			recs = append(recs, r)
		}
		if len(recs) >= 4 || time.Now().After(deadline) {
			return recs
		}
		time.Sleep(10 * time.Millisecond)
	}
}
