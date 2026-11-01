// Super Mario Maker 2 course storage: upload, download, search and list.
//
// Wire shapes are kinnay's NintendoClients datastore_smm2 definitions (MIT) and
// its wiki page "Data Store Protocol (SMM 2)". The wiki leaves most of
// PreparePostCourseParam as "Unknown"; which of its fields carry the course name,
// description, style and theme is INFERRED from the order of CourseInfo (see
// parsePreparePostCourse) and has not been seen from a console. The raw
// parameter is stored with every course so a first capture can correct the
// mapping without losing uploads.
//
// The server stores nothing itself: the metadata lives behind SMM2Backend
// (Postgres in production) and the level body and thumbnails are S3 objects the
// console reads and writes directly through presigned URLs (SMM2Blobs).
package protocols

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// SMM2 course statuses.
const (
	SMM2Pending  = 0 // PreparePost answered, body not confirmed yet
	SMM2Complete = 1 // visible
	SMM2Deleted  = 2
)

// SMM2MaxCourses is the "32/32" upload slot count of one maker.
const SMM2MaxCourses = 32

// Relation object types a console uploads next to a course (method 132).
const (
	SMM2RelOneScreen = 1
	SMM2RelEntire    = 2
	SMM2RelReport    = 3
	SMM2RelReplay    = 5
)

// SMM2Course is one uploaded course as the store keeps it.
type SMM2Course struct {
	DataID         uint64
	Owner          uint64
	Name           string
	Description    string
	Style          uint8
	Theme          uint8
	Difficulty     uint8
	Tag1, Tag2     uint8
	ClearCondition uint32
	ClearMagnitude uint16
	Status         int
	Created        time.Time
	Updated        time.Time
	Plays          uint32
	Clears         uint32
	Attempts       uint32
	Stars          uint32 // hearts
	BodySize       uint32
	Rel            map[uint8]uint32 // relation type -> uploaded size
	Raw            []byte           // PreparePostCourseParam as received
}

// SMM2Query selects courses for a search method.
type SMM2Query struct {
	Owners []uint64 // empty: every maker
	Sort   string   // "newest" (default), "stars", "plays"
	Text   string   // name/description substring, read API only
	Offset int
	Limit  int
	Since  time.Time // updated at or after, read API only
}

// SMM2Maker is a registered maker with the totals the read API shows.
type SMM2Maker struct {
	PID     uint64
	Name    string
	Created time.Time
	Courses int
	Stars   int
	Plays   int
}

// SMM2Backend is the durable half of the SMM2 title.
type SMM2Backend interface {
	PutUser(ctx context.Context, pid uint64, u *SMM2UserRecord) error
	GetUser(ctx context.Context, pid uint64) (*SMM2UserRecord, bool, error)
	// NewCourse allocates a data id and stores a pending course.
	NewCourse(ctx context.Context, c SMM2Course) (uint64, error)
	// UpdateCourse sets the descriptive fields of a course the owner posted.
	UpdateCourse(ctx context.Context, c SMM2Course) (bool, error)
	// CompleteCourse makes a pending course visible; false when the owner has none such.
	CompleteCourse(ctx context.Context, owner, dataID uint64, bodySize uint32) (bool, error)
	// SetRelation records an uploaded relation object (thumbnail, replay).
	SetRelation(ctx context.Context, owner, dataID uint64, typ uint8, size uint32) (bool, error)
	// LatestPending is the owner's newest pending course, 0 when none.
	LatestPending(ctx context.Context, owner uint64) (uint64, error)
	DeleteCourse(ctx context.Context, owner, dataID uint64) (bool, error)
	GetCourses(ctx context.Context, ids []uint64) (map[uint64]SMM2Course, error)
	Search(ctx context.Context, q SMM2Query) ([]SMM2Course, int, error)
	CountByOwner(ctx context.Context, owner uint64) (int, error)
}

// SMM2UserRecord is the stored maker profile.
type SMM2UserRecord = smm2User

// SMM2Blobs hands the console presigned URLs for course bodies and thumbnails.
type SMM2Blobs interface {
	// PostForm returns the upload URL and the form fields the console must send
	// with the file (S3 POST policy), limited to maxSize bytes.
	PostForm(key string, maxSize uint32, ttl time.Duration) (url string, fields [][2]string, err error)
	// GetURL is a presigned GET for key.
	GetURL(key string, ttl time.Duration) string
	// Size is the stored object's size (a HEAD), ok false when absent.
	Size(key string) (size int64, ok bool)
}

// SMM2BodyKey and SMM2RelationKey are the bucket keys.
func SMM2BodyKey(id uint64) string { return fmt.Sprintf("course/%d.bcd", id) }

// SMM2RelationKey is the key of a relation object.
func SMM2RelationKey(id uint64, typ uint8) string { return fmt.Sprintf("relation/%d/%d", id, typ) }

const (
	smm2BodyMax     = 4 << 20 // a .bcd is a few hundred KiB
	smm2RelationMax = 2 << 20 // thumbnails and replays
	smm2URLTTL      = time.Hour
	smm2DBTimeout   = 8 * time.Second
	smm2CodeBits    = 36 // data ids stay under 2^36; the checksum fills the rest of 30^9
)

// --- course codes ---------------------------------------------------------

func smm2Check(id uint64) uint64 {
	h := sha256.Sum256(binary.LittleEndian.AppendUint64([]byte("openpak-smm2-course:"), id))
	return uint64(h[0])
}

// SMM2CourseCode is the visible XXX-XXX-XXX id of a course: the data id and an
// 8-bit checksum in base 30. These are OpenPak's own codes, not Nintendo's.
func SMM2CourseCode(id uint64) string {
	n := smm2Check(id)<<smm2CodeBits | id&(1<<smm2CodeBits-1)
	b := make([]byte, 9)
	for i := 8; i >= 0; i-- {
		b[i] = makerCodeAlphabet[n%30]
		n /= 30
	}
	return string(b[0:3]) + "-" + string(b[3:6]) + "-" + string(b[6:9])
}

// SMM2ParseCourseCode reverses SMM2CourseCode; ok is false for a mistyped code.
func SMM2ParseCourseCode(code string) (uint64, bool) {
	code = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if len(code) != 9 {
		return 0, false
	}
	var n uint64
	for _, c := range code {
		i := strings.IndexRune(makerCodeAlphabet, c)
		if i < 0 {
			return 0, false
		}
		n = n*30 + uint64(i)
	}
	id := n & (1<<smm2CodeBits - 1)
	if n>>smm2CodeBits != smm2Check(id) {
		return 0, false
	}
	return id, true
}

// SMM2MakerCode is a maker's visible id.
func SMM2MakerCode(pid uint64) string { return makerCode(pid) }

// --- request parsing ------------------------------------------------------

// postCourseParam is what PreparePostCourseParam yields. Mapping is inferred.
type postCourseParam struct {
	Name, Description string
	Style, Theme      uint8
	ClearCondition    uint32
	ClearMagnitude    uint16
}

// parsePreparePostCourse reads PreparePostCourseParam's documented layout:
// string, string, u32, bool, u8, u8, u32 x4, qbuffer, u8, u32, u16, u16, bool,
// u32, u32, list<string>. Fields the wiki calls Unknown are assigned by the
// order CourseInfo uses (name, description, ..., style, theme, ...,
// clear condition, magnitude); a short or odd parameter yields what was read.
func parsePreparePostCourse(r *rmc.Reader) postCourseParam {
	var p postCourseParam
	p.Name, _ = r.String()
	p.Description, _ = r.String()
	r.U32()
	r.Bool()
	p.Style, _ = r.U8()
	p.Theme, _ = r.U8()
	for range 4 {
		r.U32()
	}
	r.QBuffer()
	r.U8()
	p.ClearCondition, _ = r.U32()
	p.ClearMagnitude, _ = r.U16()
	return p
}

func ctxDB() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), smm2DBTimeout)
}

func formList(w *rmc.Writer, kv [][2]string) {
	w.List(len(kv))
	for _, p := range kv {
		w.StructOf(0, func(w *rmc.Writer) { w.String(p[0]); w.String(p[1]) })
	}
}

func (e *Env) smm2Ready() bool { return e.SMM2 != nil && e.SMM2Blobs != nil }

// smm2CourseMethods serves the course half of the DSSMM2 DataStore.
func smm2CourseMethods(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) ([]byte, bool) {
	if !env.smm2Ready() {
		return nil, false
	}
	r := rmc.NewReader(msg.Body)
	ctx, cancel := ctxDB()
	defer cancel()
	switch msg.MethodID {
	case 0x42: // PreparePostObjectCourse(PreparePostCourseParam) -> DataStoreReqPostInfo
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		n, err := env.SMM2.CountByOwner(ctx, c.PID)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		if n >= SMM2MaxCourses {
			return errorResp(msg, rmc.ResultAccessDenied), true
		}
		pc := parsePreparePostCourse(p)
		id, err := env.SMM2.NewCourse(ctx, SMM2Course{
			Owner: c.PID, Name: pc.Name, Description: pc.Description, Style: pc.Style, Theme: pc.Theme,
			ClearCondition: pc.ClearCondition, ClearMagnitude: pc.ClearMagnitude, Raw: msg.Body,
		})
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		url, fields, err := env.SMM2Blobs.PostForm(SMM2BodyKey(id), smm2BodyMax, smm2URLTTL)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.U64(id)
			w.String(url)
			w.EmptyList() // headers
			formList(w, fields)
			w.Buffer(nil) // root CA: none pinned
		})
		return success(msg, w.Bytes()), true

	case 0x44: // CompletePostObjectsCourse(CompletePostObjectsCourseParam) -> empty
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		for range 5 {
			if _, err := p.String(); err != nil {
				return errorResp(msg, rmc.ResultInvalidArgument), true
			}
		}
		id, err := p.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		if _, inner, err := p.Struct(); err == nil {
			pc := parsePreparePostCourse(inner)
			// A refreshed description: only overwrite what the parameter carries.
			if pc.Name != "" {
				env.SMM2.UpdateCourse(ctx, SMM2Course{DataID: id, Owner: c.PID, Name: pc.Name, Description: pc.Description,
					Style: pc.Style, Theme: pc.Theme, ClearCondition: pc.ClearCondition, ClearMagnitude: pc.ClearMagnitude})
			}
		}
		if ok := smm2Finish(ctx, env, c.PID, id); !ok {
			if latest, _ := env.SMM2.LatestPending(ctx, c.PID); latest != 0 {
				ok = smm2Finish(ctx, env, c.PID, latest)
			}
			if !ok {
				return errorResp(msg, rmc.ResultDataStoreNotFound), true
			}
		}
		return success(msg, nil), true

	case 0x1A: // CompletePostObject(DataStoreCompletePostParam{dataId, success})
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		id, err := p.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		ok, err := p.Bool()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		if !ok {
			env.SMM2.DeleteCourse(ctx, c.PID, id)
			return success(msg, nil), true
		}
		if !smm2Finish(ctx, env, c.PID, id) {
			return errorResp(msg, rmc.ResultDataStoreNotFound), true
		}
		return success(msg, nil), true

	case 0x84: // PreparePostRelationObject(PreparePostRelationObjectParam) -> RelationObjectReqPostInfo
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		ds, _ := p.String()
		var u [4]uint32
		for i := range u {
			u[i], _ = p.U32()
		}
		typ, ok := relationType(u)
		if !ok {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		id, _ := strconv.ParseUint(strings.TrimSpace(ds), 10, 64)
		if id == 0 {
			id, _ = env.SMM2.LatestPending(ctx, c.PID)
		}
		if id == 0 {
			return errorResp(msg, rmc.ResultDataStoreNotFound), true
		}
		url, fields, err := env.SMM2Blobs.PostForm(SMM2RelationKey(id, typ), smm2RelationMax, smm2URLTTL)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		env.SMM2.SetRelation(ctx, c.PID, id, typ, u[1])
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.String(strconv.FormatUint(id, 10))
			w.String(url)
			w.EmptyList()
			formList(w, fields)
			w.Buffer(nil)
		})
		return success(msg, w.Bytes()), true

	case 0x19: // PrepareGetObject(DataStorePrepareGetParam) -> DataStoreReqGetInfo
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		id, err := p.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		got, err := env.SMM2.GetCourses(ctx, []uint64{id})
		cr, ok := got[id]
		if err != nil || !ok || cr.Status != SMM2Complete {
			return errorResp(msg, rmc.ResultDataStoreNotFound), true
		}
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.String(env.SMM2Blobs.GetURL(SMM2BodyKey(id), smm2URLTTL))
			w.EmptyList()
			w.U32(cr.BodySize)
			w.Buffer(nil)
			w.U64(id)
		})
		return success(msg, w.Bytes()), true

	case 0x04: // DeleteObject(DataStoreDeleteParam{dataId, password})
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		id, err := p.U64()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		if ok, _ := env.SMM2.DeleteCourse(ctx, c.PID, id); !ok {
			return errorResp(msg, rmc.ResultDataStoreNotFound), true
		}
		return success(msg, nil), true

	case 0x46: // GetCourses(GetCoursesParam{ids, option}) -> (List<CourseInfo>, List<Result>)
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		ids, err := readPIDList(p) // List<u64> of data ids
		if err != nil || len(ids) > 200 {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		got, err := env.SMM2.GetCourses(ctx, ids)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		list, results := rmc.NewWriter(), rmc.NewWriter()
		results.List(len(ids))
		n := 0
		for _, id := range ids {
			cr, ok := got[id]
			if !ok || cr.Status != SMM2Complete {
				results.U32(resultNotFoundInList)
				continue
			}
			writeSMM2CourseInfo(list, env, cr)
			results.U32(resultSuccess)
			n++
		}
		w := rmc.NewWriter()
		w.List(n)
		w.Raw(list.Bytes())
		w.Raw(results.Bytes())
		return success(msg, w.Bytes()), true

	case 0x49, 0x48, 0x4A, 0x47: // Latest, Advanced, PostedBy, PointRanking
		_, p, err := r.Struct()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		if _, err := p.U32(); err != nil { // result option
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		_, rng, err := p.Struct() // ResultRange{offset, size}
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		off, _ := rng.U32()
		size, _ := rng.U32()
		q := SMM2Query{Offset: int(min(off, 10000)), Limit: int(min(max(size, 1), 100)), Sort: "newest"}
		switch msg.MethodID {
		case 0x4A: // PostedBy: owners follow the range
			owners, err := readPIDList(p)
			if err != nil {
				return errorResp(msg, rmc.ResultInvalidArgument), true
			}
			q.Owners = owners
			if len(owners) == 0 {
				return smm2CourseList(env, msg, nil, false), true
			}
		case 0x47:
			q.Sort = "stars"
		}
		found, _, err := env.SMM2.Search(ctx, q)
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument), true
		}
		return smm2CourseList(env, msg, found, msg.MethodID == 0x47), true
	}
	return nil, false
}

func smm2Finish(ctx context.Context, env *Env, owner, id uint64) bool {
	var size uint32
	if n, ok := env.SMM2Blobs.Size(SMM2BodyKey(id)); ok {
		size = uint32(n)
	}
	ok, err := env.SMM2.CompleteCourse(ctx, owner, id, size)
	return err == nil && ok
}

// relationType picks the relation type out of PreparePostRelationObjectParam's
// four u32s: the first is the type per earlier research notes; any of the four
// matching 1, 2, 3 or 5 is accepted when it is not.
func relationType(u [4]uint32) (uint8, bool) {
	ok := func(v uint32) bool { return v == 1 || v == 2 || v == 3 || v == 5 }
	if ok(u[0]) {
		return uint8(u[0]), true
	}
	for _, v := range u[1:] {
		if ok(v) {
			return uint8(v), true
		}
	}
	return 0, false
}

// smm2CourseList answers (List<CourseInfo>, [List<u32> ranks,] Bool).
func smm2CourseList(env *Env, msg *rmc.RMCMessage, cs []SMM2Course, ranks bool) []byte {
	w := rmc.NewWriter()
	w.List(len(cs))
	for _, cr := range cs {
		writeSMM2CourseInfo(w, env, cr)
	}
	if ranks {
		w.List(len(cs))
		for i := range cs {
			w.U32(uint32(i + 1))
		}
	}
	w.Bool(true)
	return success(msg, w.Bytes())
}

// writeSMM2CourseInfo writes CourseInfo exactly as datastore_smm2 loads it. The
// result option only selects what a real server fills in; every field is present.
func writeSMM2CourseInfo(w *rmc.Writer, env *Env, cr SMM2Course) {
	rel := func(typ uint8, relType uint8, name string) func(w *rmc.Writer) {
		return func(w *rmc.Writer) {
			size, ok := cr.Rel[typ]
			url := ""
			if ok && env.SMM2Blobs != nil {
				url = env.SMM2Blobs.GetURL(SMM2RelationKey(cr.DataID, typ), smm2URLTTL)
			}
			w.String(url)
			w.U8(relType)
			w.U32(size)
			w.Buffer(nil)
			w.String(name)
		}
	}
	w.StructOf(0, func(w *rmc.Writer) {
		w.U64(cr.DataID)
		w.String(SMM2CourseCode(cr.DataID))
		w.U64(cr.Owner)
		w.String(cr.Name)
		w.String(cr.Description)
		w.U8(cr.Style)
		w.U8(cr.Theme)
		w.U64(switchnex.MakeDateTime(cr.Created))
		w.U8(cr.Difficulty)
		w.U8(cr.Tag1)
		w.U8(cr.Tag2)
		w.U8(0)
		w.U32(cr.ClearCondition)
		w.U16(cr.ClearMagnitude)
		w.U16(0)
		w.QBuffer(nil)
		// play stats: 0 plays, 1 attempts, 2 unknown, 3 clears, 4 versus plays
		u8u32Map(w, cr.Plays, cr.Attempts, 0, cr.Clears, 0)
		u8u32Map(w, cr.Stars, 0, 0)         // ratings: 0 hearts
		u8u32Map(w, 0)                      // unknown
		w.StructOf(0, func(w *rmc.Writer) { // time stats
			w.U64(0)
			w.U64(0)
			w.U32(0)
			w.U32(0)
		})
		u8u32Map(w, 0) // comment stats: 0 comments
		w.U8(0)
		w.U8(0)
		w.U8(0)
		w.U8(0)
		w.StructOf(0, rel(SMM2RelOneScreen, 2, fmt.Sprintf("%d_one_screen.jpg", cr.DataID)))
		w.StructOf(0, rel(SMM2RelEntire, 3, fmt.Sprintf("%d_entire.jpg", cr.DataID)))
	})
}
