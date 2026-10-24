// SecureConnection (0x0B) — the first protocol every title runs on the secure
// stage. The client reports the station URLs it can be reached on; the server
// assigns it a connection id and tells it which of those the world can see.
//
//	0x01 Register        -> (QResult, u32 connection id, StationURL public)
//	0x02 RequestConnectionData -> empty success
//	0x03 RequestURLs     -> (bool, list of the TARGET's station URLs)
//	0x04 RegisterEx      -> same three fields as Register
//	0x05 TestConnectivity -> empty success
//	0x06 UpdateURLs      -> empty success, and the stations are STORED
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

	case 0x02, 0x08: // RequestConnectionData / SendReport
		return success(msg, nil)

	case 0x03: // RequestURLs(u32 cidTarget, PID pidTarget) -> (bool, list<StationURL>)
		return requestURLs(env, c, msg)

	case 0x06: // UpdateURLs(list<StationURL>) -> empty
		return updateURLs(env, c, msg)

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
	local, public := stampStations(env, c, reported)
	if err := env.commitStations(c, []string{local.String(), public.String()}); err != nil {
		return errorResp(msg, rmc.ResultServiceUnavailable)
	}
	log.Printf("[%s] register pid=%s cid=%d", env.Name, redact.Tag(c.PID), c.ID)
	diagStations(env, c.PID, "register-stored", c.Stations())

	w := rmc.NewWriter()
	w.U32(rmc.ResultSuccess) // QResult success
	w.U32(c.ID)              // the connection id the client calls itself
	w.String(c.Stations()[1])
	return success(msg, w.Bytes())
}

// stampStations splits the reported stations into the LAN one and the public
// one and stamps the caller's identity onto both. Register and UpdateURLs share
// it, so a re-report keeps exactly the shape the first registration had.
func stampStations(env *Env, c *prudplite.Connection, reported []*switchnex.URL) (local, public *switchnex.URL) {
	local, public = splitStations(reported)
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

	return local, public
}

// updateURLs stores the stations a client re-reports. The previous empty
// acknowledgement dropped them, so a peer asking for this client's stations
// kept getting the ones from its first registration.
func updateURLs(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	reported := readStationList(msg.Body)
	if len(reported) == 0 {
		return success(msg, nil)
	}
	local, public := stampStations(env, c, reported)
	if err := env.commitStations(c, []string{local.String(), public.String()}); err != nil {
		return errorResp(msg, rmc.ResultServiceUnavailable)
	}
	diagStations(env, c.PID, "update-urls", c.Stations())
	return success(msg, nil)
}

// requestURLs answers RequestURLs(u32 cidTarget, PID pidTarget) with the
// TARGET's stations: (bool found, list<StationURL>). Answering with the
// caller's own stations sent a NEX P2P client to connect to itself.
func requestURLs(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	cid, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	pid, err := r.U64()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	w := rmc.NewWriter()
	target, ok := c, pid == c.PID
	if !ok {
		target, ok = env.push.connection(pid)
	}
	if !ok || (cid != 0 && target.ID != cid) {
		w.Bool(false)
		w.EmptyList()
		return success(msg, w.Bytes())
	}
	if target != c {
		if err := env.ensureStationLease(target); err != nil {
			return errorResp(msg, rmc.ResultServiceUnavailable)
		}
	}
	w.Bool(true)
	w.Raw(stationURLList(target))
	return success(msg, w.Bytes())
}

// replaceURL stores the station the console re-reports after the NAT handshake.
// That one carries the connection id a joiner needs to build its probe, so
// dropping it (the old empty acknowledgement) loses the only copy and the
// joiner's probe never completes. Stations are keyed by address with the new
// one winning, which keeps the fresh entry and drops the stale duplicate.
func replaceURL(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	if _, err := r.String(); err != nil { // target: the URL being replaced
		return success(msg, nil)
	}
	raw, err := r.String()
	if err != nil || raw == "" {
		return success(msg, nil)
	}
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
	if err := env.commitStations(c, kept); err != nil {
		return errorResp(msg, rmc.ResultServiceUnavailable)
	}
	log.Printf("[%s] replaceURL pid=%s (%d station(s))", env.Name, redact.Tag(c.PID), len(kept))
	diagStations(env, c.PID, "replaceURL", kept)
	return success(msg, nil)
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
