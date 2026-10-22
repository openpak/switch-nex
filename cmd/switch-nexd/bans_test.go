package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/openpak/switch-nex/rmc"
)

// A banned account is refused with RendezVous::AccountDisabled, and even the
// unenforced name fallback must not let it back in.
func TestBannedLoginIsRefusedEvenUnenforced(t *testing.T) {
	baas := newFakeBaas(t, 0x1122334455)
	baas.status = http.StatusForbidden // nx-baas: {"error":"account_banned"}
	ids := newIdentity(baas.URL, "k")

	authAddr, _, _ := startWith(t, "mario-kart-8-deluxe", ids)
	key := titleAccessKey(t, "mario-kart-8-deluxe")

	*requireIdentity = false
	t.Cleanup(func() { *requireIdentity = true }) // the production default
	c := dial(t, authAddr, key)
	c.handshake(nil)
	w := rmc.NewWriter()
	w.String("claimed-name")
	w.Raw(authInfoHolder(jwt(t, map[string]any{"sub": "banned", "openpak": "nx2.bound"})))
	resp := c.call(0x0A, 0x02, w.Bytes())
	if !resp.IsError {
		t.Fatal("a banned account logged in on the name fallback")
	}
	if resp.ErrorCode != rmc.ResultAccountDisabled {
		t.Fatalf("refusal = %#x, want AccountDisabled %#x", resp.ErrorCode, rmc.ResultAccountDisabled)
	}
}

// A player banned mid-session loses the secure connection on the next sweep;
// an nx-baas outage does not drop anyone.
func TestSweepDropsBannedSessions(t *testing.T) {
	const accountPID = 0x1122334455
	baas := newFakeBaas(t, accountPID)
	ids := newIdentity(baas.URL, "k")

	authAddr, secureAddr, svc := startWith(t, "mario-kart-8-deluxe", ids)
	key := titleAccessKey(t, "mario-kart-8-deluxe")

	c := dial(t, authAddr, key)
	c.handshake(nil)
	w := rmc.NewWriter()
	w.String("ignored-name")
	w.Raw(authInfoHolder(jwt(t, map[string]any{"sub": "user-1", "openpak": "nx2.bound"})))
	resp := c.call(0x0A, 0x02, w.Bytes())
	if resp.IsError {
		t.Fatalf("login refused: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	c.readLoginExResult(resp.Body)
	c.openTicket()

	s := dial(t, secureAddr, key)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(1))

	connected := func() int {
		n := 0
		for _, conn := range svc.secure.Connections() {
			if conn.PID == accountPID {
				n++
			}
		}
		return n
	}
	if connected() != 1 {
		t.Fatalf("secure connections for the player = %d, want 1", connected())
	}

	baas.status = http.StatusBadGateway // outage: keep the session
	ids.sweepBans(svc.secure)
	if connected() != 1 {
		t.Fatal("an nx-baas outage dropped a session")
	}

	baas.status = http.StatusForbidden
	ids.sweepBans(svc.secure)
	deadline := time.Now().Add(3 * time.Second)
	for connected() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("banned player's secure connection is still open")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
