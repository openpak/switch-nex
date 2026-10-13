package main

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/openpak/switch-nex/rmc"
)

// Exercise the actual title profile over two secure connections: a second
// search arrives while the first host is still checking NAT, then that host
// opens participation. The second client must join it without searching again.
func TestMarioPartyConcurrentSearchWaitsForHost(t *testing.T) {
	auth, secure, _ := start(t, "mario-party-superstars")
	key := titleAccessKey(t, "mario-party-superstars")
	host := secureSession(t, auth, secure, key, "host")
	joiner := secureSession(t, auth, secure, key, "joiner")
	r := rmc.NewReader(sessionHolderBytes(4, 1, 0))
	_, _ = r.String()
	_, _ = r.U32()
	body, err := r.Buffer()
	if err != nil {
		t.Fatal(err)
	}
	child := 5 + int(binary.LittleEndian.Uint32(body[1:5]))
	// Child header, game mode, attribute count and the single attribute.
	body[child+5+4+4+4] = 0 // participation starts closed
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.Raw(body)
		w.List(0) // search criteria
	})
	w.List(0) // stations
	first := host.call(ProtoMatchmakeExt, 0x28, w.Bytes())
	if first.IsError {
		t.Fatalf("first search: %#x", first.ErrorCode)
	}
	count, _, err := readJoinedSession(first.Body)
	if err != nil || count != 1 {
		t.Fatalf("created room must count its host: count=%d err=%v", count, err)
	}
	gid := binary.LittleEndian.Uint32(first.Body[5:9])
	done := make(chan *rmc.RMCMessage, 1)
	go func() { done <- joiner.call(ProtoMatchmakeExt, 0x28, w.Bytes()) }()
	select {
	case <-done:
		t.Fatal("second search returned before the host opened its room")
	case <-time.After(200 * time.Millisecond):
	}
	opened := host.call(ProtoMatchmakeExt, 0x02, u32le(gid))
	if opened.IsError {
		t.Fatalf("open: %#x", opened.ErrorCode)
	}
	select {
	case second := <-done:
		if second.IsError {
			t.Fatalf("second search: %#x", second.ErrorCode)
		}
		count, _, err := readJoinedSession(second.Body)
		if err != nil || count != 2 {
			t.Fatalf("joined room must count both players: count=%d err=%v", count, err)
		}
		if got := binary.LittleEndian.Uint32(second.Body[5:9]); got != gid {
			t.Fatalf("players split into rooms %d and %d", gid, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second search did not join the opened room")
	}
}
