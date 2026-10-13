package protocols

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

// relayFixture is a fake shared-connectivity service with participant
// leases and pair relays.
type relayFixture struct {
	mu         sync.Mutex
	leases     map[string]connectivity.Participant
	relays     map[string]connectivity.Relay
	bound      map[string][2]string
	allocated  int
	released   []string
	fail       bool
	rejectBind bool
}

func sharedRelayFixture(t *testing.T) (*relayFixture, *connectivity.Client) {
	t.Helper()
	f := &relayFixture{leases: map[string]connectivity.Participant{}, relays: map[string]connectivity.Relay{}, bound: map[string][2]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/participants", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) || f.fail {
			w.WriteHeader(503)
			return
		}
		var req struct {
			Key        connectivity.Key         `json:"key"`
			Candidates []connectivity.Candidate `json:"candidates"`
			TTL        int                      `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		p := connectivity.Participant{ID: "lease-" + req.Key.Player, Key: req.Key, Candidates: req.Candidates, ExpiresAt: time.Now().Add(time.Hour)}
		f.leases[p.ID] = p
		json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("POST /v1/relays", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			w.WriteHeader(503)
			return
		}
		var req struct {
			Participants [2]string `json:"participants"`
			TTL          int       `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		if _, ok := f.leases[req.Participants[0]]; !ok {
			w.WriteHeader(404)
			return
		}
		if _, ok := f.leases[req.Participants[1]]; !ok {
			w.WriteHeader(404)
			return
		}
		f.allocated++
		relay := connectivity.Relay{ID: fmt.Sprintf("relay-%d", f.allocated), Endpoint: "203.0.113.7:22901", Participants: req.Participants, ExpiresAt: time.Now().Add(time.Hour)}
		f.relays[relay.ID] = relay
		json.NewEncoder(w).Encode(relay)
	})
	mux.HandleFunc("POST /v1/relays/{id}/bind", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.rejectBind {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if f.fail {
			w.WriteHeader(503)
			return
		}
		var req struct {
			Endpoints [2]string `json:"endpoints"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		relay, ok := f.relays[r.PathValue("id")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		relay.Bound = true
		f.relays[relay.ID] = relay
		f.bound[relay.ID] = req.Endpoints
		json.NewEncoder(w).Encode(relay)
	})
	mux.HandleFunc("DELETE /v1/relays/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.released = append(f.released, r.PathValue("id"))
		delete(f.relays, r.PathValue("id"))
		w.WriteHeader(204)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := connectivity.New(server.URL, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	return f, client
}

func relayEnv(fixture *relayFixture, client *connectivity.Client) *Env {
	e := NewEnv("relay-title", "203.0.113.5", 23100)
	e.Match = NewMatchmakingStore()
	e.UseRelay = true
	e.Connectivity = client
	_ = fixture
	return e
}

func leaseFor(t *testing.T, e *Env, pid uint64, lan string) {
	t.Helper()
	key := connectivity.Key{Title: e.Name, Session: "rendezvous", Player: pidString(pid), Generation: fmt.Sprintf("gen-%d", pid), Component: "pia-udp"}
	participant, err := e.Connectivity.Register(key, []connectivity.Candidate{{Endpoint: lan, Kind: "lan"}})
	if err != nil {
		t.Fatalf("register lease: %v", err)
	}
	e.connectivityLeases = e.connectivityLeasesOrInit()
	e.connectivityLeases[uint32(pid)] = participant
}

func pidString(pid uint64) string { return fmt.Sprintf("%d", pid) }

func (e *Env) connectivityLeasesOrInit() map[uint32]connectivity.Participant {
	if e.connectivityLeases == nil {
		e.connectivityLeases = map[uint32]connectivity.Participant{}
	}
	return e.connectivityLeases
}

func TestPairKeyOrdersPIDs(t *testing.T) {
	if pairKey(3, 9) != pairKey(9, 3) || pairKey(3, 9) == pairKey(3, 10) {
		t.Fatalf("pair keys %q", pairKey(3, 9))
	}
}

func TestDuckRelayProbeOnlyNeverBinds(t *testing.T) {
	t.Setenv("SWITCH_NEX_RELAY_PROBE_ONLY", "1")
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	e.Name = "duck-game"
	leaseFor(t, e, 101, "192.168.1.10:50000")
	leaseFor(t, e, 202, "192.168.1.11:50001")
	e.ensurePairRelay(77, 101, 202)
	e.ensurePairRelay(77, 202, 101)
	f.mu.Lock()
	if f.allocated != 1 || len(f.bound) != 0 || len(e.relayLinks[77]) != 1 {
		t.Fatalf("probe allocated=%d binds=%d links=%d", f.allocated, len(f.bound), len(e.relayLinks[77]))
	}
	f.mu.Unlock()
	e.dropGatheringRelays(77)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.released) != 1 {
		t.Fatal("inactive probe socket not released")
	}
}

func TestRelayRejectedBindReleasesAndStaysDirect(t *testing.T) {
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	leaseFor(t, e, 101, "192.168.1.10:50000")
	leaseFor(t, e, 202, "192.168.1.11:50001")
	// The real service refuses reported addresses without observations toward
	// this exact destination. Such a refusal must not advertise a live relay.
	f.rejectBind = true
	e.ensurePairRelay(77, 101, 202)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allocated != 1 || len(f.released) != 1 || len(e.relayLinks) != 0 {
		t.Fatalf("allocated=%d released=%d links=%d", f.allocated, len(f.released), len(e.relayLinks))
	}
}

func TestEnsurePairRelayAllocatesAndBinds(t *testing.T) {
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	leaseFor(t, e, 101, "192.168.1.10:50000")
	leaseFor(t, e, 202, "192.168.1.11:50001")
	e.ensurePairRelay(77, 101, 202)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allocated != 1 {
		t.Fatalf("allocated=%d, want 1", f.allocated)
	}
	if len(e.relayLinks[77]) != 1 {
		t.Fatal("pair link not recorded")
	}
	for id, eps := range f.bound {
		if eps != [2]string{"192.168.1.10:50000", "192.168.1.11:50001"} {
			t.Fatalf("relay %s bound %v", id, eps)
		}
	}
	// Idempotent: a second call for either order allocates nothing new.
	e.ensurePairRelay(77, 202, 101)
	if f.allocated != 1 {
		t.Fatalf("allocated=%d after repeat, want 1", f.allocated)
	}
}

func TestEnsurePairRelayDegradesWithoutLeases(t *testing.T) {
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	leaseFor(t, e, 101, "192.168.1.10:50000")
	e.ensurePairRelay(77, 101, 999)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allocated != 0 || len(e.relayLinks) != 0 {
		t.Fatal("relay built without both leases")
	}
}

func TestEnsurePairRelayDegradesWhenServiceDown(t *testing.T) {
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	leaseFor(t, e, 101, "192.168.1.10:50000")
	leaseFor(t, e, 202, "192.168.1.11:50001")
	f.fail = true
	e.ensurePairRelay(77, 101, 202)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(e.relayLinks) != 0 {
		t.Fatal("relay recorded despite service failure")
	}
}

func TestEnsurePairRelayNeedsOptIn(t *testing.T) {
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	e.UseRelay = false
	leaseFor(t, e, 101, "192.168.1.10:50000")
	leaseFor(t, e, 202, "192.168.1.11:50001")
	e.ensurePairRelay(77, 101, 202)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allocated != 0 {
		t.Fatal("relay allocated without opt-in")
	}
}

func TestDropPairRelaysReleasesLeaver(t *testing.T) {
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	leaseFor(t, e, 101, "192.168.1.10:50000")
	leaseFor(t, e, 202, "192.168.1.11:50001")
	leaseFor(t, e, 303, "192.168.1.12:50002")
	e.ensurePairRelay(77, 101, 202)
	e.ensurePairRelay(77, 101, 303)
	e.dropPairRelays(77, 101)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.released) != 2 || len(e.relayLinks[77]) != 0 {
		t.Fatalf("released=%v links=%v", f.released, e.relayLinks[77])
	}
}

func TestDropGatheringRelaysReleasesAll(t *testing.T) {
	f, client := sharedRelayFixture(t)
	e := relayEnv(f, client)
	leaseFor(t, e, 101, "192.168.1.10:50000")
	leaseFor(t, e, 202, "192.168.1.11:50001")
	e.ensurePairRelay(77, 101, 202)
	e.dropGatheringRelays(77)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.released) != 1 || len(e.relayLinks) != 0 {
		t.Fatalf("released=%v links=%v", f.released, e.relayLinks)
	}
}

func TestBuildRelayStationClonesPeerIdentity(t *testing.T) {
	peer := []string{
		"prudp:/address=192.168.1.11;port=50001;sid=1;CID=9;PID=202;RVCID=8;type=3;natf=2;natm=1",
		"prudp:/address=198.51.100.8;port=41001;sid=1;CID=9;PID=202;RVCID=8;type=3",
	}
	got := buildRelayStation(peer, "203.0.113.7:22901")
	u := switchnex.Parse(got)
	if u.Get("address") != "203.0.113.7" || u.Get("port") != "22901" {
		t.Fatalf("relay endpoint not stamped: %q", got)
	}
	for _, k := range []string{"PID", "CID", "RVCID", "sid", "type"} {
		if u.Get(k) != switchnex.Parse(peer[1]).Get(k) {
			t.Fatalf("field %s changed: %q", k, got)
		}
	}
	if got := buildRelayStation(nil, "203.0.113.7:22901"); got != "" {
		t.Fatalf("empty stations built %q", got)
	}
	if got := buildRelayStation(peer, "not-an-endpoint"); got != "" {
		t.Fatalf("bad endpoint built %q", got)
	}
}

func TestAppendStationURLRoundTrips(t *testing.T) {
	w := rmc.NewWriter()
	w.List(1)
	w.String("prudp:/address=192.168.1.11;port=50001")
	two := appendStationURL(w.Bytes(), "prudp:/address=203.0.113.7;port=22901")
	r := rmc.NewReader(two)
	n, err := r.ListHeader()
	if err != nil || n != 2 {
		t.Fatalf("appended list holds %d: %v", n, err)
	}
	if _, err := r.String(); err != nil {
		t.Fatalf("first entry: %v", err)
	}
	last, err := r.String()
	if err != nil || last != "prudp:/address=203.0.113.7;port=22901" || r.Remaining() != 0 {
		t.Fatalf("second entry %q: %v", last, err)
	}
	if out := appendStationURL([]byte{0, 1, 2}, "prudp:/address=203.0.113.7;port=22901"); string(out) != string([]byte{0, 1, 2}) {
		t.Fatal("corrupt input not passed through")
	}
}
