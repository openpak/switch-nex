package main

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// smb35Message is the host's lobby-state DeliverMessage as SMB35 sent it on
// 2026-10-09 (a BinaryMessage "participants_cnt" to a gathering), with the
// gathering id and the body bytes after the recipient left for the test to fill.
const smb35MessageHead = "0e0042696e6172794d657373616765006c000000680000000000000000004b000000000000000000000000000000000000000000000000000000000000000100000011007061727469636970616e74735f636e74000100000010000000020000000000000000000000"

// smb35Message frames the captured message to gathering gid. The capture is
// cut after the recipient's pid; the gid and a BinaryMessage level carrying
// a 2-byte body complete it with the original lengths.
func smb35Message(t *testing.T, gid uint32) []byte {
	t.Helper()
	b, err := hex.DecodeString(smb35MessageHead)
	if err != nil {
		t.Fatal(err)
	}
	b = binary.LittleEndian.AppendUint32(b, gid)
	b = append(b, 0, 4, 0, 0, 0, 2, 0, 2, 0) // BinaryMessage level: qbuffer{2, 0}
	// Fix the holder and buffer lengths to what is actually here.
	inner := len(b) - 24
	binary.LittleEndian.PutUint32(b[16:], uint32(inner+4))
	binary.LittleEndian.PutUint32(b[20:], uint32(inner))
	return b
}

// cast sends an RMC request the server must not answer.
func (c *client) cast(protocol uint16, method uint32, body []byte) {
	c.t.Helper()
	c.callID++
	c.seq++
	c.send(&prudplite.Packet{
		Type:       prudplite.Data,
		Flags:      prudplite.FlagReliable | prudplite.FlagNeedsAck,
		SequenceID: c.seq,
		Payload:    rmc.EncodeRequest(protocol, method, c.callID, body),
	})
}

// TestDeliverMessageReachesTheGathering: SMB35's host announces its lobby
// state with DeliverMessage to its gathering. The server used to acknowledge
// it and forward nothing, so the joiner never learned the count (host 2/2,
// joiner 1/1, no match). It must reach every participant, the sender too,
// stamped with the sender, and the sender gets no RMC answer.
func TestDeliverMessageReachesTheGathering(t *testing.T) {
	const name = "super-mario-bros-35"
	authAddr, secureAddr, _ := start(t, name)
	key := titleAccessKey(t, name)
	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")
	outsider := secureSession(t, authAddr, secureAddr, key, "outsider")

	resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(35, 3, 77))
	if resp.IsError {
		t.Fatalf("create: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	gid, _ := rmc.NewReader(resp.Body[5:]).U32()
	param := rmc.NewWriter()
	param.StructOf(0, func(w *rmc.Writer) { w.U32(gid) })
	if resp := joiner.call(ProtoMatchmakeExt, 0x27, param.Bytes()); resp.IsError {
		t.Fatalf("join: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	host.pushes, joiner.pushes = nil, nil

	host.cast(0x1B, 0x01, smb35Message(t, gid))

	// The host first: its next answer comes after its cast was handled (one
	// connection is served in order), so by then the relay has gone out to
	// everyone and the joiner's poll cannot overtake it.
	for _, rc := range []struct {
		who string
		c   *client
	}{{"host", host}, {"joiner", joiner}} {
		who, c := rc.who, rc.c
		resp := c.call(ProtoUtility, 0x01, nil)
		if resp.MethodID != 0x01 || resp.Protocol != ProtoUtility {
			t.Fatalf("%s: the next answer is for proto=%#x method=%#x: DeliverMessage was answered", who, resp.Protocol, resp.MethodID)
		}
		var got *rmc.RMCMessage
		for _, p := range c.pushes {
			if p.Protocol == 0x1B && p.MethodID == 0x01 {
				got = p
			}
		}
		if got == nil {
			t.Fatalf("%s never received the host's message", who)
		}
		// Class name, sizes, Data header, UserMessage header, id, parent, sender.
		r := rmc.NewReader(got.Body)
		if cls, _ := r.String(); cls != "BinaryMessage" {
			t.Fatalf("%s: class %q", who, cls)
		}
		r.U32()
		inner, err := r.Buffer()
		if err != nil || len(inner) < 26 {
			t.Fatalf("%s: holder: %v", who, err)
		}
		if sender := binary.LittleEndian.Uint64(inner[18:]); sender != host.pid {
			t.Fatalf("%s: sender not stamped with the host", who)
		}
		if reception := binary.LittleEndian.Uint64(inner[26:]); reception == 0 {
			t.Fatalf("%s: no reception time", who)
		}
	}
	outsider.call(ProtoUtility, 0x01, nil)
	for _, p := range outsider.pushes {
		if p.Protocol == 0x1B {
			t.Fatal("a player outside the gathering received its message")
		}
	}
}
