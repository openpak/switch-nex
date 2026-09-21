// Command switch-nexd serves every Switch-era NEX title on the switch-nex
// lineage: one process, one TLS listener per endpoint (auth on the title's
// port base, secure on base+1) and an HTTP GET /healthz on base+2.
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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	{"mario-party-superstars", 22110, "e915510f", func(e *protocols.Env) {
		e.StationScheme = "prudps"
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.SanitizeDetailFind = true // FindByGatheringIDDetail strips SessionKey/UserPassword
		e.IntegerSettings = envIntegerSettings("MPS_INTEGER_SETTINGS")
		e.Ranking = true
		e.DataStore = protocols.DSEmptyList
	}},
	{"mario-golf-super-rush", 22130, "1cb8027c", func(e *protocols.Env) {
		e.StationScheme = "prudps"
		// The reference's Golf profile is the MODERN shape — its own
		// GOLF_LEGACY_PIA defaults to 0, and the legacy type=0x03 is the
		// opt-in. We had the two the wrong way round, so Golf's public
		// station went back type=3 with no Pa while a Pia 5.19+ title
		// expects type=0x0B and a Pa to punch toward. The era-guess the
		// reference made is now measured: Ryujinx reports Golf linking
		// Pia 5.33.0, well past the 5.19 cutover.
		e.Pia = protocols.Pia519()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		// Ranked Speed and Standard queues share every server field except
		// the search criteria's primary option: without this both modes seat
		// one lobby (2026-09-21, console).
		e.MatchModeAttribute = true
		// The explicit-join family answers the full session, as MK8D and
		// Clubhouse need. Golf's ranked path joins through auto-match, which
		// already echoes the session — this changes nothing there; it covers
		// explicit joins (friend/private rooms) only.
		e.JoinEchoSession = true
		// The reference normalises EVERY session it creates, for every title.
		// Without it Golf's lobby went back keyless, with a participation count
		// of one while two players stood in it: both sides punched through to
		// each other, neither ever started, and each gave up after ~60 s
		// (2026-09-20).
		e.FinalizeCreateSession = true
	}},
	{"arms", 22140, "b6b34c51", func(e *protocols.Env) {
		e.StationScheme = "prudps"
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSStub
	}},
	{"mario-kart-8-deluxe", 22150, "09c1c475", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		// A created session goes back occupied by its host, keyed and
		// timestamped: a session with no key and nobody in it is not one a
		// console can start P2P on.
		e.FinalizeCreateSession = true
		// The join family answers the full session, not a bool: the joiner
		// parses the answer as the MatchmakeSession it is joining and builds
		// its P2P mesh from the echoed pre-join count and session key. A bare
		// bool leaves it nothing to parse — observed as the joiner hanging up
		// two seconds after a room join (2026-09-17).
		e.JoinEchoSession = true
		e.Ranking = true
	}},
	{"super-mario-bros-35", 22160, "0a69c592", func(e *protocols.Env) {
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking2 = true
		e.MatchmakeReferee = true
		e.MessageDelivery = true
		e.SourceKeyBytes = 16 // 16-byte source key attested for this title
		e.IntegerSettings = protocols.SMB35IntegerSettings
	}},
	{"mario-tennis-aces", 22170, "29f267b9", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.ForceOpenParticipation = true // sessions arrive closed; two searchers deadlock
		e.Ranking = true
		e.DataStore = protocols.DSEmptyList
	}},
	{"animal-crossing-new-horizons", 22180, "v43a10em", func(e *protocols.Env) {
		// prudps, not prudp: ACNH builds its secure station with the
		// secure scheme, and on prudp its CONNECT arrives empty and the
		// title dies when Pia needs the session (servers/nex provenance).
		e.StationScheme = "prudps"
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSStub
		// The one title that uses find-by-participant for real: island visits
		// resolve through it, and it needs the session key in the answer.
		e.FindByParticipant = true
		// A created island goes back occupied by its host, keyed and
		// timestamped, and joins answer the full session: without a
		// session key the visitor has nothing to start P2P with, and the
		// Dodo-code browse result is unusable (2026-09-21, NMT18 visit).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
	}},
	{"luigis-mansion-3", 22190, "aab95246", func(e *protocols.Env) {
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.Presence = true // 0x79 station/presence stub
	}},
	{"mario-strikers-battle-league", 22300, "MAGNI5jG", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSEmptyList
		// Its Pia interrogates five protocols at boot and each one needs the
		// right answer SHAPE (protocols/strikers.go).
		e.StrikersInit = true
	}},
	{"minecraft", 22310, "f4983d87", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
	}},
	{"splatoon-2", 22320, "4eb18d39", func(e *protocols.Env) {
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		// This title takes an empty body for every DataStore method, GetMeta
		// included — not the NotFound the others want.
		e.DataStore = protocols.DSEmptySuccess
	}},
	{"super-mario-maker-2", 22330, "fdf6617f", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSSMM2
	}},
	{"super-mario-odyssey", 22340, "afef0ecf", func(e *protocols.Env) {
		// No matchmaking: Odyssey has no lobby or P2P — Utility, Ranking and
		// DataStore only.
		e.StationScheme = "prudps"
		e.Ranking = true
		e.DataStore = protocols.DSBalloonErrors
	}},
	{"super-smash-bros-ultimate", 22350, "9587602b", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSEmptyList
	}},
	{"super-mario-3d-world-bowsers-fury", 22370, "67f366f2", func(e *protocols.Env) {
		// Game server 2B306A00, key and NEX 4.6.4 read from our v1.2.2 dump
		// (Openpak/prds/switch-catalogue-prd.md). The delay and the pre-join
		// count are the reference's measured matchmaking policy.
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.JoinEchoSession = true
		e.ParticipateDelay = 100 * time.Millisecond
	}},
	{"kirbys-dream-buffet", 22380, "6wVacwwe", func(e *protocols.Env) {
		// Game server 25374500 and key read from our v0 dump; nothing measured
		// yet. Ranking2 and DataStore are the components the binary links —
		// the first boot's NotImplemented log is the real backlog.
		// prudps, not prudp: Kirby's NEX picks its secure transport by scheme,
		// and only SecureStream (prudps) sends the ticket in CONNECT. On prudp
		// it connects with an empty payload and the server has to guess the
		// PID (decompiled 2026-09-19: CanHandle 0x4f13dc vs 0x4bca70).
		e.StationScheme = "prudps"
		e.Match = protocols.NewMatchmakingStore()
		// No ForceOpenParticipation: the host opens its room itself once its NAT
		// check and station update are done (~2 s). Forced open, a joiner got in
		// first, fetched the host's pre-NAT station (port=1, CID=0) and probed
		// nothing until the host's 10 s lobby had closed (2618-0502, 2026-09-19).
		// Two players searching within those ~2 s each host their own room.
		// A joiner that got the host's room sat 10 s without asking for any
		// station, then left (EndParticipation): the room had no session key
		// for it to join with. Clubhouse's create-time normalisation mints one.
		e.FinalizeCreateSession = true
		e.Ranking2 = true
		e.DataStore = protocols.DSEmptyList
	}},
	{"clubhouse-games-51", 22870, "8c3661ff", func(e *protocols.Env) {
		// Clubhouse Games: 51 Worldwide Classics (010047700D540000, game
		// server 2035BB00). Account setup runs DataStore before matchmaking;
		// sessions arrive closed, so two searchers each host and never match —
		// the same open-participation fix as Mario Tennis Aces, plus the
		// create-time normalisation and full-session join answers.
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSClubhouse
		e.ForceOpenParticipation = true
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
	}},
}

// dumpTitles prints each title's configuration as newService resolves it, so
// what every game needs can be checked against one list.
func dumpTitles() {
	for _, t := range titles {
		e := protocols.NewEnv(t.name, *stationAddress, t.base)
		if t.env != nil {
			t.env(e)
		}
		pia := "5.19 (type 0x0B, Pa)"
		if !e.Pia.SetPa {
			pia = "legacy (type 0x03, no Pa)"
		}
		var behaviour []string
		for _, f := range []struct {
			on   bool
			name string
		}{
			{e.IntegerSettings != nil, "IntegerSettings table"},
			{e.ForceOpenParticipation, "ForceOpenParticipation"},
			{e.FindByParticipant, "FindByParticipant"},
			{e.SanitizeDetailFind, "SanitizeDetailFind"},
			{e.FinalizeCreateSession, "FinalizeCreateSession"},
			{e.JoinEchoSession, "JoinEchoSession"},
			{e.StrikersInit, "StrikersInit"},
			{e.ParticipateDelay > 0, "ParticipateDelay " + e.ParticipateDelay.String()},
		} {
			if f.on {
				behaviour = append(behaviour, f.name)
			}
		}
		if len(behaviour) == 0 {
			behaviour = []string{"none"}
		}
		fmt.Printf("## %s\n", t.name)
		fmt.Printf("- ports: auth %d, secure %d, healthz %d\n", t.base, t.base+1, t.base+2)
		fmt.Printf("- access key: %s\n", t.accessKey)
		fmt.Printf("- station URL: %s\n", e.SecureStationURL())
		fmt.Printf("- source key: %d bytes\n", e.SourceKeyBytes)
		fmt.Printf("- Pia: %s\n", pia)
		fmt.Printf("- DataStore: %s\n", e.DataStore)
		fmt.Printf("- behaviour: %s\n", strings.Join(behaviour, ", "))
		fmt.Printf("- protocols: %s\n\n", strings.Join(protocols.Served(e), ", "))
	}
}

// securePassword is every title's secure-account kerberos password. Both the
// auth endpoint (ticket encryption) and the secure endpoint (ticket
// decryption) derive their key from it, so any value works as long as it is
// the same on both sides.
const securePassword = "openpak-switch-nex-secure"

var (
	stationAddress  = flag.String("station-address", "145.241.199.19", "address carried in every station URL (raw IP; the console dials it without DNS)")
	certFile        = flag.String("cert", "/app/nex.pem", "TLS certificate (CN covers *.s.n.srv.nintendo.net)")
	keyFile         = flag.String("key", "/app/nex.key", "TLS key")
	only            = flag.String("only", "", "comma-separated title names to serve (default: all)")
	recallFile      = flag.String("recall-file", "", "file the IP-to-PID recall table is persisted to (empty: memory only, lost on restart)")
	identityURL     = flag.String("identity-url", "", "nx-baas internal base URL; empty disables account resolution and every login is accepted on its unproven name")
	identityKey     = flag.String("identity-key", "", "shared key for the nx-baas internal API (X-Internal-Key)")
	requireIdentity = flag.Bool("require-identity", false, "refuse logins whose token cannot be proven, instead of falling back to the name")
	dump            = flag.Bool("dump", false, "print every served title's resolved configuration and exit")
	loginExStruct   = flag.Bool("loginex-structured", false, "answer TicketGranting method 2 with the ValidateAndRequestTicketResult structure instead of the flat layout (see docs/login-shape-conflict.md)")
)

// recall remembers login PID by client IP for the ticketless secure stage, and
// the login name a title may later ask about instead of a PID.
//
// It SURVIVES A RESTART. The table used to live only in memory, so restarting
// the server refused every console already mid-session that re-CONNECTed to a
// secure port without replaying a full login — and a console does that often,
// on every lobby change and reconnect. Writes are debounced off the request
// path and the file is re-read at startup.
type recall struct {
	mu    sync.Mutex
	m     map[string]recallEntry
	names map[string]uint64 // login name -> PID
	ttl   time.Duration
	file  string
	dirty atomic.Bool
}

type recallEntry struct {
	PID uint64 `json:"pid"`
	At  int64  `json:"at"` // unix seconds
}

// recallMax bounds the table: a recall table must not grow without end.
const recallMax = 50000

// recallFlushEvery is how often a dirty table reaches the disk.
const recallFlushEvery = 5 * time.Second

func newRecall(ttl time.Duration, file string) *recall {
	r := &recall{m: map[string]recallEntry{}, names: map[string]uint64{}, ttl: ttl, file: file}
	r.load()
	if file != "" {
		go r.flushLoop()
	}
	return r
}

func (r *recall) remember(host string, pid uint64) {
	r.mu.Lock()
	r.m[host] = recallEntry{PID: pid, At: time.Now().Unix()}
	r.mu.Unlock()
	r.dirty.Store(true)
}

func (r *recall) take(host string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[host]
	if !ok || e.PID == 0 {
		return 0, false
	}
	if time.Since(time.Unix(e.At, 0)) > r.ttl {
		// Expire on read as well as on flush: an IP-to-player association
		// that outlived its session would otherwise be inherited by whoever
		// holds that address next, which behind CGNAT is a stranger.
		delete(r.m, host)
		return 0, false
	}
	return e.PID, true
}

// recallByTitleWindow is how long a login stays claimable by title alone. The
// game opens its secure connection within a second of the login answer.
const recallByTitleWindow = 30 * time.Second

func titleKey(name string) string { return "title:" + name }

// addressSaysNothing reports whether a source address is one a NAT put there
// rather than the client's own.
func addressSaysNothing(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

// takeRecent is take with a tighter age limit than the table's.
func (r *recall) takeRecent(key string, window time.Duration) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[key]
	if !ok || e.PID == 0 || time.Since(time.Unix(e.At, 0)) > window {
		return 0, false
	}
	return e.PID, true
}

// rememberName records the login-name-to-PID mapping. Some titles never speak
// our internal PID: they ask for profiles by the name the console logged in
// under, and only the login ever saw both halves, so the mapping cannot be
// recovered later by arithmetic.
//
// ponytail: the table is kept but nothing reads it yet — the profile store it
// feeds does not exist. Serving a profile means emitting the title's 26-field
// user structure, and a partial one shifts every field after it, so the current
// "no profile stored" answer is the correct one until there is real data behind
// it. Wire this up when the store lands, not before.
func (r *recall) rememberName(name string, pid uint64) {
	if name == "" || pid == 0 {
		return
	}
	r.mu.Lock()
	r.names[name] = pid
	r.mu.Unlock()
}

// pidForName resolves a login name to the PID it logged in as.
func (r *recall) pidForName(name string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pid, ok := r.names[name]
	return pid, ok
}

func (r *recall) load() {
	if r.file == "" {
		return
	}
	b, err := os.ReadFile(r.file)
	if err != nil {
		return
	}
	var m map[string]recallEntry
	if json.Unmarshal(b, &m) != nil {
		return
	}
	n := 0
	for host, e := range m {
		if e.PID == 0 || time.Since(time.Unix(e.At, 0)) > r.ttl {
			continue
		}
		r.m[host] = e
		n++
	}
	log.Printf("recall: restored %d entry(ies) from %s", n, r.file)
}

func (r *recall) flushLoop() {
	t := time.NewTicker(recallFlushEvery)
	defer t.Stop()
	for range t.C {
		if !r.dirty.Swap(false) {
			continue
		}
		r.flush()
	}
}

func (r *recall) flush() {
	r.mu.Lock()
	out := make(map[string]recallEntry, len(r.m))
	for host, e := range r.m {
		if time.Since(time.Unix(e.At, 0)) > r.ttl {
			delete(r.m, host)
			continue
		}
		if len(out) < recallMax {
			out[host] = e
		}
	}
	r.mu.Unlock()

	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	tmp := r.file + ".tmp"
	// 0600: this file is a list of which address played as whom.
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, r.file) // atomic replace
	}
}

// orEnv prefers an explicitly given value, falling back to the environment.
func orEnv(given, envVar string) string {
	if given != "" {
		return given
	}
	return os.Getenv(envVar)
}

// pidFor returns a stable NEX PID for a login username.
//
// ACNH (and potentially other titles with an out-of-band account id) logs in
// with its NUMERIC account id as the username — observed on the wire
// 2026-09-21 ("12297829382472990721", "12297829382472990722"). A numeric
// username IS the principal: hashing it (below) mints a second, unrelated
// identity, and anything that resolves by id — FindMatchmakeSessionBy-
// Participant island visits included — looks for the number the game knows
// and never finds the hash. So a username that parses as a nonzero uint64
// is used verbatim; only opaque names fall back to the hash.
func pidFor(username string) uint64 {
	if id, err := strconv.ParseUint(username, 10, 64); err == nil && id != 0 {
		return id
	}
	h := sha512.Sum512_256([]byte("openpak-switch-nex-pid:" + username))
	return 0x6C000000 | uint64(binary.BigEndian.Uint32(h[:4])&0x00FFFFFF)
}

func main() {
	flag.Parse()
	if *dump {
		dumpTitles()
		return
	}
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

	// A day covers a whole play session: a console re-CONNECTs to the secure
	// port repeatedly without replaying the full auth, so a short window
	// refuses legitimate players mid-session.
	recall := newRecall(24*time.Hour, *recallFile)

	// The key is a secret, so the environment is the better home for it: a
	// command-line flag shows up in ps for every user on the box. Flags still
	// win when given, which keeps a one-off run easy to drive.
	ids := newIdentity(
		orEnv(*identityURL, "NX_INTERNAL_URL"),
		orEnv(*identityKey, "NX_INTERNAL_KEY"),
	)
	switch {
	case !ids.enabled():
		log.Printf("identity: DISABLED — every login is accepted on its unproven name, and nx-baas never sees the player")
	case *requireIdentity:
		log.Printf("identity: enforcing — a login whose token does not prove is refused")
	default:
		// The RESOLVED url, not the flag: the value usually arrives by
		// environment, and a startup line that reports where it is resolving
		// is worthless if it reports the wrong place.
		log.Printf("identity: resolving against %s, falling back to the login name when a token does not prove", ids.url)
	}

	for _, t := range titles {
		if len(want) > 0 && !want[t.name] {
			continue
		}
		svc := newService(t, *stationAddress, recall, ids)
		// healthz is bound whatever happens to the game ports: a title whose
		// listener failed answers 503 there, which says more than a refused
		// connection does.
		serveHealth(fmt.Sprintf(":%d", t.base+2), svc)

		authLn, err := tls.Listen("tcp", fmt.Sprintf(":%d", t.base), tlsCfg.Clone())
		if err != nil {
			log.Printf("[%s] auth listen :%d: %v", t.name, t.base, err)
			continue
		}
		secureLn, err := tls.Listen("tcp", fmt.Sprintf(":%d", t.base+1), tlsCfg.Clone())
		if err != nil {
			log.Printf("[%s] secure listen :%d: %v", t.name, t.base+1, err)
			// Half a title is no title: a login would hand out a secure
			// station nobody answers. Release the auth port too.
			authLn.Close()
			continue
		}
		log.Printf("[%s] serving auth :%d secure :%d healthz :%d", t.name, t.base, t.base+1, t.base+2)
		svc.serve(authLn, secureLn)
	}

	select {} // serve forever
}

// service is one title's pair of endpoints plus the state they share. Built
// away from main so a test can drive the whole stack in process: the harness
// exercises the same construction production does, which is the only way an
// end-to-end assertion means anything.
type service struct {
	env       *protocols.Env
	targetKey []byte
	auth      *prudplite.Endpoint
	secure    *prudplite.Endpoint

	// base is the title's port block; healthz reports base and base+1.
	base int
	// authUp and secureUp are set once each endpoint is serving on a bound
	// listener. healthz is 200 only while both are.
	authUp, secureUp atomic.Bool
}

// newService wires one title: the auth endpoint that mints tickets and the
// secure endpoint that spends them, sharing the derived secure-account key.
func newService(t title, stationAddress string, recall *recall, ids *identity) *service {
	env := protocols.NewEnv(t.name, stationAddress, t.base)
	if t.env != nil {
		t.env(env)
	}
	if *loginExStruct {
		env.LoginExStructured = true
	}
	targetKey := switchnex.DeriveKey([]byte(securePassword), env.SecurePID)

	s := &service{env: env, targetKey: targetKey, base: t.base}
	s.auth = &prudplite.Endpoint{
		Name:      t.name + "/auth",
		StreamID:  1,
		AccessKey: t.accessKey,
		OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
			return handleAuthRMC(ids, recall, env, targetKey, c, msg)
		},
	}
	s.secure = &prudplite.Endpoint{
		Name:        t.name + "/secure",
		StreamID:    1,
		IsSecure:    true,
		AccessKey:   t.accessKey,
		KerberosKey: targetKey,
		RecallPID: func(host string) (uint64, bool) {
			// A ticketless CONNECT inherits the PID that logged in from this
			// very address. That is leniency, not authentication: the Lite
			// signature proves knowledge of the title's PUBLIC access key,
			// never identity.
			//
			// Harmless while every player is an unproven name. The moment
			// identities are real it is an account-takeover path for anyone
			// sharing a public address — a CGNAT range, or one household — so
			// once logins are enforced the ticket is the only way in.
			if *requireIdentity {
				log.Printf("[%s] secure: ticketless CONNECT from %s refused (identity enforced)", env.Name, host)
				return 0, false
			}
			pid, ok := recall.take(host)
			if !ok {
				// ponytail: the auth stage rides Traefik, so a login is
				// recorded under Traefik's address, never the console's; the
				// secure dial arrives with the console's real address (host
				// networking) or a rewritten one (any forwarder). Either way
				// the address cannot pair the two stages, and the client —
				// which dials secure ticketless — starves on silence. The
				// title's latest login inside a short window is the pairing.
				// Ceiling: two players logging in to one title within
				// recallByTitleWindow can be handed each other's PID. Real
				// fix: PROXY protocol from Traefik, so the login sees the
				// console's address too and by-IP pairing turns exact.
				pid, ok = recall.takeRecent(titleKey(env.Name), recallByTitleWindow)
			}
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
		OnDisconnect: func(c *prudplite.Connection) {
			env.Disconnected(c.PID)
		},
	}
	// The secure endpoint is also the path the server originates on:
	// notification events and NAT probe relays go back out over it.
	env.BindPush(s.secure)
	return s
}

// serve starts both endpoints on already-open listeners.
func (s *service) serve(authLn, secureLn net.Listener) {
	go s.auth.Listen(authLn)
	go s.secure.Listen(secureLn)
	s.authUp.Store(true)
	s.secureUp.Store(true)
}

// healthz answers GET /healthz in the servers/nex shape:
// "ok <title> auth=<port>/<transport> secure=<port>/<transport>", 200 when
// both of the title's listeners are serving, otherwise 503 with the same line
// headed "down" and the listeners that are not up named at the end.
func (s *service) healthz(w http.ResponseWriter, _ *http.Request) {
	line := fmt.Sprintf("%s auth=%d/tls secure=%d/tls", s.env.Name, s.base, s.base+1)
	var down []string
	if !s.authUp.Load() {
		down = append(down, "auth")
	}
	if !s.secureUp.Load() {
		down = append(down, "secure")
	}
	if len(down) > 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "down %s not listening: %s\n", line, strings.Join(down, ","))
		return
	}
	fmt.Fprintf(w, "ok %s\n", line)
}

// serveHealth binds s's healthz on addr (all interfaces, like the game ports)
// and serves it for the life of the process. A failed bind is logged, not
// fatal: the title's game ports matter more than its probe.
func serveHealth(addr string, s *service) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[%s] healthz listen %s: %v", s.env.Name, addr, err)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
}

// handleAuthRMC answers the auth-connection RMCs: TicketGranting 0x0A methods
// 0x02 (LoginEx, the Switch staple) and 0x06 (WithParam, the NEX 4.4+ path —
// the reference measured method 6 as the live login path on 4.4+ titles).
// Method 0x02 returns the flat result body; method 0x06 wraps the same fields
// in a version-1 structure header.
func handleAuthRMC(ids *identity, recall *recall, env *protocols.Env, targetKey []byte, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.Protocol == 0x0A && (msg.MethodID == 0x01 || msg.MethodID == 0x02) {
		// Login (1) and LoginEx (2) take the same answer.
		return handleLoginEx(ids, recall, env, targetKey, c, msg, false)
	}
	if msg.Protocol == 0x0A && msg.MethodID == 0x06 {
		return handleLoginEx(ids, recall, env, targetKey, c, msg, true)
	}
	log.Printf("[%s] auth unimplemented proto=0x%X method=0x%X (%dB)", env.Name, msg.Protocol, msg.MethodID, len(msg.Body))
	return rmc.EncodeError(msg.Protocol, msg.CallID, rmc.ResultNotImplemented)
}

// resolveLogin turns a login into a principal id.
//
// With identity configured the BAAS token in the login's extra data is proved
// by nx-baas and its account's PID is what goes into the ticket — so the
// secure stage, which already recovers the PID from that ticket, becomes
// authenticated end to end with no further work.
//
// Without it, or when a token will not prove and enforcement is off, the PID is
// derived from the login NAME. That name is an unverified claim: anyone who
// reaches this port can send any name and be issued a ticket for it. The
// fallback exists so the fleet can be migrated without locking every player out
// on the first deploy, and it is logged every time precisely because it should
// not be permanent.
// The refusal code travels back with the failure rather than in shared state:
// logins from different consoles run concurrently, and a package-level variable
// would hand one console the reason another was refused.
func resolveLogin(ids *identity, env *protocols.Env, username string, in *rmc.Reader) (pid uint64, refusal uint32, ok bool) {
	if !ids.enabled() {
		return pidFor(username), 0, true
	}

	token, found := authenticationToken(in)
	if !found {
		if *requireIdentity {
			log.Printf("[%s] login refused: no authentication token in the login", env.Name)
			return 0, rmc.ResultAuthTokenParse, false
		}
		log.Printf("[%s] login UNPROVEN (no token): falling back to the name %q", env.Name, username)
		return pidFor(username), 0, true
	}

	p, err := ids.resolve(token)
	if err != nil {
		if *requireIdentity {
			log.Printf("[%s] login refused: %v", env.Name, err)
			return 0, loginResultCode(err), false
		}
		log.Printf("[%s] login UNPROVEN (%v): falling back to the name %q", env.Name, err, username)
		return pidFor(username), 0, true
	}
	log.Printf("[%s] login pid=%d nickname=%q", env.Name, p.PID, p.Nickname)
	return p.PID, 0, true
}

// handleLoginEx mints the two-layer Switch v1 ticket and returns the login
// result. The two methods do NOT share a body: 0x02 is the flat LoginEx layout
// (retval, PID, ticket, RVConnectionData, message, source key) and 0x06 is the
// ValidateAndRequestTicketResult structure, which has no retval and carries the
// secure endpoint as a plain URL string plus the server time. Answering 0x06
// with the LoginEx layout wrapped in a header is rejected as an invalid
// argument.
func handleLoginEx(ids *identity, recall *recall, env *protocols.Env, targetKey []byte, c *prudplite.Connection, msg *rmc.RMCMessage, withStructHeader bool) []byte {
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

	pid, refusal, ok := resolveLogin(ids, env, username, in)
	if !ok {
		// The code matters: a retryable one for an account we simply do not
		// know puts the console in a refresh loop.
		return rmc.EncodeError(0x0A, msg.CallID, refusal)
	}

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
	// Method 6 always answers the structure. Method 6's shape is settled; the
	// method-2 shape is the one two lineages disagree about, so it is a
	// per-title switch rather than a constant.
	var body []byte
	if withStructHeader || env.LoginExStructured {
		body = switchnexTicketResult(env.Name, url, ticket, now, pid, sourceKey)
	} else {
		body = switchnexResult(env.Name, url, ticket, now, pid, sourceKey)
	}

	recall.remember(c.Host, pid)
	recall.remember(titleKey(env.Name), pid)
	recall.rememberName(username, pid)
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
