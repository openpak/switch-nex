// Matchmaking (0x15), MatchMakingExt (0x32) and MatchmakeExtension (0x6D)
// over one in-memory gathering store.
//
// Behaviour is matched to the working reference servers title for title:
// sessions are real (friends must be able to find each other — the MPS
// "friend could not see my lobby" bug was an empty answer here), searches
// filter on OpenParticipation, and find/detail responses carry the full
// session the way ACNH measured (bare-session answers desync the client).
// Request parsing reads only the leading fields each method needs and ignores
// trailing extension bytes (the ponytail block lists).
package protocols

import (
	"crypto/rand"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// gathering is a stored session plus its participants.
type gathering struct {
	sess         *Session
	participants map[uint64]bool
}

// MatchmakingStore holds a title's live sessions. In memory on purpose:
// lobbies die with the console and rebuild on the next connect.
type MatchmakingStore struct {
	mu    sync.Mutex
	next  uint32
	byGID map[uint32]*gathering
}

// NewMatchmakingStore returns an empty store.
func NewMatchmakingStore() *MatchmakingStore {
	// ponytail: gathering ids restart with the process, and ACNH derives its
	// Dodo Code from the id (gid 1 is always 0JJJK), so every deploy handed
	// out the same codes. A clock-seeded start keeps ids unique across
	// restarts; kept under 2^20 so a five-character code still encodes it.
	return &MatchmakingStore{byGID: map[uint32]*gathering{}, next: uint32(time.Now().Unix()) % (1 << 20)}
}

func (m *MatchmakingStore) create(sess *Session) uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	sess.GID = m.next
	m.byGID[sess.GID] = &gathering{sess: sess, participants: map[uint64]bool{}}
	return sess.GID
}

func (m *MatchmakingStore) get(gid uint32) (*gathering, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.byGID[gid]
	return g, ok
}

func (m *MatchmakingStore) delete(gid uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byGID, gid)
}

func (m *MatchmakingStore) all() []*gathering {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*gathering, 0, len(m.byGID))
	for _, g := range m.byGID {
		out = append(out, g)
	}
	return out
}

// open returns the sessions accepting participants.
func (m *MatchmakingStore) open() []*Session {
	var out []*Session
	for _, g := range m.all() {
		if g.sess.OpenParticipation {
			out = append(out, g.sess)
		}
	}
	return out
}

func (m *MatchmakingStore) sessionsOf(pid uint64) []*Session {
	var out []*Session
	for _, g := range m.all() {
		if g.participants[pid] {
			out = append(out, g.sess)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// MatchmakeExtension 0x6D
// ---------------------------------------------------------------------------

func matchmakeExtHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01, 0x02: // CloseParticipation / OpenParticipation(gid)
		if gid, err := r.U32(); err == nil {
			if g, ok := env.Match.get(gid); ok {
				g.sess.OpenParticipation = msg.MethodID == 0x02
			}
		}
		return success(msg, nil)

	case 0x03: // AutoMatchmakePostpone(GatheringHolder session, string msg, list<URL>)
		// The pre-param path carries no search criteria: match anything.
		sess, err := readSessionFromHolder(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return autoMatchmake(env, c, msg, sess, nil)

	case 0x34: // BrowseMatchmakeSessionNoHolderNoResultRange(criteria) -> list<session>
		// ACNH's Dodo-code lookup (console-measured 2026-09-22, code
		// "0JJJK"): the criteria carry the code in their codeword field,
		// and the answer is a RAW list<MatchmakeSession> — u32 count and
		// bodies, no ResultRange, no holder. The ResultRange prefix the
		// other browse variants answer with was the 2306-0116 on this call.
		// A code names the island the visitor is allowed into, so the
		// session key stays (the visit needs it, as with find-by-participant).
		code := criteriaCodeword(r)
		var sessions []*Session
		for _, s := range env.Match.open() {
			if code == "" || s.CodeWord == code {
				sessions = append(sessions, s)
			}
		}
		log.Printf("[%s] matchmake: browse code=%q -> %d session(s)", env.Name, code, len(sessions))
		return success(msg, sessionList(sessions))

	case 0x04, 0x2A: // BrowseMatchmakeSession[NoHolder](criteria, resultRange)
		sessions := env.Match.open()
		return browseResponse(msg, sessions)

	case 0x05, 0x2B, 0x35: // BrowseMatchmakeSessionWithHostURLs* -> (range, sessions, urls)
		sessions := env.Match.open()
		return browseWithURLsResponse(msg, sessions)

	case 0x06: // CreateMatchmakeSession(GatheringHolder session, list<URL>)
		sess, err := readSessionFromHolder(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return createSession(env, c, msg, sess)

	case 0x07: // JoinMatchmakeSession(gid, string, list<URL>)
		return joinByGID(env, c, msg, r)

	case 0x08, 0x0C: // ModifyCurrentGameAttribute / UpdateMatchmakeSessionAttribute(gid, u8, u32)
		if gid, err := r.U32(); err == nil {
			if g, ok := env.Match.get(gid); ok {
				if idx, err := r.U8(); err == nil {
					if v, err := r.U32(); err == nil {
						for len(g.sess.Attributes) <= int(idx) {
							g.sess.Attributes = append(g.sess.Attributes, 0)
						}
						g.sess.Attributes[idx] = v
					}
				}
			}
		}
		return success(msg, nil)

	case 0x09: // UpdateNotificationData(u32 type, pid param1, pid param2, string)
		// "My gate is open": the host publishes, its online friends get the
		// same event pushed at type*1000, and friends who ask later (0x0A,
		// 0x0D) read it back. Reference-measured shape for ACNH.
		return updateNotificationData(env, c, msg, r)

	case 0x0A, 0x0D: // GetFriendNotificationData(s32 type) / GetlstFriendNotificationData(list<u32>)
		return friendNotificationData(env, c, msg, r, msg.MethodID == 0x0D)

	case 0x0B: // UpdateApplicationBuffer(gid, buffer)
		if gid, err := r.U32(); err == nil {
			if g, ok := env.Match.get(gid); ok {
				if buf, err := r.Buffer(); err == nil {
					g.sess.ApplicationBuffer = buf
				}
			}
		}
		return success(msg, nil)

	case 0x0E: // UpdateMatchmakeSession(GatheringHolder session)
		sess, err := readSessionFromHolder(r)
		if err != nil || sess.GID == 0 {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if g, ok := env.Match.get(sess.GID); ok {
			keep := g.sess.OpenParticipation
			sess.GID = g.sess.GID
			g.sess = sess
			g.sess.OpenParticipation = g.sess.OpenParticipation || keep
		}
		return success(msg, nil)

	case 0x10: // GetPlayingSession(list<PID>) -> list<(PID, session)>
		pids, err := readPIDList(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return success(msg, playingSessionList(env, pids))

	case 0x1E: // JoinMatchmakeSessionEx(gid, string, bool, u16, list<URL>)
		return joinByGID(env, c, msg, r)

	case 0x1F: // GetSimplePlayingSession() -> list<(PID, gid, gameMode, attr0)>
		return success(msg, simplePlayingSessionList(env, []uint64{c.PID}))

	case 0x42: // TournamentCreate -> the descriptor, echoed
		return createTournament(env, c, msg)

	case 0x43: // TournamentDelete -> empty success (owner only)
		return deleteTournament(env, c, msg, r)

	case 0x22: // UpdateProgressScore(gid, u8)
		if gid, err := r.U32(); err == nil {
			if g, ok := env.Match.get(gid); ok {
				if score, err := r.U8(); err == nil {
					g.sess.ProgressScore = score
				}
			}
		}
		return success(msg, nil)

	case 0x24: // GenerateMatchmakeSessionSystemPassword(gid) -> string
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.String("")
			return w.Bytes()
		}())

	case 0x25: // ClearMatchmakeSessionSystemPassword(gid)
		return success(msg, nil)

	case 0x26: // CreateMatchmakeSessionWithParam(param, list<URL>)
		sess, err := readSessionInParam(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return createSession(env, c, msg, sess)

	case 0x27: // JoinMatchmakeSessionWithParam(param, list<URL>)
		// Param: struct { gid u32, ... } — gid first, the rest unread.
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return joinByGID(env, c, msg, r)

	case 0x28: // AutoMatchmakeWithParamPostpone(param, list<URL>)
		// Param: AutoMatchmakeParam — the source session, then the search
		// criteria the room has to satisfy. The criteria (not the source
		// session) name the lobby: GameMode plus the non-empty attribute
		// strings, exactly as the reference matches them.
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		sess, err := readSession(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		criteria, err := readSearchCriteriaList(r)
		if err != nil {
			// A param shape beyond the session is one this title drew
			// differently — match without criteria (the v0.2.28 behaviour)
			// rather than refuse the search outright.
			log.Printf("[%s] matchmake: unreadable search criteria for pid=%d, matching without", env.Name, c.PID)
			criteria = nil
		}
		return autoMatchmake(env, c, msg, sess, criteria)

	case 0x29: // FindMatchmakeSessionByGatheringIDDetail(gid) -> session
		gid, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		g, ok := env.Match.get(gid)
		if !ok {
			return errorResp(msg, rmc.ResultSessionVoid)
		}
		if env.SanitizeDetailFind {
			// MPS: anyone holding the gathering ID can ask; strip secrets.
			clone := *g.sess
			clone.SessionKey = nil
			clone.UserPassword = ""
			return success(msg, clone.bytes())
		}
		return success(msg, g.sess.bytes())

	case 0x2C: // UpdateMatchmakeSessionPart(gid, flags, partial fields)
		return updateSessionPart(env, msg, r)

	case 0x2D, 0x2E, 0x2F: // RequestMatchmaking / WithdrawMatchmaking / WithdrawMatchmakingAll
		return success(msg, nil)

	case 0x30: // FindMatchmakeSessionByGatheringID(list<gid>) -> list<session>
		n, err := r.ListHeader()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		var found []*Session
		for i := uint32(0); i < n; i++ {
			gid, err := r.U32()
			if err != nil {
				return errorResp(msg, rmc.ResultInvalidArgument)
			}
			if g, ok := env.Match.get(gid); ok {
				found = append(found, scrubbed(g.sess))
			}
		}
		return success(msg, sessionList(found))

	case 0x31: // FindMatchmakeSessionBySingleGatheringID(gid) -> session
		gid, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		g, ok := env.Match.get(gid)
		if !ok {
			return errorResp(msg, rmc.ResultSessionVoid)
		}
		// A read by id scrubs the key and the password: holding the id is not
		// the same as being in the session.
		return success(msg, scrubbed(g.sess).bytes())

	case 0x32: // FindMatchmakeSessionByOwner(gid?, u32 resultRange) -> list<session>
		if _, err := r.U32(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		var owned []*Session
		for _, s := range env.Match.all() {
			if s.sess.OwnerPID == c.PID {
				owned = append(owned, s.sess)
			}
		}
		return success(msg, sessionList(owned))

	case 0x33: // FindMatchmakeSessionByParticipant(param) -> list<(PID, session)>
		// The parameter is a STRUCTURE — header first, then the PID list, the
		// options and a block list. Reading the list straight off the body
		// consumes the version byte and half the length prefix as the count.
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		pids, err := readPIDList(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if !env.FindByParticipant {
			// Only the titles that use this method get real results. The
			// others start up against an empty list and a populated one
			// changes what they do at boot.
			return emptyList(msg)
		}
		return success(msg, participantResultList(env, c, pids))

	case 0x3C: // CustomGetSimplePlayingSession(list<PID>, bool includeSelf)
		// This is how the friend list learns WHICH gathering to join. An empty
		// answer is why a friend-join fails: the joiner never gets a gathering
		// id to look up, so there is nothing for it to join.
		pids, err := readPIDList(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		includeSelf, _ := r.Bool()
		if !includeSelf {
			kept := pids[:0]
			for _, pid := range pids {
				if pid != c.PID {
					kept = append(kept, pid)
				}
			}
			pids = kept
		}
		return success(msg, simplePlayingSessionList(env, pids))

	case 0x41: // CustomFriendsQuery -> empty list
		return emptyList(msg)

	case 0x37, 0x44: // arena / private-room code create -> (String code, DateTime)
		return createRoomCode(env, c, msg)

	case 0x38, 0x45: // arena / private-room code resolve -> u32 gid
		return resolveRoomCode(env, msg, r)

	case 0x3A: // one number, two meanings — see probeOrTournaments
		return probeOrTournaments(env, msg)

	case 0x39: // TournamentSearch -> list of tournament descriptors
		return searchTournaments(env, msg)

	case 0x3F: // TournamentJoin -> empty success
		return success(msg, nil)

	default:
		return notImplemented(env, msg)
	}
}

// createSession assigns a GID, registers the creator and echoes the session.
func createSession(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, sess *Session) []byte {
	sess.OwnerPID = c.PID
	sess.HostPID = c.PID
	if env.ForceOpenParticipation {
		sess.OpenParticipation = true
	}
	if env.FinalizeCreateSession {
		finalizeCreatedSession(env, sess)
	}
	gid := env.Match.create(sess)
	if g, ok := env.Match.get(gid); ok {
		g.participants[c.PID] = true
	}
	log.Printf("[%s] matchmake: created gid=%d pid=%d max=%d open=%v", env.Name, gid, c.PID, sess.MaxParticipants, sess.OpenParticipation)
	// The creator joins its own session, and a join is announced to the owner
	// — here the same player. MK8D 3.0.5 was observed (2026-09-17) to sit for
	// exactly 10 s after a successful create, send nothing, open no P2P
	// socket, then unregister the gathering and report a communication error:
	// it was waiting to hear this. Pretendo's common matchmaking announces the
	// creator's own join the same way.
	env.notify(c.PID, notificationEvent{
		SourcePID: c.PID,
		Type:      NotifyParticipate,
		Param1:    uint64(gid),
		Param2:    c.PID,
		Param3:    1, // participants joining with this call
	})
	return success(msg, sess.bytes())
}

// finalizeCreatedSession normalises a session at create time to the shape
// Clubhouse Games was measured working against on the reference: a lobby the
// matchmaker can immediately use — bounded, occupied by its host, keyed,
// timestamped, and (with the open-participation fix) open and active.
func finalizeCreatedSession(env *Env, s *Session) {
	if s.MaxParticipants == 0 {
		s.MaxParticipants = 12
	}
	s.ParticipationCount = 1 // the host
	if len(s.SessionKey) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err == nil {
			s.SessionKey = key
		}
	}
	s.StartedTime = switchnex.NowDateTime()
	s.SystemPasswordEnabled = false
	if env.ForceOpenParticipation {
		s.UserPasswordEnabled = false
		s.State = 1 // open / active
	}
	if !env.FinalizeParams {
		// Titles without measured param behaviour keep exactly what the
		// client sent: foreign keys (@GIR) and padded attributes break
		// strict readers (ACNH 2306-0116, 2026-09-21).
		return
	}
	for len(s.Attributes) < 6 {
		s.Attributes = append(s.Attributes, 0)
	}
	// The reference stamps these two on every session it creates. Left out, the
	// param map goes back exactly as the client sent it, which for a session the
	// client has not filled in is empty.
	if s.MatchmakeParam == nil {
		s.MatchmakeParam = map[string]rmc.Variant{}
	}
	s.MatchmakeParam["@SR"] = rmc.Variant{TypeID: 3, Bool: true}
	s.MatchmakeParam["@GIR"] = rmc.Variant{TypeID: 1, I64: 3}
}

// updateNotificationData stores the caller's published data for its type and
// pushes it to every online friend as type*1000 — the real-time presence
// event ACNH's visitor waits on (reference, measured against the original
// service). Param2 is the gate: 1 open, 0 closed.
func updateNotificationData(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, r *rmc.Reader) []byte {
	typ, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	p1, err := r.U64()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	p2, err := r.U64()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	str, err := r.String()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	ev := notificationEvent{SourcePID: c.PID, Type: typ, Param1: p1, Param2: p2, StrParam: str}
	env.putNotifData(c.PID, ev)
	pushed := 0
	for _, friend := range env.FriendPIDs(c.PID) {
		if _, online := env.push.connection(friend); !online {
			continue
		}
		live := ev
		live.Type = typ * 1000
		env.notify(friend, live)
		pushed++
	}
	log.Printf("[%s] notifdata: pid=%d type=%d param1=%d param2=%d pushed to %d online friend(s)", env.Name, c.PID, typ, p1, p2, pushed)
	return success(msg, nil)
}

// friendNotificationData answers the caller's friends' published data for the
// requested types, open gates only (Param2 != 0), then pushes each entry to
// the caller as type*1000 after the answer — the reference's measured order.
func friendNotificationData(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, r *rmc.Reader, list bool) []byte {
	var types []uint32
	if list {
		n, err := r.ListHeader()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		for i := uint32(0); i < n && i < 16; i++ {
			t, err := r.U32()
			if err != nil {
				return errorResp(msg, rmc.ResultInvalidArgument)
			}
			types = append(types, t)
		}
	} else {
		t, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		types = []uint32{t}
	}
	var events []notificationEvent
	for _, friend := range env.FriendPIDs(c.PID) {
		for _, typ := range types {
			if ev, ok := env.notifDataOf(friend, typ); ok && ev.Param2 != 0 {
				events = append(events, ev)
			}
		}
	}
	w := rmc.NewWriter()
	w.List(len(events))
	for _, ev := range events {
		w.Raw(ev.bytes())
	}
	log.Printf("[%s] notifdata: pid=%d types=%v -> %d friend entr(y/ies)", env.Name, c.PID, types, len(events))
	resp := success(msg, w.Bytes())
	for _, ev := range events {
		live := ev
		live.Type = ev.Type * 1000
		env.notify(c.PID, live)
	}
	return resp
}

// autoMatchmake joins the first session the caller can actually reach, and
// opens its own when there is none.
//
// "Can reach" is the load-bearing part: two peers behind unfriendly NATs never
// punch through to each other, and dropping such a pair into one lobby gives
// EVERYONE in it a communication error, not just the two. A player nobody can
// reach ends up hosting alone, which is a worse experience for one person
// rather than a broken one for five.
//
// "Same game" is the other half, matched the way the reference matches it:
// against the searcher's search criteria, not its source session. Each
// criterion names a GameMode and attribute strings; a room qualifies when the
// GameMode agrees and every non-empty attribute string equals the room's
// attribute at the same index (or falls in its min,max range). Attribute
// index 1 is reserved for the selection method and never filters. Empty
// criteria (the pre-param 0x03 path) match any room.
//
// This is what separates Golf's ranked queues: the criteria carry the mode
// and settings as strings (2026-09-21, console: ["3","","259",""…""] vs
// ["1","","320",""…""]), while the source sessions alone do not tell them
// apart. v0.2.29 matched on the session attributes instead — which vary per
// search inside one mode — and separated everything; v0.2.30 reverted to
// password+NAT while the criteria were decoded.
func autoMatchmake(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, sess *Session, criteria []*searchCriteria) []byte {
	logSessionTemplate(env, c.PID, sess)
	for _, candidate := range env.Match.open() {
		g, ok := env.Match.get(candidate.GID)
		if !ok {
			continue
		}
		if candidate.MaxParticipants > 0 && candidate.ParticipationCount >= uint32(candidate.MaxParticipants) {
			continue
		}
		// A password room is only for searchers carrying the same password, and a
		// random search (no password) never lands in one.
		if candidate.UserPassword != sess.UserPassword {
			continue
		}
		if env.MatchModeAttribute && !modeAttrMatch(g.sess, criteria) {
			log.Printf("[%s] matchmake: gid=%d skipped for pid=%d (mode)", env.Name, candidate.GID, c.PID)
			continue
		}
		if !env.nat.compatibleWith(g, c.PID) {
			log.Printf("[%s] matchmake: gid=%d skipped for pid=%d (NAT)", env.Name, candidate.GID, c.PID)
			continue
		}
		if !g.participants[c.PID] {
			g.participants[c.PID] = true
			candidate.ParticipationCount++
			// The reference's shape: the joiner is announced to everyone, itself
			// included, then gets one Participate per member already there. Pia
			// waits on these to learn who it joined; without them Kirby's joiner
			// sat 10 s and ended its participation (2026-09-19).
			count := uint64(len(g.participants))
			for _, pid := range participantPIDs(g) {
				env.notify(pid, notificationEvent{
					SourcePID: c.PID, Type: NotifyParticipate,
					Param1: uint64(candidate.GID), Param2: c.PID, Param3: count,
				})
			}
			for _, pid := range participantPIDs(g) {
				if pid == c.PID {
					continue
				}
				env.notify(c.PID, notificationEvent{
					SourcePID: c.PID, Type: NotifyParticipate,
					Param1: uint64(candidate.GID), Param2: pid, Param3: count,
				})
			}
		}
		log.Printf("[%s] matchmake: auto-joined gid=%d pid=%d", env.Name, candidate.GID, c.PID)
		return success(msg, candidate.bytes())
	}
	return createSession(env, c, msg, sess)
}

// searchCriteria is one MatchmakeSessionSearchCriteria as far as matching
// needs it: the GameMode string and the attribute strings. Everything else
// in the structure (participant bounds, system type, codeword…) is unread.
type searchCriteria struct {
	Attribs  []string
	GameMode string
}

// readSearchCriteriaList parses the AutoMatchmakeParam tail past the source
// session: the participant block-list, the check gid, the option, the join
// message, the participant count, then the criteria list. Only the criteria
// are kept.
func readSearchCriteriaList(r *rmc.Reader) ([]*searchCriteria, error) {
	if _, err := readPIDList(r); err != nil { // additional participants
		return nil, err
	}
	if _, err := r.U32(); err != nil { // GID for participation check
		return nil, err
	}
	if _, err := r.U32(); err != nil { // auto-matchmake option
		return nil, err
	}
	if _, err := r.String(); err != nil { // join message
		return nil, err
	}
	if _, err := r.U16(); err != nil { // participation count
		return nil, err
	}
	n, err := r.ListHeader()
	if err != nil {
		return nil, err
	}
	out := make([]*searchCriteria, 0, min(int(n), 8))
	for i := uint32(0); i < n && len(out) < 8; i++ {
		_, cr, err := r.Struct()
		if err != nil {
			return nil, err
		}
		an, err := cr.ListHeader()
		if err != nil {
			return nil, err
		}
		c := &searchCriteria{}
		for k := uint32(0); k < an && len(c.Attribs) < 16; k++ {
			s, err := cr.String()
			if err != nil {
				return nil, err
			}
			c.Attribs = append(c.Attribs, s)
		}
		if c.GameMode, err = cr.String(); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// modeAttrMatch reports whether a stored room takes the searcher: at least
// one criterion names the room's primary option. Only the first attribute is
// compared — it is the lobby's primary option (Golf: the ranked queue), while
// the later attributes are per-player settings that never agree between two
// different players (console-measured 2026-09-21: character slots 259 vs 320
// are constant per player across every mode and round). Matching those, as
// v0.2.32 did following the reference's exact-match rule, strands every pair
// in private rooms. No criteria (the pre-param path) matches everything.
func modeAttrMatch(room *Session, criteria []*searchCriteria) bool {
	if len(criteria) == 0 {
		return true
	}
	for _, c := range criteria {
		if c.GameMode != "" && !numberMatch(c.GameMode, uint64(room.GameMode)) {
			continue
		}
		if len(c.Attribs) == 0 || c.Attribs[0] == "" {
			return true
		}
		if len(room.Attributes) == 0 {
			continue
		}
		if numberMatch(c.Attribs[0], uint64(room.Attributes[0])) {
			return true
		}
	}
	return false
}

// numberMatch compares a criteria number string against a room value: either
// an exact number or a min,max range, as the reference accepts both.
func numberMatch(expr string, v uint64) bool {
	if lo, hi, ok := strings.Cut(expr, ","); ok {
		min, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 32)
		if err != nil {
			return false
		}
		max, err := strconv.ParseUint(strings.TrimSpace(hi), 10, 32)
		if err != nil {
			return false
		}
		return min <= v && v <= max
	}
	n, err := strconv.ParseUint(strings.TrimSpace(expr), 10, 32)
	if err != nil {
		return false
	}
	return n == v
}

// logSessionTemplate dumps a searcher's full session template in one line:
// the GameMode, attributes, application buffer, matchmake-param map and
// description are all candidates for the field that names the lobby's mode.
// Diagnostic, for the Golf Speed/Standard split (2026-09-21) — searches are
// infrequent, so one line per search is cheap.
func logSessionTemplate(env *Env, pid uint64, sess *Session) {
	const maxAppBuf = 64
	app := sess.ApplicationBuffer
	if len(app) > maxAppBuf {
		app = app[:maxAppBuf]
	}
	params := make([]string, 0, len(sess.MatchmakeParam))
	for k, v := range sess.MatchmakeParam {
		params = append(params, variantString(k, v))
	}
	sort.Strings(params)
	log.Printf("[%s] matchmake: template pid=%d gameMode=%d attrs=%v appBuf=%x params={%s} desc=%q pw=%v",
		env.Name, pid, sess.GameMode, sess.Attributes, app,
		strings.Join(params, " "), sess.Description, sess.UserPassword != "")
}

// variantString renders one matchmake-param entry for the template log.
func variantString(k string, v rmc.Variant) string {
	switch v.TypeID {
	case 1:
		return fmt.Sprintf("%s=i64:%d", k, v.I64)
	case 2:
		return fmt.Sprintf("%s=f64:%v", k, v.F64)
	case 3:
		return fmt.Sprintf("%s=bool:%v", k, v.Bool)
	case 4:
		return fmt.Sprintf("%s=str:%q", k, v.Str)
	default:
		return fmt.Sprintf("%s=t%d:u64:%d", k, v.TypeID, v.U64)
	}
}

// Disconnected tells a title's sessions that a player's connection went away:
// drop them from every gathering, tell the rest, and hand on ownership if the
// one that left owned it. Without this a session keeps a participant that is
// no longer there and the count never comes back down.
func (e *Env) Disconnected(pid uint64) {
	if e.Match == nil || pid == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, g := range e.Match.all() {
		if !g.participants[pid] {
			continue
		}
		delete(g.participants, pid)
		if g.sess.ParticipationCount > 0 {
			g.sess.ParticipationCount--
		}
		e.notifyGathering(g.sess.GID, pid, notificationEvent{
			SourcePID: pid,
			Type:      NotifyParticipantDisconnect,
			Param1:    uint64(g.sess.GID),
			Param2:    pid,
		})
		migrateIfHostLeft(e, g.sess.GID, g, pid)
	}
}

// joinByGID handles 0x07/0x1E (gid leads the parameter list) and 0x27 (the
// gid has been uncovered by the caller). With JoinEchoSession the answer is
// the full session — the client builds its P2P mesh from the echoed
// participation count, which must be the PRE-join value for a new joiner
// (Clubhouse Games measured working this way on the reference).
func joinByGID(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, r *rmc.Reader) []byte {
	gid, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	g, ok := env.Match.get(gid)
	if !ok {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	was := g.participants[c.PID]
	g.participants[c.PID] = true
	if !was && env.JoinRecapParticipate {
		// The reference's measured pair: announce the joiner to everyone,
		// joiner included, then recap each existing participant to the
		// joiner. Pushed before the RMC answer, as measured.
		pids := participantPIDs(g)
		count := uint64(len(pids))
		for _, pid := range pids {
			env.notify(pid, notificationEvent{
				SourcePID: c.PID, Type: NotifyParticipate,
				Param1: uint64(gid), Param2: c.PID, Param3: count,
			})
		}
		for _, pid := range pids {
			if pid == c.PID {
				continue
			}
			env.notify(c.PID, notificationEvent{
				SourcePID: c.PID, Type: NotifyParticipate,
				Param1: uint64(gid), Param2: pid, Param3: count,
			})
		}
	} else if !was {
		// The pairing unblock: everyone already in the session is waiting on
		// this event, not on a poll.
		env.notifyGathering(gid, c.PID, notificationEvent{
			SourcePID: c.PID,
			Type:      NotifyParticipate,
			Param1:    uint64(gid),
			Param2:    c.PID,
		})
		env.notify(c.PID, notificationEvent{
			SourcePID: g.sess.OwnerPID,
			Type:      NotifyAddedToGathering,
			Param1:    uint64(gid),
			Param2:    c.PID,
		})
	}
	if env.JoinEchoSession {
		if !was {
			g.sess.ParticipationCount++
		}
		echo := *g.sess
		if !was {
			echo.ParticipationCount = g.sess.ParticipationCount - 1
		}
		log.Printf("[%s] matchmake: joined gid=%d pid=%d count=%d", env.Name, gid, c.PID, echo.ParticipationCount)
		return success(msg, echo.bytes())
	}
	g.sess.ParticipationCount++
	log.Printf("[%s] matchmake: joined gid=%d pid=%d", env.Name, gid, c.PID)
	return success(msg, func() []byte {
		w := rmc.NewWriter()
		w.Bool(true)
		return w.Bytes()
	}())
}

// migrateOwnership hands a gathering to another participant. The candidates the
// caller names are tried in order; a candidate who is not in the session is
// skipped rather than trusted. With nobody left the gathering goes, because an
// ownerless session is one nothing can ever tear down.
func migrateOwnership(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, r *rmc.Reader, boolReply bool) []byte {
	gid, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	g, ok := env.Match.get(gid)
	if !ok {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	if g.sess.OwnerPID != c.PID {
		// Only the owner gives the gathering away.
		return errorResp(msg, rmc.ResultPermissionDenied)
	}
	candidates, _ := readPIDList(r)
	next := firstOtherParticipant(g, c.PID, candidates)
	if next == 0 {
		env.codes.forget(gid)
		env.Match.delete(gid)
		if boolReply {
			return boolTrue(msg)
		}
		return success(msg, nil)
	}
	g.sess.OwnerPID = next
	g.sess.HostPID = next
	env.notifyGathering(gid, 0, notificationEvent{
		SourcePID: c.PID,
		Type:      NotifyOwnershipChanged,
		Param1:    uint64(gid),
		Param2:    next,
	})
	log.Printf("[%s] matchmake: gid=%d owner %d -> %d", env.Name, gid, c.PID, next)
	if boolReply {
		return boolTrue(msg)
	}
	return success(msg, nil)
}

// migrateIfHostLeft moves ownership when the owner is the one leaving, so a
// session outlives its creator instead of stranding everyone still in it.
func migrateIfHostLeft(env *Env, gid uint32, g *gathering, leaving uint64) {
	if g.sess.OwnerPID != leaving {
		return
	}
	next := firstOtherParticipant(g, leaving, nil)
	if next == 0 {
		env.codes.forget(gid)
		env.Match.delete(gid)
		return
	}
	g.sess.OwnerPID = next
	g.sess.HostPID = next
	env.notifyGathering(gid, 0, notificationEvent{
		SourcePID: leaving,
		Type:      NotifyOwnershipChanged,
		Param1:    uint64(gid),
		Param2:    next,
	})
}

// firstOtherParticipant picks the next owner: the first named candidate who is
// really in the session, else any other participant.
func firstOtherParticipant(g *gathering, except uint64, candidates []uint64) uint64 {
	for _, pid := range candidates {
		if pid != except && g.participants[pid] {
			return pid
		}
	}
	for _, pid := range participantPIDs(g) {
		if pid != except {
			return pid
		}
	}
	return 0
}

// updateSessionPart applies 0x2C UpdateMatchmakeSessionPart. The wire body
// is an UpdateMatchmakeSessionParam: EVERY field is present, in order, and
// the modification flags say which ones to apply — they do not gate what is
// serialised. Measured on ACNH 3.0.3 (2026-09-22, gid=1, flags=0x4002, 93B):
// gid, flags, attributes[6], openParticipation, applicationBuffer,
// progressScore, matchmakeParam (struct-wrapped map), startedTime,
// userPassword, gameMode, description, min, max, matchmakeSystemType,
// participationPolicy, policyArgument, codeword. Reading only the flagged
// fields (the previous shape) took the attribute count as the bool and the
// zeroed attributes as the codeword, so the Dodo Code was six NULs and the
// island stayed closed — the "nobody can find my island" bug. Flag table:
// 0x1 attributes, 0x2 openParticipation, 0x4 applicationBuffer,
// 0x8 progressScore, 0x20/0x40 matchmakeParam, 0x80 startedTime,
// 0x100 userPassword, 0x200 gameMode, 0x400 description, 0x800 min,
// 0x1000 max, 0x2000 matchmakeSystemType, 0x4000 codeword.
func updateSessionPart(env *Env, msg *rmc.RMCMessage, r *rmc.Reader) []byte {
	bad := func() []byte { return errorResp(msg, rmc.ResultInvalidArgument) }
	if _, _, err := r.StructHeader(); err != nil {
		return bad()
	}
	gid, err := r.U32()
	if err != nil {
		return bad()
	}
	flags, err := r.U32()
	if err != nil {
		return bad()
	}
	n, err := r.ListHeader()
	if err != nil {
		return bad()
	}
	attrs := make([]uint32, 0, n)
	for i := uint32(0); i < n; i++ {
		v, err := r.U32()
		if err != nil {
			return bad()
		}
		attrs = append(attrs, v)
	}
	open, err := r.Bool()
	if err != nil {
		return bad()
	}
	appBuf, err := r.Buffer()
	if err != nil {
		return bad()
	}
	progress, err := r.U8()
	if err != nil {
		return bad()
	}
	params, err := readStringVariantMap(r)
	if err != nil {
		return bad()
	}
	started, err := r.U64()
	if err != nil {
		return bad()
	}
	password, err := r.String()
	if err != nil {
		return bad()
	}
	gameMode, err := r.U32()
	if err != nil {
		return bad()
	}
	description, err := r.String()
	if err != nil {
		return bad()
	}
	minP, err := r.U16()
	if err != nil {
		return bad()
	}
	maxP, err := r.U16()
	if err != nil {
		return bad()
	}
	systemType, err := r.U32()
	if err != nil {
		return bad()
	}
	if _, err := r.U32(); err != nil { // participationPolicy
		return bad()
	}
	if _, err := r.U32(); err != nil { // policyArgument
		return bad()
	}
	codeword, err := r.String()
	if err != nil {
		return bad()
	}
	g, ok := env.Match.get(gid)
	if !ok {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	s := g.sess
	if flags&0x0001 != 0 {
		s.Attributes = attrs
	}
	if flags&0x0002 != 0 {
		s.OpenParticipation = open
	}
	if flags&0x0004 != 0 {
		s.ApplicationBuffer = appBuf
	}
	if flags&0x0008 != 0 {
		s.ProgressScore = progress
	}
	if flags&0x0060 != 0 {
		s.MatchmakeParam = params
	}
	if flags&0x0080 != 0 {
		s.StartedTime = started
	}
	if flags&0x0100 != 0 {
		s.UserPassword = password
	}
	if flags&0x0200 != 0 {
		s.GameMode = gameMode
	}
	if flags&0x0400 != 0 {
		s.Description = description
	}
	if flags&0x0800 != 0 {
		s.MinParticipants = minP
	}
	if flags&0x1000 != 0 {
		s.MaxParticipants = maxP
	}
	if flags&0x2000 != 0 {
		s.MatchmakeSystemType = systemType
	}
	if flags&0x4000 != 0 {
		s.CodeWord = codeword // ACNH's Dodo Code
	}
	log.Printf("[%s] matchmake: session part gid=%d flags=%#x open=%v codeword=%q", env.Name, gid, flags, s.OpenParticipation, s.CodeWord)
	return success(msg, nil)
}

// ---------------------------------------------------------------------------
// MatchMaking 0x15
// ---------------------------------------------------------------------------

func matchMakingHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01: // RegisterGathering(Gathering) -> u32 gid
		// Bare Gathering (no matchmake fields): header + ten base fields.
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		sess := &Session{}
		var err error
		if _, err = r.U32(); err != nil { // client-proposed gid (ignored)
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.OwnerPID, err = r.U64(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.HostPID, err = r.U64(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.MinParticipants, err = r.U16(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.MaxParticipants, err = r.U16(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.ParticipationPolicy, err = r.U32(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.PolicyArgument, err = r.U32(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.Flags, err = r.U32(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.State, err = r.U32(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if sess.Description, err = r.String(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		sess.OpenParticipation = true
		gid := env.Match.create(sess)
		if g, ok := env.Match.get(gid); ok {
			g.participants[c.PID] = true
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U32(gid)
			return w.Bytes()
		}())

	case 0x02: // UnregisterGathering(gid) -> bool
		if gid, err := r.U32(); err == nil {
			env.notifyGathering(gid, c.PID, notificationEvent{
				SourcePID: c.PID,
				Type:      NotifyGatheringUnregistered,
				Param1:    uint64(gid),
			})
			env.codes.forget(gid)
			env.Match.delete(gid)
		}
		return boolTrue(msg)

	case 0x03: // UnregisterGatherings(list<gid>) -> bool
		if n, err := r.ListHeader(); err == nil {
			for i := uint32(0); i < n; i++ {
				if gid, err := r.U32(); err == nil {
					env.Match.delete(gid)
				}
			}
		}
		return boolTrue(msg)

	case 0x04: // UpdateGathering(gid, Gathering) -> bool
		return boolTrue(msg)

	case 0x0B, 0x0C: // Participate / CancelParticipation(gid, list<string>) -> bool
		if gid, err := r.U32(); err == nil {
			if g, ok := env.Match.get(gid); ok {
				if msg.MethodID == 0x0B {
					g.participants[c.PID] = true
				} else {
					delete(g.participants, c.PID)
				}
			}
		}
		return boolTrue(msg)

	case 0x0D, 0x0F: // GetParticipants(gid) -> list<PID>; GetDetailedParticipants -> list<details>
		gid, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		g, ok := env.Match.get(gid)
		if !ok {
			return success(msg, func() []byte {
				w := rmc.NewWriter()
				w.EmptyList()
				return w.Bytes()
			}())
		}
		if msg.MethodID == 0x0D {
			return success(msg, func() []byte {
				w := rmc.NewWriter()
				writeParticipants(w, g)
				return w.Bytes()
			}())
		}
		// ParticipantDetails: (PID, string name, string message, u32 count)
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			pids := participantPIDs(g)
			w.List(len(pids))
			for _, pid := range pids {
				n := rmc.NewWriter()
				n.U64(pid)
				n.String("")
				n.String("")
				n.U32(1)
				w.Struct(1, n.Bytes())
			}
			return w.Bytes()
		}())

	case 0x10: // GetParticipantsURLs(gid, list<pid>) -> list<RVConnectionData>
		return getParticipantsURLs(env, c, msg)

	case 0x15: // FindBySingleID(gid) -> GatheringHolder
		return errorResp(msg, rmc.ResultSessionVoid)

	case 0x16: // FindByOwner -> list<holder>
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList()
			return w.Bytes()
		}())

	case 0x1A: // LaunchSession(gid) -> bool
		return boolTrue(msg)

	case 0x1B, 0x1C: // UpdateSessionURL / GetSessionURL
		if msg.MethodID == 0x1C {
			return success(msg, func() []byte {
				w := rmc.NewWriter()
				w.String("")
				return w.Bytes()
			}())
		}
		return success(msg, nil)

	case 0x1D: // GetState(gid) -> u32
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U32(0)
			return w.Bytes()
		}())

	case 0x1E: // SetState(gid, u32)
		return success(msg, nil)

	case 0x21: // DeleteGathering(gid) -> bool
		if gid, err := r.U32(); err == nil {
			env.notifyGathering(gid, c.PID, notificationEvent{
				SourcePID: c.PID,
				Type:      NotifyGatheringUnregistered,
				Param1:    uint64(gid),
			})
			env.codes.forget(gid)
			env.Match.delete(gid)
		}
		return boolTrue(msg)

	case 0x24: // MigrateGatheringOwnershipV1(gid, list<PID> candidates)
		return migrateOwnership(env, c, msg, r, false)

	case 0x2C: // MigrateGatheringOwnership(gid, list<PID> candidates, bool)
		return migrateOwnership(env, c, msg, r, true)

	case 0x2B: // UpdateGatheringOwnership(gid, bool participantsOnly) -> bool
		return migrateOwnership(env, c, msg, r, true)

	case 0x28, 0x2A: // UpdateSessionHostV1 / UpdateSessionHost(gid, …)
		// The caller is claiming the host role for itself.
		if gid, err := r.U32(); err == nil {
			if g, ok := env.Match.get(gid); ok && g.participants[c.PID] {
				g.sess.HostPID = c.PID
				env.notifyGathering(gid, c.PID, notificationEvent{
					SourcePID: c.PID,
					Type:      NotifyHostChanged,
					Param1:    uint64(gid),
					Param2:    c.PID,
				})
			}
		}
		return success(msg, nil)

	case 0x29: // GetSessionURLs(gid) -> list<URL>
		// How a joiner learns where the host is: the HOST's station URLs.
		// Critical: wait for the host's ReplaceURL (which carries the post-NAT
		// UDP port and RVCID). Without it, the joiner gets the host's TCP port
		// and the P2P probe never lands. OpenPak waits up to 2.5s polling
		// every 100ms for the host to report ReplaceURL.
		gid, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		g, ok := env.Match.get(gid)
		if !ok {
			return errorResp(msg, rmc.ResultSessionVoid)
		}
		if !g.participants[c.PID] {
			return errorResp(msg, rmc.ResultPermissionDenied)
		}
		peer, connected := env.push.connection(g.sess.HostPID)
		if !connected {
			return emptyList(msg)
		}
		return success(msg, stationURLListWithHostWait(peer))

	default:
		return notImplemented(env, msg)
	}
}

// ---------------------------------------------------------------------------
// MatchMakingExt 0x32
// ---------------------------------------------------------------------------

func matchMakingExtHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01: // EndParticipation(gid, string) -> bool
		if gid, err := r.U32(); err == nil {
			if g, ok := env.Match.get(gid); ok {
				delete(g.participants, c.PID)
				if g.sess.ParticipationCount > 0 {
					g.sess.ParticipationCount--
				}
				env.notifyGathering(gid, c.PID, notificationEvent{
					SourcePID: c.PID,
					Type:      NotifyEndParticipation,
					Param1:    uint64(gid),
					Param2:    c.PID,
				})
				migrateIfHostLeft(env, gid, g, c.PID)
			}
		}
		return boolTrue(msg)

	case 0x02: // GetParticipants(gid, bool) -> list<PID>
		gid, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		g, ok := env.Match.get(gid)
		if !ok {
			return success(msg, func() []byte {
				w := rmc.NewWriter()
				w.EmptyList()
				return w.Bytes()
			}())
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			writeParticipants(w, g)
			return w.Bytes()
		}())

	case 0x03: // GetDetailedParticipants -> list<details>
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList()
			return w.Bytes()
		}())

	case 0x04: // GetParticipantsURLs -> list<RVConnectionData>
		return getParticipantsURLsExt(env, msg)

	case 0x05: // GetGatheringRelations(gid, pid, option) -> map<u32, list<u32>>
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList() // empty map
			return w.Bytes()
		}())

	case 0x06: // DeleteFromDeletions
		return success(msg, nil)

	default:
		return notImplemented(env, msg)
	}
}

// ---------------------------------------------------------------------------
// body helpers
// ---------------------------------------------------------------------------

func boolTrue(msg *rmc.RMCMessage) []byte {
	return success(msg, func() []byte {
		w := rmc.NewWriter()
		w.Bool(true)
		return w.Bytes()
	}())
}

func sessionList(sessions []*Session) []byte {
	w := rmc.NewWriter()
	w.List(len(sessions))
	for _, s := range sessions {
		s.write(w)
	}
	return w.Bytes()
}

// criteriaCodeword reads one MatchmakeSessionSearchCriteria (NEX 4.0.0
// layout: attribs, gameMode, min, max, system, three bools, selection
// method, vacant participants, param map, two bools, refer gid, codeword,
// result range) and returns its codeword; anything unreadable is "".
func criteriaCodeword(r *rmc.Reader) string {
	_, cr, err := r.Struct()
	if err != nil {
		return ""
	}
	n, err := cr.ListHeader()
	if err != nil {
		return ""
	}
	for i := uint32(0); i < n; i++ {
		if _, err := cr.String(); err != nil {
			return ""
		}
	}
	for i := 0; i < 4; i++ { // gameMode, min, max, system
		if _, err := cr.String(); err != nil {
			return ""
		}
	}
	for i := 0; i < 3; i++ { // vacantOnly, excludeLocked, excludeNonHostPID
		if _, err := cr.Bool(); err != nil {
			return ""
		}
	}
	if _, err := cr.U32(); err != nil { // selection method
		return ""
	}
	if _, err := cr.U16(); err != nil { // vacant participants
		return ""
	}
	if _, err := readStringVariantMap(cr); err != nil {
		return ""
	}
	for i := 0; i < 2; i++ { // excludeUserPassword, excludeSystemPassword
		if _, err := cr.Bool(); err != nil {
			return ""
		}
	}
	if _, err := cr.U32(); err != nil { // refer gid
		return ""
	}
	code, err := cr.String()
	if err != nil {
		return ""
	}
	return code
}

// browseResponse: BrowseMatchmakeSession*(criteria, resultRange) ->
// (ResultRange, list<session>).
func browseResponse(msg *rmc.RMCMessage, sessions []*Session) []byte {
	w := rmc.NewWriter()
	w.StructOf(1, func(w *rmc.Writer) {
		w.U32(0)
		w.U32(uint32(len(sessions)))
	})
	w.List(len(sessions))
	for _, s := range sessions {
		s.write(w)
	}
	return success(msg, w.Bytes())
}

// browseWithURLsResponse: + list<list<URL>> after the sessions.
func browseWithURLsResponse(msg *rmc.RMCMessage, sessions []*Session) []byte {
	w := rmc.NewWriter()
	w.StructOf(1, func(w *rmc.Writer) {
		w.U32(0)
		w.U32(uint32(len(sessions)))
	})
	w.List(len(sessions))
	for _, s := range sessions {
		s.write(w)
	}
	w.List(len(sessions))
	for range sessions {
		w.EmptyList()
	}
	return success(msg, w.Bytes())
}

// scrubbed copies a session without the two fields that are secrets to anyone
// not already in it. The session KEY stays where the caller is a participant —
// island visits need it — and is stripped on the by-id reads.
func scrubbed(s *Session) *Session {
	c := *s
	c.SessionKey = nil
	c.UserPassword = ""
	return &c
}

// participantResultList: FindMatchmakeSessionByParticipant ->
// list<struct { PID u64, session }>. An empty PID list means "any session with
// room", which is how a console asks to be placed anywhere. The PID reported is
// the session's HOST — that is who the caller will be joining, and what the
// client uses to identify the game. The user password is stripped; the session
// key is not, because the joiner needs it.
func participantResultList(env *Env, c *prudplite.Connection, pids []uint64) []byte {
	type pair struct {
		pid uint64
		s   *Session
	}
	var pairs []pair
	seen := map[uint32]bool{}

	add := func(host uint64, s *Session) {
		if seen[s.GID] {
			return
		}
		seen[s.GID] = true
		clone := *s
		clone.UserPassword = ""
		pairs = append(pairs, pair{host, &clone})
	}

	if len(pids) == 0 {
		for _, g := range env.Match.all() {
			if len(g.participants) == 0 || g.participants[c.PID] {
				continue // never offer the caller the session it is already in
			}
			if !env.nat.compatibleWith(g, c.PID) {
				continue
			}
			add(g.sess.OwnerPID, g.sess)
		}
	} else {
		for _, pid := range pids {
			for _, s := range env.Match.sessionsOf(pid) {
				add(s.OwnerPID, s)
			}
		}
	}

	w := rmc.NewWriter()
	w.List(len(pairs))
	for _, p := range pairs {
		n := rmc.NewWriter()
		n.U64(p.pid)
		p.s.write(n)
		w.Struct(1, n.Bytes())
	}
	return w.Bytes()
}

// playingSessionList: GetPlayingSession -> list<struct { PID, session }>.
func playingSessionList(env *Env, pids []uint64) []byte {
	if len(pids) == 0 {
		pids = []uint64{0}
	}
	var out []struct {
		pid uint64
		s   *Session
	}
	seen := map[uint32]bool{}
	for _, pid := range pids {
		for _, s := range env.Match.sessionsOf(pid) {
			if !seen[s.GID] {
				seen[s.GID] = true
				out = append(out, struct {
					pid uint64
					s   *Session
				}{pid, s})
			}
		}
	}
	w := rmc.NewWriter()
	w.List(len(out))
	for _, p := range out {
		n := rmc.NewWriter()
		n.U64(p.pid)
		p.s.write(n)
		w.Struct(1, n.Bytes())
	}
	return w.Bytes()
}

// simplePlayingSessionList: list<struct { PID, gid, gameMode, attribute0 }>.
func simplePlayingSessionList(env *Env, pids []uint64) []byte {
	type sps struct {
		pid uint64
		s   *Session
	}
	var out []sps
	for _, pid := range pids {
		for _, s := range env.Match.sessionsOf(pid) {
			out = append(out, sps{pid, s})
		}
	}
	w := rmc.NewWriter()
	w.List(len(out))
	for _, e := range out {
		n := rmc.NewWriter()
		n.U64(e.pid)
		n.U32(e.s.GID)
		n.U32(e.s.GameMode)
		attr0 := uint32(0)
		if len(e.s.Attributes) > 0 {
			attr0 = e.s.Attributes[0]
		}
		n.U32(attr0)
		w.Struct(1, n.Bytes())
	}
	return w.Bytes()
}

func participantPIDs(g *gathering) []uint64 {
	out := make([]uint64, 0, len(g.participants))
	for pid := range g.participants {
		out = append(out, pid)
	}
	return out
}

func writeParticipants(w *rmc.Writer, g *gathering) {
	pids := participantPIDs(g)
	w.List(len(pids))
	for _, pid := range pids {
		w.U64(pid)
	}
}

func readPIDList(r *rmc.Reader) ([]uint64, error) {
	n, err := r.ListHeader()
	if err != nil {
		return nil, err
	}
	out := make([]uint64, 0, min(int(n), 1024))
	for i := uint32(0); i < n; i++ {
		pid, err := r.U64()
		if err != nil {
			return nil, err
		}
		out = append(out, pid)
	}
	return out, nil
}

// readSessionFromHolder parses a GatheringHolder: string class name, u32
// length, u32 buffer length, session structure.
func readSessionFromHolder(r *rmc.Reader) (*Session, error) {
	name, err := r.String()
	if err != nil {
		return nil, err
	}
	if _, err := r.U32(); err != nil {
		return nil, err
	}
	if _, err := r.U32(); err != nil {
		return nil, err
	}
	if name != "MatchmakeSession" && name != "Gathering" {
		// Communities arrive as other class names; parsed as sessions they
		// desync — refuse rather than guess.
		return nil, errUnknownHolder
	}
	return readSession(r)
}

type holderError string

func (e holderError) Error() string { return string(e) }

const errUnknownHolder = holderError("unknown gathering holder class")

// readSessionInParam parses CreateMatchmakeSessionWithParam: the param
// structure wraps the session as its first field.
func readSessionInParam(r *rmc.Reader) (*Session, error) {
	if _, _, err := r.StructHeader(); err != nil {
		return nil, err
	}
	return readSession(r)
}

// getParticipantsURLs implements MatchMaking 0x15 method 0x10:
// GetParticipantsURLs(gid, list<pid>) -> list<RVConnectionData>
// Each RVConnectionData is a list<String> of station URLs for that participant.
func getParticipantsURLs(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	gid, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	pids, err := readPIDList(r)
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	g, ok := env.Match.get(gid)
	if !ok {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	if !g.participants[c.PID] {
		return errorResp(msg, rmc.ResultPermissionDenied)
	}

	w := rmc.NewWriter()
	w.List(len(pids))
	for _, pid := range pids {
		peer, connected := env.push.connection(pid)
		if !connected {
			w.EmptyList()
			continue
		}
		stations := peer.Stations()
		w.List(len(stations))
		for _, s := range stations {
			w.String(s)
		}
	}
	return success(msg, w.Bytes())
}

// getParticipantsURLsExt implements MatchMakingExt 0x32 method 0x04:
// GetParticipantsURLs(list<pid>) -> list<RVConnectionData>
func getParticipantsURLsExt(env *Env, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	pids, err := readPIDList(r)
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}

	w := rmc.NewWriter()
	w.List(len(pids))
	for _, pid := range pids {
		peer, connected := env.push.connection(pid)
		if !connected {
			w.EmptyList()
			continue
		}
		stations := peer.Stations()
		w.List(len(stations))
		for _, s := range stations {
			w.String(s)
		}
	}
	return success(msg, w.Bytes())
}
