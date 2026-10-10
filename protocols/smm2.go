// Super Mario Maker 2's DataStore title methods (0x73, DSSMM2): the user
// profile the title menu and Course World read first, and valid empty answers
// for every course list the Course World hub asks for.
//
// Shapes are kinnay's NintendoClients datastore_smm2 definitions and the wiki's
// Data-Store-Protocol-(SMM-2) page; which methods the console calls, and which
// answers it accepted, come from the reference's notes (facts only).
//
// Boot asks GetUsers (0x30) for the caller's own profile. A player who never
// registered gets NotFound and, on first entering Course World, picks a Mii,
// which sends RegisterUser (0x2F) and asks GetUsers again: that second answer
// must carry the profile just registered. Profiles live in memory, like the
// sessions: a restart sends a returning player through the Mii picker again.
//
// No course storage yet: every list is empty, so Course World opens with
// nothing in it and nothing can be uploaded or downloaded.
package protocols

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// resultSuccess is Core::Success as it travels inside a List<Result>.
const resultSuccess uint32 = 0x00010001

// resultNotFoundInList is DataStore::NotFound inside a List<Result>: the raw
// code with its error bit. (The RMC error path adds the bit itself.)
const resultNotFoundInList = rmc.ResultDataStoreNotFound | rmc.ErrorCodeBit

// smm2User is one registered maker.
type smm2User struct {
	Name    string
	Clothes [4]uint16 // UnknownStruct1: pose, hat, shirt, pants
	Mii     []byte
	Region  uint8
	Country string
	Device  string
}

type smm2Store struct {
	mu    sync.Mutex
	users map[uint64]*smm2User
}

func (s *smm2Store) get(pid uint64) (*smm2User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[pid]
	return u, ok
}

func (s *smm2Store) put(pid uint64, u *smm2User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.users == nil {
		s.users = map[uint64]*smm2User{}
	}
	s.users[pid] = u
}

// makerCodeAlphabet is the 30 characters SMM2 maker and course codes use.
const makerCodeAlphabet = "0123456789BCDFGHJKLMNPQRSTVWXY"

// makerCode is a player's 9-character maker id, derived from the PID so it
// is the same on every session and after a restart.
func makerCode(pid uint64) string {
	h := sha256.Sum256(binary.LittleEndian.AppendUint64([]byte("openpak-smm2-maker:"), pid))
	n := binary.LittleEndian.Uint64(h[:8])
	b := make([]byte, 9)
	for i := range b {
		b[i] = makerCodeAlphabet[n%uint64(len(makerCodeAlphabet))]
		n /= uint64(len(makerCodeAlphabet))
	}
	return string(b)
}

// smm2DataStore serves the DSSMM2 methods it knows and reports whether it
// answered; everything else falls through to the generic DataStore switch.
func smm2DataStore(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) ([]byte, bool) {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x19: // PrepareGetObject: no objects stored, so nothing to fetch
		return errorResp(msg, rmc.ResultDataStoreNotFound), true

	case 0x2F: // RegisterUser(RegisterUserParam) -> empty
		u, err := readSMM2Profile(r, false)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		env.smm2.put(c.PID, u)
		return success(msg, nil), true

	case 0x30: // GetUsers(GetUsersParam) -> (List<UserInfo>, List<Result>)
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		pids, err := readPIDList(p)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		if len(pids) == 0 { // an empty list asks for the caller's own profile
			pids = []uint64{c.PID}
		}
		found := rmc.NewWriter()
		results := rmc.NewWriter()
		n := 0
		results.List(len(pids))
		for _, pid := range pids {
			u, ok := env.smm2.get(pid)
			if !ok {
				results.U32(resultNotFoundInList)
				continue
			}
			writeSMM2UserInfo(found, pid, u)
			results.U32(resultSuccess)
			n++
		}
		w := rmc.NewWriter()
		w.List(n)
		w.Raw(found.Bytes())
		w.Raw(results.Bytes())
		return success(msg, w.Bytes()), true

	case 0x31: // SyncUserProfile(SyncUserProfileParam) -> SyncUserProfileResult
		u, err := readSMM2Profile(r, true)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		old, ok := env.smm2.get(c.PID)
		if !ok {
			return errorResp(msg, rmc.ResultDataStoreNotFound), true
		}
		u.Device = old.Device
		env.smm2.put(c.PID, u)
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.U64(c.PID)
			w.String(u.Name)
			writeClothes(w, u.Clothes)
			w.QBuffer(u.Mii)
			w.U8(u.Region)
			w.String(u.Country)
			w.U8(0)
			w.Bool(false)
			w.Bool(false)
		})
		return success(msg, w.Bytes()), true

	case 0x3C: // CanPostCourse -> (bool, u32); a lone u32 desyncs the client
		w := rmc.NewWriter()
		w.Bool(true)
		w.U32(0)
		return success(msg, w.Bytes()), true
	}
	if body, ok := smm2EmptyAnswers[msg.MethodID]; ok {
		return success(msg, body), true
	}
	return nil, false
}

// readSMM2Profile reads RegisterUserParam, or with sync the longer
// SyncUserProfileParam: both open with name, clothes, Mii, a region byte and
// the country.
func readSMM2Profile(r *rmc.Reader, sync bool) (*smm2User, error) {
	_, p, err := r.Struct()
	if err != nil {
		return nil, err
	}
	u := &smm2User{}
	if u.Name, err = p.String(); err != nil {
		return nil, err
	}
	_, cl, err := p.Struct()
	if err != nil {
		return nil, err
	}
	for i := range u.Clothes {
		if u.Clothes[i], err = cl.U16(); err != nil {
			return nil, err
		}
	}
	if u.Mii, err = p.QBuffer(); err != nil {
		return nil, err
	}
	if u.Region, err = p.U8(); err != nil {
		return nil, err
	}
	if u.Country, err = p.String(); err != nil {
		return nil, err
	}
	if !sync {
		if u.Device, err = p.String(); err != nil {
			return nil, err
		}
	}
	return u, nil
}

func writeClothes(w *rmc.Writer, c [4]uint16) {
	w.StructOf(0, func(w *rmc.Writer) {
		for _, v := range c {
			w.U16(v)
		}
	})
}

// u8u32Map writes a Map<u8,u32> holding keys 0..len(values)-1.
func u8u32Map(w *rmc.Writer, values ...uint32) {
	w.List(len(values))
	for k, v := range values {
		w.U8(uint8(k))
		w.U32(v)
	}
}

// writeSMM2UserInfo writes UserInfo version 3: a new maker with nothing
// played, made or uploaded yet. Every map is present whatever the result
// option asked for; the option only selects what the server fills in.
func writeSMM2UserInfo(w *rmc.Writer, pid uint64, u *smm2User) {
	now := nowDateTime()
	w.StructOf(3, func(w *rmc.Writer) {
		w.U64(pid)
		w.String(makerCode(pid))
		w.String(u.Name)
		writeClothes(w, u.Clothes)
		w.QBuffer(u.Mii)
		w.String(u.Country)
		w.U8(u.Region)
		w.U64(now)                          // last active
		w.Bool(true)                        // unknown; the reference sent the three flags true
		w.Bool(true)                        // comments enabled: false reads "the creator restricted comments"
		w.Bool(true)                        // tags enabled
		u8u32Map(w, 0, 0, 0, 0)             // play stats: played, cleared, attempts, deaths
		u8u32Map(w, 0, 0)                   // maker stats: likes, maker points
		u8u32Map(w, 0, 0, 0, 0)             // endless high scores per difficulty
		u8u32Map(w, make([]uint32, 15)...)  // multiplayer stats, keys 0..14
		u8u32Map(w, 0)                      // weekly maker points
		w.EmptyList()                       // badges
		u8u32Map(w, 0, 0)                   // first clears, world records
		u8u32Map(w, 0, 32)                  // uploaded courses, upload slots
		w.Bool(false)                       // v1: Nintendo employee
		w.U64(now)                          // v1: last uploaded
		w.Bool(false)                       // v1: unknown
		w.StructOf(0, func(w *rmc.Writer) { // v2: UnknownStruct3
			w.Bool(false)
			w.U64(now)
		})
		w.StringEmptyZero("") // v3: super world id (none)
		u8u32Map(w, 0)        // v3: unique super world clears
		w.Bool(true)          // v3: unknown; the reference sent true
	})
}

// smm2EmptyAnswers are the valid "nothing here" answers for the methods the
// Course World hub and the user screens call, shapes per kinnay's
// datastore_smm2. An NotImplemented on any of them reads as "A communication
// error occurred".
var smm2EmptyAnswers = func() map[uint32][]byte {
	empty := func(build func(w *rmc.Writer)) []byte {
		w := rmc.NewWriter()
		build(w)
		return w.Bytes()
	}
	list := empty(func(w *rmc.Writer) { w.EmptyList() })
	listList := empty(func(w *rmc.Writer) { w.EmptyList(); w.EmptyList() })
	listBool := empty(func(w *rmc.Writer) { w.EmptyList(); w.Bool(true) })
	listListBool := empty(func(w *rmc.Writer) { w.EmptyList(); w.EmptyList(); w.Bool(true) })
	rating := func(w *rmc.Writer) { w.StructOf(0, func(w *rmc.Writer) { w.U32(0); w.U8(0) }) }
	return map[uint32][]byte{
		0x35: list,         // SearchUsersPlayedCourse
		0x36: list,         // SearchUsersClearedCourse
		0x37: list,         // SearchUsersPositiveRatedCourse
		0x3A: listListBool, // SearchUsersUserPointRanking
		0x3B: nil,          // UpdateLastLoginTime
		0x3F: list,         // GetMiiClothes
		0x40: nil,          // PostActivityQuest
		0x41: {0},          // GetUserNameNgType
		0x46: listList,     // GetCourses
		0x47: listListBool, // SearchCoursesPointRanking
		0x48: listBool,     // SearchCoursesAdvanced
		0x49: listBool,     // SearchCoursesLatest
		0x4A: listBool,     // SearchCoursesPostedBy
		0x4B: list,         // SearchCoursesPositiveRatedBy
		0x4C: list,         // SearchCoursesPlayedBy
		0x4F: list,         // SearchCoursesEndlessMode
		0x50: listBool,     // SearchCoursesFirstClear
		0x51: listBool,     // SearchCoursesBestTime
		0x53: listListBool, // SearchCoursesTermsRanking
		0x54: list,         // SearchCoursesPickUp
		0x55: listList,     // GetCoursesEvent
		0x56: list,         // SearchCoursesEvent
		0x5E: listBool,     // SearchCommentsInOrder
		0x5F: list,         // SearchComments
		0x6C: empty(func(w *rmc.Writer) { // GetEndlessModeStatus
			w.StructOf(0, func(w *rmc.Writer) { w.EmptyList(); w.EmptyList() })
		}),
		0x75: empty(func(w *rmc.Writer) { w.Bool(false); rating(w); rating(w) }), // GetBattleModeRating
		0x99: list,                                                               // GetEventCourseStamp
		0x9A: empty(func(w *rmc.Writer) { // GetEventCourseStatus
			w.StructOf(0, func(w *rmc.Writer) { w.U64(0); w.Bool(false); w.U64(nowDateTime()) })
		}),
		0xA0: listList, // GetWorldMap
		0xA2: list,     // SearchWorldMapPickUp
	}
}()
