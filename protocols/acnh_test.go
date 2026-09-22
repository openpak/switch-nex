package protocols

import (
	"encoding/hex"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// Console-measured 2026-09-22: the host's Dodo-code update (0x6D/0x2C,
// flags 0x4002, every field present) and the visitor's lookup (0x6D/0x34,
// codeword "0JJJK" in the criteria). Both bytes are as logged, except the
// truncated tail of the update, rebuilt as the 5-char code plus NUL.
const acnhSessionPart = "005d000000010000000240000006000000000000000000000000000000000000000000000000000000010000000000000400000000000000000000000000000001000000000000010000ffffffff010000005f000000000000000600304a4a4a4b00"
const acnhBrowse = "00560000000600000001000001000001000001000001000001000002003200020031000500312c313200020031000001010000000001000004000000000000000101000000000600304a4a4a4b0000080000000000000010000000"

func TestACNHDodoCodeUpdateAndLookup(t *testing.T) {
	env := testEnv()
	env.Match.next = 0 // the captured update names gid 1; the store seeds from the clock
	host := &Session{OwnerPID: 1900000001, HostPID: 1900000001, MaxParticipants: 8}
	gid := env.Match.create(host)
	if gid != 1 {
		t.Fatalf("gid = %d, want 1 (the captured update names gid 1)", gid)
	}
	other := &Session{OwnerPID: 2, HostPID: 2, MaxParticipants: 8, OpenParticipation: true, CodeWord: "AAAAA"}
	env.Match.create(other)

	part, _ := hex.DecodeString(acnhSessionPart)
	resp := matchmakeExtHandler(env, conn(1900000001), request(0x6D, 0x2C, part))
	if len(resp) < 10 || resp[5] != 1 {
		t.Fatalf("session part answered %x, want success", resp)
	}
	if host.CodeWord != "0JJJK" || !host.OpenParticipation {
		t.Fatalf("after part: codeword %q open %v, want 0JJJK/open", host.CodeWord, host.OpenParticipation)
	}

	browse, _ := hex.DecodeString(acnhBrowse)
	resp = matchmakeExtHandler(env, conn(1900000002), request(0x6D, 0x34, browse))
	body := resp[14:] // rmc response header: len, proto, ok, callid, method
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil || n != 1 {
		t.Fatalf("browse by code answered %d sessions (err %v), want exactly the island with that code", n, err)
	}
	got, err := readSession(r)
	if err != nil {
		t.Fatalf("browse body is not a raw session list: %v", err)
	}
	if got.CodeWord != "0JJJK" {
		t.Fatalf("browse returned codeword %q", got.CodeWord)
	}
}

// The "visit a friend" menu: a host publishes its gate (0x09), and a friend
// asking (0x0D) gets it back — open gates only — plus a live push at
// type*1000. A stranger gets nothing.
func TestACNHFriendNotificationData(t *testing.T) {
	env := testEnv()
	var pushed []notificationEvent
	env.sent = func(pid uint64, ev notificationEvent) { pushed = append(pushed, ev) }
	host, friend, stranger := uint64(0xaaaaaaaaaaaa0001), uint64(0xaaaaaaaaaaaa0002), uint64(7)
	env.SetFriends(friend, []uint64{host})

	publish := func(open uint64) {
		w := rmc.NewWriter()
		w.U32(101)
		w.U64(0x4f)
		w.U64(open)
		w.String("island")
		resp := matchmakeExtHandler(env, conn(host), request(0x6D, 0x09, w.Bytes()))
		if resp[5] != 1 {
			t.Fatalf("publish answered %x", resp)
		}
	}
	ask := func(pid uint64) int {
		w := rmc.NewWriter()
		w.List(1)
		w.U32(101)
		resp := matchmakeExtHandler(env, conn(pid), request(0x6D, 0x0D, w.Bytes()))
		n, err := rmc.NewReader(resp[14:]).ListHeader()
		if err != nil {
			t.Fatalf("answer is not a list: %v", err)
		}
		return int(n)
	}
	publish(1)
	if n := ask(friend); n != 1 {
		t.Fatalf("friend sees %d open gate(s), want 1", n)
	}
	if len(pushed) == 0 || pushed[len(pushed)-1].Type != 101000 || pushed[len(pushed)-1].SourcePID != host {
		t.Fatalf("friend was not pushed the host's presence at type*1000: %+v", pushed)
	}
	if n := ask(stranger); n != 0 {
		t.Fatalf("stranger sees %d gate(s), want 0", n)
	}
	publish(0)
	if n := ask(friend); n != 0 {
		t.Fatalf("closed gate still listed (%d)", n)
	}
}
