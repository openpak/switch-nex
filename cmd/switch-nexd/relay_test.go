package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/internal/connectivity"
	"github.com/openpak/switch-nex/rmc"
)

// relayBackend is a fake shared-connectivity service with endpoint leases
// and pair relays, so the full TLS flow can prove the relay leg without a
// production deployment.
type relayBackend struct {
	mu        sync.Mutex
	leases    map[string]connectivity.Participant
	relays    map[string]connectivity.Relay
	bound     map[string][2]string
	allocated int
	released  []string
}

func startRelayBackend(t *testing.T) (*relayBackend, *connectivity.Client) {
	t.Helper()
	b := &relayBackend{leases: map[string]connectivity.Participant{}, relays: map[string]connectivity.Relay{}, bound: map[string][2]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/participants", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key        connectivity.Key         `json:"key"`
			Candidates []connectivity.Candidate `json:"candidates"`
			TTL        int                      `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		p := connectivity.Participant{ID: "lease-" + req.Key.Player + "-" + req.Key.Generation, Key: req.Key, Candidates: req.Candidates, ExpiresAt: time.Now().Add(time.Hour)}
		b.leases[p.ID] = p
		json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("DELETE /v1/participants/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/relays", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Participants [2]string `json:"participants"`
			TTL          int       `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.leases[req.Participants[0]]; !ok {
			w.WriteHeader(404)
			return
		}
		if _, ok := b.leases[req.Participants[1]]; !ok {
			w.WriteHeader(404)
			return
		}
		b.allocated++
		relay := connectivity.Relay{ID: fmt.Sprintf("relay-%d", b.allocated), Endpoint: "203.0.113.7:22901", Participants: req.Participants, ExpiresAt: time.Now().Add(time.Hour)}
		b.relays[relay.ID] = relay
		json.NewEncoder(w).Encode(relay)
	})
	mux.HandleFunc("POST /v1/relays/{id}/bind", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Endpoints [2]string `json:"endpoints"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		relay, ok := b.relays[r.PathValue("id")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		relay.Bound = true
		b.relays[relay.ID] = relay
		b.bound[relay.ID] = req.Endpoints
		json.NewEncoder(w).Encode(relay)
	})
	mux.HandleFunc("DELETE /v1/relays/{id}", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.released = append(b.released, r.PathValue("id"))
		delete(b.relays, r.PathValue("id"))
		w.WriteHeader(204)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := connectivity.New(server.URL, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	return b, client
}

func relayStations(t *testing.T, body []byte) []string {
	t.Helper()
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil {
		t.Fatalf("station list: %v", err)
	}
	out := make([]string, 0, n)
	for range n {
		s, err := r.String()
		if err != nil {
			t.Fatalf("station entry: %v", err)
		}
		out = append(out, s)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing bytes %d", r.Remaining())
	}
	return out
}

// TestDuckGameRelayPairFlowsThroughTLS: with the shared backend attached, a
// host and joiner allocate exactly one bound pair relay on join; both
// GetSessionURLs and GetParticipantsURLs carry the relay station alongside
// the direct ones; leaving releases it. Direct play never depends on it.
func TestDuckGameRelayPairFlowsThroughTLS(t *testing.T) {
	const name = "duck-game"
	authAddr, secureAddr, svc := start(t, name)
	backend, connClient := startRelayBackend(t)
	svc.env.Connectivity = connClient
	svc.env.UseRelay = true
	key := titleAccessKey(t, name)
	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")

	register := func(c *client) {
		stations := rmc.NewWriter()
		stations.List(1)
		stations.String("prudp:/address=192.168.1.20;port=50000;sid=1;natf=0")
		if resp := c.call(ProtoSecureConnection, 0x01, stations.Bytes()); resp.IsError {
			t.Fatalf("register: %#x", resp.ErrorCode)
		}
	}
	register(host)
	register(joiner)

	if resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(4, 3, 77)); resp.IsError {
		t.Fatalf("create: %#x", resp.ErrorCode)
	}
	resp := joiner.call(ProtoMatchmakeExt, 0x34, nil)
	if resp.IsError {
		t.Fatalf("browse: %#x", resp.ErrorCode)
	}
	r := rmc.NewReader(resp.Body)
	count, err := r.ListHeader()
	if err != nil || count != 1 {
		t.Fatalf("browse found %d sessions: %v", count, err)
	}
	_, gathering, err := r.Struct()
	if err != nil {
		t.Fatalf("browse gathering: %v", err)
	}
	gid, err := gathering.U32()
	if err != nil || gid == 0 {
		t.Fatalf("browse gathering ID %d: %v", gid, err)
	}
	param := rmc.NewWriter()
	param.StructOf(0, func(w *rmc.Writer) { w.U32(gid) })
	if resp := joiner.call(ProtoMatchmakeExt, 0x27, param.Bytes()); resp.IsError {
		t.Fatalf("join: %#x", resp.ErrorCode)
	}

	backend.mu.Lock()
	allocated := backend.allocated
	bound := len(backend.bound)
	backend.mu.Unlock()
	if allocated != 1 || bound != 1 {
		t.Fatalf("allocated=%d bound=%d, want exactly one bound pair", allocated, bound)
	}

	sessionURLs := relayStations(t, mustCall(t, joiner, ProtoMatchMaking, 0x29, u32le(gid)).Body)
	foundRelay, foundDirect := false, false
	for _, s := range sessionURLs {
		u := switchnex.Parse(s)
		if u.Get("address") == "203.0.113.7" && u.Get("port") == "22901" {
			foundRelay = true
			if u.Get("PID") != itoa(host.pid) {
				t.Fatalf("relay station names PID %s, want host %d", u.Get("PID"), host.pid)
			}
		}
		if u.Get("address") == "192.168.1.20" {
			foundDirect = true
		}
	}
	if !foundRelay || !foundDirect {
		t.Fatalf("session URLs relay=%v direct=%v: %q", foundRelay, foundDirect, sessionURLs)
	}

	partURLs := relayStations(t, mustCall(t, joiner, ProtoMatchMaking, 0x10, u32le(gid)).Body)
	foundRelay = false
	for _, s := range partURLs {
		if switchnex.Parse(s).Get("port") == "22901" {
			foundRelay = true
		}
	}
	if !foundRelay {
		t.Fatalf("participants URLs carry no relay station: %q", partURLs)
	}

	if resp := joiner.call(ProtoMatchMaking, 0x02, u32le(gid)); resp.IsError {
		t.Fatalf("leave: %#x", resp.ErrorCode)
	}
	backend.mu.Lock()
	released := len(backend.released)
	backend.mu.Unlock()
	if released != 1 {
		t.Fatalf("released=%d, want the pair relay back", released)
	}
}

func mustCall(t *testing.T, c *client, proto uint16, method uint32, body []byte) *rmc.RMCMessage {
	t.Helper()
	resp := c.call(proto, method, body)
	if resp.IsError {
		t.Fatalf("call %#x/%#x: %#x", proto, method, resp.ErrorCode)
	}
	return resp
}
