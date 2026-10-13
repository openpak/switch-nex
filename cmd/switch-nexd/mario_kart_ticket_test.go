package main

import (
	"strings"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// The client chooses SecureStream from the login URL, before CONNECT. A
// synthetic ticket alone misses a server that advertises the wrong stream.
func TestMarioKartLoginRequestsSecureStream(t *testing.T) {
	authAddr, secureAddr, _ := start(t, mk8d)
	for _, structured := range []bool{false, true} {
		auth := dial(t, authAddr, "09c1c475")
		auth.login(randomName("kart-secure-stream"), structured)
		if !strings.HasPrefix(auth.stationURL, "prudps:/") {
			t.Fatalf("login station %q does not select SecureStream", auth.stationURL)
		}
		secure := dial(t, secureAddr, "09c1c475")
		secure.pid, secure.ticket, secure.sessionKey = auth.pid, auth.ticket, auth.sessionKey
		secure.handshake(secure.secureConnectPayload(1))
		response := secure.call(ProtoUtility, 1, nil)
		if response.IsError {
			t.Fatalf("ticketed connection refused: %#x", response.ErrorCode)
		}
		pid, err := rmc.NewReader(response.Body).U64()
		if err != nil || pid != auth.pid {
			t.Fatal("ticketed connection acquired a different login identity")
		}
	}
}
