// WebSocket carriage for PRUDPLite.
//
// Some clients open the TLS stream with an HTTP/1.1 websocket upgrade and then
// carry each PRUDPLite frame in a binary message (observed: MK8D 3.0.5 on the
// Ryujinx fork, 2026-09-17 — "GET / HTTP/1.1 … Upgrade: websocket" as the first
// plaintext, and 2306-0502 when the server hung up on it). Others send bare
// frames. The transport follows the client: a connection that upgraded is
// answered framed, one that did not is answered bare — answering a bare client
// framed stalls it silently (SPEC.md).
package prudplite

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// maxWSMessage bounds a single message; a PRUDPLite frame is at most
// 12 + 255 + 65535 bytes.
const maxWSMessage = 1 << 17

// sniffTransport reads the start of a fresh connection. A websocket upgrade is
// answered and the returned conn speaks messages; anything else is returned
// untouched (bar the buffering) for the bare path.
func sniffTransport(conn net.Conn) (net.Conn, error) {
	br := bufio.NewReader(conn)
	first, err := br.Peek(4)
	if err != nil {
		return nil, err
	}
	if string(first) != "GET " {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	req, err := http.ReadRequest(br)
	if err != nil {
		return nil, err
	}
	key := req.Header.Get("Sec-WebSocket-Key")
	if !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") || key == "" {
		return nil, fmt.Errorf("prudplite: HTTP request that is not a websocket upgrade: %s %s", req.Method, req.URL)
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n"
	if proto := req.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		resp += "Sec-WebSocket-Protocol: " + strings.TrimSpace(strings.Split(proto, ",")[0]) + "\r\n"
	}
	if _, err := conn.Write([]byte(resp + "\r\n")); err != nil {
		return nil, err
	}
	return &wsConn{Conn: conn, r: br}, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// wsConn presents a websocket as the byte stream the PRUDPLite reader expects:
// Read drains message payloads back to back, Write sends one binary message.
type wsConn struct {
	net.Conn
	r       *bufio.Reader
	pending []byte
	wmu     sync.Mutex // a pong from the read loop must not interleave with a send
}

func (w *wsConn) Read(p []byte) (int, error) {
	for len(w.pending) == 0 {
		if err := w.nextFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, w.pending)
	w.pending = w.pending[n:]
	return n, nil
}

func (w *wsConn) nextFrame() error {
	var h [2]byte
	if _, err := io.ReadFull(w.r, h[:]); err != nil {
		return err
	}
	opcode, masked, length := h[0]&0x0F, h[1]&0x80 != 0, uint64(h[1]&0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.r, ext[:]); err != nil {
			return err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.r, ext[:]); err != nil {
			return err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > maxWSMessage {
		return fmt.Errorf("prudplite: websocket frame of %d bytes", length)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(w.r, mask[:]); err != nil {
			return err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(w.r, payload); err != nil {
		return err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	switch opcode {
	case 0x0, 0x1, 0x2: // continuation, text, binary: all just bytes of the stream
		w.pending = payload
	case 0x8:
		return io.EOF
	case 0x9: // ping
		return w.writeFrame(0xA, payload)
	}
	return nil
}

func (w *wsConn) Write(p []byte) (int, error) {
	if err := w.writeFrame(0x2, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// writeFrame sends one unmasked (server-to-client) frame in a single write.
func (w *wsConn) writeFrame(opcode byte, p []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	frame := []byte{0x80 | opcode}
	switch {
	case len(p) < 126:
		frame = append(frame, byte(len(p)))
	case len(p) <= 0xFFFF:
		frame = append(frame, 126, byte(len(p)>>8), byte(len(p)))
	default:
		frame = append(frame, 127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(len(p)))
	}
	_, err := w.Conn.Write(append(frame, p...))
	return err
}

// UpgradeClient performs the client half of the upgrade, for the probe and the
// harness. Frames go out unmasked, which RFC 6455 forbids a client and this
// server accepts — it is a test client for this server, not a websocket client.
func UpgradeClient(conn net.Conn, host string) (net.Conn, error) {
	req := "GET / HTTP/1.1\r\nHost: " + host + "\r\nUpgrade: websocket\r\nConnection: upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("prudplite: upgrade answered %s", resp.Status)
	}
	return &wsConn{Conn: conn, r: br}, nil
}
