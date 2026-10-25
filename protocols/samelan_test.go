package protocols

import (
	"testing"

	"github.com/openpak/switch-nex/prudplite"
)

func TestSameLAN(t *testing.T) {
	at := func(host, lan string) *prudplite.Connection {
		c := &prudplite.Connection{Host: host}
		c.SetStations([]string{"prudp:/address=" + lan + ";port=1", "prudp:/address=" + host + ";port=1"})
		return c
	}
	cases := []struct {
		name string
		a, b *prudplite.Connection
		want bool
	}{
		{"one household, one subnet", at("37.228.205.45", "10.87.0.13"), at("37.228.205.45", "10.87.0.7"), true},
		{"one household, two subnets", at("37.228.205.45", "10.87.0.13"), at("37.228.205.45", "192.168.103.2"), false},
		{"two households, same private range", at("37.228.205.45", "10.87.0.13"), at("198.51.100.9", "10.87.0.7"), false},
		{"carrier-grade NAT is not a LAN", at("109.255.146.8", "100.72.232.242"), at("109.255.146.8", "100.72.232.250"), false},
		{"IPv6 ULA, one /64", at("2001:db8::1", "fd00:1:2:3::10"), at("2001:db8::1", "fd00:1:2:3::20"), true},
		{"IPv6 ULA, two /64s", at("2001:db8::1", "fd00:1:2:3::10"), at("2001:db8::1", "fd00:1:2:4::20"), false},
		{"unknown host", at("", "10.87.0.13"), at("", "10.87.0.7"), false},
	}
	for _, tc := range cases {
		if got := sameLAN(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: sameLAN=%v want %v", tc.name, got, tc.want)
		}
	}
}
