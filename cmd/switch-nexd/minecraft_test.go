package main

import (
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

func TestMinecraftResolvesAFriendsWorldBeforeJoining(t *testing.T) {
	const name = "minecraft"
	authAddr, secureAddr, _ := start(t, name)
	key := titleAccessKey(t, name)
	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")

	// Minecraft creates with 0x6D/0x26, then resolves the world advertised
	// in friend presence with 0x15/0x15 (observed on 2026-10-12).
	holder := rmc.NewReader(sessionHolderBytes(8, 0, 0))
	holder.String()
	holder.U32()
	session, err := holder.Buffer()
	if err != nil {
		t.Fatal(err)
	}
	param := rmc.NewWriter()
	param.StructOf(0, func(w *rmc.Writer) { w.Raw(session) })
	resp := host.call(ProtoMatchmakeExt, 0x26, param.Bytes())
	if resp.IsError {
		t.Fatalf("create: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	gid := le32(resp.Body[5:])
	resp = joiner.call(ProtoMatchMaking, 0x15, u32le(gid))
	if resp.IsError {
		t.Fatalf("find existing world: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	r := rmc.NewReader(resp.Body)
	found, err := r.Bool()
	if err != nil || !found {
		t.Fatalf("lookup result: %v, %v", found, err)
	}
	class, err := r.String()
	if err != nil || class != "MatchmakeSession" {
		t.Fatalf("holder class: %q, %v", class, err)
	}
	size, err := r.U32()
	if err != nil || int(size) != r.Remaining() {
		t.Fatalf("holder size: %d, remaining=%d, %v", size, r.Remaining(), err)
	}
	session, err = r.Buffer()
	if err != nil || len(session) < 9 || le32(session[5:]) != gid {
		t.Fatalf("lookup did not return the hosted gathering: %v", err)
	}
	count, sessionKey, err := readJoinedSession(session)
	if err != nil || count != 1 || len(sessionKey) != 32 {
		t.Fatalf("lookup session: count=%d key=%dB err=%v", count, len(sessionKey), err)
	}
	resp = joiner.call(ProtoMatchMaking, 0x15, u32le(0))
	if !resp.IsError || resp.ErrorCode != rmc.ResultSessionVoid {
		t.Fatal("a missing world must still answer SessionVoid")
	}
}
