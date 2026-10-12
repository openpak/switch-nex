package main

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/openpak/switch-nex/rmc"
)

// TestRoomJoinRecapsTheHost covers the explicit-join path (a private room or a
// listed room, 0x6D.0x27 on a gathering id) for the titles the 2026-10-10
// sweep caught leaving exactly 10 s after a clean join: Mario Golf's private
// rooms and 3D World's listed rooms. Their joiners were pushed two events only
// (3001 to the host, 122000 to the joiner) and never heard that the host was
// there, so they had no mesh to build. A recapping join pushes three: 3001
// about the joiner to each participant, the joiner included, then one 3001 per
// participant already there, to the joiner. 3D World also counted nobody in
// the created session (count=0 in the join echo) until its creation was
// normalised. The first new NEX profiles use the same join contract; this
// checks their initial profiles without claiming the games have been run.
func TestRoomJoinRecapsTheHost(t *testing.T) {
	for _, name := range []string{
		"mario-golf-super-rush", "super-mario-3d-world-bowsers-fury",
		"castle-crashers-remastered", "nobody-saves-the-world", "pico-park",
		"monopoly",
	} {
		t.Run(name, func(t *testing.T) {
			authAddr, secureAddr, _ := start(t, name)
			key := titleAccessKey(t, name)
			host := secureSession(t, authAddr, secureAddr, key, "host")
			joiner := secureSession(t, authAddr, secureAddr, key, "joiner")

			resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(4, 3, 77))
			if resp.IsError {
				t.Fatalf("create: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
			}
			gid, err := rmc.NewReader(resp.Body[5:]).U32() // gid leads the Gathering structure
			if err != nil || gid == 0 {
				t.Fatalf("create answer gid=%d err=%v", gid, err)
			}

			param := rmc.NewWriter()
			param.StructOf(0, func(w *rmc.Writer) { w.U32(gid) })
			resp = joiner.call(ProtoMatchmakeExt, 0x27, param.Bytes())
			if resp.IsError {
				t.Fatalf("join: %#x", resp.ErrorCode)
			}
			count, sessionKey, err := readJoinedSession(resp.Body)
			if err != nil {
				t.Fatalf("join answer: %v", err)
			}
			if count != 1 {
				t.Fatalf("join echoed count=%d, want the pre-join 1 (the host)", count)
			}
			if len(sessionKey) == 0 {
				t.Fatal("join answer carried no session key")
			}

			// 3D World holds every Participate back 100 ms; collect after it.
			time.Sleep(250 * time.Millisecond)
			joiner.call(ProtoUtility, 0x01, nil)
			host.call(ProtoUtility, 0x01, nil)

			heard := map[uint64]bool{} // whom the joiner was told is in the room
			for _, p := range joiner.pushes {
				typ, about := pushTypeAbout(t, p)
				if typ == 3001 {
					heard[about] = true
				}
				if typ == 122000 {
					t.Errorf("joiner got AddedToGathering (122000): the non-recap pairing")
				}
			}
			if !heard[host.pid] {
				t.Fatalf("joiner was never told the host is in the room (pushes=%d): the 10-s-then-leave join", len(joiner.pushes))
			}
			if !heard[joiner.pid] {
				t.Fatal("joiner was not told about its own Participate")
			}
			hostHeardJoiner := false
			for _, p := range host.pushes {
				if typ, about := pushTypeAbout(t, p); typ == 3001 && about == joiner.pid {
					hostHeardJoiner = true
				}
			}
			if !hostHeardJoiner {
				t.Fatal("host was never told the joiner arrived")
			}
		})
	}
}

// pushTypeAbout reads a ProcessNotificationEvent push: its type and Param2,
// the principal the event is about.
func pushTypeAbout(t *testing.T, p *rmc.RMCMessage) (uint32, uint64) {
	t.Helper()
	if p.Protocol != ProtoNotification || len(p.Body) < 5+8+4+8+8 {
		t.Fatalf("push is not a notification event (proto=%#x, %d bytes)", p.Protocol, len(p.Body))
	}
	return binary.LittleEndian.Uint32(p.Body[5+8:]), binary.LittleEndian.Uint64(p.Body[5+8+4+8:])
}
