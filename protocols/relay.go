package protocols

import (
	"log"
	"net/netip"
	"os"
	"strconv"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// Native pair relay through the shared connectivity service.
//
// Direct Pia mesh fails for some pairs (unreachable advertised addresses,
// one-sided NAT, broken LAN paths) while the signalling plane is healthy.
// The relay pool forwards UDP between two exact bound endpoints, so a pair
// that cannot meet directly can still mesh through it. The relay is purely
// additive: direct stations keep flowing untouched, and every relay failure
// degrades to direct-only play. Nothing here ever fails a matchmaking call.
//
// A stock NEX client is never told about TURN and never speaks a new
// transport. The only thing handed to it is one more station URL — a clone
// of the peer's own public station with the address and port replaced by
// the pair relay's endpoint. Whether Pia probes and uses an injected relay
// station is client-measured per title; Duck Game is the validation vehicle.
//
// Relay use is opt-in per title (Env.UseRelay). Endpoints bound into a relay
// are NEX-verified (ticket identity plus reported candidates), never guesses:
// a wrong bind blackholes the pair, while the exact-match forwarding drops
// anything spoofed. Leases use the same title/session/component rendezvous
// as endpoint leases so both sides of a pair always qualify together.
type pairRelay struct {
	id       string
	pair     [2]uint64
	endpoint string
}

// pairKey orders two PIDs so a pair maps to one entry whichever side joins.
func pairKey(a, b uint64) string {
	if a < b {
		return strconv.FormatUint(a, 10) + "<" + strconv.FormatUint(b, 10)
	}
	return strconv.FormatUint(b, 10) + "<" + strconv.FormatUint(a, 10)
}

const relayTTLSeconds = 3600

// relayLeaseID returns the live endpoint-lease ID for pid, if any.
func (e *Env) relayLeaseID(pid uint64) (string, bool) {
	want := strconv.FormatUint(pid, 10)
	best := ""
	var bestExp time.Time
	for _, lease := range e.connectivityLeases {
		if lease.Key.Player != want || lease.ID == "" {
			continue
		}
		if time.Now().After(lease.ExpiresAt) {
			continue
		}
		if best == "" || lease.ExpiresAt.After(bestExp) {
			best, bestExp = lease.ID, lease.ExpiresAt
		}
	}
	return best, best != ""
}

// relayBindEndpoint picks the endpoint to bind for pid: the LAN candidate
// first (stable, exact), else the reported public one. Anything else (or
// nothing usable) means no relay for this pair — direct only.
func (e *Env) relayBindEndpoint(pid uint64) (string, bool) {
	want := strconv.FormatUint(pid, 10)
	public := ""
	for _, lease := range e.connectivityLeases {
		if lease.Key.Player != want || time.Now().After(lease.ExpiresAt) {
			continue
		}
		for _, candidate := range lease.Candidates {
			ap, err := netip.ParseAddrPort(candidate.Endpoint)
			if err != nil || !ap.IsValid() {
				continue
			}
			switch candidate.Kind {
			case "lan":
				return candidate.Endpoint, true
			case "reported_public":
				if public == "" {
					public = candidate.Endpoint
				}
			}
		}
	}
	return public, public != ""
}

// ensurePairRelays allocates and binds pair relays between joined and every
// other participant of gid. Idempotent per pair; silent unless UseRelay and a
// live connectivity client. Never fails outward.
func (e *Env) ensurePairRelays(gid uint32, joined uint64) {
	g, ok := e.Match.get(gid)
	if !ok {
		return
	}
	for _, pid := range participantPIDs(g) {
		if pid == joined {
			continue
		}
		e.ensurePairRelay(gid, joined, pid)
	}
}

// ensurePairRelay builds one bound pair relay. Direct play continues on any
// failure: a missing lease, missing endpoint, refused allocation or refused
// bind each just logs and returns.
func (e *Env) ensurePairRelay(gid uint32, a, b uint64) {
	if !e.UseRelay || e.Connectivity == nil || a == 0 || b == 0 || a == b {
		return
	}
	key := pairKey(a, b)
	if links := e.relayLinks[gid]; links != nil {
		if _, ok := links[key]; ok {
			return
		}
	}
	first, ok := e.relayLeaseID(a)
	if !ok {
		return
	}
	second, ok := e.relayLeaseID(b)
	if !ok {
		return
	}
	epA, ok := e.relayBindEndpoint(a)
	if !ok {
		return
	}
	epB, ok := e.relayBindEndpoint(b)
	if !ok {
		return
	}
	relay, err := e.Connectivity.AllocateRelay(first, second, relayTTLSeconds)
	if err != nil {
		log.Printf("[%s] relay: allocate gid=%d skipped: %v", e.Name, gid, err)
		return
	}
	// Operator-only canary: an inactive socket lets us measure whether retail
	// Pia probes an additional station. It NEVER binds reported addresses or
	// forwards packets. Keep this restricted to Duck until a native mapping
	// verifier exists; ordinary deployments still degrade to direct-only.
	if e.Name == "duck-game" && os.Getenv("SWITCH_NEX_RELAY_PROBE_ONLY") == "1" {
		e.storePairRelay(gid, key, a, b, relay.ID, relay.Endpoint)
		log.Printf("[%s] relay: probe-only candidate gid=%d endpoint=%s (inactive)", e.Name, gid, relay.Endpoint)
		return
	}
	bound, err := e.Connectivity.BindRelay(relay.ID, epA, epB)
	if err != nil || !bound.Bound {
		log.Printf("[%s] relay: bind gid=%d skipped: %v", e.Name, gid, err)
		_ = e.Connectivity.ReleaseRelay(relay.ID)
		return
	}
	e.storePairRelay(gid, key, a, b, relay.ID, bound.Endpoint)
	log.Printf("[%s] relay: pair gid=%d endpoint=%s bound", e.Name, gid, bound.Endpoint)
}

func (e *Env) storePairRelay(gid uint32, key string, a, b uint64, id, endpoint string) {
	if e.relayLinks == nil {
		e.relayLinks = map[uint32]map[string]*pairRelay{}
	}
	if e.relayLinks[gid] == nil {
		e.relayLinks[gid] = map[string]*pairRelay{}
	}
	e.relayLinks[gid][key] = &pairRelay{id: id, pair: [2]uint64{a, b}, endpoint: endpoint}
}

// relayStationFor clones the peer's public station with the pair relay's
// address and port, for self to probe. Empty when no bound relay joins the
// two, or when either side is missing — the caller then sends direct
// stations only.
func (e *Env) relayStationFor(gid uint32, self, peer uint64) string {
	if !e.UseRelay || self == 0 || peer == 0 || self == peer {
		return ""
	}
	links := e.relayLinks[gid]
	link, ok := links[pairKey(self, peer)]
	if !ok {
		return ""
	}
	peerConn, connected := e.push.connection(peer)
	if !connected {
		return ""
	}
	return buildRelayStation(peerConn.Stations(), link.endpoint)
}

// buildRelayStation clones the peer's public station with the pair relay's
// address and port. Identity fields (PID, CID, RVCID, sid, type) stay exactly
// as the peer reported them: the prober addresses the same principal through
// a different path. Empty when there is nothing to clone or the relay
// endpoint does not parse.
func buildRelayStation(peerStations []string, relayEndpoint string) string {
	source := ""
	for _, raw := range peerStations {
		u := switchnex.Parse(raw)
		if source == "" {
			source = raw
		}
		if !isPrivateAddr(u.Get("address")) {
			source = raw
			break
		}
	}
	if source == "" {
		return ""
	}
	endpoint, err := netip.ParseAddrPort(relayEndpoint)
	if err != nil || !endpoint.IsValid() {
		return ""
	}
	u := switchnex.Parse(source)
	u.Set("address", endpoint.Addr().String())
	u.SetInt("port", uint64(endpoint.Port()))
	return u.String()
}

// appendStationURL adds one URL to an encoded list<StationURL> body. Any
// parse failure returns the input untouched: a relay station must never
// corrupt the direct stations it rides with.
func appendStationURL(encoded []byte, extra string) []byte {
	r := rmc.NewReader(encoded)
	n, err := r.ListHeader()
	if err != nil {
		return encoded
	}
	stations := make([]string, 0, n+1)
	for range n {
		s, err := r.String()
		if err != nil {
			return encoded
		}
		stations = append(stations, s)
	}
	if r.Remaining() != 0 {
		return encoded
	}
	stations = append(stations, extra)
	w := rmc.NewWriter()
	w.List(len(stations))
	for _, s := range stations {
		w.String(s)
	}
	return w.Bytes()
}

// dropPairRelays releases every pair relay involving leaving in gid.
func (e *Env) dropPairRelays(gid uint32, leaving uint64) {
	links := e.relayLinks[gid]
	if len(links) == 0 {
		return
	}
	for key, link := range links {
		if link.pair[0] != leaving && link.pair[1] != leaving {
			continue
		}
		if e.Connectivity != nil {
			if err := e.Connectivity.ReleaseRelay(link.id); err != nil {
				log.Printf("[%s] relay: release gid=%d deferred: %v", e.Name, gid, err)
			}
		}
		delete(links, key)
	}
	if len(links) == 0 {
		delete(e.relayLinks, gid)
	}
}

// dropGatheringRelays releases every pair relay of a torn-down gathering.
func (e *Env) dropGatheringRelays(gid uint32) {
	links := e.relayLinks[gid]
	if len(links) == 0 {
		return
	}
	for _, link := range links {
		if e.Connectivity != nil {
			if err := e.Connectivity.ReleaseRelay(link.id); err != nil {
				log.Printf("[%s] relay: release gid=%d deferred: %v", e.Name, gid, err)
			}
		}
	}
	delete(e.relayLinks, gid)
}
