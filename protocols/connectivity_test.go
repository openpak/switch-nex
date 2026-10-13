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
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

type leaseFixture struct {
	mu       sync.Mutex
	requests []connectivity.Participant
	releases int
	fail     bool
}

func sharedLeaseFixture(t *testing.T) (*leaseFixture, *connectivity.Client) {
	t.Helper()
	fixture := &leaseFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
			w.WriteHeader(401)
			return
		}
		if fixture.fail {
			w.WriteHeader(503)
			return
		}
		if r.Method == "DELETE" {
			fixture.releases++
			w.WriteHeader(204)
			return
		}
		var request struct {
			Key        connectivity.Key         `json:"key"`
			Candidates []connectivity.Candidate `json:"candidates"`
			TTL        int                      `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if request.TTL != 3600 || request.Key.Component != "pia-udp" || request.Key.Session != "rendezvous" {
			t.Error("invalid shared lease scope")
		}
		participant := connectivity.Participant{ID: fmt.Sprintf("lease-%s-%s", request.Key.Player, request.Key.Generation), Key: request.Key, Candidates: request.Candidates, ExpiresAt: time.Now().Add(time.Hour)}
		fixture.requests = append(fixture.requests, participant)
		json.NewEncoder(w).Encode(participant)
	}))
	t.Cleanup(server.Close)
	client, err := connectivity.New(server.URL, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	return fixture, client
}

func registerThroughSharedLease(t *testing.T, env *Env, c *prudplite.Connection, lan string, port int) {
	t.Helper()
	w := rmc.NewWriter()
	w.List(1)
	w.String(fmt.Sprintf("prudp:/address=%s;port=%d;PID=999;CID=21;sid=1;type=1;natm=1;natf=2", lan, port))
	m, err := rmc.Decode(Dispatch(env, c, &rmc.RMCMessage{IsRequest: true, Protocol: ProtoSecureConnection, MethodID: 4, CallID: 1, Body: w.Bytes()}))
	if err != nil || m.IsError {
		t.Fatalf("shared registration failed: %v %+v", err, m)
	}
}

func TestSharedLeasePreservesHouseholdCandidatesAndPiaFields(t *testing.T) {
	fixture, client := sharedLeaseFixture(t)
	env := NewEnv("test-title", "203.0.113.5", 22000)
	env.Connectivity = client
	a := &prudplite.Connection{PID: 101, ID: 10, Host: "198.51.100.8"}
	b := &prudplite.Connection{PID: 202, ID: 20, Host: "198.51.100.8"}
	registerThroughSharedLease(t, env, a, "192.168.1.10", 50000)
	registerThroughSharedLease(t, env, b, "192.168.1.11", 50000)
	fixture.mu.Lock()
	requests := append([]connectivity.Participant(nil), fixture.requests...)
	fixture.mu.Unlock()
	if len(requests) != 2 || requests[0].ID == requests[1].ID || requests[0].Key.Generation == requests[1].Key.Generation {
		t.Fatal("household lease collapsed")
	}
	for i, c := range []*prudplite.Connection{a, b} {
		stations := c.Stations()
		local, public := switchnex.Parse(stations[0]), switchnex.Parse(stations[1])
		if local.Get("address") != fmt.Sprintf("192.168.1.%d", 10+i) || local.GetInt("port") != 50000 || public.Get("address") != "198.51.100.8" || public.GetInt("port") != 50000 {
			t.Fatal("game socket mapping changed")
		}
		for _, station := range []*switchnex.URL{local, public} {
			if station.GetInt("PID") != c.PID || station.GetInt("RVCID") != uint64(c.ID) || station.GetInt("CID") != 21 || station.GetInt("natm") != 1 || station.GetInt("natf") != 2 {
				t.Fatal("Pia fields changed")
			}
		}
		for _, candidate := range requests[i].Candidates {
			if candidate.Kind == "observed" {
				t.Fatal("client report promoted to verified mapping")
			}
		}
	}
	env.ReleaseConnectivity(a)
	fixture.mu.Lock()
	released := fixture.releases
	fixture.mu.Unlock()
	if released != 1 || len(env.connectivityLeases) != 1 {
		t.Fatal("disconnect released the wrong household participant")
	}
}

func TestSharedLeaseFailurePreservesLastAcceptedStations(t *testing.T) {
	fixture, client := sharedLeaseFixture(t)
	env := NewEnv("test-title", "203.0.113.5", 22000)
	env.Connectivity = client
	c := &prudplite.Connection{PID: 101, ID: 10, Host: "198.51.100.8"}
	registerThroughSharedLease(t, env, c, "192.168.1.10", 50000)
	before := strings.Join(c.Stations(), "\n")
	fixture.mu.Lock()
	fixture.fail = true
	fixture.mu.Unlock()
	w := rmc.NewWriter()
	w.String(c.Stations()[1])
	w.String("prudp:/address=198.51.100.8;port=51000;CID=22")
	m, err := rmc.Decode(Dispatch(env, c, &rmc.RMCMessage{IsRequest: true, Protocol: ProtoSecureConnection, MethodID: 7, CallID: 1, Body: w.Bytes()}))
	if err != nil || !m.IsError || m.ErrorCode != rmc.ResultServiceUnavailable || strings.Join(c.Stations(), "\n") != before {
		t.Fatal("unaccepted update was committed")
	}
	// Existing valid leases keep working through a control-plane outage.
	m, err = rmc.Decode(Dispatch(env, c, &rmc.RMCMessage{IsRequest: true, Protocol: ProtoSecureConnection, MethodID: 3, CallID: 2}))
	if err != nil || m.IsError {
		t.Fatal("valid cached lease unnecessarily rejected")
	}
	lease := env.connectivityLeases[c.ID]
	lease.ExpiresAt = time.Now().Add(-time.Second)
	env.connectivityLeases[c.ID] = lease
	m, err = rmc.Decode(Dispatch(env, c, &rmc.RMCMessage{IsRequest: true, Protocol: ProtoSecureConnection, MethodID: 3, CallID: 3}))
	if err != nil || !m.IsError || m.ErrorCode != rmc.ResultServiceUnavailable {
		t.Fatal("expired candidate authority silently renewed locally")
	}
}
