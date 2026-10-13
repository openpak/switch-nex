package connectivity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeService answers participant and relay calls with canned records.
type fakeService struct {
	relays map[string]Relay
	bound  map[string][2]string
}

func newFakeService() (*fakeService, *httptest.Server) {
	f := &fakeService{relays: map[string]Relay{}, bound: map[string][2]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/relays", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Participants [2]string `json:"participants"`
			TTL          int       `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		relay := Relay{ID: "relay-" + req.Participants[0], Endpoint: "203.0.113.7:22901", Participants: req.Participants, ExpiresAt: time.Now().Add(time.Hour)}
		f.relays[relay.ID] = relay
		w.Header().Set("Content-Type", "application/json")
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
		relay, ok := f.relays[r.PathValue("id")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		relay.Bound = true
		f.relays[relay.ID] = relay
		f.bound[relay.ID] = req.Endpoints
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(relay)
	})
	mux.HandleFunc("DELETE /v1/relays/{id}", func(w http.ResponseWriter, r *http.Request) {
		delete(f.relays, r.PathValue("id"))
		w.WriteHeader(204)
	})
	return f, httptest.NewServer(mux)
}

func testClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := New(url, strings.Repeat("k", 32))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return c
}

func TestRelayAllocateBindRelease(t *testing.T) {
	f, server := newFakeService()
	defer server.Close()
	c := testClient(t, server.URL)

	relay, err := c.AllocateRelay("p-host", "p-joiner", 300)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if relay.Endpoint != "203.0.113.7:22901" || relay.Bound {
		t.Fatalf("fresh relay %+v, want endpoint set and unbound", relay)
	}
	bound, err := c.BindRelay(relay.ID, "192.168.1.10:3074", "192.168.1.11:3074")
	if err != nil || !bound.Bound {
		t.Fatalf("bind: %+v %v", bound, err)
	}
	if got := f.bound[relay.ID]; got != [2]string{"192.168.1.10:3074", "192.168.1.11:3074"} {
		t.Fatalf("bound endpoints %v", got)
	}
	if err := c.ReleaseRelay(relay.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok := f.relays[relay.ID]; ok {
		t.Fatal("relay survived release")
	}
}

func TestRelayBindUnknownIsUnavailable(t *testing.T) {
	_, server := newFakeService()
	defer server.Close()
	c := testClient(t, server.URL)
	if _, err := c.BindRelay("no-such-relay", "192.168.1.10:3074", "192.168.1.11:3074"); err != ErrUnavailable {
		t.Fatalf("bind unknown: %v, want unavailable", err)
	}
}

func TestRelayServerDownIsUnavailable(t *testing.T) {
	c := testClient(t, "http://127.0.0.1:1")
	if _, err := c.AllocateRelay("a", "b", 300); err != ErrUnavailable {
		t.Fatalf("allocate down: %v, want unavailable", err)
	}
	if err := c.ReleaseRelay("x"); err != ErrUnavailable {
		t.Fatalf("release down: %v, want unavailable", err)
	}
}
