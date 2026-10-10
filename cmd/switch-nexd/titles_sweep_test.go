package main

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// TestMarioPartyGetsItsSixSettings: MPS looks up keys 0-5 of index 0. An
// empty map sent it back to its menu (sweep 2026-10-10); a missing key aborts it.
func TestMarioPartyGetsItsSixSettings(t *testing.T) {
	const name = "mario-party-superstars"
	t.Setenv("MPS_INTEGER_SETTINGS", "")
	authAddr, secureAddr, _ := start(t, name)
	s := secureSession(t, authAddr, secureAddr, titleAccessKey(t, name), "player")
	resp := s.call(ProtoUtility, 0x07, u32le(0))
	if resp.IsError {
		t.Fatalf("GetIntegerSettings: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	r := rmc.NewReader(resp.Body)
	n, _ := r.ListHeader()
	got := map[uint16]uint32{}
	for range n {
		k, _ := r.U16()
		v, _ := r.U32()
		got[k] = v
	}
	want := map[uint16]uint32{0: 1000, 1: 1000, 2: 500, 3: 250, 4: 20, 5: 1}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("key %d = %d, want %d (map %v)", k, got[k], v, got)
		}
	}
}

// TestMakerTwoBootsThroughGetUsers: SMM2 now gets a prudps station (its
// CONNECT carries the ticket) and its boot GetUsers, framed as the console
// sends it, parses.
func TestMakerTwoBootsThroughGetUsers(t *testing.T) {
	const name = "super-mario-maker-2"
	authAddr, secureAddr, _ := start(t, name)
	key := titleAccessKey(t, name)
	c := dial(t, authAddr, key)
	c.login(randomName("maker"), true)
	if !strings.HasPrefix(c.stationURL, "prudps:/") {
		t.Fatalf("station %q, want prudps", c.stationURL)
	}
	s := dial(t, secureAddr, key)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(1))

	body := []byte{0, 16, 0, 0, 0, 1, 0, 0, 0}
	body = binary.LittleEndian.AppendUint64(body, s.pid)
	body = binary.LittleEndian.AppendUint32(body, 0xE284)
	resp := s.call(ProtoDataStore, 0x30, body)
	if resp.IsError {
		t.Fatalf("GetUsers: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	r := rmc.NewReader(resp.Body)
	users, _ := r.ListHeader()
	results, _ := r.ListHeader()
	code, _ := r.U32()
	if users != 0 || results != 1 || code != 0x80690004 {
		t.Fatalf("GetUsers for a new maker: users=%d results=%d code=%#x", users, results, code)
	}
}
