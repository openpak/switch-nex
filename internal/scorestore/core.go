package scorestore

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"

	"github.com/openpak/switch-nex/internal/lb"
	pb "github.com/openpak/switch-nex/internal/leaderboardspb"
)

// Core files a title's scores in the core leaderboards service (card #63). The title's neutral
// key is the NEX title name; its native id on the "switch" network is the same string.
type Core struct {
	C *lb.Client
	// Name gives a board its display name; "" falls back to DefaultName.
	Name func(title, board string) string

	mu       sync.Mutex
	declared map[string]string
}

// UseCore sends the scores of linked players to the core; the local rows keep the rest.
func (s *Store) UseCore(c *Core) {
	if s != nil {
		s.core = c
	}
}

// HasCore reports whether scores are served from the core.
func (s *Store) HasCore() bool { return s != nil && s.core != nil }

func (c *Core) title(t string) lb.Title { return lb.Title{Key: t, Native: t, Name: t} }

func (c *Core) policy(title string, e entry) *pb.Policy {
	name := ""
	if c.Name != nil {
		name = c.Name(title, e.board)
	}
	if name == "" {
		name = DefaultName(e.board)
	}
	p := &pb.Policy{Name: name, Kind: "score", Order: "desc", Aggregation: "best"}
	if e.order == Asc {
		p.Order = "asc"
	}
	switch {
	case e.add:
		p.Aggregation = "sum"
	case e.latest:
		p.Aggregation = "latest"
	}
	return p
}

// Register maps the title once, before data exists.
func (c *Core) Register(ctx context.Context, title string) error {
	return c.C.MapTitle(ctx, c.title(title))
}

// submit writes one entry for its player's account. ErrNoLink is returned as is.
func (c *Core) submit(ctx context.Context, title string, e entry) error {
	acct, err := c.C.Account(ctx, e.pid)
	if err != nil {
		return err
	}
	t := c.title(title)
	pol := c.policy(title, e)
	k := title + "\x00" + e.board
	c.mu.Lock()
	if c.declared == nil {
		c.declared = map[string]string{}
	}
	same := c.declared[k] == pol.Aggregation
	c.mu.Unlock()
	if !same {
		if _, err := c.C.LB.UpsertBoard(ctx, &pb.UpsertBoardRequest{Ref: t.Ref(e.board), Policy: pol, TitleName: t.Name}); err != nil {
			return err
		}
		c.mu.Lock()
		c.declared[k] = pol.Aggregation
		c.mu.Unlock()
	}
	cols := map[string]int64{}
	if e.uid != 0 {
		cols["uid"] = e.uid
	}
	if e.misc != 0 {
		cols["misc"] = e.misc
	}
	_, err = c.C.LB.Submit(ctx, &pb.SubmitRequest{Ref: t.Ref(e.board), Account: acct, Score: e.score, Columns: cols, CreatePolicy: pol})
	return err
}

// Row is one ranked entry as the protocols need it.
type Row struct {
	PID       uint64
	Score     int64
	UID, Misc int64
	Position  int64
}

// Page returns a window of a board (offset 0-based; limit 0 means up to 2000 rows) and the board's
// total. Without a core it reads the local rows.
func (s *Store) Page(ctx context.Context, title, board string, offset, limit int) ([]Row, int, error) {
	if s == nil {
		return nil, 0, nil
	}
	if s.core == nil {
		es, err := s.be.list(ctx, title, board)
		if err != nil {
			return nil, 0, err
		}
		rows := make([]Row, 0, len(es))
		for i, e := range es {
			rows = append(rows, Row{PID: e.pid, Score: e.score, UID: e.uid, Misc: e.misc, Position: int64(i + 1)})
		}
		lo := min(offset, len(rows))
		hi := len(rows)
		if limit > 0 {
			hi = min(lo+limit, hi)
		}
		return rows[lo:hi], len(rows), nil
	}
	if limit <= 0 {
		limit = 2000
	}
	c := s.core
	ref := c.title(title).Ref(board)
	var out []Row
	total := 0
	for len(out) < limit {
		n := min(limit-len(out), 100)
		r, err := c.C.LB.Range(ctx, &pb.RangeRequest{Ref: ref, Start: int64(offset+len(out)) + 1, Limit: int64(n)})
		if err != nil {
			return nil, 0, err
		}
		total = int(r.GetTotal())
		for _, e := range r.GetEntries() {
			pid, err := c.C.ID.PID(ctx, e.Account)
			if err != nil {
				continue
			}
			out = append(out, Row{PID: pid, Score: e.Score, UID: e.Columns["uid"], Misc: e.Columns["misc"], Position: e.Position})
		}
		if len(r.GetEntries()) < n {
			break
		}
	}
	return out, total, nil
}

// PositionOf returns a player's position and score on a board (0 when unranked).
func (s *Store) PositionOf(ctx context.Context, title, board string, pid uint64) (pos int64, score int64, total int, err error) {
	if s.core == nil {
		rows, t, err := s.Page(ctx, title, board, 0, 0)
		for _, r := range rows {
			if r.PID == pid {
				return r.Position, r.Score, t, err
			}
		}
		return 0, 0, t, err
	}
	acct, aerr := s.core.C.Account(ctx, pid)
	ref := s.core.title(title).Ref(board)
	if errors.Is(aerr, lb.ErrNoLink) {
		t, err := s.core.C.LB.Total(ctx, &pb.TotalRequest{Ref: ref})
		return 0, 0, int(t.GetTotal()), err
	}
	if aerr != nil {
		return 0, 0, 0, aerr
	}
	r, err := s.core.C.LB.ByAccounts(ctx, &pb.ByAccountsRequest{Ref: ref, Accounts: []string{acct}})
	if err != nil {
		return 0, 0, 0, err
	}
	if len(r.GetEntries()) == 0 {
		return 0, 0, int(r.GetTotal()), nil
	}
	e := r.GetEntries()[0]
	return e.Position, e.Score, int(r.GetTotal()), nil
}

// ImportStats is what one Import pass did.
type ImportStats struct{ Imported, Waiting int }

// Import moves the local rows of players that now have an account link into the core. Rows are
// marked migrated, never deleted; the rest wait for their link and for the core. Safe to repeat.
func (s *Store) Import(ctx context.Context, title string) (ImportStats, error) {
	var st ImportStats
	if s == nil || s.core == nil {
		return st, nil
	}
	rows, err := s.be.pending(ctx, title, 5000)
	if err != nil {
		return st, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].e.updated.Before(rows[j].e.updated) })
	for _, p := range rows {
		err := s.core.submit(ctx, title, p.e)
		if errors.Is(err, lb.ErrNoLink) {
			st.Waiting++
			continue
		}
		if err != nil {
			return st, err
		}
		if err := s.be.markMigrated(ctx, title, p.e); err != nil {
			return st, err
		}
		st.Imported++
	}
	if st.Imported+st.Waiting > 0 {
		log.Printf("scores: %s: imported %d local rows into the core, %d waiting for an account link", title, st.Imported, st.Waiting)
	}
	return st, nil
}

// SeedPending files a row read from an older store (the Ranking2 blobs) as a local row, once: an
// existing row for the same board and player is left alone.
func (s *Store) SeedPending(ctx context.Context, title, board string, pid uint64, score, uid, misc int64) error {
	return s.be.seed(ctx, title, entry{board: board, pid: pid, score: score, order: Desc, uid: uid, misc: misc})
}
