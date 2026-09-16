// Command switch-nexd serves every Switch-era NEX title on the switch-nex
// lineage: one process, one TLS listener per endpoint (auth on the title's
// port base, secure on base+1).
//
// Auth endpoint: TicketGranting 0x0A — LoginEx (0x02) and
// ValidateAndRequestTicketWithParam (0x06, the NEX 4.4+ path) both answered
// with the wire-verified ValidateAndRequestTicketResult (result.go, SPEC.md).
//
// Secure endpoint: the protocol set each title was measured using against the
// working reference servers, implemented in ./protocols with response
// behaviour matched title for title (docs/legacy-parity.md). Unimplemented
// methods answer Core::NotImplemented and are logged — the game tells us the
// backlog.
//
// MHGU (22120) is deliberately absent: it is the one Switch-era NEX title
// speaking raw PRUDP over UDP with kerberos v0 tickets (3DS lineage under an
// AArch64 shim) — a different transport this lineage does not provide, so it
// stays on its nex-go container (MIGRATION.md).
package main

import (
	"crypto/sha512"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/protocols"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// title is one served game: its port block and the protocol behaviour matched
// to the working reference (provenance per field in docs/legacy-parity.md).
type title struct {
	name string
	base int
	// accessKey is the title's PRUDP access key (a public constant baked
	// into each game binary). Empty = CONNECT signatures are not verified.
	accessKey string

	env func(e *protocols.Env) // per-title behaviour overrides
}

var titles = []title{
	{"mario-party-superstars", 22110, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.SanitizeDetailFind = true // FindByGatheringIDDetail strips SessionKey/UserPassword
		e.IntegerSettings = envIntegerSettings("MPS_INTEGER_SETTINGS")
	}},
	{"mario-golf-super-rush", 22130, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
	}},
	{"arms", 22140, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.DataStore = protocols.DSStub
	}},
	{"mario-kart-8-deluxe", 22150, "09c1c475", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
	}},
	{"super-mario-bros-35", 22160, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking2 = true
		e.MatchmakeReferee = true
		e.MessageDelivery = true
		e.SecurePID = 257049437023956657 // the retail secure-principal constant
		e.SourceKeyBytes = 16            // 16-byte source key attested against the reference
		e.IntegerSettings = protocols.SMB35IntegerSettings
	}},
	{"mario-tennis-aces", 22170, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.ForceOpenParticipation = true // sessions arrive closed; two searchers deadlock
		e.Ranking = true
		e.DataStore = protocols.DSStub
	}},
	{"animal-crossing-new-horizons", 22180, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSStub
	}},
	{"luigis-mansion-3", 22190, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Presence = true // 0x79 station/presence stub
	}},
	{"mario-strikers-battle-league", 22300, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.DataStore = protocols.DSStub
	}},
	{"minecraft", 22310, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
	}},
	{"splatoon-2", 22320, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSStub
	}},
	{"super-mario-maker-2", 22330, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSSMM2
	}},
	{"super-mario-odyssey", 22340, "", func(e *protocols.Env) {
		// No matchmaking: Odyssey has no lobby/P2P — Utility + DataStore only.
		e.DataStore = protocols.DSBalloonErrors
	}},
	{"super-smash-bros-ultimate", 22350, "", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.DataStore = protocols.DSStub
	}},
	{"clubhouse-games-51", 22870, "8c3661ff", func(e *protocols.Env) {
		// Clubhouse Games: 51 Worldwide Classics (010047700D540000, game
		// server 2035BB00). Account setup runs DataStore before matchmaking;
		// sessions arrive closed, so two searchers each host and never match
		// — the same open-participation fix as Mario Tennis Aces, plus the
		// create-time normalisation and full-session join answers the
		// working reference was measured serving (docs/legacy-parity.md).
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSClubhouse
		e.ForceOpenParticipation = true
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
	}},
}

// securePassword is every title's secure-account kerberos password. Both the
// auth endpoint (ticket encryption) and the secure endpoint (ticket
// decryption) derive their key from it, so any value works as long as it is
// the same on both sides.
const securePassword = "openpak-switch-nex-secure"

var (
	stationAddress = flag.String("station-address", "145.241.199.19", "address carried in every station URL (raw IP; the console dials it without DNS)")
	certFile       = flag.String("cert", "/app/nex.pem", "TLS certificate (CN covers *.s.n.srv.nintendo.net)")
	keyFile        = flag.String("key", "/app/nex.key", "TLS key")
	only           = flag.String("only", "", "comma-separated title names to serve (default: all)")
)

// recall remembers login PID by client IP for the ticketless secure stage.
type recall struct {
	mu  sync.Mutex
	m   map[string]recallEntry
	ttl time.Duration
}

type recallEntry struct {
	pid uint64
	at  time.Time
}

func newRecall(ttl time.Duration) *recall {
	return &recall{m: map[string]recallEntry{}, ttl: ttl}
}

func (r *recall) remember(host string, pid uint64) {
	r.mu.Lock()
	r.m[host] = recallEntry{pid: pid, at: time.Now()}
	r.mu.Unlock()
}

func (r *recall) take(host string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[host]
	if !ok || time.Since(e.at) > r.ttl {
		return 0, false
	}
	return e.pid, true
}

// pidFor returns a stable NEX PID for a login username: the console must keep
// recognizing itself as the same principal across logins.
func pidFor(username string) uint64 {
	h := sha512.Sum512_256([]byte("openpak-switch-nex-pid:" + username))
	return 0x6C000000 | uint64(binary.BigEndian.Uint32(h[:4])&0x00FFFFFF)
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.LUTC)

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("cert: %v", err)
	}
	// Switch NEX clients negotiate legacy TLS (nn::ssl); the Go default
	// minimum of 1.2 rejects their ClientHello outright.
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
	}

	want := map[string]bool{}
	if *only != "" {
		for _, n := range splitCSV(*only) {
			want[n] = true
		}
	}

	recall := newRecall(24 * time.Hour)

	for _, t := range titles {
		if len(want) > 0 && !want[t.name] {
			continue
		}
		env := protocols.NewEnv(t.name, *stationAddress, t.base)
		if t.env != nil {
			t.env(env)
		}
		targetKey := switchnex.DeriveKey([]byte(securePassword), env.SecurePID)

		auth := &prudplite.Endpoint{
			StreamID:  1,
			AccessKey: t.accessKey,
			OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
				return handleAuthRMC(recall, env, targetKey, c, msg)
			},
		}
		secure := &prudplite.Endpoint{
			StreamID:    1,
			IsSecure:    true,
			AccessKey:   t.accessKey,
			KerberosKey: targetKey,
			RecallPID: func(host string) (uint64, bool) {
				pid, ok := recall.take(host)
				if ok {
					log.Printf("[%s] secure: recalled pid=%d for %s", env.Name, pid, host)
				}
				return pid, ok
			},
			OnConnect: func(c *prudplite.Connection) {
				log.Printf("[%s] secure: connected pid=%d cid=%d host=%s", env.Name, c.PID, c.CID, c.Host)
			},
			OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
				return protocols.Dispatch(env, c, msg)
			},
		}

		authLn, err := tls.Listen("tcp", fmt.Sprintf(":%d", t.base), tlsCfg.Clone())
		if err != nil {
			log.Printf("[%s] auth listen :%d: %v", t.name, t.base, err)
			continue
		}
		secureLn, err := tls.Listen("tcp", fmt.Sprintf(":%d", t.base+1), tlsCfg.Clone())
		if err != nil {
			log.Printf("[%s] secure listen :%d: %v", t.name, t.base+1, err)
			continue
		}
		log.Printf("[%s] serving auth :%d secure :%d", t.name, t.base, t.base+1)
		go auth.Listen(authLn)
		go secure.Listen(secureLn)
	}

	select {} // serve forever
}

// handleAuthRMC answers the auth-connection RMCs: TicketGranting 0x0A methods
// 0x02 (LoginEx, the Switch staple) and 0x06 (WithParam, the NEX 4.4+ path —
// the reference measured method 6 as the live login path on 4.4+ titles).
// Method 0x02 returns the flat result body; method 0x06 wraps the same fields
// in a version-1 structure header.
func handleAuthRMC(recall *recall, env *protocols.Env, targetKey []byte, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.Protocol == 0x0A && msg.MethodID == 0x02 {
		return handleLoginEx(recall, env, targetKey, c, msg, false)
	}
	if msg.Protocol == 0x0A && msg.MethodID == 0x06 {
		return handleLoginEx(recall, env, targetKey, c, msg, true)
	}
	log.Printf("[%s] auth unimplemented proto=0x%X method=0x%X (%dB)", env.Name, msg.Protocol, msg.MethodID, len(msg.Body))
	return rmc.EncodeError(msg.Protocol, msg.CallID, rmc.ResultNotImplemented)
}

// handleLoginEx mints the two-layer Switch v1 ticket and returns the
// ValidateAndRequestTicketResult body. withStructHeader selects the method
// 0x06 shape (u8 version + u32 content length ahead of the flat fields).
func handleLoginEx(recall *recall, env *protocols.Env, targetKey []byte, c *prudplite.Connection, msg *rmc.RMCMessage, withStructHeader bool) []byte {
	in := rmc.NewReader(msg.Body)
	if withStructHeader {
		if _, _, err := in.StructHeader(); err != nil { // u8 version, u32 content length
			return rmc.EncodeError(msg.Protocol, msg.CallID, rmc.ResultInvalidArgument)
		}
		if _, err := in.U32(); err != nil { // PlatformType
			return rmc.EncodeError(msg.Protocol, msg.CallID, rmc.ResultInvalidArgument)
		}
	}
	username, _ := in.String()
	pid := pidFor(username)

	sourceKey := make([]byte, env.SourceKeyBytes)
	if _, err := randomRead(sourceKey); err != nil {
		log.Printf("[%s] login: rand: %v", env.Name, err)
		return rmc.EncodeError(0x0A, msg.CallID, rmc.ResultInvalidArgument)
	}
	ticket, _, err := switchnex.Mint(sourceKey, pid, env.SecurePID, targetKey)
	if err != nil {
		log.Printf("[%s] login: mint: %v", env.Name, err)
		return rmc.EncodeError(0x0A, msg.CallID, rmc.ResultInvalidArgument)
	}

	url := env.SecureStationURL()
	now := switchnex.NowDateTime()
	body := switchnexResult(env.Name, url, ticket, now, pid, sourceKey)
	if withStructHeader {
		w := rmc.NewWriter()
		w.StructOf(1, func(n *rmc.Writer) { n.Raw(body) })
		body = w.Bytes()
	}

	recall.remember(c.Host, pid)
	log.Printf("[%s] login pid=%d url=%q ticket=%dB", env.Name, pid, url, len(ticket))
	return rmc.EncodeSuccess(0x0A, msg.CallID, msg.MethodID, body)
}

// envIntegerSettings parses "index:key=value;index:key=value;…" (negative
// values as two's-complement u32), the MPS settings override shape the old
// lineage served.
func envIntegerSettings(varName string) func(uint32) (map[uint16]uint32, uint32) {
	spec := os.Getenv(varName)
	tables := map[uint32]map[uint16]uint32{}
	if spec != "" {
		for _, entry := range strings.Split(spec, ";") {
			parts := strings.Split(strings.TrimSpace(entry), ":")
			if len(parts) != 2 {
				log.Printf("warning: %s: bad entry %q (want index:key=value)", varName, entry)
				continue
			}
			index, err := strconv.ParseUint(parts[0], 10, 32)
			if err != nil {
				log.Printf("warning: %s: bad index %q", varName, parts[0])
				continue
			}
			kv := strings.SplitN(parts[1], "=", 2)
			if len(kv) != 2 {
				log.Printf("warning: %s: bad key=value %q", varName, parts[1])
				continue
			}
			key, err := strconv.ParseUint(kv[0], 10, 16)
			if err != nil {
				log.Printf("warning: %s: bad key %q", varName, kv[0])
				continue
			}
			sval, err := strconv.ParseInt(kv[1], 10, 32)
			if err != nil {
				log.Printf("warning: %s: bad value %q", varName, kv[1])
				continue
			}
			if tables[uint32(index)] == nil {
				tables[uint32(index)] = map[uint16]uint32{}
			}
			tables[uint32(index)][uint16(key)] = uint32(sval)
		}
	}
	return func(index uint32) (map[uint16]uint32, uint32) {
		t, ok := tables[index]
		if !ok {
			return map[uint16]uint32{}, 0
		}
		return t, 0
	}
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + "," {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	return out
}

func randomRead(b []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Read(b)
}
