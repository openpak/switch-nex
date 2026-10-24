package main

import (
	"testing"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// registeredClient signs a console in and registers one LAN station.
func registeredClient(t *testing.T, authAddr, secureAddr string, key string, cid uint32, lan string) *client {
	t.Helper()
	c := dial(t, authAddr, key)
	c.login(randomName("p2p"), false)
	s := dial(t, secureAddr, key)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(cid))
	w := rmc.NewWriter()
	w.List(1)
	w.String(lan)
	if resp := s.call(ProtoSecureConnection, 0x01, w.Bytes()); resp.IsError {
		t.Fatalf("register: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	return s
}

// readURLsReply decodes RequestURLs' (bool, list<StationURL>) reply.
func readURLsReply(t *testing.T, resp *rmc.RMCMessage) (bool, []string) {
	t.Helper()
	if resp.IsError {
		t.Fatalf("RequestURLs: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	r := rmc.NewReader(resp.Body)
	ok, err := r.Bool()
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.ListHeader()
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for range n {
		s, err := r.String()
		if err != nil {
			t.Fatal(err)
		}
		urls = append(urls, s)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d trailing bytes", r.Remaining())
	}
	return ok, urls
}

// TestRequestURLsAnswersWithTheTargetsStations: NEX P2P asks for a peer's
// stations by (cid, pid). Answering with the caller's own sends it to itself.
func TestRequestURLsAnswersWithTheTargetsStations(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "nobody-saves-the-world")
	key := titleAccessKey(t, "nobody-saves-the-world")
	host := registeredClient(t, authAddr, secureAddr, key, 1, "prudp:/address=192.168.1.20;port=50000;sid=15;type=2")
	join := registeredClient(t, authAddr, secureAddr, key, 2, "prudp:/address=192.168.7.30;port=50001;sid=15;type=2")

	hostID := switchnex.Parse(stationsOf(t, host)[0]).GetInt("RVCID")
	req := rmc.NewWriter()
	req.U32(uint32(hostID))
	req.U64(host.pid)
	ok, urls := readURLsReply(t, join.call(ProtoSecureConnection, 0x03, req.Bytes()))
	if !ok || len(urls) == 0 {
		t.Fatalf("found=%v urls=%v", ok, urls)
	}
	for _, raw := range urls {
		if got := switchnex.Parse(raw).GetInt("PID"); got != host.pid {
			t.Errorf("station %q has PID %d, want the host's %d", raw, got, host.pid)
		}
	}

	// An unknown principal is a clean "not found", not an error.
	miss := rmc.NewWriter()
	miss.U32(0)
	miss.U64(host.pid + 999999)
	if ok, urls := readURLsReply(t, join.call(ProtoSecureConnection, 0x03, miss.Bytes())); ok || len(urls) != 0 {
		t.Fatalf("unknown pid: found=%v urls=%v", ok, urls)
	}
}

// TestUpdateURLsStoresTheStations: UpdateURLs used to be acknowledged and
// dropped, so peers kept the first registration's endpoint.
func TestUpdateURLsStoresTheStations(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "nobody-saves-the-world")
	key := titleAccessKey(t, "nobody-saves-the-world")
	s := registeredClient(t, authAddr, secureAddr, key, 1, "prudp:/address=192.168.1.20;port=50000;sid=15;type=2")

	w := rmc.NewWriter()
	w.List(1)
	w.String("prudp:/address=192.168.1.20;port=50123;sid=15;type=2")
	if resp := s.call(ProtoSecureConnection, 0x06, w.Bytes()); resp.IsError {
		t.Fatalf("UpdateURLs: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	stations := stationsOf(t, s)
	if len(stations) == 0 || switchnex.Parse(stations[0]).Get("port") != "50123" {
		t.Fatalf("stations after UpdateURLs = %v", stations)
	}
	for _, raw := range stations {
		u := switchnex.Parse(raw)
		if u.GetInt("PID") != s.pid || u.Get("RVCID") == "" {
			t.Errorf("updated station %q lost its identity", raw)
		}
	}
}

// stationsOf asks the server for the caller's own stations (RequestURLs on itself).
func stationsOf(t *testing.T, s *client) []string {
	t.Helper()
	req := rmc.NewWriter()
	req.U32(0)
	req.U64(s.pid)
	_, urls := readURLsReply(t, s.call(ProtoSecureConnection, 0x03, req.Bytes()))
	return urls
}
