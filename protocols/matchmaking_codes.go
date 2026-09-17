// Room codes and the two methods that share a number.
//
// A room code is the short string a player reads out so a friend can join
// directly: Mario Kart's private room (0x44 create, 0x45 resolve) and Smash's
// arena (0x37, 0x38) are the same pair of structures under different numbers.
// Create answers (String code, DateTime); resolve answers a bare u32 gathering
// id, so a caller can go straight to FindMatchmakeSessionBySingleGatheringID.
package protocols

import (
	"crypto/rand"
	"log"
	"strings"
	"sync"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// codeAlphabet omits the characters a player misreads aloud: no O/0, no I/1,
// no S/5. A code exists to be spoken.
const codeAlphabet = "ABCDEFGHJKLMNPQRTUVWXYZ2346789"

// codeLength is five: the length the working reference server issues to
// Mario Kart and Smash, and the length its resolve path expects back
// (earlier-nex matchmaking, read for the fact 2026-09-17). Six was our own
// choice and no console had ever been shown one. 30^5 is about 24 million.
const codeLength = 5

// codeStore maps room codes to gatherings.
type codeStore struct {
	mu     sync.Mutex
	byCode map[string]uint32
	byGID  map[uint32]string
}

// assign mints a code for a gathering, or returns the one it already has so a
// second call does not orphan the first code.
func (cs *codeStore) assign(gid uint32) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.byCode == nil {
		cs.byCode, cs.byGID = map[string]uint32{}, map[uint32]string{}
	}
	if existing, ok := cs.byGID[gid]; ok {
		return existing
	}
	for range 16 { // collisions are rare; give up rather than loop forever
		code := randomCode()
		if _, taken := cs.byCode[code]; taken {
			continue
		}
		cs.byCode[code] = gid
		cs.byGID[gid] = code
		return code
	}
	return ""
}

func (cs *codeStore) resolve(code string) (uint32, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	gid, ok := cs.byCode[strings.ToUpper(strings.TrimSpace(code))]
	return gid, ok
}

// forget drops a gathering's code when the gathering goes.
func (cs *codeStore) forget(gid uint32) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if code, ok := cs.byGID[gid]; ok {
		delete(cs.byCode, code)
		delete(cs.byGID, gid)
	}
}

func randomCode() string {
	b := make([]byte, codeLength)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	out := make([]byte, codeLength)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out)
}

// createRoomCode mints a code for the caller's current session.
func createRoomCode(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	var gid uint32
	for _, s := range env.Match.sessionsOf(c.PID) {
		gid = s.GID
		break
	}
	if gid == 0 {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	code := env.codes.assign(gid)
	if code == "" {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	log.Printf("[%s] room code %s -> gid=%d pid=%d", env.Name, code, gid, c.PID)
	// The answer is a STRUCTURE — header, then (String code, DateTime) — like
	// every other compound value on this wire. Sent bare, MK8D 3.0.5 left its
	// freshly created room 0.2 s after reading it (2026-09-17).
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.String(code)
		w.U64(nowDateTime())
	})
	return success(msg, w.Bytes())
}

// resolveRoomCode turns a code back into a gathering id.
func resolveRoomCode(env *Env, msg *rmc.RMCMessage, r *rmc.Reader) []byte {
	// The code arrives as a bare string or inside a structure header, depending
	// on the title. A bare string of codeLength reads as u16 length = codeLength+1;
	// anything else is the header, skipped.
	body := msg.Body
	if len(body) >= 2 && int(body[0])|int(body[1])<<8 != codeLength+1 && len(body) > 5 {
		r = rmc.NewReader(body[5:])
	}
	code, err := r.String()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	gid, ok := env.codes.resolve(code)
	if !ok {
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	if _, live := env.Match.get(gid); !live {
		env.codes.forget(gid)
		return errorResp(msg, rmc.ResultSessionVoid)
	}
	w := rmc.NewWriter()
	w.U32(gid)
	return success(msg, w.Bytes())
}

// probeOrTournaments answers 0x6D/0x3A, which two titles use for two unrelated
// things. Smash sends it as the probe just before AutoMatchmake and wants a
// bare u32 of 2. Mario Kart sends it as "give me these tournaments": a u32
// count followed by that many u32 ids, so the body is exactly 4+4n bytes, and
// it wants a LIST back — answering 2 there makes it read two entries that are
// not in the buffer.
//
// The request shape is the only thing that tells them apart.
func probeOrTournaments(env *Env, msg *rmc.RMCMessage) []byte {
	if isTournamentIDList(msg.Body) {
		return emptyList(msg)
	}
	w := rmc.NewWriter()
	w.U32(2)
	return success(msg, w.Bytes())
}

// isTournamentIDList reports whether the body is exactly a u32 count followed
// by that many u32 ids.
func isTournamentIDList(body []byte) bool {
	if len(body) < 4 {
		return false
	}
	r := rmc.NewReader(body)
	n, err := r.ListHeader()
	if err != nil {
		return false
	}
	return len(body) == 4+4*int(n)
}

// ---------------------------------------------------------------------------
// Tournaments
// ---------------------------------------------------------------------------

// tournament is one stored tournament. The descriptor is OPAQUE: the console
// sends a structure we have no need to understand, and the only thing the
// server owes it is to hand back exactly what it was given. Parsing it would
// buy nothing and would be one more shape to get wrong.
type tournament struct {
	id      uint32
	owner   uint64
	version uint8
	blob    []byte
}

// tournamentStore holds a title's tournaments in memory.
type tournamentStore struct {
	mu   sync.Mutex
	next uint32
	byID map[uint32]tournament
}

// maxTournaments bounds the store and the search answer. The list is a reply to
// an unauthenticated-ish query and a console will not page through thousands.
const maxTournaments = 100

func (ts *tournamentStore) create(owner uint64, version uint8, blob []byte) tournament {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.byID == nil {
		ts.byID = map[uint32]tournament{}
	}
	ts.next++
	t := tournament{id: ts.next, owner: owner, version: version, blob: append([]byte(nil), blob...)}
	ts.byID[t.id] = t
	return t
}

func (ts *tournamentStore) all() []tournament {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	out := make([]tournament, 0, len(ts.byID))
	for _, t := range ts.byID {
		out = append(out, t)
		if len(out) == maxTournaments {
			break
		}
	}
	return out
}

// remove deletes a tournament, but only for its owner. Ids are sequential
// integers, so without the check anyone could delete somebody else's tournament
// by guessing one.
func (ts *tournamentStore) remove(id uint32, pid uint64) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.byID[id]
	if !ok || t.owner != pid {
		return false
	}
	delete(ts.byID, id)
	return true
}

// createTournament stores the descriptor and echoes it back framed the way it
// arrived: u8 version, u32 length, contents.
func createTournament(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	version, length, err := r.StructHeader()
	if err != nil || length == 0 || int(length) > r.Remaining() {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	blob := make([]byte, length)
	for i := range blob {
		b, err := r.U8()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		blob[i] = b
	}
	t := env.tournaments.create(c.PID, version, blob)
	log.Printf("[%s] tournament %d created by pid=%d (%d bytes)", env.Name, t.id, c.PID, len(blob))

	w := rmc.NewWriter()
	w.Struct(t.version, t.blob)
	return success(msg, w.Bytes())
}

// searchTournaments answers the list: u32 count, then each entry framed.
func searchTournaments(env *Env, msg *rmc.RMCMessage) []byte {
	all := env.tournaments.all()
	w := rmc.NewWriter()
	w.List(len(all))
	for _, t := range all {
		w.Struct(t.version, t.blob)
	}
	return success(msg, w.Bytes())
}

// deleteTournament removes one, if the caller owns it.
func deleteTournament(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage, r *rmc.Reader) []byte {
	id, err := r.U32()
	if err != nil {
		return errorResp(msg, rmc.ResultInvalidArgument)
	}
	if !env.tournaments.remove(id, c.PID) {
		return errorResp(msg, rmc.ResultPermissionDenied)
	}
	return success(msg, nil)
}
