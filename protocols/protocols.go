// Package protocols implements the secure-stage NEX protocols the Switch
// titles call after login, with response behaviour matched to the working
// reference servers (OpenPak) title for title — see docs/legacy-parity.md
// for the comparison and the per-title provenance of every answer shape.
//
// Protocol IDs and method IDs follow the public NEX protocol definitions
// (Pretendo nex-protocols-go tables, recorded per fact in provenance); the
// response behaviour is what the games were measured working against.
package protocols

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// Protocol IDs (NEX service protocols).
const (
	ProtoNatTraversal     uint16 = 0x03
	ProtoSecureConnection uint16 = 0x0B
	ProtoNotification     uint16 = 0x0E
	ProtoMatchMaking      uint16 = 0x15
	ProtoMessaging        uint16 = 0x17
	ProtoMessageDelivery  uint16 = 0x1B
	ProtoMatchMakingExt   uint16 = 0x32
	ProtoUtility          uint16 = 0x6E
	ProtoMatchmakeExt     uint16 = 0x6D
	ProtoRanking          uint16 = 0x70
	ProtoDataStore        uint16 = 0x73
	ProtoMatchmakeReferee uint16 = 0x78
	ProtoRanking2         uint16 = 0x7A
	ProtoPresence         uint16 = 0x79
)

// Env is one title's serving environment: identity of the server on the wire
// plus the title's state and behaviour hooks. One Env per title.
type Env struct {
	Name string

	// mu serialises the title's secure stage: every Dispatch and every
	// Disconnected. Gatherings are shared between connections and their
	// fields are not individually locked, so a disconnect tearing a player
	// out of a session raced the handlers reading it.
	mu sync.Mutex

	// StationURL parameters for the secure endpoint handed out at login.
	StationAddress string
	SecurePort     uint16
	SecurePID      uint64 // station URL PID= (2 for every title)

	// StationScheme is the station URL scheme this title's console parses,
	// "prudp" or the secure "prudps". It is part of the string, so it is per
	// title and not a constant.
	StationScheme string

	// Pia is the public-station shape Register hands back, following the
	// title's Pia version (see PiaStationShape).
	Pia PiaStationShape

	// LoginExStructured selects what TicketGranting method 2 answers.
	//
	// Two OpenPak lineages disagree about this and both cite console
	// evidence about these same titles, so it is a switch rather than a
	// decision (docs/login-shape-conflict.md):
	//
	//   false — the flat layout: u32 retval, PID, ticket, RVConnectionData
	//           structure, message, source key. What this lineage serves and
	//           what production runs.
	//   true  — the same ValidateAndRequestTicketResult structure method 6
	//           returns. What servers/nex serves.
	//
	// The two are easy to confuse because the flat retval 0x00010001 is the
	// bytes 01 00 01 00, which reads convincingly as a version byte followed
	// by a length. Only a console can say which is right.
	LoginExStructured bool

	// SourceKeyBytes is the per-login kerberos source key size. 32 on Switch
	// generally (MK8D, MPS, ACNH, LM3, S2, SMM2 measured), 16 attested for
	// SMB35 against the reference — the length is per-title, not a protocol
	// constant (see servers/nex provenance).
	SourceKeyBytes int

	// Matchmaking state (nil when the title registers no matchmaking).
	Match *MatchmakingStore

	// Utility behaviour hooks. Nil hooks fall back to the reference answers:
	// empty settings maps, a process-local unique-ID counter, PID-derived tag.
	// IntegerSettings overrides GetIntegerSettings per index. It returns the
	// settings map plus an error code: 0 answers the map (empty maps are a
	// real answer), nonzero answers that RMC error — SMB35's tuning table
	// answers Core::InvalidArgument for unknown indices.
	IntegerSettings func(index uint32) (map[uint16]uint32, uint32)

	// ForceOpenParticipation implements the Mario Tennis Aces fix measured on
	// the working reference (sessions arrive closed and deadlock two searchers
	// — the fix opens every session at create time).
	ForceOpenParticipation bool

	// FindByParticipant serves real results from 0x6D/0x33. Off by default:
	// the titles that do not use the method start up against an empty list,
	// and a populated one changes what they do at boot.
	FindByParticipant bool

	// SanitizeDetailFind implements Mario Party Superstars behaviour:
	// FindMatchmakeSessionByGatheringIDDetail is answerable by anyone holding
	// the gathering ID, so SessionKey and UserPassword are stripped.
	SanitizeDetailFind bool

	// FinalizeCreateSession applies the create-time normalisation measured on
	// the working reference for Clubhouse Games (measured working there):
	// bound unbounded sessions, count the host as the first participant, mint
	// a 32-byte session key, stamp the start time, and — with
	// ForceOpenParticipation — mark the session open and active.
	FinalizeCreateSession bool

	// FinalizeParams adds the matchmake-param normalisation to the above:
	// pad attributes to 6 and stamp `@SR`=true / `@GIR`=3. Measured on the
	// reference for Clubhouse Games (and tolerated by MK8D, Golf and Kirby),
	// but ACNH's binary knows `@SR` and has never heard of `@GIR`: foreign
	// keys in its sessions break its parse (2306-0116 on a Dodo-code browse
	// result, 2026-09-21). Off unless the title was measured with them.
	FinalizeParams bool

	// MatchModeAttribute matches auto-match rooms on the search criteria's
	// primary option (GameMode plus the first attribute string) instead of
	// taking any open room. Golf-only: its ranked queues share every server
	// field except the criteria, and the later attributes are per-player
	// settings that never agree between two players.
	MatchModeAttribute bool

	// JoinEchoSession answers the join family with the full session instead
	// of a bare bool. The client builds its P2P mesh from the echoed
	// participation count, which must report the PRE-join value for a newly
	// joined caller (Clubhouse Games measured working this way).
	JoinEchoSession bool

	// JoinRecapParticipate sends the reference's explicit-join notification
	// set instead of the auto-match one: Participate (3001) about the joiner
	// to every participant INCLUDING the joiner, then a recap Participate
	// per pre-existing participant to the joiner, so it learns who is
	// already in the session. ACNH's Pia waits on that recap to build its
	// mesh: without it the visitor sits ten seconds after the join, sends
	// EndParticipation and shows "A communication error has occurred"
	// (console-measured 2026-09-22, Ryujinx visiting NMT18). MK8D and the
	// auto-match titles keep the 3001-to-others + 122000-to-joiner pair
	// they were measured working with.
	JoinRecapParticipate bool

	// ParticipateDelay holds back every Participate (3001) push by this long.
	// Super Mario 3D World + Bowser's Fury was measured working on the
	// reference with a 100 ms delay (ExternalReference/super-mario-3d-world).
	ParticipateDelay time.Duration

	// sent, when set, sees every notification as it is pushed (tests).
	sent func(pid uint64, event notificationEvent)

	// DataStore mode. dsOff: not registered. dsStub: reference-level answers
	// (empty lists, NotFound on GetMeta, real data IDs from PostMetaBinary).
	// dsBalloonErrors: Super Mario Odyssey's balloon methods (0x73.0x2F-0x35)
	// answer Core::NotImplemented. dsSMM2: Super Mario Maker 2's title
	// methods (0x30 GetUsers etc.) answer with reference shapes.
	// dsClubhouse: Clubhouse Games' account-setup answers — GetPersistenceInfos
	// (0x1E) empty + NotFound per slot, GetMeta NotFound, everything else a
	// bare empty list. See the DSMode table for the per-title shapes.
	DataStore DSMode

	// Ranking present (0x70). Ranking2/Referee/Messaging/Presence are keyed
	// off their own flags so the registry stays declarative.
	Ranking          bool
	Ranking2         bool // SMB35
	MatchmakeReferee bool // SMB35
	MessageDelivery  bool // SMB35
	Presence         bool // LM3 0x79

	// StrikersInit serves Mario Strikers' bring-up table on the five protocols
	// its Pia interrogates at startup (see strikers.go).
	StrikersInit bool

	// internal state
	dataID      atomic.Uint64
	push        pusher
	nat         natStore
	codes       codeStore
	tournaments tournamentStore
	referee     roundStore
	ranking     rankingStore
	r2common    r2CommonStore
}

// DSMode selects the DataStore behaviour.
type DSMode int

// DataStore behaviour levels.
const (
	DSOff           DSMode = iota
	DSEmptyList            // GetMeta -> NotFound, everything else a bare empty list
	DSEmptySuccess         // every method, GetMeta included, answers an empty body
	DSStub                 // + real data IDs from PostMetaBinary, search ranges
	DSBalloonErrors        // + title balloon methods answer NotImplemented (SMO)
	DSSMM2                 // + SMM2 title methods (0x2F-0x31, 0x3C) answer in shapes
	DSClubhouse            // account-setup answers: GetPersistenceInfos empties + per-slot NotFound
)

// BindPush attaches the title's secure endpoint so the server can originate
// RMC requests on it (notification events, NAT probe relays).
func (e *Env) BindPush(endpoint *prudplite.Endpoint) { e.push.bind(endpoint) }

// NewEnv builds a title environment.
func NewEnv(name, stationAddress string, portBase int) *Env {
	return &Env{
		Name:           name,
		StationAddress: stationAddress,
		SecurePort:     uint16(portBase + 1),
		SecurePID:      2,
		SourceKeyBytes: 32,
		StationScheme:  "prudp",
		Pia:            Pia519(),
	}
}

// SecureStationURL renders the secure station URL this title hands out.
func (e *Env) SecureStationURL() string {
	u := switchnex.NewScheme(e.StationScheme, e.StationAddress, e.SecurePort)
	u.SetInt("PID", e.SecurePID)
	return u.String()
}

// Dispatch routes one secure-stage RMC request to the title's protocol set.
// Unknown protocols answer Core::NotImplemented and are logged — the game
// tells us the backlog.
func Dispatch(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	env.mu.Lock()
	defer env.mu.Unlock()
	h, ok := handlersFor(env)[msg.Protocol]
	if !ok {
		log.Printf("[%s] secure unimplemented proto=0x%X method=0x%X (%dB)", env.Name, msg.Protocol, msg.MethodID, len(msg.Body))
		return rmc.EncodeError(msg.Protocol, msg.CallID, rmc.ResultNotImplemented)
	}
	return h(env, c, msg)
}

// handler is one protocol's method dispatcher.
type handler func(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte

// handlersFor assembles the title's protocol set. Built per dispatch call —
// the map is tiny and this keeps every title's set declarative in one place.
func handlersFor(env *Env) map[uint16]handler {
	h := map[uint16]handler{
		ProtoSecureConnection: secureConnectionHandler,
		ProtoUtility:          utilityHandler,
		ProtoNotification:     notificationHandler,
		ProtoMessaging:        messagingHandler,
	}
	if env.MessageDelivery {
		h[ProtoMessageDelivery] = messageDeliveryHandler
	}
	if env.Match != nil {
		h[ProtoMatchMaking] = matchMakingHandler
		h[ProtoMatchMakingExt] = matchMakingExtHandler
		h[ProtoMatchmakeExt] = matchmakeExtHandler
		h[ProtoNatTraversal] = natTraversalHandler
	}
	if env.DataStore != DSOff {
		h[ProtoDataStore] = dataStoreHandler
	}
	if env.Ranking {
		h[ProtoRanking] = rankingHandler
	}
	if env.Ranking2 {
		h[ProtoRanking2] = ranking2Handler
	}
	if env.MatchmakeReferee {
		h[ProtoMatchmakeReferee] = refereeHandler
	}
	if env.Presence {
		h[ProtoPresence] = presenceHandler
	}
	if env.StrikersInit {
		// The bring-up shapes win over the generic handlers for exactly the
		// protocols this title interrogates at boot.
		for _, proto := range []uint16{ProtoUtility, ProtoDataStore, ProtoRanking2, ProtoMatchmakeReferee, ProtoPresence} {
			h[proto] = strikersHandler(proto)
		}
	}
	return h
}

var protoNames = map[uint16]string{
	ProtoNatTraversal: "NATTraversal", ProtoSecureConnection: "SecureConnection",
	ProtoNotification: "Notification", ProtoMatchMaking: "MatchMaking",
	ProtoMessaging: "Messaging", ProtoMessageDelivery: "MessageDelivery",
	ProtoMatchMakingExt: "MatchMakingExt", ProtoUtility: "Utility",
	ProtoMatchmakeExt: "MatchmakeExtension", ProtoRanking: "Ranking",
	ProtoDataStore: "DataStore", ProtoMatchmakeReferee: "MatchmakeReferee",
	ProtoRanking2: "Ranking2", ProtoPresence: "Presence",
}

// Served lists the secure-stage protocols env answers, by id — what the
// dispatcher routes, so a dump built on it cannot drift from what is served.
func Served(env *Env) []string {
	h := handlersFor(env)
	ids := make([]int, 0, len(h))
	for id := range h {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = fmt.Sprintf("0x%02X %s", id, protoNames[uint16(id)])
	}
	return out
}

func (m DSMode) String() string {
	return [...]string{"off", "empty-list", "empty-success", "stub", "balloon-errors", "smm2", "clubhouse"}[m]
}

// success frames an RMC success response for the request's protocol.
func success(msg *rmc.RMCMessage, body []byte) []byte {
	return rmc.EncodeSuccess(msg.Protocol, msg.CallID, msg.MethodID, body)
}

// errorResp frames an RMC error response for the request's protocol.
func errorResp(msg *rmc.RMCMessage, code uint32) []byte {
	return rmc.EncodeError(msg.Protocol, msg.CallID, code)
}

// notImplemented logs the miss and answers Core::NotImplemented — the game
// tells us the backlog.
func notImplemented(env *Env, msg *rmc.RMCMessage) []byte {
	log.Printf("[%s] proto=0x%X method=0x%X (%dB): NotImplemented", env.Name, msg.Protocol, msg.MethodID, len(msg.Body))
	return errorResp(msg, rmc.ResultNotImplemented)
}
