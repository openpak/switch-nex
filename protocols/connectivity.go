package protocols

import (
	"fmt"
	"log"
	"net/netip"
	"strconv"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/internal/connectivity"
	"github.com/openpak/switch-nex/prudplite"
)

// commitStations publishes usable candidates before changing the native station
// set. Pia fields stay in this codec; endpoint normalization/lifetime is shared.
// Called under Env.mu, like all secure-stage handlers.
func (e *Env) commitStations(c *prudplite.Connection, stations []string) error {
	if e.Connectivity == nil {
		c.SetStations(stations)
		return nil
	}
	candidates := make([]connectivity.Candidate, 0, len(stations))
	indices := map[int]connectivity.Candidate{}
	for i, raw := range stations {
		u := switchnex.Parse(raw)
		ip, err := netip.ParseAddr(u.Get("address"))
		port := u.GetInt("port")
		// Initial station metadata can be incomplete. Do not claim an absent
		// port or hostname is a usable, observed UDP mapping.
		if err != nil || port == 0 {
			continue
		}
		if port > 65535 {
			return connectivity.ErrUnavailable
		}
		kind := "reported_public"
		if isPrivateAddr(ip.String()) {
			kind = "lan"
		}
		candidate := connectivity.Candidate{Endpoint: netip.AddrPortFrom(ip.Unmap(), uint16(port)).String(), Kind: kind}
		indices[i] = candidate
		candidates = append(candidates, candidate)
	}
	key := connectivity.Key{Title: e.Name, Session: "rendezvous", Player: strconv.FormatUint(c.PID, 10), Generation: fmt.Sprintf("%s-%d", e.Connectivity.BootID, c.ID), Component: "pia-udp"}
	participant, err := e.Connectivity.Register(key, candidates)
	if err != nil {
		return err
	}
	accepted := map[connectivity.Candidate]bool{}
	for _, candidate := range participant.Candidates {
		accepted[candidate] = true
	}
	// A shared response must preserve this participant's candidate set. Never
	// accept a candidate belonging to another participant or socket.
	for _, candidate := range indices {
		if !accepted[candidate] {
			return connectivity.ErrUnavailable
		}
	}
	for _, candidate := range participant.Candidates {
		found := false
		for _, input := range indices {
			if candidate == input {
				found = true
				break
			}
		}
		if !found {
			return connectivity.ErrUnavailable
		}
	}
	canonical := append([]string(nil), stations...)
	for i, candidate := range indices {
		endpoint, err := netip.ParseAddrPort(candidate.Endpoint)
		if err != nil {
			return connectivity.ErrUnavailable
		}
		u := switchnex.Parse(canonical[i])
		u.Set("address", endpoint.Addr().String())
		u.SetInt("port", uint64(endpoint.Port()))
		canonical[i] = u.String()
	}
	if e.connectivityLeases == nil {
		e.connectivityLeases = map[uint32]connectivity.Participant{}
	}
	e.connectivityLeases[c.ID] = participant
	c.SetStations(canonical)
	log.Printf("[%s] connectivity: participant endpoint lease accepted", e.Name)
	return nil
}

func (e *Env) ensureStationLease(c *prudplite.Connection) error {
	if e.Connectivity == nil || len(c.Stations()) == 0 {
		return nil
	}
	if lease, ok := e.connectivityLeases[c.ID]; ok && time.Now().Before(lease.ExpiresAt) {
		return nil
	}
	return e.commitStations(c, c.Stations())
}

// ReleaseConnectivity is invoked after the native disconnect cleanup. Expiry
// bounds leaked leases if the control plane is temporarily unreachable.
func (e *Env) ReleaseConnectivity(c *prudplite.Connection) {
	if e.Connectivity == nil {
		return
	}
	e.mu.Lock()
	lease, ok := e.connectivityLeases[c.ID]
	delete(e.connectivityLeases, c.ID)
	e.mu.Unlock()
	if ok {
		if err := e.Connectivity.Release(lease.ID); err != nil {
			log.Printf("[%s] connectivity: release deferred to lease expiry", e.Name)
		}
	}
}
