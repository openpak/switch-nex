// NAT traversal (0x03) — hole-punch coordination. The punch itself happens
// directly between consoles; what the server owes them is a probe that fires
// from BOTH ends at once, an honest answer about relays, and enough knowledge of
// each peer's NAT to avoid seating two that can never reach each other.
//
//	0x01 RequestProbeInitiation        -> empty success
//	0x02 InitiateProbe                 -> empty success
//	0x03 RequestProbeInitiationExt     -> empty success, and InitiateProbe is
//	                                      PUSHED to every named target
//	0x04 ReportNATTraversalResult      -> empty success
//	0x05 ReportNATProperties           -> empty success, properties recorded
//	0x06 GetRelaySignatureKey          -> the six-field "no relay offered" reply
//	0x07 ReportNATTraversalResultDetail-> empty success
//
// Answering 0x06 with an empty body is not the same as declining a relay: the
// newer Pia (Strikers' RelayMesh stack) performs this handshake before starting
// the match, cannot parse an empty answer, and never completes its match-start —
// the match connects and then never renders.
package protocols

import (
	"log"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
	"github.com/openpak/switch-nex/rmc"
)

func natTraversalHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch msg.MethodID {
	case 0x03: // RequestProbeInitiationExt(list<StationURL> targets, StationURL self)
		pushInitiateProbe(env, c, msg)
		return success(msg, nil)

	case 0x05: // ReportNATProperties(u32 natMapping, u32 natFiltering, u32 rtt)
		recordNATProperties(env, c, msg)
		return success(msg, nil)

	case 0x06: // GetRelaySignatureKey -> relay descriptor
		return success(msg, relaySignatureKey())

	case 0x04, 0x07: // ReportNATTraversalResult(Ext): the client's own verdict
		reportNATResult(env, c, msg)
		return success(msg, nil)

	case 0x01, 0x02:
		return success(msg, nil)

	default:
		return notImplemented(env, msg)
	}
}

// relaySignatureKey answers "no relay offered", well-formed. We are not a relay
// server: an empty address and port zero tell the game to proceed on its direct
// mesh, which is what it does.
//
// Layout: relayMode i32, currentUTCTime DateTime, relay address String,
// port u16, addressType i32, gameServerID u32.
func relaySignatureKey() []byte {
	w := rmc.NewWriter()
	w.U32(0) // relayMode
	w.U64(nowDateTime())
	w.String("") // no relay address
	w.U16(0)     // no relay port
	w.U32(0)     // addressType
	w.U32(0)     // gameServerID
	return w.Bytes()
}

// pushInitiateProbe relays a server-to-client InitiateProbe to every target the
// caller named, carrying the caller's own station so each target probes back.
// Without it only the joiner's NAT opens and the other console's Pia waits for a
// punch that never arrives.
func pushInitiateProbe(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) {
	r := rmc.NewReader(msg.Body)
	n, err := r.ListHeader()
	if err != nil || n > 32 {
		return
	}
	targets := make([]string, 0, n)
	for range n {
		s, err := r.String()
		if err != nil {
			return
		}
		targets = append(targets, s)
	}
	station, err := r.String()
	if err != nil {
		return
	}

	// Which station a peer is told to probe decides whether the punch can land
	// at all, so both halves are logged: what the caller asked us to push, and
	// every station it has registered. Two peers in one household advertise a
	// LAN address, and the port they pair it with is the one their NAT was seen
	// to use -- which is not their local port unless the router preserved it
	// (2026-09-20, Mario Kart 8 Deluxe: race starts, dies ten seconds in).
	log.Printf("[%s] nat: probe request pid=%s targets=%d stations=%d",
		env.Name, redact.Tag(c.PID), len(targets), len(c.Stations()))
	diagStations(env, c.PID, "probe-targets", targets)
	diagStations(env, c.PID, "probe-self", []string{station})

	params := rmc.NewWriter()
	params.String(station)
	for _, t := range targets {
		pid := stationPID(t)
		if pid == 0 || pid == c.PID {
			continue
		}
		peer, ok := env.push.connection(pid)
		if !ok {
			continue
		}
		req := rmc.EncodeRequest(ProtoNatTraversal, 0x02, env.push.nextCallID(), params.Bytes())
		if err := peer.SendRMC(req); err != nil {
			log.Printf("[%s] nat: probe push to pid=%s failed: %v", env.Name, redact.Tag(pid), err)
			continue
		}
		log.Printf("[%s] nat: pushed InitiateProbe pid=%s -> pid=%s", env.Name, redact.Tag(c.PID), redact.Tag(pid))
	}
}

// natClass buckets a peer's reported NAT behaviour.
type natClass int

// NAT buckets. Unknown stays optimistic: a peer we cannot classify yet is never
// isolated on a guess.
const (
	natUnknown natClass = iota
	natOpen
	natModerate
	natStrict
)

func (n natClass) String() string {
	switch n {
	case natOpen:
		return "open"
	case natModerate:
		return "moderate"
	case natStrict:
		return "strict"
	default:
		return "unknown"
	}
}

// classifyNAT buckets from the mapping and filtering behaviour the console
// reports: both at most endpoint-independent is open, an independent mapping
// with stricter filtering is moderate, and a symmetric mapping is strict.
func classifyNAT(mapping, filtering uint32) natClass {
	if mapping == 0 && filtering == 0 {
		return natUnknown
	}
	if mapping <= 1 && filtering <= 1 {
		return natOpen
	}
	if mapping <= 1 {
		return natModerate
	}
	return natStrict
}

// natCompatible reports whether two peers can likely punch to each other. Only
// the pairs that reliably fail are refused; unknown is fail-open. Seating a
// pair that cannot connect gives EVERYONE in that lobby a communication error,
// not just the two of them.
func natCompatible(a, b natClass) bool {
	if a == natUnknown || b == natUnknown {
		return true
	}
	if a == natStrict && (b == natStrict || b == natModerate) {
		return false
	}
	if b == natStrict && (a == natStrict || a == natModerate) {
		return false
	}
	return true
}

// recordNATProperties stores what a peer reports about its own NAT.
func recordNATProperties(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) {
	r := rmc.NewReader(msg.Body)
	mapping, err := r.U32()
	if err != nil {
		return
	}
	filtering, err := r.U32()
	if err != nil {
		return
	}
	class := classifyNAT(mapping, filtering)
	env.nat.put(c.PID, class)
	log.Printf("[%s] nat: pid=%s mapping=%d filtering=%d -> %s", env.Name, redact.Tag(c.PID), mapping, filtering, class)
}

// stationPID reads the PID parameter out of a station URL string.
func stationPID(station string) uint64 {
	return parseStationPID(station)
}

// reportNATResult reads the verdict a client gives on its own hole punch. The
// answer is an empty acknowledgement either way -- the punch happens between
// the consoles and nothing here can change it -- but the verdict is the only
// place the client says whether it reached its peer, which is otherwise
// guesswork from the outside (2026-09-20, a race that plays and then ends).
//
// Layout: connection id of the peer, bool result, (detail i32 on 0x07), rtt.
func reportNATResult(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) {
	r := rmc.NewReader(msg.Body)
	cid, err := r.U32()
	if err != nil {
		return
	}
	ok, err := r.Bool()
	if err != nil {
		return
	}
	detail := uint32(0)
	if msg.MethodID == 0x07 {
		if detail, err = r.U32(); err != nil {
			return
		}
	}
	rtt, _ := r.U32()
	verdict := "FAILED"
	if ok {
		verdict = "ok"
	}
	log.Printf("[%s] nat: pid=%s punch to cid=%d %s (rtt=%dms detail=%d)", env.Name, redact.Tag(c.PID), cid, verdict, rtt, detail)
}
