// Package scorestore keeps the scores the generic NEX Ranking (0x70) and Ranking2 (0x7A)
// protocols upload, one row per (title, board, player), and serves them through the OpenPak
// rankings contract (docs/rankings-contract.md).
//
// With a Postgres URL the rows are durable (schema "scores", table "entries", created on
// open); without one they live in memory and are lost on restart, which is what the servers
// did before this package, so nothing regresses. A nil *Store is valid and ignores everything.
//
// Rows carry the NEX pid the server holds. Boards are named "Board N" unless the caller
// knows the game's own name for them.
package scorestore

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openpak/switch-nex/internal/rankingsapi"
)

// Sort orders: the values of the NEX RankingOrder flag (0 ascending, 1 descending).
const (
	Asc  uint8 = 0
	Desc uint8 = 1
)

// maxRows bounds one board read and the in-memory store.
const (
	maxRows    = 10000
	maxMemRows = 200000
)

type entry struct {
	board   string
	pid     uint64
	score   int64
	order   uint8
	updated time.Time
}

type boardInfo struct {
	id    string
	order uint8
}

type backend interface {
	// submit keeps the better score (per order), or always overwrites when overwrite is set.
	submit(ctx context.Context, title string, e entry, overwrite bool) error
	// add sums delta into the player's score.
	add(ctx context.Context, title string, e entry) error
	boards(ctx context.Context, title string) ([]boardInfo, error)
	// list returns a board best first: score by order, then who set it first, then pid.
	list(ctx context.Context, title, board string) ([]entry, error)
}

// Store is the shared score store.
type Store struct{ be backend }

// NewMemory returns a store that keeps scores in memory only.
func NewMemory() *Store { return &Store{be: &memory{rows: map[key]entry{}}} }

// Open returns a Postgres-backed store, or a memory store when dsn is empty.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return NewMemory(), nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{be: &pg{pool: pool}}, nil
}

// Durable reports whether scores survive a restart.
func (s *Store) Durable() bool {
	if s == nil {
		return false
	}
	_, ok := s.be.(*pg)
	return ok
}

// Close releases the database pool.
func (s *Store) Close() {
	if s != nil {
		if p, ok := s.be.(*pg); ok {
			p.pool.Close()
		}
	}
}

// Submit records a score on a board: kept when better than the player's stored one (best is
// the lowest for Asc, the highest for Desc), or always when overwrite is set.
func (s *Store) Submit(ctx context.Context, title, board string, pid uint64, score int64, order uint8, overwrite bool) error {
	if s == nil {
		return nil
	}
	return s.be.submit(ctx, title, entry{board: board, pid: pid, score: score, order: order & 1}, overwrite)
}

// Add sums delta into the player's score on a board (a Splatfest contribution).
func (s *Store) Add(ctx context.Context, title, board string, pid uint64, delta int64, order uint8) error {
	if s == nil {
		return nil
	}
	return s.be.add(ctx, title, entry{board: board, pid: pid, score: delta, order: order & 1})
}

// --- memory ---------------------------------------------------------------

type key struct {
	title, board string
	pid          uint64
}

type memory struct {
	mu   sync.Mutex
	rows map[key]entry
}

func better(order uint8, a, b int64) bool {
	if order == Asc {
		return a < b
	}
	return a > b
}

func (m *memory) submit(_ context.Context, title string, e entry, overwrite bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key{title, e.board, e.pid}
	old, ok := m.rows[k]
	if !ok && len(m.rows) >= maxMemRows {
		return nil
	}
	if ok && !overwrite && !better(e.order, e.score, old.score) {
		return nil
	}
	e.updated = time.Now()
	m.rows[k] = e
	return nil
}

func (m *memory) add(_ context.Context, title string, e entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key{title, e.board, e.pid}
	old, ok := m.rows[k]
	if !ok && len(m.rows) >= maxMemRows {
		return nil
	}
	e.score += old.score
	e.updated = time.Now()
	m.rows[k] = e
	return nil
}

func (m *memory) boards(_ context.Context, title string) ([]boardInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]uint8{}
	for k, e := range m.rows {
		if k.title == title {
			seen[k.board] = e.order
		}
	}
	out := make([]boardInfo, 0, len(seen))
	for id, o := range seen {
		out = append(out, boardInfo{id, o})
	}
	return out, nil
}

func (m *memory) list(_ context.Context, title, board string) ([]entry, error) {
	m.mu.Lock()
	var out []entry
	for k, e := range m.rows {
		if k.title == title && k.board == board {
			out = append(out, e)
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return better(a.order, a.score, b.score)
		}
		if !a.updated.Equal(b.updated) {
			return a.updated.Before(b.updated)
		}
		return a.pid < b.pid
	})
	if len(out) > maxRows {
		out = out[:maxRows]
	}
	return out, nil
}

// --- contract -------------------------------------------------------------

// Source serves one title's boards through the rankings contract.
type Source struct {
	Store *Store
	Title string
	// Name gives a board its human name; "" or nil falls back to DefaultName.
	Name func(board string) string
}

var _ rankingsapi.Source = Source{}

// DefaultName is the neutral board name: "Board N" for a category, "Board N (set 2)" for a
// Ranking2 category (board id "r2-N"), "Splatfest N" for a Splatfest (board id "fest<N>").
func DefaultName(board string) string {
	switch {
	case strings.HasPrefix(board, "r2-"):
		return "Board " + strings.TrimPrefix(board, "r2-") + " (set 2)"
	case strings.HasPrefix(board, "fest"):
		return "Splatfest " + strings.TrimPrefix(board, "fest")
	}
	return "Board " + board
}

func (s Source) name(id string) string {
	if s.Name != nil {
		if n := s.Name(id); n != "" {
			return n
		}
	}
	return DefaultName(id)
}

func orderName(o uint8) string {
	if o == Asc {
		return rankingsapi.OrderAsc
	}
	return rankingsapi.OrderDesc
}

// Boards lists every board that holds a score, ordered by id.
func (s Source) Boards(ctx context.Context) ([]rankingsapi.Board, error) {
	bs, err := s.Store.be.boards(ctx, s.Title)
	if err != nil {
		return nil, err
	}
	sort.Slice(bs, func(i, j int) bool { return lessID(bs[i].id, bs[j].id) })
	out := make([]rankingsapi.Board, 0, len(bs))
	for _, b := range bs {
		out = append(out, rankingsapi.Board{ID: b.id, Name: s.name(b.id), Kind: rankingsapi.KindScore,
			Order: orderName(b.order), Unit: "points"})
	}
	return out, nil
}

// lessID sorts numeric ids numerically, then the prefixed ones.
func lessID(a, b string) bool {
	x, e1 := strconv.ParseUint(a, 10, 64)
	y, e2 := strconv.ParseUint(b, 10, 64)
	if e1 == nil && e2 == nil {
		return x < y
	}
	if (e1 == nil) != (e2 == nil) {
		return e1 == nil
	}
	return a < b
}

// Rows pages one board; the pid of every row is the unsigned decimal NEX pid.
func (s Source) Rows(ctx context.Context, board string, q rankingsapi.Query) (rankingsapi.Page, error) {
	es, err := s.Store.be.list(ctx, s.Title, board)
	if err != nil {
		return rankingsapi.Page{}, err
	}
	if len(es) == 0 {
		return rankingsapi.Page{}, rankingsapi.ErrNoBoard
	}
	rows := make([]rankingsapi.Row, len(es))
	for i, e := range es {
		rows[i] = rankingsapi.Row{PID: strconv.FormatUint(e.pid, 10), Score: e.score, UpdatedAt: e.updated.UTC()}
	}
	return rankingsapi.Window(rankingsapi.KindScore, rows, q), nil
}
