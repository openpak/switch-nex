package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/switch-nex/internal/scorestore"
	"github.com/openpak/switch-nex/protocols"
)

const (
	acctA = "11111111-1111-4111-8111-111111111111"
	acctB = "22222222-2222-4222-8222-222222222222"
)

type fakeLinks map[string][]uint64

func (f fakeLinks) SwitchPIDs(_ context.Context, account string) ([]uint64, error) {
	if account == "00000000-0000-4000-8000-00000000dead" {
		return nil, errors.New("core down")
	}
	return f[account], nil
}

func newTestEraser(t *testing.T) (*eraser, *scorestore.Store, *recall, *protocols.Env) {
	t.Helper()
	st := scorestore.NewMemory()
	rc := newRecall(time.Hour, "")
	env := protocols.NewEnv("test-title", "127.0.0.1", 23000)
	ids := newIdentity("http://nx-baas.invalid", "k")
	e := &eraser{
		key: "secret", core: fakeLinks{acctA: {0xaaaa0001, 0xaaaa0002}, acctB: {0xbbbb0001}},
		ids: ids, voice: &voiceIndex{ids: ids, envs: []*protocols.Env{env}}, recal: rc,
		score: func() (*scorestore.Store, error) { return st, nil },
	}
	return e, st, rc, env
}

func TestEraseRemovesOnlyTheAccount(t *testing.T) {
	ctx := context.Background()
	e, st, rc, env := newTestEraser(t)
	for _, pid := range []uint64{0xaaaa0001, 0xaaaa0002, 0xbbbb0001} {
		st.Submit(ctx, "t", "1", pid, 100, scorestore.Desc, false)
	}
	st.PutBlob(ctx, "t", "r2common", "aaaa0001", []byte("x"))
	st.PutBlob(ctx, "t", "r2board", "7/aaaa0002", []byte("x"))
	st.PutBlob(ctx, "t", "festvote", "3/bbbb0001", []byte("x"))
	st.PutBlob(ctx, "t", "festsum", "3", []byte("x"))
	rc.noteLogin("t", "203.0.113.5", 0xaaaa0001)
	rc.noteLogin("t", "198.51.100.9", 0xbbbb0001)
	env.SetFriends(0xaaaa0001, []uint64{1})
	env.SetFriends(0xbbbb0001, []uint64{1})
	e.ids.members.Store(uint64(0xaaaa0001), member{AccountID: acctA})

	n, err := e.Erase(ctx, acctA)
	if err != nil || n != 4 {
		t.Fatalf("first erase: rows=%d err=%v (want 4)", n, err)
	}
	if rows, _, _ := st.Page(ctx, "t", "1", 0, 0); len(rows) != 1 || rows[0].PID != 0xbbbb0001 {
		t.Fatalf("scores after erase: %+v", rows)
	}
	if m, _ := st.Blobs(ctx, "t", "r2common"); len(m) != 0 {
		t.Fatalf("r2common left: %v", m)
	}
	if m, _ := st.Blobs(ctx, "t", "festvote"); len(m) != 1 {
		t.Fatalf("other account's vote lost: %v", m)
	}
	if m, _ := st.Blobs(ctx, "t", "festsum"); len(m) != 1 {
		t.Fatalf("tally must stay: %v", m)
	}
	if len(env.FriendPIDs(0xaaaa0001)) != 0 || len(env.FriendPIDs(0xbbbb0001)) != 1 {
		t.Fatal("friends memory not scoped to the account")
	}
	if _, ok := e.ids.members.Load(uint64(0xaaaa0001)); ok {
		t.Fatal("voice membership kept")
	}
	if got := rc.seen[seenKey("t", "203.0.113.5")]; len(got) != 0 {
		t.Fatalf("recall kept the address: %v", got)
	}
	if got := rc.seen[seenKey("t", "198.51.100.9")]; len(got) != 1 {
		t.Fatal("recall lost the other account")
	}
	// Idempotent.
	if n, err := e.Erase(ctx, acctA); err != nil || n != 0 {
		t.Fatalf("second erase: rows=%d err=%v", n, err)
	}
}

func TestEraseNeedsTheCore(t *testing.T) {
	e, _, _, _ := newTestEraser(t)
	e.core = nil
	if _, err := e.Erase(context.Background(), acctA); err == nil {
		t.Fatal("erase without a core must fail, not report success")
	}
}

func TestEraseEndpoint(t *testing.T) {
	e, st, _, _ := newTestEraser(t)
	st.Submit(context.Background(), "t", "1", 0xaaaa0001, 1, scorestore.Desc, false)
	mux := http.NewServeMux()
	e.register(mux)
	call := func(auth, account string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/internal/accounts/"+account+"/data", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	if w := call("", acctA); w.Code != 401 {
		t.Fatalf("no key: %d", w.Code)
	}
	if w := call("Bearer wrong", acctA); w.Code != 401 {
		t.Fatalf("wrong key: %d", w.Code)
	}
	if w := call("Bearer secret", "not-a-uuid"); w.Code != 400 {
		t.Fatalf("bad id: %d", w.Code)
	}
	if w := call("Bearer secret", "00000000-0000-4000-8000-00000000dead"); w.Code != 502 {
		t.Fatalf("core error must not look like success: %d", w.Code)
	}
	w := call("Bearer secret", strings.ToUpper(acctA))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"service":"switch-nex"`) || !strings.Contains(w.Body.String(), `"rows":1`) {
		t.Fatalf("erase: %d %s", w.Code, w.Body.String())
	}
	e.key = ""
	if w := call("Bearer ", acctA); w.Code != 503 {
		t.Fatalf("unset key must be 503: %d", w.Code)
	}
}
