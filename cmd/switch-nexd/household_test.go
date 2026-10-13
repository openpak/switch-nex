package main

import (
	"fmt"
	"net"
	"testing"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/protocols"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// The rendezvous TLS connection and the game's UDP socket are independent.
// Consoles sharing an uplink must retain their own LAN addresses/game ports,
// even when they bind the same game port. Exercise every title's station profile.
func TestHouseholdStationsAcrossTitles(t *testing.T) {
	for _, spec := range titles {
		t.Run(spec.name, func(t *testing.T) {
			for _, method := range []uint32{1, 4} {
				for _, reportedPublic := range []bool{false, true} {
					env := protocols.NewEnv(spec.name, "192.0.2.1", spec.base+1)
					spec.env(env)
					for i := 0; i < 2; i++ {
						c := &prudplite.Connection{PID: uint64(100 + i), ID: uint32(10 + i), Host: "198.51.100.1",
							Remote: &net.TCPAddr{IP: net.ParseIP("198.51.100.1"), Port: 41000 + i}}
						lan := fmt.Sprintf("192.168.1.%d", 20+i)
						w := rmc.NewWriter()
						if reportedPublic {
							w.List(2)
						} else {
							w.List(1)
						}
						w.String(fmt.Sprintf("prudp:/address=%s;port=50000;sid=1;type=1", lan))
						publicPort := 50000
						if reportedPublic {
							publicPort = 52000 + i
							w.String(fmt.Sprintf("prudp:/address=198.51.100.1;port=%d;sid=1;type=3", publicPort))
						}
						response := protocols.Dispatch(env, c, &rmc.RMCMessage{IsRequest: true, Protocol: protocols.ProtoSecureConnection, MethodID: method, CallID: 1, Body: w.Bytes()})
						m, err := rmc.Decode(response)
						if err != nil || m.IsError {
							t.Fatalf("register: %v / %+v", err, m)
						}
						stations := c.Stations()
						if len(stations) != 2 {
							t.Fatalf("stations=%v", stations)
						}
						local, public := switchnex.Parse(stations[0]), switchnex.Parse(stations[1])
						if local.Get("address") != lan || local.GetInt("port") != 50000 {
							t.Fatalf("LAN endpoint changed: %s", stations[0])
						}
						if public.GetInt("port") != uint64(publicPort) {
							t.Fatalf("method=%d reportedPublic=%t: game port replaced by TLS source port: %s", method, reportedPublic, stations[1])
						}
						for _, station := range []*switchnex.URL{local, public} {
							if station.GetInt("PID") != c.PID || station.GetInt("RVCID") != uint64(c.ID) {
								t.Fatalf("household identity changed: %s", station.String())
							}
						}
					}
				}
			}
		})
	}
}
