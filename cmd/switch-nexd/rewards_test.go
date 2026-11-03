package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	emit "github.com/openpak/rewards-emit"
	"github.com/openpak/switch-nex/protocols"
	"github.com/openpak/switch-nex/rmc"
)

// Two emulated consoles create and join a Mario Kart 8 Deluxe gathering through the real
// secure stage; the watcher reports one session each once they have shared it long enough.
func TestOnlineSessionReachesRewards(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in struct{ Events []map[string]any }
		json.Unmarshal(b, &in)
		mu.Lock()
		got = append(got, in.Events...)
		mu.Unlock()
		w.Write([]byte(`{"results":[]}`))
	}))
	defer sink.Close()

	name := "mario-kart-8-deluxe"
	authAddr, secureAddr, svc := start(t, name)
	key := titleAccessKey(t, name)
	rw := emit.New(emit.Config{URL: sink.URL, Key: "k", Source: "t", Flush: 10 * time.Millisecond})
	svc.env.Rewards = rw
	ms := sessionMetrics(name, svc.env)
	if len(ms) != 2 || ms[0] != "kart_sessions.mk8dx" || ms[1] != "game_sessions.mario-kart-8-deluxe" {
		t.Fatalf("metrics %v", ms)
	}
	stop := svc.env.WatchSessions(ms[0], 400*time.Millisecond, 50*time.Millisecond)

	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")
	resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(4, 3, 77))
	if resp.IsError {
		t.Fatalf("create: %#x", resp.ErrorCode)
	}
	gid, _ := rmc.NewReader(resp.Body[5:]).U32()

	time.Sleep(300 * time.Millisecond) // a lobby of one counts nothing
	mu.Lock()
	if len(got) != 0 {
		t.Fatalf("a lobby of one reported %v", got)
	}
	mu.Unlock()

	param := rmc.NewWriter()
	param.StructOf(0, func(w *rmc.Writer) { w.U32(gid) })
	if r := joiner.call(ProtoMatchmakeExt, 0x27, param.Bytes()); r.IsError {
		t.Fatalf("join: %#x", r.ErrorCode)
	}
	time.Sleep(250 * time.Millisecond) // together for less than the minimum
	mu.Lock()
	if len(got) != 0 {
		t.Fatalf("reported before the minimum: %v", got)
	}
	mu.Unlock()
	time.Sleep(700 * time.Millisecond)
	stop()
	rw.Close(context.Background())

	mu.Lock()
	defer mu.Unlock()
	pids := map[any]bool{}
	for _, e := range got {
		id := e["identity"].(map[string]any)
		if e["metric"] != "kart_sessions.mk8dx" || id["namespace"] != "switch" {
			t.Fatalf("%v", e)
		}
		pids[id["subject"]] = true
	}
	if len(got) != 2 || len(pids) != 2 {
		t.Fatalf("host and joiner once each: %v", got)
	}
}

// Every title with a matchmaking store reports game_sessions.<name>; the family metrics stay.
func TestEveryMatchTitleReportsGameSessions(t *testing.T) {
	family := map[string]string{}
	var without []string
	for _, tt := range titles {
		env := protocols.NewEnv(tt.name, "127.0.0.1", tt.base)
		tt.env(env)
		ms := sessionMetrics(tt.name, env)
		if env.Match == nil {
			if len(ms) != 0 {
				t.Fatalf("%s has no matchmaking store but reports %v", tt.name, ms)
			}
			without = append(without, tt.name)
			continue
		}
		if ms[len(ms)-1] != "game_sessions."+tt.name {
			t.Fatalf("%s: %v", tt.name, ms)
		}
		if len(ms) == 2 {
			family[tt.name] = ms[0]
		}
	}
	want := map[string]string{"mario-kart-8-deluxe": "kart_sessions.mk8dx", "splatoon-2": "turf_sessions.s2", "super-smash-bros-ultimate": "brawl_sessions.ult"}
	if len(family) != 3 {
		t.Fatalf("family metrics %v", family)
	}
	for k, v := range want {
		if family[k] != v {
			t.Fatalf("%s: %q", k, family[k])
		}
	}
	if len(without) != 2 || without[0] != "tmnt-shredders-revenge" || without[1] != "super-mario-odyssey" {
		t.Fatalf("titles without gatherings: %v", without)
	}
}
