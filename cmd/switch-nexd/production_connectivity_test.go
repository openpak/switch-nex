package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// Opt-in operator probe, compiled into a separate test executable and run only
// on the deployment host. Synthetic names do not carry real account credentials.
func TestProductionConnectivityCanary(t *testing.T) {
	if os.Getenv("OPENPAK_LIVE_NEX_PROBE") != "1" {
		t.Skip("private deployment probe disabled")
	}
	auth, secure := os.Getenv("OPENPAK_NEX_PROBE_AUTH"), os.Getenv("OPENPAK_NEX_PROBE_SECURE")
	for _, addr := range []string{auth, secure} {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			t.Fatal("probe accepts only deployment-host loopback listeners")
		}
	}
	titleName := os.Getenv("OPENPAK_NEX_PROBE_TITLE")
	if titleName == "" {
		titleName = mk8d
	}
	key := titleAccessKey(t, titleName)
	a, b := dial(t, auth, key), dial(t, auth, key)
	a.login(randomName("connectivity-probe-a"), false)
	b.login(randomName("connectivity-probe-b"), false)
	for _, owner := range []*client{a, b} {
		if !strings.HasPrefix(owner.stationURL, "prudps:/") {
			t.Fatal("ticket-required endpoint advertised a ticketless transport scheme")
		}
	}
	if a.pid == b.pid {
		t.Fatal("independent synthetic logins share one principal")
	}
	if dial(t, secure, key).connectTicketless() {
		t.Fatal("live secure endpoint still admits IP-based recall")
	}
	// Spend the tickets in reverse login order: IP and arrival order are irrelevant.
	for i, owner := range []*client{b, a} {
		peer := dial(t, secure, key)
		peer.pid, peer.ticket, peer.sessionKey = owner.pid, owner.ticket, owner.sessionKey
		peer.handshake(peer.secureConnectPayload(uint32(i + 1)))
		w := rmc.NewWriter()
		w.List(2)
		w.String(fmt.Sprintf("prudp:/address=192.168.254.%d;port=50000;CID=%d;sid=1;type=1", 10+i, i+1))
		w.String(fmt.Sprintf("prudp:/address=198.51.100.8;port=%d;CID=%d;sid=1;type=3", 51000+i, i+1))
		response := peer.call(0x0B, 4, w.Bytes())
		if response.IsError {
			t.Fatal("live shared endpoint registration refused")
		}
		reader := rmc.NewReader(response.Body)
		result, err := reader.U32()
		if err != nil || result != rmc.ResultSuccess {
			t.Fatal("invalid registration result")
		}
		if _, err := reader.U32(); err != nil {
			t.Fatal("connection ID missing")
		}
		raw, err := reader.String()
		if err != nil {
			t.Fatal("public candidate missing")
		}
		station := switchnex.Parse(raw)
		if station.GetInt("PID") != owner.pid || station.GetInt("port") != uint64(51000+i) {
			t.Fatal("principal or translated port changed")
		}
		urls := peer.call(0x0B, 3, nil)
		if urls.IsError {
			t.Fatal("registered candidate lookup failed")
		}
		r := rmc.NewReader(urls.Body)
		n, err := r.ListHeader()
		if err != nil || n != 2 {
			t.Fatal("flat candidate list invalid")
		}
		for range n {
			raw, err := r.String()
			if err != nil || switchnex.Parse(raw).GetInt("PID") != owner.pid {
				t.Fatal("foreign candidate supplied")
			}
		}
	}
	t.Log("live ticket proof, reversed simultaneous login order, shared candidate registration and flat replies passed")
}
