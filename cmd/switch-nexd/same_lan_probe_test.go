package main

import (
	"strconv"
	"testing"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// Two players behind one router, the shape every household trace on record
// has (ARMS, Mario Party Superstars, Clubhouse Games 51; 2026-10-24/25): each
// console registers its LAN station with the placeholder port 1, then
// ReplaceURLs it with its LAN address and the port its NAT check saw on the
// PUBLIC side, then names its real LAN socket and its public mapping as
// "self" in two back-to-back RequestProbeInitiationExt calls.
const (
	householdIP = "203.0.113.7"
	protoNAT    = 0x03
)

type household struct {
	lan       string // LAN address
	localPort int    // the socket Pia probes from (ephemeral)
	mapped    int    // the router's public port for that socket
}

func (h household) station(pid uint64, addr string, port int, extra string) string {
	return "prudp:/address=" + addr + ";port=" + strconv.Itoa(port) + ";Pl=2;natf=2;natm=1;pmp=0;sid=15;upnp=0" + extra + ";PID=" + strconv.FormatUint(pid, 10)
}

// register runs Register then ReplaceURL the way the console does.
func (h household) register(t *testing.T, c *client) {
	t.Helper()
	w := rmc.NewWriter()
	w.List(1)
	w.String("prudp:/address=" + h.lan + ";port=1;Pl=2;natf=0;natm=0;pmp=0;sid=15;type=2;upnp=0")
	if resp := c.call(ProtoSecureConnection, 0x01, w.Bytes()); resp.IsError {
		t.Fatalf("register: %#x", resp.ErrorCode)
	}
	w = rmc.NewWriter()
	w.String("prudp:/address=" + h.lan + ";port=1;Pl=2;natf=0;natm=0;pmp=0;sid=15;type=2;upnp=0")
	w.String("prudp:/address=" + h.lan + ";port=" + strconv.Itoa(h.mapped) + ";Pl=2;natf=2;natm=1;pmp=0;sid=15;upnp=0")
	if resp := c.call(ProtoSecureConnection, 0x07, w.Bytes()); resp.IsError {
		t.Fatalf("replaceURL: %#x", resp.ErrorCode)
	}
}

// probe sends one RequestProbeInitiationExt naming target and self.
func probe(t *testing.T, c *client, target, self string) {
	t.Helper()
	w := rmc.NewWriter()
	w.List(1)
	w.String(target)
	w.String(self)
	if resp := c.call(protoNAT, 0x03, w.Bytes()); resp.IsError {
		t.Fatalf("probe: %#x", resp.ErrorCode)
	}
}

// ownStations reads the caller's stored stations back (RequestURLs on self).
func ownStations(t *testing.T, c *client) map[string]*switchnex.URL {
	t.Helper()
	resp := c.call(ProtoSecureConnection, 0x03, make([]byte, 12))
	if resp.IsError {
		t.Fatalf("requestURLs: %#x", resp.ErrorCode)
	}
	return readStationMap(t, resp.Body)
}

func readStationMap(t *testing.T, body []byte) map[string]*switchnex.URL {
	t.Helper()
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*switchnex.URL{}
	for range n {
		raw, err := r.String()
		if err != nil {
			t.Fatal(err)
		}
		u := switchnex.Parse(raw)
		out[u.Get("address")] = u
	}
	return out
}

// probesReceived drains the pushes a client has seen and returns the stations
// carried by InitiateProbe (0x03/0x02) pushes.
func probesReceived(t *testing.T, c *client) []*switchnex.URL {
	t.Helper()
	c.call(ProtoSecureConnection, 0x05, nil) // any round trip collects queued pushes
	var out []*switchnex.URL
	for _, p := range c.pushes {
		if p.Protocol != protoNAT || p.MethodID != 0x02 {
			continue
		}
		raw, err := rmc.NewReader(p.Body).String()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, switchnex.Parse(raw))
	}
	c.pushes = nil
	return out
}

func secureSessionFrom(t *testing.T, authAddr, secureAddr, key, who, from string) *client {
	t.Helper()
	c := dialAs(t, authAddr, key, from)
	c.login(randomName(who), false)
	s := dialAs(t, secureAddr, key, from)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(1))
	return s
}

// Both the legacy (type 3) and the 5.19 (type 11, Pa) station shapes, and the
// two titles the household drops were seen in.
var sameLANTitles = []string{"clubhouse-games-51", "mario-party-superstars", "arms", "mario-kart-8-deluxe"}

func TestSameLANStationsCarryLiveCandidates(t *testing.T) {
	for _, name := range sameLANTitles {
		t.Run(name, func(t *testing.T) {
			authAddr, secureAddr, _ := start(t, name)
			key := titleAccessKey(t, name)
			sw := household{lan: "10.87.0.13", localPort: 59705, mapped: 12013}
			c := secureSessionFrom(t, authAddr, secureAddr, key, "switch", householdIP)
			sw.register(t, c)

			// After ReplaceURL the public candidate carries the mapped port,
			// not Register's placeholder port 1.
			got := ownStations(t, c)
			pub := got[householdIP]
			if pub == nil || pub.GetInt("port") != uint64(sw.mapped) || pub.Get("natm") != "1" || pub.Get("natf") != "2" {
				t.Fatalf("public station after ReplaceURL: %v", got[householdIP])
			}

			// Its own probe names the real LAN socket: that becomes the LAN
			// candidate, and the public one is confirmed.
			probe(t, c, sw.station(c.pid+1, "10.87.0.7", 1, ""), sw.station(c.pid, sw.lan, sw.localPort, ";type=0"))
			got = ownStations(t, c)
			if lan := got[sw.lan]; lan == nil || lan.GetInt("port") != uint64(sw.localPort) {
				t.Fatalf("LAN station not corrected from the probe: %v", got[sw.lan])
			}
			if got[sw.lan].GetInt("PID") != c.pid || got[sw.lan].Get("natm") != "1" {
				t.Fatalf("LAN station lost its identity: %s", got[sw.lan])
			}
			if got[householdIP].GetInt("port") != uint64(sw.mapped) {
				t.Fatalf("public station moved: %s", got[householdIP])
			}

			// A self station for an address the client never registered is
			// ignored.
			probe(t, c, sw.station(c.pid+1, "10.87.0.7", 1, ""), sw.station(c.pid, "10.99.0.1", 4000, ""))
			if got := ownStations(t, c); got["10.99.0.1"] != nil || len(got) != 2 {
				t.Fatalf("foreign self station stored: %v", got)
			}
		})
	}
}

func TestSameLANPairIsNotPushedThroughTheHairpin(t *testing.T) {
	for _, name := range sameLANTitles {
		t.Run(name, func(t *testing.T) {
			authAddr, secureAddr, _ := start(t, name)
			key := titleAccessKey(t, name)
			hostH := household{lan: "10.87.0.13", localPort: 59705, mapped: 12013}
			joinH := household{lan: "10.87.0.7", localPort: 58102, mapped: 12134}
			host := secureSessionFrom(t, authAddr, secureAddr, key, "host", householdIP)
			join := secureSessionFrom(t, authAddr, secureAddr, key, "joiner", householdIP)
			hostH.register(t, host)
			joinH.register(t, join)

			hostLAN := hostH.station(host.pid, hostH.lan, hostH.mapped, "")
			probe(t, join, hostLAN, joinH.station(join.pid, joinH.lan, joinH.localPort, ";type=0"))
			probe(t, join, hostLAN, joinH.station(join.pid, householdIP, joinH.mapped, ";type=3"))

			pushed := probesReceived(t, host)
			if len(pushed) != 1 || pushed[0].Get("address") != joinH.lan || pushed[0].GetInt("port") != uint64(joinH.localPort) {
				t.Fatalf("host was pushed %d probe(s), want only the joiner's LAN socket: %v", len(pushed), pushed)
			}

			// The host's own pair of calls: only its LAN socket reaches the
			// joiner, so both punches stay on the LAN.
			joinLAN := joinH.station(join.pid, joinH.lan, joinH.localPort, ";type=0")
			probe(t, host, joinLAN, hostH.station(host.pid, hostH.lan, hostH.localPort, ";type=0"))
			probe(t, host, joinLAN, hostH.station(host.pid, householdIP, hostH.mapped, ";type=3"))
			pushed = probesReceived(t, join)
			if len(pushed) != 1 || pushed[0].Get("address") != hostH.lan || pushed[0].GetInt("port") != uint64(hostH.localPort) {
				t.Fatalf("joiner was pushed %d probe(s), want only the host's LAN socket: %v", len(pushed), pushed)
			}
		})
	}
}

// One public address is not one LAN. A guest network or a second router puts
// the phone on another subnet; a carrier puts strangers behind one address.
// Those pairs keep the public push, which may be the only path between them.
func TestPublicProbeKeptWhenNotOneLAN(t *testing.T) {
	cases := []struct {
		name               string
		hostFrom, joinFrom string
		hostLAN, joinLAN   string
		lanFirst           bool
	}{
		{"other subnet behind one router", householdIP, householdIP, "10.87.0.13", "192.168.103.2", true},
		{"two households", householdIP, "198.51.100.9", "10.87.0.13", "10.87.0.7", false},
		{"carrier-grade NAT", householdIP, householdIP, "100.72.232.242", "100.72.232.250", true},
		{"public only, no LAN push first", householdIP, householdIP, "10.87.0.13", "10.87.0.7", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authAddr, secureAddr, _ := start(t, "clubhouse-games-51")
			key := titleAccessKey(t, "clubhouse-games-51")
			hostH := household{lan: tc.hostLAN, localPort: 59705, mapped: 12013}
			joinH := household{lan: tc.joinLAN, localPort: 58102, mapped: 12134}
			host := secureSessionFrom(t, authAddr, secureAddr, key, "host", tc.hostFrom)
			join := secureSessionFrom(t, authAddr, secureAddr, key, "joiner", tc.joinFrom)
			hostH.register(t, host)
			joinH.register(t, join)

			hostLAN := hostH.station(host.pid, hostH.lan, hostH.mapped, "")
			want := 1
			if tc.lanFirst {
				probe(t, join, hostLAN, joinH.station(join.pid, joinH.lan, joinH.localPort, ";type=0"))
				want = 2
			}
			probe(t, join, hostLAN, joinH.station(join.pid, tc.joinFrom, joinH.mapped, ";type=3"))
			if pushed := probesReceived(t, host); len(pushed) != want {
				t.Fatalf("host was pushed %d probe(s), want %d: %v", len(pushed), want, pushed)
			}
		})
	}
}
