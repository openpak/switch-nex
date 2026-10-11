// Command switch-nexd serves every Switch-era NEX title on the switch-nex
// lineage: one process, one TLS listener per endpoint (auth on the title's
// port base, secure on base+1) and an HTTP GET /healthz on base+2 (which also
// answers the internal voice-chat gathering lookup, see voice.go).
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
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/protocols"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
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
		// The game's own six built-in values unless the deploy overrides them.
		e.IntegerSettings = protocols.MPSIntegerSettings
		if os.Getenv("MPS_INTEGER_SETTINGS") != "" {
			e.IntegerSettings = envIntegerSettings("MPS_INTEGER_SETTINGS")
		}
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
		// FinalizeParams stays ON for Golf, unlike the bring-up default: Golf's
		// ranked lobbies were console-measured working with @SR/@GIR stamped
		// (2026-10-09 sweep, a full 3-hole round), and its private-room joiner
		// parsed the joined session cleanly. Only ACNH is known to reject them.
		e.FinalizeParams = true
		// Private rooms join explicitly (room code -> 0x6D.0x27), which without
		// this pushes only 3001 to the host and 122000 to the joiner. The joiner
		// never hears the host is there, sits exactly 10 s, sends
		// EndParticipation and shows "Unable to enter the room." (sweep
		// 2026-10-10, gid 581211). Ranked already got the recap: auto-match
		// always sends it.
		e.JoinRecapParticipate = true
	}},
	{"arms", 22140, "b6b34c51", func(e *protocols.Env) {
		e.StationScheme = "prudps"
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSStub
		// Not console-measured for this title: applied from the pattern that held
		// on every Switch NEX title actually tested on 2026-09-25 (Risk of Rain,
		// Risk of Rain Returns, Streets of Rage 4). Nothing here was ever measured
		// working WITHOUT them -- this title has simply never been on a console.
		// FinalizeParams stays off (it breaks ACNH).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
	}},
	{"mario-kart-8-deluxe", 22150, "09c1c475", func(e *protocols.Env) {
		e.Match = protocols.NewMatchmakingStore()
		// A created session goes back occupied by its host, keyed and
		// timestamped: a session with no key and nobody in it is not one a
		// console can start P2P on.
		e.FinalizeCreateSession = true
		e.FinalizeParams = true
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
		// Mario Tennis Aces (0100BDE00862A000, update 3.1.1): NEX 4.3.5 with
		// Pia 5.9.6 (the SDK banners in its main NSO, not the 5.19 the parity
		// table guessed). The public station stays the 5.19 shape: it is what
		// the reference served this title, and the punch is measured working.
		e.Match = protocols.NewMatchmakingStore()
		e.ForceOpenParticipation = true // sessions arrive closed; two searchers deadlock
		// The reference this title was measured working against normalises
		// every session it creates: host counted, a 32-byte session key, the
		// start time, attributes padded to 6, @SR/@GIR stamped. Without it the
		// session kept whatever count and key the creating client sent, and a
		// join only added one to that count: matchmaking, the punch and the Pia
		// mesh all completed, then both consoles sat on the match loading screen
		// with no call and no error (2026-10-11, a real Switch and Harbor on
		// the ROG 3, gid 741123 and 741124, both roles). Every title that
		// plays peer-to-peer here has this; Tennis was one of five without.
		e.FinalizeCreateSession = true
		e.FinalizeParams = true
		// Friend joins ("join a friend's game") take the explicit-join path:
		// the full session echoed, and the recap so the joiner learns who is
		// already there.
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
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
		// FinalizeParams too: the reference stamps @SR/@GIR on every ACNH
		// island and its Dodo-code visits work. v0.2.36 dropped them for
		// ACNH blaming a 2306-0116 on a Dodo-code browse result, but that
		// 0116 was the ResultRange prefix on the 0x34 answer (fixed in
		// v0.2.37): with a correctly parsed, code-matched, open island and
		// no params, the visitor still says "no island I can take you to"
		// (console-measured 2026-09-22, code 0JJJK).
		e.FinalizeCreateSession = true
		e.FinalizeParams = true
		e.JoinEchoSession = true
		// The visitor waits for the join recap (Participate per existing
		// participant) before it will build its mesh; see the flag.
		e.JoinRecapParticipate = true
	}},
	{"luigis-mansion-3", 22190, "aab95246", func(e *protocols.Env) {
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.Presence = true // 0x79 station/presence stub
		// Not console-measured for this title: applied from the pattern that held
		// on every Switch NEX title actually tested on 2026-09-25 (Risk of Rain,
		// Risk of Rain Returns, Streets of Rage 4). Nothing here was ever measured
		// working WITHOUT them -- this title has simply never been on a console.
		// FinalizeParams stays off (it breaks ACNH).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
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
		// Not console-measured for this title: applied from the pattern that held
		// on every Switch NEX title actually tested on 2026-09-25 (Risk of Rain,
		// Risk of Rain Returns, Streets of Rage 4). Nothing here was ever measured
		// working WITHOUT them -- this title has simply never been on a console.
		// FinalizeParams stays off (it breaks ACNH).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
	}},
	{"splatoon-2", 22320, "4eb18d39", func(e *protocols.Env) {
		e.Pia = protocols.LegacyPia()
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		// This title takes an empty body for every DataStore method, GetMeta
		// included — not the NotFound the others want.
		e.DataStore = protocols.DSEmptySuccess
		// Not console-measured for this title: applied from the pattern that held
		// on every Switch NEX title actually tested on 2026-09-25 (Risk of Rain,
		// Risk of Rain Returns, Streets of Rage 4). Nothing here was ever measured
		// working WITHOUT them -- this title has simply never been on a console.
		// FinalizeParams stays off (it breaks ACNH).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
	}},
	{"super-mario-maker-2", 22330, "fdf6617f", func(e *protocols.Env) {
		// prudps: on prudp the console completes the handshake and then sends
		// an EMPTY CONNECT, no ticket, and SMM2 dies at matchmaking (the
		// reference's measured note, servers/nex SMM2 provenance).
		e.StationScheme = "prudps"
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.DataStore = protocols.DSSMM2
		// Not console-measured for this title: applied from the pattern that held
		// on every Switch NEX title actually tested on 2026-09-25 (Risk of Rain,
		// Risk of Rain Returns, Streets of Rage 4). Nothing here was ever measured
		// working WITHOUT them -- this title has simply never been on a console.
		// FinalizeParams stays off (it breaks ACNH).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
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
		// Not console-measured for this title: applied from the pattern that held
		// on every Switch NEX title actually tested on 2026-09-25 (Risk of Rain,
		// Risk of Rain Returns, Streets of Rage 4). Nothing here was ever measured
		// working WITHOUT them -- this title has simply never been on a console.
		// FinalizeParams stays off (it breaks ACNH).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
	}},
	{"super-mario-3d-world-bowsers-fury", 22370, "67f366f2", func(e *protocols.Env) {
		// Game server 2B306A00, key and NEX 4.6.4 read from our v1.2.2 dump
		// (Openpak/prds/switch-catalogue-prd.md). The delay and the pre-join
		// count are the reference's measured matchmaking policy.
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.JoinEchoSession = true
		e.ParticipateDelay = 100 * time.Millisecond
		// Listed-room joins (Online Play > Join Game, 0x6D.0x27) failed the
		// 2026-10-10 sweep with the bring-up notes' two signatures at once: the
		// join echo said count=0 (the created room counted nobody and carried no
		// session key) and the joiner, told only 122000 about itself, left
		// exactly 10 s later with "An error has occurred". FinalizeParams stays
		// off: nothing measured says this title wants @SR/@GIR.
		e.FinalizeCreateSession = true
		e.JoinRecapParticipate = true
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
		// Two players searching in that window used to host a room each
		// (2026-10-11, 37 ms apart); the second search now waits for the first
		// room to open, up to 4 s, and joins it.
		e.HoldForOpening = 4 * time.Second
		e.FinalizeCreateSession = true
		e.FinalizeParams = true
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
		e.FinalizeParams = true
		e.JoinEchoSession = true
	}},
	{"risk-of-rain", 22890, "61b8f3a6", func(e *protocols.Env) {
		// Risk of Rain (0100AAA00ACBE800, game server 29C91300, nnSdk 4.5.0).
		// Both values are read from the game's own RomFS config, not guessed:
		// nx/config.csv holds game_server_id 0x29C91300 and ngs_access_key
		// 61b8f3a6 (the binary's *game_server_id / *ngs_access_key defaults are
		// the NEX sample demo's 0x27851C01 / 12345678 and must not be used).
		// No Pia: this is bare NEX 4.5, ws/wss with prudp and prudps schemes.
		// It links MatchmakeExtension, Ranking, Ranking2, MatchmakeReferee,
		// DataStore, NATTraversal and SecureConnection; which of them it
		// actually calls is the first boot's NotImplemented log. Not
		// console-verified.
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.Ranking2 = true
		e.MatchmakeReferee = true
		e.DataStore = protocols.DSEmptyList
		// Console-measured 2026-09-25 (Eden hosting, Ryujinx joining): the
		// joiner takes the session, then 2 s later sends MatchMakingExt
		// EndParticipation without ever asking for participant URLs — the
		// ACNH shape. It only learns of itself from AddedToGathering (122000)
		// and never hears about the host, so the recap is what it waits on.
		e.JoinRecapParticipate = true
		// Console-measured 2026-09-25 (a real Switch and Ryujinx, same shape):
		// hosting creates the gathering and reports NAT, then the game dies
		// two seconds later -- its own log says `netgroup: OpenSocket` /
		// `Open failed` with NOT ONE socket call behind it, and it then calls
		// a method on the object Open never built. A failure decided before
		// any syscall is a parse of something already in hand, and the only
		// thing we hand it is its own registered station. This title has no
		// Pia at all, and we were advertising the 5.19 public shape
		// (type=0x0B plus Pa=) that its NEX 4.5 station parser predates.
		// Risk of Rain Returns is NEX 4.6 and takes that shape happily, which
		// is why it hosts on the same daemon. First user of LegacyPia.
		e.Pia = protocols.LegacyPia()
		// The legacy shape was correct and changed nothing: the host still dies
		// at the same instruction. Create is handled by identical code for both
		// Risk of Rain titles and Returns hosts fine from the same console, so
		// the only server-side lever left on this path is what the create
		// ANSWER contains. Clubhouse's normalisation is the one thing that
		// changes it: count the host as its own first participant, mint a
		// session key, stamp the start time. A host whose own session reports
		// zero participants is a candidate for the empty object the game then
		// calls a method on. Experiment, not a diagnosis -- if this does not
		// take, the next step is the title's own code at 0x184838.
		e.FinalizeCreateSession = true
		// That fixed the host: the Switch now creates a gathering and holds it,
		// and the guest log reads `netgroup: OpenSocket` with no `Open failed`
		// after it. The joiner then joined cleanly and sat 19 s doing nothing --
		// no GetParticipantsURLs, no probe request -- before giving up and
		// creating its own session. Same shape Risk of Rain Returns was measured
		// with on 0x6D.0x27: the bare Bool(true) is one byte where the client
		// reads a whole session.
		e.JoinEchoSession = true
	}},
	{"streets-of-rage-4", 22880, "14c73f27", func(e *protocols.Env) {
		// Streets of Rage 4 (0100EC9010258000, game server 27C90000, NEX 4.6.3,
		// Pia 5.26.2). Key read from our v0 dump, next to the game's network
		// strings. The binary links NAT traversal and the notification
		// protocols only: no Ranking, no DataStore.
		e.Match = protocols.NewMatchmakingStore()
		// Console-measured 2026-09-25, and predicted before it was measured:
		// this title was the only served matchmaking title left reporting
		// `behaviour: none` in -dump, so it had to fail the way Risk of Rain
		// and Risk of Rain Returns did. It does -- 2306-0116 on join. Same
		// pair, same reasons: createSession otherwise leaves ParticipationCount
		// at 0 with an empty SessionKey, and the join family answers a bare
		// Bool(true) where the client parses a whole session.
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		// Console-measured 2026-09-25, after the two above: the join now
		// succeeds (gid=451565, count=1, no more 2306-0116) and the joiner then
		// sits exactly 10 s, sends EndParticipation and shows "Connection was
		// lost". It had been told only AddedToGathering (122000) about itself and
		// nothing about the host. That ten-seconds-then-EndParticipation signature
		// is the one ACNH was measured with.
		e.JoinRecapParticipate = true
	}},
	{"risk-of-rain-returns", 23000, "", func(e *protocols.Env) {
		// Risk of Rain Returns (0100D1801A0F4000, game server 45559E00). The
		// game server id is console-measured, not guessed: the title resolves
		// g45559e00-lp1.s.n.srv.nintendo.net and nothing else, on three
		// separate attempts in one Ryujinx run (2026-09-25). It is NEX despite
		// the GameMaker runner exporting the whole switch_npln_* API — the
		// runner links both stacks and this title picks NEX, and it never asks
		// for a t-*-lp1 NPLN tenant.
		//
		// Access key unknown, and empty on purpose. Unlike Risk of Rain it
		// ships no nx/config.csv (its RomFS has none), the key is not in
		// game.win, and no 8-hex string sits in main/subsdk0/subsdk1/sdk, so
		// it is built as an ARM64 immediate. Empty means CONNECT signatures go
		// unverified, which costs nothing here: the server never signs its own
		// CONNECT-Ack, so the client has nothing of ours to check. Recovering
		// it later is a brute force over one logged handshake — the signature
		// is HMAC-MD5(md5(key), md5(key) || connSig) over 32 bytes.
		//
		// Same protocol set as Risk of Rain, read from the binary:
		// MatchmakeExtension, Ranking, Ranking2, MatchmakeReferee, DataStore,
		// NATTraversal, SecureConnection. The game drives switch_matchmaking_*
		// and switch_leaderboard_{get_scores,post_score,post_common_data}.
		// It calls session_open_participation / session_close_participation
		// itself, so participation is left alone rather than forced open.
		// Console-verified playable 2026-09-25: Ryujinx hosted gid=450172 and a
		// real Switch joined it, count=1, session held. Zero NotImplemented
		// across every run -- Ranking, Ranking2, MatchmakeReferee and DataStore
		// stay registered but were never called.
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.Ranking2 = true
		e.MatchmakeReferee = true
		e.DataStore = protocols.DSEmptyList
		// Console-measured 2026-09-25 (Ryujinx hosting, Eden joining): the
		// host's lobby is browsable and the joiner finds it, then JoinMatchmakeSession
		// (0x6D.0x27) fails on screen with 2306-0116 -- Core::InvalidArgument,
		// a parse rejection, not a timeout. The bare Bool(true) we answered is
		// one byte where the client reads a whole session, the same shift ACNH
		// documents for the same code. Echo the session back instead.
		e.JoinEchoSession = true
		// Console-measured 2026-09-25 (Switch and Ryujinx, either side hosting):
		// with the join answer fixed, a quick lobby showed two players to the
		// host and counted down, while the joiner's screen stayed empty. The
		// Pia punch is not the problem -- tcpdump caught the probe and its
		// reply crossing the LAN in both directions, three attempts, each one
		// succeeding and then going quiet. What the joiner never received is
		// the game-level participant list: the default pairing tells everyone
		// already in the gathering about the joiner, and tells the joiner only
		// about itself (AddedToGathering). The recap is the same cure
		// risk-of-rain needed, for the same reason.
		e.JoinRecapParticipate = true
		// The recap alone was not enough: the joiner heard about the host and
		// still timed out, and the join echo logged count=0. ParticipationCount
		// is only ever set to 1 (the host) in finalizeCreatedSession, and
		// createSession never generates the 32-byte SessionKey either, so a
		// title that leaves this off hands its joiner a session reporting
		// nobody in it and carrying no key. ACNH's provenance records Pia
		// using that key to seed the P2P session before JOIN -- which matches
		// what the wire showed here: the probe and its reply cross the LAN
		// fine, three times, and nothing follows them.
		//
		// FinalizeParams stays false, so this returns before touching
		// attributes or the param map: foreign keys and padded attributes are
		// what broke ACNH with 2306-0116, and this title reads strictly.
		e.FinalizeCreateSession = true
	}},
	{"dont-starve-together", 23050, "c8e4be5d", func(e *protocols.Env) {
		// Don't Starve Together (010090100E334000 v1.29.0, game server
		// 251BB200, NEX 4.6.7, Pia 5.45.0). Neither value is guessed: the
		// title's own nxInternetStartup passes the game server id in w2
		// (mov #0xb200 / movk #0x251b) right beside the literal access key
		// "c8e4be5d" at main.nso 0x227dc4, in the call that logs
		// "nxInternetStartup: Performing NGS login...".
		//
		// This is half of a two-stack title. Klei's own HTTPS (servers/klei)
		// carries the account and the server browser; NEX carries the session
		// and NAT traversal. The game's order is Klei login -> TokenPurpose ->
		// NGS login -> start NAT session -> create the RakNet server, and the
		// failure the player sees when any of it is missing is the same one:
		// "Server failed to start".
		//
		// It links MatchmakeExtension, NATTraversal, SecureConnection, Ranking,
		// Ranking2, MatchmakeReferee, DataStore and Utility. Which of them it
		// actually calls is the first boot's NotImplemented log. Pia 5.45 is
		// well past 5.19, so the default station shape is the right one.
		// Not console-verified.
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.Ranking2 = true
		e.MatchmakeReferee = true
		e.DataStore = protocols.DSEmptyList
		// The pattern that held on every Switch NEX title actually measured on
		// 2026-09-25 (Risk of Rain, Risk of Rain Returns, Streets of Rage 4).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
		// Console-measured 2026-09-28. Everything above was already in place and
		// the host still never built a transport: it creates the gathering, is
		// told about its own Participate, starts both shards -- and then each
		// shard reports "no available shard [Master|Caves] connected" forever,
		// with NO socket call behind it. DST's RakNet on Switch is backed by
		// Pia (the binary links the whole nn::pia::transport stack), so a shard
		// link that never opens is a netgroup that never opened.
		//
		// That is the Risk of Rain failure with the crash filed off: "Pia will
		// not build a netgroup from" a session it does not accept -- and what
		// we hand DST is a session the reference would never emit.
		//
		// The reference (earlier-nex matchmaking_handlers.go finalizeCreatedSession,
		// read for facts only) normalises EVERY created session, for every title,
		// by padding attributes to 6 and stamping `@SR`=true and `@GIR`=3. Here
		// that half is behind FinalizeParams, off for titles with no measured
		// behaviour -- so DST's Pia was reading a session with short attributes
		// and neither param. A Pia that reads attribute[5] off the end, or looks
		// for a param that is not there, fails exactly the way this does: before
		// any syscall. ACNH is the one title known to break WITH `@GIR`, and it
		// is not a Pia 5.45 title.
		e.FinalizeParams = true
		// The other half of that normalisation. The reference gates `State = 1`
		// (open/active) and defaults it off, for discoverability rather than for
		// Pia -- but DST already sets OpenParticipation itself, so the only thing
		// this changes for it is the state flag and the user password, and a
		// session its host never sees marked active is the remaining difference.
		e.ForceOpenParticipation = true
	}},
	{"risk-of-rain-2", 23040, "8bac191f", func(e *protocols.Env) {
		// Risk of Rain 2 (010076D00E4BA000, update v1.4.1, NEX 4.6.7 + Pia
		// 5.45.0 — Unity IL2CPP, Mirror networking over Pia).
		//
		// Game server 2855FF00, CONSOLE-MEASURED 2026-09-25: the title resolves
		// g2855ff00-lp1.s.n.srv.nintendo.net and nothing else, on a real Switch
		// and on Ryujinx alike. This corrects a static guess of 23C34600 that
		// cost a boot — see the changelog. 2855FF00 is not in main.bin AT ALL:
		// not as a little-endian word, not as ASCII, not as a movz/movk pair at
		// any distance, and not in global-metadata.dat either. gsid.py could
		// not have found it, and every candidate it did offer was wrong. For a
		// Unity title, boot it and read the DNS line first; the static scan is
		// for titles that keep the id in the NSO.
		//
		// Access key 8bac191f is a STATIC GUESS AND PROBABLY WRONG. It is the
		// only 8-char hex-shaped literal in global-metadata.dat, but it sits in
		// a long alphabetical run of 32-char Unity asset GUIDs, so "hex-shaped
		// literal" does not distinguish an access key from an asset id here —
		// and the game server id, found by the same kind of reasoning, did not
		// survive the console. A wrong key is cheap and self-announcing: the
		// CONNECT frame fails its signature check and prudplite logs
		// `CONNECT bad signature got=… want=…`. If that appears, empty the key
		// — signatures then go unverified, which is how risk-of-rain-returns
		// runs and plays — and the logged signature is enough to recover the
		// real key offline, since it is
		// HMAC-MD5(md5(key), md5(key) || connSig) over 32 bytes.
		//
		// Protocols from the SDK banners the binary carries: NEX_MM, NEX_RK,
		// NEX_R2, NEX_DS, NEX_UT, NEX_SS, NEX_CO. No NEX_MR, so no
		// MatchmakeReferee — unlike the two Risk of Rain titles. Pia 5.45 is
		// well past 5.19, so the default public station shape is right.
		// Nothing about the session lifecycle is measured yet, so no
		// JoinEchoSession / recap / finalize: those were each a response to a
		// specific console symptom in the other two titles, and guessing them
		// here would change what the first boot means.
		e.Match = protocols.NewMatchmakingStore()
		e.Ranking = true
		e.Ranking2 = true
		e.DataStore = protocols.DSEmptyList
		// Session-lifecycle flags requested by the title's author (openpak-55)
		// 2026-09-25, on the evidence from the other titles rather than from this
		// one: FinalizeCreateSession + JoinEchoSession are 4-for-4 tonight and the
		// recap 4-for-4 with them. Neither affects what the first boot is for here
		// -- gsid 23C34600 and key 8bac191f are settled at DNS and handshake time,
		// nowhere near matchmaking. FinalizeParams stays off (it breaks ACNH).
		e.FinalizeCreateSession = true
		e.JoinEchoSession = true
		e.JoinRecapParticipate = true
		// Console-measured 2026-09-25 (a real Switch as Leia and Ryujinx as
		// tobagin, both directions): hosting works -- login, register, create,
		// NAT and OpenParticipation all succeed -- but "join your friend's
		// game" finds nothing. The friend lookup is MatchmakeExtension 0x33,
		// FindMatchmakeSessionByParticipant, and each client sends its friends'
		// real PIDs in it (Leia asked for tobagin's 0xAAAAAAAAAAAA0001 and
		// tobagin for Leia's 0xAAAAAAAAAAAA0002, each alongside a third
		// friend). With FindByParticipant off we answered an empty list to
		// both, so neither ever received a gathering id to join.
		e.FindByParticipant = true
		// NOT ForceOpenParticipation, and the two-rooms-on-quickplay report is
		// measured as a race rather than a server fault: this title creates its
		// session closed and opens it itself about two seconds later, once the
		// NAT check and station update are done (0x6D.0x02 OpenParticipation
		// arrived at 21:40:26 for a gid created at 21:40:24). autoMatchmake only
		// considers Match.open(), so two players who both press quickplay
		// inside that window each create their own room -- exactly what the log
		// shows (451906 at :24, 451907 at :26, neither open yet). Kirby's Dream
		// Buffet measured the cost of forcing it open anyway: a joiner gets in
		// before the host's station update and probes a port=1 placeholder
		// (2618-0502). The second search waits for the first room to open.
		e.HoldForOpening = 4 * time.Second
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
			{e.FinalizeParams, "FinalizeParams"},
			{e.MatchModeAttribute, "MatchModeAttribute"},
			{e.JoinEchoSession, "JoinEchoSession"},
			{e.JoinRecapParticipate, "JoinRecapParticipate"},
			{e.StrikersInit, "StrikersInit"},
			{e.HoldForOpening > 0, "HoldForOpening " + e.HoldForOpening.String()},
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
	recallFile      = flag.String("recall-file", "", "file the address-to-PID reconnect table is persisted to (empty: memory only, lost on restart)")
	identityURL     = flag.String("identity-url", "", "nx-baas internal base URL; empty disables account resolution and every login is accepted on its unproven name")
	identityKey     = flag.String("identity-key", "", "shared key for the nx-baas internal API (X-Internal-Key)")
	requireIdentity = flag.Bool("require-identity", false, "refuse logins whose token cannot be proven, instead of falling back to the name")
	dump            = flag.Bool("dump", false, "print every served title's resolved configuration and exit")
	loginExStruct   = flag.Bool("loginex-structured", false, "answer TicketGranting method 2 with the ValidateAndRequestTicketResult structure instead of the flat layout (see docs/login-shape-conflict.md)")
)

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

	voice := &voiceIndex{ids: ids}
	for _, t := range titles {
		if len(want) > 0 && !want[t.name] {
			continue
		}
		svc := newService(t, *stationAddress, recall, ids)
		voice.add(svc.env)
		// healthz is bound whatever happens to the game ports: a title whose
		// listener failed answers 503 there, which says more than a refused
		// connection does. Every one of them also answers the voice lookup
		// for the whole process (voice.go).
		serveHealth(fmt.Sprintf(":%d", t.base+2), svc, voice)

		authLn, err := listenTitle(fmt.Sprintf(":%d", t.base), tlsCfg.Clone())
		if err != nil {
			log.Printf("[%s] auth listen :%d: %v", t.name, t.base, err)
			continue
		}
		secureLn, err := listenTitle(fmt.Sprintf(":%d", t.base+1), tlsCfg.Clone())
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
	ids       *identity // nil in tests that build a service without one

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

	s := &service{env: env, targetKey: targetKey, base: t.base, ids: ids}
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
			// A ticketless CONNECT inherits the PID of the one login it can
			// belong to. That is leniency, not authentication: the Lite
			// signature proves knowledge of the title's PUBLIC access key,
			// never identity.
			//
			// Harmless while every player is an unproven name. The moment
			// identities are real it is an account-takeover path for anyone
			// sharing a public address — a CGNAT range, or one household — so
			// once logins are enforced the ticket is the only way in.
			if *requireIdentity {
				log.Printf("[%s] secure: ticketless CONNECT from %s refused (identity enforced)", env.Name, redact.Tag(host))
				return 0, false
			}
			// Bound to its own login or refused, never "the title's latest
			// login": two consoles logging in to one title in the same second
			// were both handed the second one's account (sweep 2026-10-10,
			// MK8D). recall.go has the rules; no ids or addresses in the log.
			pid, how := recall.bind(env.Name, host)
			switch how {
			case bindAmbiguous:
				log.Printf("[%s] secure: ticketless CONNECT refused: more than one player it could be (only a ticket tells them apart)", env.Name)
				return 0, false
			case bindNone:
				return 0, false
			}
			log.Printf("[%s] secure: ticketless CONNECT bound to %s", env.Name, how)
			return pid, true
		},
		OnConnect: func(c *prudplite.Connection) {
			log.Printf("[%s] secure: connected pid=%s cid=%d host=%s", env.Name, redact.Tag(c.PID), c.CID, redact.Tag(c.Host))
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

// serve starts both endpoints on already-open listeners. Listen returns at
// once (it starts its own accept loop), so it runs inline: started in a
// goroutine, a Close straight after serve could find the endpoint not yet
// initialised and close a nil channel (TestHealthzFollowsTheListeners in CI).
func (s *service) serve(authLn, secureLn net.Listener) {
	s.auth.Listen(authLn)
	s.secure.Listen(secureLn)
	go s.ids.watchBans(s.secure)
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
func serveHealth(addr string, s *service, voice *voiceIndex) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[%s] healthz listen %s: %v", s.env.Name, addr, err)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	if voice != nil {
		mux.HandleFunc("GET /internal/voice/gathering", voice.gathering)
	}
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
		log.Printf("[%s] login UNPROVEN (no token): falling back to the name", env.Name)
		pid = pidFor(username)
		env.SetAppVersion(pid, "")
		return pid, 0, true
	}

	ai, av := appClaims(token)
	p, err := ids.resolve(token)
	if err != nil {
		if errors.Is(err, errBanned) {
			// Refused whether or not identity is enforced: the name fallback
			// below would let a banned account straight back in.
			log.Printf("[%s] login refused: %v", env.Name, err)
			return 0, loginResultCode(err), false
		}
		if *requireIdentity {
			log.Printf("[%s] login refused: %v", env.Name, err)
			return 0, loginResultCode(err), false
		}
		log.Printf("[%s] login UNPROVEN (%v): falling back to the name", env.Name, err)
		pid = pidFor(username)
		env.SetAppVersion(pid, av)
		return pid, 0, true
	}
	pid = p.nexPID()
	// Latest version only. aauth already turns an old version away with
	// 2124-3001; a login that still arrives with one (a token cached from
	// before an update was published) gets NEX's own refusal, which carries
	// the same update-required dialog on the console (2306-0819).
	if old, latest := ids.outdated(ai, av); old {
		log.Printf("[%s] login refused: pid=%s title %s version %s is older than %s (ApplicationVersionIsOld)", env.Name, redact.Tag(pid), ai, av, latest)
		return 0, rmc.ResultAppVersionOld, false
	}
	env.SetFriends(pid, p.friendPIDs())
	env.SetAppVersion(pid, av)
	log.Printf("[%s] login pid=%s friends=%d title=%s version=%s", env.Name, redact.Tag(pid), len(p.Friends), ai, av)
	return pid, 0, true
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

	recall.noteLogin(env.Name, c.Host, pid)
	recall.rememberName(username, pid)
	log.Printf("[%s] login pid=%s url=%q ticket=%dB", env.Name, redact.Tag(pid), url, len(ticket))
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
