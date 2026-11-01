package smm2store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/switch-nex/protocols"
)

// Reader is what the read API needs from the store.
type Reader interface {
	Search(ctx context.Context, q protocols.SMM2Query) ([]protocols.SMM2Course, int, error)
	GetCourses(ctx context.Context, ids []uint64) (map[uint64]protocols.SMM2Course, error)
	Makers(ctx context.Context, limit, offset int) ([]protocols.SMM2Maker, int, error)
	MakerByCode(ctx context.Context, code string) (protocols.SMM2Maker, bool, error)
	UserNames(ctx context.Context, pids []uint64) (map[uint64]string, error)
}

// API is the internal read API the course site copies from (the same
// contract as the Super Mario Maker (Wii U) title).
type API struct {
	Store Reader
	Blobs *Blobs // thumbnails; nil: no thumbnails
	Key   string // X-Internal-Key; empty refuses every call
}

var styles = []string{"Super Mario Bros.", "Super Mario Bros. 3", "Super Mario World", "New Super Mario Bros. U", "Super Mario 3D World"}
var themes = []string{"Ground", "Underground", "Castle", "Airship", "Underwater", "Ghost House", "Snow", "Desert", "Sky", "Forest"}
var difficulties = []string{"Easy", "Normal", "Expert", "Super Expert"}

type courseJSON struct {
	Game         string    `json:"game"`
	ID           string    `json:"id"`
	DataID       uint64    `json:"data_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	MakerID      string    `json:"maker_id"`
	MakerName    string    `json:"maker_name"`
	Style        string    `json:"style"`
	Theme        string    `json:"theme"`
	Difficulty   string    `json:"difficulty"`
	Tags         []string  `json:"tags"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	Stars        uint32    `json:"stars"`
	Plays        uint32    `json:"plays"`
	Clears       uint32    `json:"clears"`
	Attempts     uint32    `json:"attempts"`
	HasThumbnail bool      `json:"has_thumbnail"`
}

type makerJSON struct {
	Game    string    `json:"game"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Courses int       `json:"courses"`
	Stars   int       `json:"stars"`
	Plays   int       `json:"plays"`
	Created time.Time `json:"created"`
}

func pick(list []string, i uint8) string {
	if int(i) < len(list) {
		return list[i]
	}
	return ""
}

func (a *API) courseJSON(c protocols.SMM2Course, names map[uint64]string) courseJSON {
	_, has := c.Rel[protocols.SMM2RelOneScreen]
	if _, e := c.Rel[protocols.SMM2RelEntire]; e {
		has = true
	}
	return courseJSON{
		Game: "smm2", ID: protocols.SMM2CourseCode(c.DataID), DataID: c.DataID, Name: c.Name, Description: c.Description,
		MakerID: protocols.SMM2MakerCode(c.Owner), MakerName: names[c.Owner],
		Style: pick(styles, c.Style), Theme: pick(themes, c.Theme), Difficulty: pick(difficulties, c.Difficulty),
		Tags: []string{}, Created: c.Created, Updated: c.Updated,
		Stars: c.Stars, Plays: c.Plays, Clears: c.Clears, Attempts: c.Attempts, HasThumbnail: has && a.Blobs != nil,
	}
}

func makerOut(m protocols.SMM2Maker) makerJSON {
	return makerJSON{Game: "smm2", ID: protocols.SMM2MakerCode(m.PID), Name: m.Name, Courses: m.Courses,
		Stars: m.Stars, Plays: m.Plays, Created: m.Created}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// Handler serves /v1/courses, /v1/courses/{id}, /v1/courses/{id}/thumbnail,
// /v1/makers and /v1/makers/{id}.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/courses", a.auth(a.listCourses))
	mux.HandleFunc("GET /v1/courses/{id}", a.auth(a.getCourse))
	mux.HandleFunc("GET /v1/courses/{id}/thumbnail", a.auth(a.thumbnail))
	mux.HandleFunc("GET /v1/makers", a.auth(a.listMakers))
	mux.HandleFunc("GET /v1/makers/{id}", a.auth(a.getMaker))
	return mux
}

func (a *API) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("X-Internal-Key")
		if a.Key == "" || subtle.ConstantTimeCompare([]byte(k), []byte(a.Key)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h(w, r)
	}
}

func (a *API) names(ctx context.Context, cs []protocols.SMM2Course) map[uint64]string {
	seen := map[uint64]bool{}
	var pids []uint64
	for _, c := range cs {
		if !seen[c.Owner] {
			seen[c.Owner] = true
			pids = append(pids, c.Owner)
		}
	}
	n, _ := a.Store.UserNames(ctx, pids)
	return n
}

func (a *API) listCourses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lim, _ := strconv.Atoi(q.Get("limit"))
	off, _ := strconv.Atoi(q.Get("offset"))
	sq := protocols.SMM2Query{Sort: q.Get("sort"), Text: q.Get("q"), Limit: min(max(lim, 1), 200), Offset: max(off, 0)}
	if lim == 0 {
		sq.Limit = 50
	}
	if sq.Sort == "" || sq.Sort == "newest" {
		sq.Sort = "newest"
	}
	if s := q.Get("updated_since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad updated_since"})
			return
		}
		sq.Since = t
		sq.Sort = "updated"
	}
	if m := q.Get("maker"); m != "" {
		mk, ok, err := a.Store.MakerByCode(r.Context(), m)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "store"})
			return
		}
		if !ok {
			writeJSON(w, 200, map[string]any{"total": 0, "courses": []courseJSON{}})
			return
		}
		sq.Owners = []uint64{mk.PID}
	}
	cs, total, err := a.Store.Search(r.Context(), sq)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store"})
		return
	}
	names := a.names(r.Context(), cs)
	out := make([]courseJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, a.courseJSON(c, names))
	}
	writeJSON(w, 200, map[string]any{"total": total, "courses": out})
}

// lookup resolves a course code (or a decimal data id) to a visible course.
func (a *API) lookup(r *http.Request) (protocols.SMM2Course, bool) {
	raw := r.PathValue("id")
	id, ok := protocols.SMM2ParseCourseCode(raw)
	if !ok {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return protocols.SMM2Course{}, false
		}
		id = n
	}
	got, err := a.Store.GetCourses(r.Context(), []uint64{id})
	c, found := got[id]
	return c, err == nil && found && c.Status == protocols.SMM2Complete
}

func (a *API) getCourse(w http.ResponseWriter, r *http.Request) {
	c, ok := a.lookup(r)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, a.courseJSON(c, a.names(r.Context(), []protocols.SMM2Course{c})))
}

func (a *API) thumbnail(w http.ResponseWriter, r *http.Request) {
	c, ok := a.lookup(r)
	if !ok || a.Blobs == nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	for _, typ := range []uint8{protocols.SMM2RelEntire, protocols.SMM2RelOneScreen} {
		if _, has := c.Rel[typ]; !has {
			continue
		}
		resp, err := a.Blobs.http.Get(a.Blobs.internalGet(protocols.SMM2RelationKey(c.DataID, typ)))
		if err != nil {
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			continue
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=300")
		io.Copy(w, io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		return
	}
	writeJSON(w, 404, map[string]string{"error": "no thumbnail"})
}

func (a *API) listMakers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lim, _ := strconv.Atoi(q.Get("limit"))
	off, _ := strconv.Atoi(q.Get("offset"))
	if lim <= 0 || lim > 200 {
		lim = 50
	}
	ms, total, err := a.Store.Makers(r.Context(), lim, max(off, 0))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store"})
		return
	}
	out := make([]makerJSON, 0, len(ms))
	for _, m := range ms {
		out = append(out, makerOut(m))
	}
	writeJSON(w, 200, map[string]any{"total": total, "makers": out})
}

func (a *API) getMaker(w http.ResponseWriter, r *http.Request) {
	m, ok, err := a.Store.MakerByCode(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "store"})
		return
	}
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, makerOut(m))
}
