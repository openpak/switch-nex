package prudplite

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"testing"
)

var synBytes, _ = hex.DecodeString("80060000330f010000000000000400000000")

// A client that upgrades gets its frames unwrapped and its answers wrapped.
func TestWebsocketCarriage(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		client.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: upgrade\r\n" +
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"))
		mask := []byte{1, 2, 3, 4}
		frame := append([]byte{0x82, 0x80 | byte(len(synBytes))}, mask...)
		for i, b := range synBytes {
			frame = append(frame, b^mask[i%4])
		}
		client.Write(frame)
	}()

	br := bufio.NewReader(client)
	done := make(chan net.Conn, 1)
	go func() {
		conn, err := sniffTransport(server)
		if err != nil {
			t.Error(err)
		}
		done <- conn
	}()
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept key %q", got) // the RFC 6455 worked example
	}
	conn := <-done
	if _, ok := conn.(*wsConn); !ok {
		t.Fatalf("got %T, want *wsConn", conn)
	}
	got := make([]byte, len(synBytes))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, synBytes) {
		t.Fatalf("read %x, %v", got, err)
	}

	go conn.Write(synBytes)
	out := make([]byte, 2+len(synBytes))
	if _, err := io.ReadFull(br, out); err != nil {
		t.Fatal(err)
	}
	if out[0] != 0x82 || int(out[1]) != len(synBytes) || !bytes.Equal(out[2:], synBytes) {
		t.Fatalf("framed answer %x", out)
	}
}

// A client that never upgrades is left bare, first bytes intact.
func TestBareCarriageIsUntouched(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go client.Write(synBytes)
	conn, err := sniffTransport(server)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conn.(*wsConn); ok {
		t.Fatal("bare client was treated as websocket")
	}
	got := make([]byte, len(synBytes))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, synBytes) {
		t.Fatalf("read %x, %v", got, err)
	}
}
