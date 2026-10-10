package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// PROXY protocol (v1 text and v2 binary) on the title listeners.
//
// The auth stage reaches us through Traefik's TCP passthrough router, which
// adds nothing to the stream, so without this every login comes from Traefik's
// own address and nothing ties it to the console that will open the secure
// connection a second later (recall.go). With `proxyProtocol` on the router's
// service, Traefik writes one header line before the TLS bytes naming the
// console's address, and the login is recorded under it.
//
// Only a peer whose own address says nothing (private or loopback: Traefik on
// this host) is allowed to speak for someone else. A header from a public peer
// is not looked for at all, so a console dialling a port directly cannot claim
// another address. A connection without a header passes through untouched,
// which keeps the listener correct before and after the router is switched.

// proxyHeaderWait bounds how long Accept waits for a trusted peer's first
// bytes. Traefik writes the header the moment it connects.
const proxyHeaderWait = 3 * time.Second

// listenTitle opens one title port: TCP, PROXY header, then TLS. Production and
// the harness both build their listeners here.
func listenTitle(addr string, cfg *tls.Config) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return tls.NewListener(proxyListener{ln}, cfg), nil
}

type proxyListener struct{ net.Listener }

func (l proxyListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		peer, ok := c.RemoteAddr().(*net.TCPAddr)
		if !ok || !addressSaysNothing(peer.IP.String()) {
			return c, nil
		}
		br := bufio.NewReader(c)
		_ = c.SetReadDeadline(time.Now().Add(proxyHeaderWait))
		src, err := readProxyHeader(br)
		_ = c.SetReadDeadline(time.Time{})
		if err != nil {
			// A trusted hop that sent a broken header (or nothing at all) is
			// a misconfiguration; the stream cannot be read as TLS either way.
			c.Close()
			continue
		}
		return &proxiedConn{Conn: c, r: br, remote: src}, nil
	}
}

// proxiedConn reads through the buffer the header was parsed from and reports
// the address the header named.
type proxiedConn struct {
	net.Conn
	r      *bufio.Reader
	remote *net.TCPAddr // nil: no header, keep the peer's own
}

func (c *proxiedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

func (c *proxiedConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

var proxyV2Sig = []byte("\r\n\r\n\x00\r\nQUIT\n")

// readProxyHeader consumes a PROXY header if one starts the stream. It returns
// (nil, nil) when there is none, and a nil address for a header that names no
// client (LOCAL / UNKNOWN: a proxy health check).
func readProxyHeader(br *bufio.Reader) (*net.TCPAddr, error) {
	first, err := br.Peek(1)
	if err != nil {
		return nil, err
	}
	switch first[0] {
	case 'P':
		return readProxyV1(br)
	case '\r':
		return readProxyV2(br)
	}
	return nil, nil
}

func readProxyV1(br *bufio.Reader) (*net.TCPAddr, error) {
	head, err := br.Peek(6)
	if err != nil || string(head) != "PROXY " {
		return nil, nil // not a header; let TLS reject it
	}
	var line []byte
	for len(line) < 108 { // the spec's maximum, CRLF included
		b, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		line = append(line, b)
		if b == '\n' {
			break
		}
	}
	f := strings.Fields(strings.TrimSpace(string(line)))
	if len(f) >= 2 && f[1] == "UNKNOWN" {
		return nil, nil
	}
	if len(f) != 6 || (f[1] != "TCP4" && f[1] != "TCP6") {
		return nil, errors.New("proxy: malformed v1 header")
	}
	ip := net.ParseIP(f[2])
	port, perr := strconv.Atoi(f[4])
	if ip == nil || perr != nil || port < 0 || port > 65535 {
		return nil, errors.New("proxy: malformed v1 address")
	}
	return &net.TCPAddr{IP: ip, Port: port}, nil
}

func readProxyV2(br *bufio.Reader) (*net.TCPAddr, error) {
	head, err := br.Peek(16)
	if err != nil || !bytes.Equal(head[:12], proxyV2Sig) {
		return nil, nil
	}
	n := int(binary.BigEndian.Uint16(head[14:16]))
	buf := make([]byte, 16+n)
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}
	verCmd, fam, body := buf[12], buf[13], buf[16:]
	if verCmd>>4 != 2 {
		return nil, errors.New("proxy: unknown v2 version")
	}
	if verCmd&0x0F == 0 { // LOCAL
		return nil, nil
	}
	switch fam >> 4 {
	case 1: // AF_INET: src(4) dst(4) srcport dstport
		if len(body) < 12 {
			return nil, errors.New("proxy: short v2 inet header")
		}
		return &net.TCPAddr{IP: net.IP(append([]byte(nil), body[0:4]...)), Port: int(binary.BigEndian.Uint16(body[8:10]))}, nil
	case 2: // AF_INET6
		if len(body) < 36 {
			return nil, errors.New("proxy: short v2 inet6 header")
		}
		return &net.TCPAddr{IP: net.IP(append([]byte(nil), body[0:16]...)), Port: int(binary.BigEndian.Uint16(body[32:34]))}, nil
	}
	return nil, nil // AF_UNSPEC / unix: names no TCP client
}
