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
	"log"
	"sync"

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
	return &MatchmakingStore{byGID: map[uint32]*gathering{}}
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
		sess, err := readSessionFromHolder(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return autoMatchmake(env, c, msg, sess)

	case 0x04, 0x2A, 0x34: // BrowseMatchmakeSession[NoHolder][NoResultRange](criteria, resultRange?)
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

	case 0x09, 0x0A, 0x0D: // UpdateNotificationData / GetFriendNotificationData / GetlstFriendNotificationData
		if msg.MethodID == 0x0A || msg.MethodID == 0x0D {
			return success(msg, func() []byte {
				w := rmc.NewWriter()
				w.EmptyList()
				return w.Bytes()
			}())
		}
		return success(msg, nil)

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
		// Param: struct { SourceMatchmakeSession, list<PID>, ... } — session
		// first, trailing ponytail block-list unread.
		if _, _, err := r.StructHeader(); err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		sess, err := readSession(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return autoMatchmake(env, c, msg, sess)

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
				found = append(found, g.sess)
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
		return success(msg, g.sess.bytes())

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

	case 0x33: // FindMatchmakeSessionByParticipant(list<PID>, resultOptions[, blockList]) -> list<(PID, session)>
		pids, err := readPIDList(r)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		if len(pids) > 300 { // MPS measured bound
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		return success(msg, participantResultList(env, pids))

	case 0x3C: // title extension: get-simple-playing-session variant -> empty list
		// (Splatoon 2 measured: the reference answered an empty list.)
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList()
			return w.Bytes()
		}())

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
	for len(s.Attributes) < 6 {
		s.Attributes = append(s.Attributes, 0)
	}
	if s.MatchmakeParam == nil {
		s.MatchmakeParam = map[string]rmc.Variant{}
	}
	s.MatchmakeParam["@SR"] = rmc.Variant{TypeID: 3, Bool: true}
	s.MatchmakeParam["@GIR"] = rmc.Variant{TypeID: 1, I64: 3}
}

// autoMatchmake creates when no open session exists, else joins the first.
func autoMatchmake(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, sess *Session) []byte {
	if open := env.Match.open(); len(open) > 0 {
		target := open[0]
		if g, ok := env.Match.get(target.GID); ok {
			g.participants[c.PID] = true
		}
		target.ParticipationCount++
		log.Printf("[%s] matchmake: auto-joined gid=%d pid=%d", env.Name, target.GID, c.PID)
		return success(msg, target.bytes())
	}
	return createSession(env, c, msg, sess)
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

// updateSessionPart applies 0x2C flag-gated partial updates. Flags follow the
// public modification-flag table (provenance: nex-protocols-go constants):
// 0x1 attributes, 0x2 openParticipation, 0x4 applicationBuffer,
// 0x8 progressScore, 0x20/0x40 matchmakeParam, 0x80 startedTime,
// 0x100 userPassword, 0x200 gameMode, 0x400 description, 0x800 min, 0x1000
// max, 0x2000 matchmakeSystemType, 0x4000 codeword.
func updateSessionPart(env *Env, msg *rmc.RMCMessage, r *rmc.Reader) []byte {
	if _, _, err := r.StructHeader(); err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	gid, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	flags, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	g, ok := env.Match.get(gid)
	if !ok {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	s := g.sess
	if flags&0x0001 != 0 {
		n, err := r.ListHeader()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		attrs := make([]uint32, 0, n)
		for i := uint32(0); i < n; i++ {
			v, err := r.U32()
			if err != nil {
				return errorResp(msg, rmc.ResultInvalidArgument)
			}
			attrs = append(attrs, v)
		}
		s.Attributes = attrs
	}
	if flags&0x0002 != 0 {
		if v, err := r.Bool(); err == nil {
			s.OpenParticipation = v
		}
	}
	if flags&0x0004 != 0 {
		if v, err := r.Buffer(); err == nil {
			s.ApplicationBuffer = v
		}
	}
	if flags&0x0008 != 0 {
		if v, err := r.U8(); err == nil {
			s.ProgressScore = v
		}
	}
	if flags&0x0020 != 0 || flags&0x0040 != 0 {
		if m, err := readStringVariantMap(r); err == nil {
			s.MatchmakeParam = m
		}
	}
	if flags&0x0080 != 0 {
		if v, err := r.U64(); err == nil {
			s.StartedTime = v
		}
	}
	if flags&0x0100 != 0 {
		if v, err := r.String(); err == nil {
			s.UserPassword = v
		}
	}
	if flags&0x0200 != 0 {
		if v, err := r.U32(); err == nil {
			s.GameMode = v
		}
	}
	if flags&0x0400 != 0 {
		if v, err := r.String(); err == nil {
			s.Description = v
		}
	}
	if flags&0x0800 != 0 {
		if v, err := r.U16(); err == nil {
			s.MinParticipants = v
		}
	}
	if flags&0x1000 != 0 {
		if v, err := r.U16(); err == nil {
			s.MaxParticipants = v
		}
	}
	if flags&0x2000 != 0 {
		if v, err := r.U32(); err == nil {
			s.MatchmakeSystemType = v
		}
	}
	if flags&0x4000 != 0 {
		if v, err := r.String(); err == nil {
			s.CodeWord = v // ACNH's Dodo Code rides in here (flags 0x4002)
		}
	}
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
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList()
			return w.Bytes()
		}())

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
			env.Match.delete(gid)
		}
		return boolTrue(msg)

	case 0x29: // GetSessionURLs(list<gid>) -> list<URL>
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList()
			return w.Bytes()
		}())

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
				g.sess.ParticipationCount--
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
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList()
			return w.Bytes()
		}())

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

// participantResultList: FindMatchmakeSessionByParticipant ->
// list<struct { PID u64, session }> for every live session of the named
// players. ACNH measured: the session must be the full structure including
// the session key.
func participantResultList(env *Env, pids []uint64) []byte {
	type pair struct {
		pid uint64
		s   *Session
	}
	var pairs []pair
	seen := map[uint32]bool{}
	for _, pid := range pids {
		for _, s := range env.Match.sessionsOf(pid) {
			if !seen[s.GID] {
				seen[s.GID] = true
				pairs = append(pairs, pair{pid, s})
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
