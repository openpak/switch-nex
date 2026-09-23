package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

// A real create-and-join over the wire, then the website's question: which
// gathering is this account in, and who is in it with them.
func TestVoiceLookupFindsTheAccountsGathering(t *testing.T) {
	authAddr, secureAddr, svc := start(t, "mario-kart-8-deluxe")
	key := titleAccessKey(t, "mario-kart-8-deluxe")
	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")
	stranger := secureSession(t, authAddr, secureAddr, key, "stranger")

	if resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(4, 3, 77)); resp.IsError {
		t.Fatalf("create: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	req := rmc.NewWriter()
	req.List(1)
	req.U64(host.pid)
	req.Bool(false)
	r := rmc.NewReader(joiner.call(ProtoMatchmakeExt, 0x3C, req.Bytes()).Body)
	r.ListHeader()
	r.StructHeader()
	r.U64()
	gid, _ := r.U32()
	if resp := joiner.call(ProtoMatchmakeExt, 0x07, u32le(gid)); resp.IsError {
		t.Fatalf("join: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}

	// What a proven login records (identity.resolve); the joiner is proven,
	// the host too, the stranger is in no gathering.
	ids := newIdentity("http://nx-baas.invalid", "internal-key")
	ids.members.Store(host.pid, member{AccountID: "acct-host", Nickname: "Host"})
	ids.members.Store(joiner.pid, member{AccountID: "acct-joiner", Nickname: "Joiner"})
	ids.members.Store(stranger.pid, member{AccountID: "acct-stranger", Nickname: "Stranger"})
	voice := &voiceIndex{ids: ids}
	voice.add(svc.env)

	ask := func(account, key string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/internal/voice/gathering?account_id="+account, nil)
		req.Header.Set("X-Internal-Key", key)
		rec := httptest.NewRecorder()
		voice.gathering(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, _ := ask("acct-joiner", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong key answered %d", code)
	}
	code, out := ask("acct-joiner", "internal-key")
	if code != http.StatusOK || out["title"] != "mario-kart-8-deluxe" || uint32(out["gid"].(float64)) != gid {
		t.Fatalf("lookup = %d %v, want gid %d", code, out, gid)
	}
	var accounts []string
	for _, p := range out["participants"].([]any) {
		accounts = append(accounts, p.(map[string]any)["account_id"].(string))
	}
	slices.Sort(accounts)
	if !slices.Equal(accounts, []string{"acct-host", "acct-joiner"}) {
		t.Fatalf("participants = %v", accounts)
	}
	if code, _ := ask("acct-stranger", "internal-key"); code != http.StatusNotFound {
		t.Fatalf("an account in no gathering answered %d", code)
	}

	// Leaving the game leaves the room.
	svc.env.Disconnected(joiner.pid)
	if code, _ := ask("acct-joiner", "internal-key"); code != http.StatusNotFound {
		t.Fatalf("after disconnect: %d", code)
	}
}
