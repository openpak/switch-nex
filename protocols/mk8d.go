// Mario Kart 8 Deluxe: durable Ranking (0x70), competitions (tournament
// leaderboards) and DataStore ghosts.
//
// Wire layouts are the public NintendoClients wiki by Kinnay (MIT):
// "Ranking Protocol", "Ranking Protocol (MK8D)", "Matchmake Extension Protocol
// (MK8D)" and "Data Store Protocol". What is FACT (documented layout) and what
// is INFERRED (meaning of fields the wiki calls Unknown, or behaviour no
// console capture has confirmed yet) is listed in docs/mk8d-rankings.md.
//
// The server stores through MK8DBackend (Postgres in production, see
// internal/mk8dstore); with no backend the title keeps its old stateless
// behaviour (empty boards, acknowledged uploads).
package protocols

import (
	"context"
	"encoding/hex"
	"log"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/redact"
	"github.com/openpak/switch-nex/rmc"
)

// RankingScore is one RankingScoreData plus who uploaded it.
type RankingScore struct {
	Category   uint32
	Score      uint32
	OrderBy    uint8
	UpdateMode uint8
	Groups     []byte
	Param      uint64
}

// Ranking modes (Ranking wiki, "Ranking Mode").
const (
	RankModeGlobal = 0 // top of the board, only the first 1000 can be downloaded
	RankModeAround = 1 // global ranks around the caller's own entry
	RankModeSelf   = 4 // the caller's own entry only
)

// RankingMaxOffset is the "only the top 1000 entries can be downloaded" ceiling.
const RankingMaxOffset = 1000

// RankQuery selects board rows.
type RankQuery struct {
	Category   uint32
	Mode       uint8
	Calc       uint8 // 0 standard (1224), 1 ordinal (1234)
	GroupIndex uint8 // 0xFF: no group filter
	GroupNum   uint8
	Offset     uint32
	Length     uint32
	Self       uint64   // the caller (modes 1 and 4)
	PIDs       []uint64 // non-nil: only these players (GetRankingByPIDList)
	UniqueIDs  []uint64 // non-nil: only these unique ids
}

// RankRow is one RankingRankData.
type RankRow struct {
	PID        uint64
	UniqueID   uint64
	Order      uint32
	Category   uint32
	Score      uint32
	Groups     []byte
	Param      uint64
	CommonData []byte
	Updated    time.Time
}

// CompetitionUpload is a CompetitionRankingUploadScoreParam. The wiki leaves
// every field Unknown; the order id, season, ?, score, team, team score, first
// upload flag, metadata is the Wii U layout, which has the same field types.
type CompetitionUpload struct {
	ID, Season, Unknown, Score uint32
	TeamID                     uint8
	TeamScore                  uint32
	First                      bool
	Metadata                   []byte
}

// CompetitionScore is one CompetitionRankingScoreData.
type CompetitionScore struct {
	Rank     uint32
	PID      uint64
	Score    uint32
	Updated  time.Time
	TeamID   uint8
	Metadata []byte
}

// CompetitionSeason is one CompetitionRankingScoreInfo.
type CompetitionSeason struct {
	Season       uint32
	Scores       []CompetitionScore
	Participants uint32
	TeamScores   []uint32
}

// CompetitionSummary is one CompetitionRankingInfo.
type CompetitionSummary struct {
	ID           uint32
	Participants uint32
	TeamScores   []uint32
}

// MK8DTournament is a stored SimpleSearchObject descriptor.
type MK8DTournament struct {
	ID      uint32
	Owner   uint64
	Version uint8
	Blob    []byte // structure contents, as the console sent them
}

// MK8DObject is a DataStore object (a time-trial ghost).
type MK8DObject struct {
	DataID      uint64
	Owner       uint64
	Name        string
	Size        uint32
	DataType    uint16
	Meta        []byte
	Tags        []string
	Slot        int // -1: no persistence slot
	ReferDataID uint32
	Flag        uint32
	Permission  uint8
	Status      int
	Created     time.Time
	Updated     time.Time
	Raw         []byte // PreparePostParam as received
}

// MK8D object statuses.
const (
	MK8DPending  = 0
	MK8DComplete = 1
	MK8DDeleted  = 2
)

// MK8DSearch selects objects for SearchObject.
type MK8DSearch struct {
	Owners    []uint64
	DataTypes []uint16
	Tags      []string
	Offset    int
	Limit     int
}

// MK8DBackend is the durable half of Mario Kart 8 Deluxe.
type MK8DBackend interface {
	// UploadScore stores a score under the update rules; stored is false when
	// an existing better score was kept.
	UploadScore(ctx context.Context, pid, uniqueID uint64, s RankingScore) (stored bool, err error)
	Ranking(ctx context.Context, q RankQuery) (rows []RankRow, total int, err error)
	PutCommonData(ctx context.Context, pid, uniqueID uint64, blob []byte) error
	CommonData(ctx context.Context, pids []uint64) (map[uint64][]byte, error)
	PutScorePack(ctx context.Context, pid uint64, category uint32, meta []byte) error
	ScorePacks(ctx context.Context, pids []uint64, category uint32) (map[uint64][]byte, error)

	CompetitionUpload(ctx context.Context, pid uint64, p CompetitionUpload) error
	// CompetitionScores answers the seasons current-seasons .. current.
	CompetitionScores(ctx context.Context, id uint32, seasons uint32, top int) ([]CompetitionSeason, error)
	CompetitionList(ctx context.Context, offset, limit int) ([]CompetitionSummary, error)

	SaveTournament(ctx context.Context, owner uint64, version uint8, blob []byte) (uint32, error)
	ListTournaments(ctx context.Context, limit int) ([]MK8DTournament, error)
	DeleteTournament(ctx context.Context, id uint32, owner uint64) (bool, error)

	NewObject(ctx context.Context, o MK8DObject) (uint64, error)
	CompleteObject(ctx context.Context, owner, id uint64, size uint32) (bool, error)
	DeleteObject(ctx context.Context, owner, id uint64) (bool, error)
	Objects(ctx context.Context, ids []uint64) (map[uint64]MK8DObject, error)
	// SlotObject is the newest complete object in the owner's persistence slot.
	SlotObject(ctx context.Context, owner uint64, slot uint16) (MK8DObject, bool, error)
	SearchObjects(ctx context.Context, q MK8DSearch) ([]MK8DObject, int, error)
}

// MK8DBlobs hands the console presigned URLs for ghost bodies.
type MK8DBlobs interface {
	PostForm(key string, maxSize uint32, ttl time.Duration) (url string, fields [][2]string, err error)
	GetURL(key string, ttl time.Duration) string
	Size(key string) (size int64, ok bool)
}

// MK8DObjectKey is the bucket key of a ghost body.
func MK8DObjectKey(id uint64) string { return "ghost/" + uitoa(id) + ".bin" }

func uitoa(v uint64) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
		if v == 0 {
			break
		}
	}
	return string(b[i:])
}

func (e *Env) mk8dReady() bool { return e.MK8D != nil }

// logHex logs a request body verbatim for the first console capture. The
// bodies logged here (scores, ghost parameters, competition uploads) carry no
// tokens; capped so a hostile client cannot flood the log.
func logHex(env *Env, pid uint64, what string, body []byte) {
	b := body
	if len(b) > 512 {
		b = b[:512]
	}
	log.Printf("[%s] mk8d: %s pid=%s len=%d hex=%s", env.Name, what, redact.Tag(pid), len(body), hex.EncodeToString(b))
}

// --- Ranking wire -----------------------------------------------------------

// readRankingScoreData reads RankingScoreData from a request reader.
func readRankingScoreData(r *rmc.Reader) (RankingScore, error) {
	var s RankingScore
	_, p, err := r.Struct()
	if err != nil {
		return s, err
	}
	if s.Category, err = p.U32(); err != nil {
		return s, err
	}
	if s.Score, err = p.U32(); err != nil {
		return s, err
	}
	if s.OrderBy, err = p.U8(); err != nil {
		return s, err
	}
	if s.UpdateMode, err = p.U8(); err != nil {
		return s, err
	}
	n, err := p.ListHeader()
	if err != nil || n > 256 {
		return s, errBadList
	}
	for range n {
		b, err := p.U8()
		if err != nil {
			return s, err
		}
		s.Groups = append(s.Groups, b)
	}
	s.Param, err = p.U64()
	return s, err
}

type mk8dErr string

func (e mk8dErr) Error() string { return string(e) }

const errBadList = mk8dErr("mk8d: bad list")

// readOrderParam reads RankingOrderParam into q.
func readOrderParam(r *rmc.Reader, q *RankQuery) error {
	_, p, err := r.Struct()
	if err != nil {
		return err
	}
	if q.Calc, err = p.U8(); err != nil {
		return err
	}
	if q.GroupIndex, err = p.U8(); err != nil {
		return err
	}
	if q.GroupNum, err = p.U8(); err != nil {
		return err
	}
	if _, err = p.U8(); err != nil { // timeScope: boards here do not reset
		return err
	}
	if q.Offset, err = p.U32(); err != nil {
		return err
	}
	l, err := p.U8()
	q.Length = uint32(l)
	return err
}

func readU64List(r *rmc.Reader, max uint32) ([]uint64, error) {
	n, err := r.ListHeader()
	if err != nil || n > max {
		return nil, errBadList
	}
	out := make([]uint64, 0, n)
	for range n {
		v, err := r.U64()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// parseRankingRequest reads the request of GetRanking (0x09),
// GetRankingByPIDList (0x0C) or GetRankingByUniqueIDList (0x0D).
func parseRankingRequest(method uint32, body []byte, caller uint64) (RankQuery, error) {
	q := RankQuery{GroupIndex: 0xFF, Self: caller}
	r := rmc.NewReader(body)
	var err error
	switch method {
	case methodRankingGetRanking:
		if q.Mode, err = r.U8(); err != nil {
			return q, err
		}
		if q.Category, err = r.U32(); err != nil {
			return q, err
		}
		if err = readOrderParam(r, &q); err != nil {
			return q, err
		}
		if _, err = r.U64(); err != nil { // uniqueId
			return q, err
		}
		if pid, perr := r.U64(); perr == nil && pid != 0 {
			q.Self = pid
		}
	case methodRankingByPIDList, methodRankingByUniqueIDList:
		ids, err := readU64List(r, commonDataMaxEntries)
		if err != nil {
			return q, err
		}
		if method == methodRankingByPIDList {
			q.PIDs = ids
		} else {
			q.UniqueIDs = ids
		}
		if q.Mode, err = r.U8(); err != nil {
			return q, err
		}
		if q.Category, err = r.U32(); err != nil {
			return q, err
		}
		if err = readOrderParam(r, &q); err != nil {
			return q, err
		}
	}
	return q, nil
}

// writeRankingResult renders RankingResult: rank rows, total, sinceTime.
func writeRankingResult(rows []RankRow, total int) []byte {
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.List(len(rows))
		for _, row := range rows {
			w.StructOf(0, func(w *rmc.Writer) {
				w.U64(row.PID)
				w.U64(row.UniqueID)
				w.U32(row.Order)
				w.U32(row.Category)
				w.U32(row.Score)
				w.List(len(row.Groups))
				for _, g := range row.Groups {
					w.U8(g)
				}
				w.U64(row.Param)
				w.Buffer(row.CommonData)
			})
		}
		w.U32(uint32(total))
		w.U64(nowDateTime())
	})
	return w.Bytes()
}

// rankingBoard answers the three board reads from the backend. A request it
// cannot parse, or a store that fails, answers the empty board: an empty board
// is a valid answer where an error walls the mode off.
func rankingBoard(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	empty := writeRankingResult(nil, 0)
	if !env.mk8dReady() {
		return success(msg, empty)
	}
	q, err := parseRankingRequest(msg.MethodID, msg.Body, c.PID)
	if err != nil {
		logHex(env, c.PID, "ranking read unparsed method="+uitoa(uint64(msg.MethodID)), msg.Body)
		return success(msg, empty)
	}
	ctx, cancel := ctxDB()
	defer cancel()
	rows, total, err := env.MK8D.Ranking(ctx, q)
	if err != nil {
		log.Printf("[%s] mk8d: ranking read: %v", env.Name, err)
		return success(msg, empty)
	}
	return success(msg, writeRankingResult(rows, total))
}

// --- Competitions -------------------------------------------------------------

func readResultRange(r *rmc.Reader) (offset, length uint32, err error) {
	_, p, err := r.Struct()
	if err != nil {
		return 0, 0, err
	}
	if offset, err = p.U32(); err != nil {
		return
	}
	length, err = p.U32()
	return
}

func readCompetitionUpload(body []byte) (CompetitionUpload, error) {
	var u CompetitionUpload
	_, p, err := rmc.NewReader(body).Struct()
	if err != nil {
		return u, err
	}
	if u.ID, err = p.U32(); err != nil {
		return u, err
	}
	if u.Season, err = p.U32(); err != nil {
		return u, err
	}
	if u.Unknown, err = p.U32(); err != nil {
		return u, err
	}
	if u.Score, err = p.U32(); err != nil {
		return u, err
	}
	if u.TeamID, err = p.U8(); err != nil {
		return u, err
	}
	if u.TeamScore, err = p.U32(); err != nil {
		return u, err
	}
	if u.First, err = p.Bool(); err != nil {
		return u, err
	}
	u.Metadata, err = p.QBuffer()
	return u, err
}

func writeU32List(w *rmc.Writer, v []uint32) {
	w.List(len(v))
	for _, x := range v {
		w.U32(x)
	}
}

func writeCompetitionSeasons(seasons []CompetitionSeason) []byte {
	w := rmc.NewWriter()
	w.List(len(seasons))
	for _, s := range seasons {
		w.StructOf(0, func(w *rmc.Writer) {
			w.U32(s.Season)
			w.List(len(s.Scores))
			for _, sc := range s.Scores {
				w.StructOf(0, func(w *rmc.Writer) {
					w.U32(sc.Rank)
					w.U64(sc.PID)
					w.U32(sc.Score)
					w.U64(switchnex.MakeDateTime(sc.Updated))
					w.U8(sc.TeamID)
					w.QBuffer(sc.Metadata)
				})
			}
			w.U32(s.Participants)
			ts := s.TeamScores
			if len(ts) != 4 {
				ts = make([]uint32, 4)
			}
			writeU32List(w, ts)
		})
	}
	return w.Bytes()
}

func writeCompetitionList(list []CompetitionSummary) []byte {
	w := rmc.NewWriter()
	w.List(len(list))
	for _, s := range list {
		w.StructOf(0, func(w *rmc.Writer) {
			w.U32(s.ID)
			w.U32(s.Participants)
			ts := s.TeamScores
			if len(ts) != 4 {
				ts = make([]uint32, 4)
			}
			writeU32List(w, ts)
		})
	}
	return w.Bytes()
}
