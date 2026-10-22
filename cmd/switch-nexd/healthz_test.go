package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// healthz must say "down" until both of the title's listeners are serving, and
// "ok" in the platforms/switch/games/switch-nex shape once they are.
func TestHealthzFollowsTheListeners(t *testing.T) {
	var spec title
	for _, tt := range titles {
		if tt.name == "mario-kart-8-deluxe" {
			spec = tt
		}
	}
	svc := newService(spec, "127.0.0.1", newRecall(time.Hour, ""), newIdentity("", ""))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	addr := ln.Addr().String()
	serveHealth(addr, svc, nil)

	get := func() (int, string) {
		t.Helper()
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := get(); code != http.StatusServiceUnavailable ||
		body != "down mario-kart-8-deluxe auth=22150/tls secure=22151/tls not listening: auth,secure\n" {
		t.Fatalf("before serve: %d %q", code, body)
	}

	authLn, _ := net.Listen("tcp", "127.0.0.1:0")
	secureLn, _ := net.Listen("tcp", "127.0.0.1:0")
	svc.serve(authLn, secureLn)
	t.Cleanup(func() { svc.auth.Close(); svc.secure.Close() })

	if code, body := get(); code != http.StatusOK ||
		body != "ok mario-kart-8-deluxe auth=22150/tls secure=22151/tls\n" {
		t.Fatalf("after serve: %d %q", code, body)
	}
}
