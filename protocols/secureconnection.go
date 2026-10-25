// SecureConnection (0x0B) — the first protocol every title runs on the secure
// stage. The client reports the station URLs it can be reached on; the server
// assigns it a connection id and tells it which of those the world can see.
//
//	0x01 Register        -> (QResult, u32 connection id, StationURL public)
//	0x02 RequestConnectionData -> empty success
//	0x03 RequestURLs     -> list of station URLs (the caller's own)
//	0x04 RegisterEx      -> same three fields as Register
//	0x05 TestConnectivity -> empty success
//	0x06 UpdateURLs      -> empty success
//	0x07 ReplaceURL      -> empty success, and the new station is STORED
//	0x08 SendReport      -> empty success
//
// Register and RegisterEx answer identically. The pre-5.19 Pia titles (ARMS,
// Mario Tennis Aces, Splatoon 2, Strikers) call plain Register, and a bare
// StationURL there is not the same shape — it desynchronises the reply.
//
// The URL handed back is the CLIENT's public station, not ours. Pia builds its
// mesh from the endpoint it is told the world sees for it; given our own secure
// address it never recognises itself and the session times out.
package protocols

import (
	"log"
	"net"
	"os"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
	"github.com/openpak/switch-nex/rmc"
)

// PiaStationShape is the public-station shape a title's Pia version expects.
// It is per title, following the title's SDK and not our preference: the older
// Pia wants the Wii U-era BehindNAT|Public and no Pa parameter at all, and Pia
// 5.19 wants the extra Switch bit plus Pa so it can build its NAT probe.
type PiaStationShape struct {
	PublicType uint64
	SetPa      bool
}

// LegacyPia is the pre-5.19 shape: type 0x03, no Pa.
func LegacyPia() PiaStationShape {
	return PiaStationShape{PublicType: switchnex.FlagBehindNAT | switchnex.FlagPublic}
}

// Pia519 is the 5.19-and-later shape: type 0x0B, Pa present.
func Pia519() PiaStationShape {
	return PiaStationShape{PublicType: switchnex.FlagBehindNAT | switchnex.FlagPublic | switchnex.FlagSwitch, SetPa: true}
}

func secureConnectionHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch msg.MethodID {
	case 0x01, 0x04: // Register / RegisterEx
		return registerStations(env, c, msg)

	case 0x02, 0x06, 0x08: // RequestConnectionData / UpdateURLs / SendReport
		return success(msg, nil)

	case 0x03: // RequestURLs(u32 cidTarget, PID pidTarget) -> list<StationURL>
		return success(msg, stationURLList(c))

	case 0x05: // TestConnectivity() -> empty
		return success(msg, nil)

	case 0x07: // ReplaceURL(StationURL target, StationURL new) -> empty
		return replaceURL(env, c, msg)

	default:
		return notImplemented(env, msg)
	}
}

// registerStations answers Register/RegisterEx: split the reported stations
// into the LAN one and the public one, stamp the caller's identity onto both,
// and hand the public one back.
func registerStations(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	reported := readStationList(msg.Body)
	if diagEnabled(env.Name, os.Getenv("SWITCH_NEX_DIAG_STATIONS")) {
		raw := make([]string, 0, len(reported))
		for _, u := range reported {
			raw = append(raw, u.String())
		}
		diagStations(env, c.PID, "register-reported", raw)
	}
	local, public := splitStations(reported)
	if local == nil {
		local = switchnex.Empty(env.StationScheme)
	}

	// derived: the console reported only LAN addresses, so the public station
	// is one we synthesised. That changes what Pa has to carry, below.
	derived := public == nil
	if derived {
		// Copy the LAN station rather than building a bare one: it carries
		// the parameters the console set (sid, Pl, Tpt, natf, natm, pmp,
		// upnp), and a station missing them is one Pia will not recognise as
		// its own endpoint.
		public = local.Copy()
		if c.Host != "" {
			public.Set("address", c.Host)
		}
		// The rendezvous carriage is TLS/TCP. Its source port is unrelated
		// to the peer's UDP game socket, even behind the same NAT. Retain
		// the reported game port until the client supplies its observed
		// public mapping through ReplaceURL; the LAN endpoint stays intact.
	}

	for _, u := range []*switchnex.URL{local, public} {
		u.SetInt("PID", c.PID)
		u.SetInt("RVCID", uint64(c.ID))
	}
	public.SetInt("type", env.Pia.PublicType)
	if env.Pia.SetPa {
		// Pa is the address the peer punches toward. When we derived the
		// public station it is that public address; when the console reported
		// its own public station, Pa is its LAN address — which is what a real
		// console sends.
		pa := local.Get("address")
		if derived {
			pa = public.Get("address")
		}
		if pa != "" {
			public.Set("Pa", pa)
		}
	} else {
		public.Remove("Pa")
	}

	if err := env.commitStations(c, []string{local.String(), public.String()}); err != nil {
		return errorResp(msg, rmc.ResultServiceUnavailable)
	}
	log.Printf("[%s] register pid=%s cid=%d", env.Name, redact.Tag(c.PID), c.ID)

	w := rmc.NewWriter()
	w.U32(rmc.ResultSuccess) // QResult success
	w.U32(c.ID)              // the connection id the client calls itself
	w.String(c.Stations()[1])
	return success(msg, w.Bytes())
}

// replaceURL stores the station the console re-reports after the NAT handshake.
// That one carries the connection id a joiner needs to build its probe, so
// dropping it (the old empty acknowledgement) loses the only copy and the
// joiner's probe never completes. Stations are keyed by address with the new
// one winning, which keeps the fresh entry and drops the stale duplicate.
func replaceURL(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	target, err := r.String() // the URL being replaced
	if err != nil {
		return success(msg, nil)
	}
	raw, err := r.String()
	if err != nil || raw == "" {
		return success(msg, nil)
	}
	diagStations(env, c.PID, "replaceURL-request", []string{target, raw})
	replacement := switchnex.Parse(raw)
	replacement.SetInt("PID", c.PID)
	addr := replacement.Get("address")

	kept := make([]string, 0, len(c.Stations())+1)
	replaced := false
	for _, s := range c.Stations() {
		if switchnex.Parse(s).Get("address") == addr {
			if !replaced {
				kept = append(kept, replacement.String())
				replaced = true
			}
			continue
		}
		kept = append(kept, s)
	}
	if !replaced {
		kept = append(kept, replacement.String())
	}
	kept = carryMappedPortToPublic(kept, replacement)
	if err := env.commitStations(c, kept); err != nil {
		return errorResp(msg, rmc.ResultServiceUnavailable)
	}
	log.Printf("[%s] replaceURL pid=%s (%d station(s))", env.Name, redact.Tag(c.PID), len(kept))
	diagStations(env, c.PID, "replaceURL", kept)
	return success(msg, nil)
}

// carryMappedPortToPublic copies the port of a LAN-addressed ReplaceURL onto
// the public station. The Switch's ReplaceURL names its LAN address but carries
// the port its NAT check saw on the public side: in every household and mobile
// trace on record (ARMS, Mario Party Superstars, Kirby, 2026-09-26 to
// 2026-10-25) that port equals the public port the same client later claims for
// itself in RequestProbeInitiationExt, while its real LAN port is a different,
// ephemeral one. The public station we synthesised at Register meanwhile still
// carried the placeholder port the console registered with (port=1), so every
// peer was handed a public candidate nobody listens on. natm/natf move with
// the port: they describe the same NAT check.
func carryMappedPortToPublic(stations []string, replacement *switchnex.URL) []string {
	port := replacement.GetInt("port")
	if !isPrivateAddr(replacement.Get("address")) || port <= 1 || port > 65535 {
		return stations
	}
	out := append([]string(nil), stations...)
	for i, raw := range out {
		u := switchnex.Parse(raw)
		if addr := u.Get("address"); addr == "" || isPrivateAddr(addr) {
			continue
		}
		u.SetInt("port", port)
		for _, k := range []string{"natm", "natf"} {
			if replacement.Has(k) {
				u.Set(k, replacement.Get(k))
			}
		}
		out[i] = u.String()
		break
	}
	return out
}

// learnSelfStation corrects a stored station from the one the client names as
// itself in RequestProbeInitiationExt. That station is the socket Pia actually
// probes from, so its port is authoritative; the LAN station from ReplaceURL
// carries the NAT-mapped port instead (see carryMappedPortToPublic), which on a
// shared LAN is a port nobody listens on. Matched by address, so a self station
// for an address the client never registered changes nothing. Reports whether
// anything changed.
func learnSelfStation(env *Env, c *prudplite.Connection, self *switchnex.URL) bool {
	addr, port := self.Get("address"), self.GetInt("port")
	if addr == "" || port <= 1 || port > 65535 {
		return false
	}
	stations := c.Stations()
	changed := false
	for i, raw := range stations {
		u := switchnex.Parse(raw)
		if u.Get("address") != addr || u.GetInt("port") == port {
			continue
		}
		u.SetInt("port", port)
		stations[i] = u.String()
		changed = true
		break
	}
	if !changed {
		return false
	}
	if err := env.commitStations(c, stations); err != nil {
		return false
	}
	return true
}

// readStationList parses a list<StationURL> request body.
func readStationList(body []byte) []*switchnex.URL {
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil || n > 32 {
		return nil
	}
	out := make([]*switchnex.URL, 0, n)
	for range n {
		s, err := r.String()
		if err != nil {
			break
		}
		out = append(out, switchnex.Parse(s))
	}
	return out
}

// splitStations picks the LAN and public stations by ADDRESS. The Switch
// commonly reports both without setting the Public flag, so selecting on the
// flag mislabels them.
func splitStations(urls []*switchnex.URL) (local, public *switchnex.URL) {
	for _, u := range urls {
		addr := u.Get("address")
		if addr == "" {
			continue
		}
		if isPrivateAddr(addr) {
			if local == nil {
				local = u
			}
			continue
		}
		if public == nil {
			public = u
		}
	}
	if local == nil && len(urls) > 0 {
		local = urls[0]
	}
	return local, public
}

// isPrivateAddr reports whether addr is a LAN address: RFC1918, loopback,
// link-local, or RFC6598 carrier-grade NAT. Parsed and range-checked rather
// than prefix-matched — "172." is a whole /8 of which only a sixteenth is
// private, and CGNAT is a /10 that "100.64." covers only a quarter of. A
// console behind CGNAT is no more reachable than an RFC1918 one, so it belongs
// on the local side of the split.
func isPrivateAddr(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// stationURLList answers RequestURLs with the caller's own stations.
func stationURLList(c *prudplite.Connection) []byte {
	stations := c.Stations()
	w := rmc.NewWriter()
	w.List(len(stations))
	for _, s := range stations {
		w.String(s)
	}
	return w.Bytes()
}

// stationURLListWithHostWait returns the host's station URLs, waiting up to
// 2.5s for the host to report its ReplaceURL (which carries the post-NAT UDP
// port and RVCID). OpenPak does this because the host's initial station URLs
// carry the WebSocket TCP port, not the real UDP port Pia needs for hole-punch.
func stationURLListWithHostWait(peer *prudplite.Connection) []byte {
	const (
		hostReplaceURLWait = 2500 * time.Millisecond
		hostReplaceURLPoll = 100 * time.Millisecond
	)
	deadline := time.Now().Add(hostReplaceURLWait)
	for time.Now().Before(deadline) {
		stations := peer.Stations()
		if hasValidReplaceURL(stations) {
			return buildStationList(stations)
		}
		time.Sleep(hostReplaceURLPoll)
	}
	// Timeout: return whatever the host has (raw stations)
	return buildStationList(peer.Stations())
}

// hasValidReplaceURL checks if the station list contains a station with valid
// post-NAT data: RVCID > 0, CID > 0, and port > 1 (not the initial port=1).
func hasValidReplaceURL(stations []string) bool {
	for _, s := range stations {
		u := switchnex.Parse(s)
		rvcid := u.GetInt("RVCID")
		cid := u.GetInt("CID")
		port := u.GetInt("port")
		if rvcid > 0 && cid > 0 && port > 1 && port <= 65535 {
			return true
		}
	}
	return false
}

func buildStationList(stations []string) []byte {
	w := rmc.NewWriter()
	w.List(len(stations))
	for _, s := range stations {
		w.String(s)
	}
	return w.Bytes()
}
