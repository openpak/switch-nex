package mk8dstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/openpak/switch-nex/internal/smm2store"
	"github.com/openpak/switch-nex/protocols"
)

// Blobs is the S3 presigner shared with the SMM2 store.
type Blobs = smm2store.Blobs

// S3Config configures Blobs.
type S3Config = smm2store.S3Config

// NewBlobs builds the presigner.
func NewBlobs(cfg S3Config) *Blobs { return smm2store.NewBlobs(cfg) }

const (
	defaultLimit = 20
	maxLimit     = 100
	maxBody      = 8 << 20
)

// API is the internal read API the OpenKart site consumes: the same shape as
// the Mario Kart 8 (Wii U) title's (lists are {total, offset, limit, items},
// PIDs are strings, errors {"error": "..."}). Container network only, never
// published.
type API struct {
	Store *Store
	Blobs *Blobs // ghost bodies; nil: /body answers 501
}

type page struct {
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Items  any `json:"items"`
}

// Object is a ghost as the API shows it (the MK8 object shape plus the ghost
// fields course, time_ms, time, name).
type Object struct {
	ID        uint64    `json:"id"`
	Owner     string    `json:"owner"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	DataType  int       `json:"data_type"`
	Slot      *int      `json:"slot,omitempty"`
	Tags      []string  `json:"tags"`
	MetaSize  int       `json:"meta_size"`
	MetaHex   string    `json:"meta_hex,omitempty"`
	Thumbnail *uint64   `json:"thumbnail,omitempty"`
	Rating    *struct{} `json:"rating,omitempty"`
	YouTubeID string    `json:"youtube_id,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Course    *int      `json:"course,omitempty"`
	TimeMS    *int64    `json:"time_ms,omitempty"`
	Time      string    `json:"time,omitempty"`
	Player    string    `json:"player,omitempty"`
}

type rankItem struct {
	Rank    int       `json:"rank"`
	Player  string    `json:"player"`
	TimeMS  int64     `json:"time_ms"`
	Time    string    `json:"time"`
	Updated time.Time `json:"updated"`
	Ghost   *uint64   `json:"ghost,omitempty"`
}

type playerRank struct {
	Course int    `json:"course"`
	Rank   int    `json:"rank"`
	Of     int    `json:"of"`
	TimeMS int64  `json:"time_ms"`
	Time   string `json:"time"`
}

type course struct {
	Course int `json:"course"`
	Ranked int `json:"ranked"`
	Ghosts int `json:"ghosts"`
}

type scoreItem struct {
	Rank    int       `json:"rank"`
	Player  string    `json:"player"`
	Score   int64     `json:"score"`
	Team    int       `json:"team"`
	Updated time.Time `json:"updated"`
}

type tournament struct {
	ID           uint32      `json:"id"`
	Owner        string      `json:"owner"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Code         string      `json:"community_code"`
	Attributes   []int64     `json:"attributes"`
	Start        *time.Time  `json:"start,omitempty"`
	End          *time.Time  `json:"end,omitempty"`
	Participants int64       `json:"participants"`
	Season       int64       `json:"season"`
	Created      time.Time   `json:"created"`
	Scores       []scoreItem `json:"scores,omitempty"`
}

var errNotFound = errors.New("not found")

// FormatTime renders a time-trial score (milliseconds) as m:ss.mmm.
func FormatTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%d:%02d.%03d", ms/60000, ms/1000%60, ms%1000)
}

// Handler serves the API.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/courses", a.courses)
	mux.HandleFunc("GET /api/v1/courses/{course}/rankings", a.rankings)
	mux.HandleFunc("GET /api/v1/courses/{course}/ghosts", a.ghosts)
	mux.HandleFunc("GET /api/v1/objects/{id}", a.object)
	mux.HandleFunc("GET /api/v1/objects/{id}/body", a.body)
	mux.HandleFunc("GET /api/v1/players/{pid}/rankings", a.playerRankings)
	mux.HandleFunc("GET /api/v1/tournaments", a.tournaments)
	mux.HandleFunc("GET /api/v1/tournaments/{id}", a.tournament)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=15")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) || errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	log.Printf("[mario-kart-8-deluxe] api: %v", err)
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func paging(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit = defaultLimit
	for name, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if v := r.URL.Query().Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				badRequest(w, name+" must be a non-negative integer")
				return 0, 0, false
			}
			*dst = n
		}
	}
	if limit == 0 {
		limit = defaultLimit
	}
	return min(limit, maxLimit), offset, true
}

func pathUint(w http.ResponseWriter, r *http.Request, name string, bits int) (uint64, bool) {
	n, err := strconv.ParseUint(r.PathValue(name), 10, bits)
	if err != nil {
		badRequest(w, name+" must be a number")
		return 0, false
	}
	return n, true
}

func isDigits(s string) bool { return s != "" && len(s) <= 20 && strings.Trim(s, "0123456789") == "" }

func items[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (a *API) courses(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Store.pool.Query(r.Context(), `SELECT c, sum(ranked)::int, sum(ghosts)::int FROM (
SELECT category AS c, count(*) AS ranked, 0 AS ghosts FROM mk8d.scores GROUP BY category
UNION ALL
SELECT data_type::bigint, 0, count(*) FROM mk8d.objects WHERE status=1 AND permission=0 GROUP BY data_type
) x GROUP BY c ORDER BY c`)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	var out []course
	for rows.Next() {
		var c course
		if err := rows.Scan(&c.Course, &c.Ranked, &c.Ghosts); err != nil {
			fail(w, err)
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items(out)})
}

func (a *API) rankings(w http.ResponseWriter, r *http.Request) {
	courseID, ok := pathUint(w, r, "course", 31)
	if !ok {
		return
	}
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	var total int
	if err := a.Store.pool.QueryRow(r.Context(), `SELECT count(*) FROM mk8d.scores WHERE category=$1`, int64(courseID)).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	rows, err := a.Store.pool.Query(r.Context(), `SELECT s.pid, s.score, s.updated,
(SELECT o.data_id FROM mk8d.objects o WHERE o.owner=s.pid AND o.data_type=s.category AND o.status=1 AND o.permission=0
 ORDER BY o.data_id DESC LIMIT 1)
FROM mk8d.scores s WHERE s.category=$1 ORDER BY s.score, s.updated, s.pid LIMIT $2 OFFSET $3`, int64(courseID), limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	var out []rankItem
	for rows.Next() {
		var it rankItem
		var pid, score int64
		var ghost *int64
		if err := rows.Scan(&pid, &score, &it.Updated, &ghost); err != nil {
			fail(w, err)
			return
		}
		it.Rank, it.Player, it.TimeMS, it.Time = offset+len(out)+1, strconv.FormatInt(pid, 10), score, FormatTime(score)
		if ghost != nil {
			g := uint64(*ghost)
			it.Ghost = &g
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, page{total, offset, limit, items(out)})
}

func (a *API) playerRankings(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	if !isDigits(pid) {
		badRequest(w, "pid must be a number")
		return
	}
	n, _ := strconv.ParseInt(pid, 10, 64)
	rows, err := a.Store.pool.Query(r.Context(), `SELECT s.category, s.score,
(SELECT count(*) FROM mk8d.scores x WHERE x.category=s.category AND x.score < s.score) + 1,
(SELECT count(*) FROM mk8d.scores x WHERE x.category=s.category)
FROM mk8d.scores s WHERE s.pid=$1 ORDER BY s.category`, n)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	var out []playerRank
	for rows.Next() {
		var p playerRank
		if err := rows.Scan(&p.Course, &p.TimeMS, &p.Rank, &p.Of); err != nil {
			fail(w, err)
			return
		}
		p.Time = FormatTime(p.TimeMS)
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items(out)})
}

func (a *API) object(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUint(w, r, "id", 63)
	if !ok {
		return
	}
	o, err := a.visible(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.objectJSON(r.Context(), o))
}

// visible is a finished, public object.
func (a *API) visible(ctx context.Context, id uint64) (protocols.MK8DObject, error) {
	got, err := a.Store.Objects(ctx, []uint64{id})
	if err != nil {
		return protocols.MK8DObject{}, err
	}
	o, ok := got[id]
	if !ok || o.Status != protocols.MK8DComplete || o.Permission != 0 {
		return o, errNotFound
	}
	return o, nil
}

// objectJSON adds the ghost fields: course is the object's data_type (INFERRED),
// the time is the owner's score on that course.
func (a *API) objectJSON(ctx context.Context, o protocols.MK8DObject) Object {
	j := Object{ID: o.DataID, Owner: strconv.FormatUint(o.Owner, 10), Name: o.Name, Size: int64(o.Size), DataType: int(o.DataType),
		Tags: items(o.Tags), MetaSize: len(o.Meta), Created: o.Created, Updated: o.Updated, Player: strconv.FormatUint(o.Owner, 10)}
	if len(o.Meta) > 0 && len(o.Meta) <= 256 {
		j.MetaHex = hex.EncodeToString(o.Meta)
	}
	if o.Slot >= 0 {
		s := o.Slot
		j.Slot = &s
	}
	c := int(o.DataType)
	j.Course = &c
	var score int64
	if err := a.Store.pool.QueryRow(ctx, `SELECT score FROM mk8d.scores WHERE category=$1 AND pid=$2`, int64(o.DataType), int64(o.Owner)).Scan(&score); err == nil {
		j.TimeMS, j.Time = &score, FormatTime(score)
	}
	return j
}

func (a *API) ghosts(w http.ResponseWriter, r *http.Request) {
	courseID, ok := pathUint(w, r, "course", 16)
	if !ok {
		return
	}
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	q := protocols.MK8DSearch{DataTypes: []uint16{uint16(courseID)}, Limit: limit, Offset: offset}
	if o := r.URL.Query().Get("owner"); o != "" {
		if !isDigits(o) {
			badRequest(w, "owner must be a PID")
			return
		}
		pid, _ := strconv.ParseUint(o, 10, 63)
		q.Owners = []uint64{pid}
	}
	found, total, err := a.Store.SearchObjects(r.Context(), q)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]Object, 0, len(found))
	for _, o := range found {
		out = append(out, a.objectJSON(r.Context(), o))
	}
	writeJSON(w, http.StatusOK, page{total, offset, limit, out})
}

func (a *API) body(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUint(w, r, "id", 63)
	if !ok {
		return
	}
	if a.Blobs == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "this server stores no object bodies"})
		return
	}
	if _, err := a.visible(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	data, ok := a.Blobs.Fetch(protocols.MK8DObjectKey(id), maxBody)
	if !ok {
		fail(w, errNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ghost-%d.bin"`, id))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

const tournamentCols = `t.id, t.owner, t.name, t.description, t.community_code, t.attributes, t.starts, t.ends, t.season, t.created,
(SELECT count(DISTINCT pid) FROM mk8d.competition_scores c WHERE c.competition=t.id)`

func scanTournament(row pgx.Row) (tournament, error) {
	var t tournament
	var id, owner int64
	if err := row.Scan(&id, &owner, &t.Name, &t.Description, &t.Code, &t.Attributes, &t.Start, &t.End, &t.Season, &t.Created, &t.Participants); err != nil {
		return t, err
	}
	t.ID, t.Owner = uint32(id), strconv.FormatInt(owner, 10)
	t.Attributes = items(t.Attributes)
	return t, nil
}

func (a *API) tournaments(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	var total int
	if err := a.Store.pool.QueryRow(r.Context(), `SELECT count(*) FROM mk8d.tournaments WHERE NOT deleted`).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	rows, err := a.Store.pool.Query(r.Context(), `SELECT `+tournamentCols+` FROM mk8d.tournaments t WHERE NOT t.deleted
ORDER BY t.created DESC, t.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	var out []tournament
	for rows.Next() {
		t, err := scanTournament(rows)
		if err != nil {
			fail(w, err)
			return
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, page{total, offset, limit, items(out)})
}

func (a *API) tournament(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUint(w, r, "id", 32)
	if !ok {
		return
	}
	var season int64
	if v := r.URL.Query().Get("season"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			badRequest(w, "season must be a positive integer")
			return
		}
		season = n
	}
	t, err := scanTournament(a.Store.pool.QueryRow(r.Context(), `SELECT `+tournamentCols+` FROM mk8d.tournaments t WHERE t.id=$1 AND NOT t.deleted`, int64(id)))
	if err != nil {
		fail(w, err)
		return
	}
	if season == 0 {
		season = t.Season
	}
	rows, err := a.Store.pool.Query(r.Context(), `SELECT pid, score, team, updated FROM mk8d.competition_scores
WHERE competition=$1 AND season=$2 ORDER BY score DESC, updated, pid LIMIT $3`, int64(id), season, maxLimit)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	t.Scores = []scoreItem{}
	for rows.Next() {
		var s scoreItem
		var pid int64
		var team int16
		if err := rows.Scan(&pid, &s.Score, &team, &s.Updated); err != nil {
			fail(w, err)
			return
		}
		s.Rank, s.Player, s.Team = len(t.Scores)+1, strconv.FormatInt(pid, 10), int(team)
		t.Scores = append(t.Scores, s)
	}
	t.Season = season
	writeJSON(w, http.StatusOK, t)
}
