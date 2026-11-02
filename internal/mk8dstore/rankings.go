package mk8dstore

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/openpak/switch-nex/internal/rankingsapi"
)

// Rankings implements rankingsapi.Source over the tables the title already keeps.
//
// Board ids:
//
//	"<n>"        time trial course n (mk8d.scores, category n): fastest first, ms.
//	"t<c>s<s>"   tournament c, season s (mk8d.competition_scores): highest score first.
//
// Ranks follow the game's own ordinal query: score, then who set it first, then pid.
// Rows carry the NEX pid only; the store keeps no account id.
type Rankings struct{ Store *Store }

var _ rankingsapi.Source = Rankings{}

// Boards lists every course with a stored time and every tournament season with a stored score.
func (r Rankings) Boards(ctx context.Context) ([]rankingsapi.Board, error) {
	out := []rankingsapi.Board{}
	rows, err := r.Store.pool.Query(ctx, `SELECT DISTINCT category FROM mk8d.scores ORDER BY category`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, rankingsapi.Board{ID: strconv.FormatInt(c, 10), Name: "Course " + strconv.FormatInt(c, 10),
			Kind: rankingsapi.KindTime, Order: rankingsapi.OrderAsc})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = r.Store.pool.Query(ctx, `SELECT DISTINCT s.competition, s.season, COALESCE(t.name, '')
FROM mk8d.competition_scores s LEFT JOIN mk8d.tournaments t ON t.id = s.competition AND NOT t.deleted
ORDER BY s.competition, s.season`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, s int64
		var name string
		if err := rows.Scan(&c, &s, &name); err != nil {
			return nil, err
		}
		if name == "" {
			name = "Tournament " + strconv.FormatInt(c, 10)
		}
		out = append(out, rankingsapi.Board{ID: tournamentID(c, s), Name: name, Kind: rankingsapi.KindScore,
			Order: rankingsapi.OrderDesc, Season: strconv.FormatInt(s, 10)})
	}
	return out, rows.Err()
}

func tournamentID(c, s int64) string {
	return "t" + strconv.FormatInt(c, 10) + "s" + strconv.FormatInt(s, 10)
}

func parseTournamentID(id string) (c, s int64, ok bool) {
	a, b, found := strings.Cut(strings.TrimPrefix(id, "t"), "s")
	if !strings.HasPrefix(id, "t") || !found {
		return 0, 0, false
	}
	c, e1 := strconv.ParseInt(a, 10, 64)
	s, e2 := strconv.ParseInt(b, 10, 64)
	return c, s, e1 == nil && e2 == nil
}

// Rows pages one board in SQL.
func (r Rankings) Rows(ctx context.Context, board string, q rankingsapi.Query) (rankingsapi.Page, error) {
	var kind, from, where, order, score string
	var args []any
	if n, err := strconv.ParseInt(board, 10, 64); err == nil && n >= 0 {
		kind, from, where, order, score = rankingsapi.KindTime, "mk8d.scores", "category = $1", "score, updated, pid", "score"
		args = []any{n}
	} else if c, s, ok := parseTournamentID(board); ok {
		kind, from, where, order, score = rankingsapi.KindScore, "mk8d.competition_scores", "competition = $1 AND season = $2", "score DESC, updated, pid", "score"
		args = []any{c, s}
	} else {
		return rankingsapi.Page{}, rankingsapi.ErrNoBoard
	}
	base := `WITH r AS (SELECT pid, ` + score + ` AS sc, updated, row_number() OVER (ORDER BY ` + order + `) AS rn FROM ` + from + ` WHERE ` + where + `)`
	n := len(args)
	var p rankingsapi.Page
	if err := r.Store.pool.QueryRow(ctx, base+` SELECT count(*) FROM r`, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	if p.Total == 0 {
		return p, rankingsapi.ErrNoBoard
	}
	var err error
	if p.Rows, err = r.page(ctx, base, kind, args, `rn > $`+strconv.Itoa(n+1)+` ORDER BY rn LIMIT $`+strconv.Itoa(n+2), q.Offset, q.Limit); err != nil {
		return p, err
	}
	if pid, perr := strconv.ParseInt(q.Around, 10, 64); perr == nil {
		var rn int
		err := r.Store.pool.QueryRow(ctx, base+` SELECT rn FROM r WHERE pid = $`+strconv.Itoa(n+1), append(append([]any{}, args...), pid)...).Scan(&rn)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return p, err
		default:
			lo := max(rn-rankingsapi.AroundSpan-1, 0)
			if p.Around, err = r.page(ctx, base, kind, args, `rn > $`+strconv.Itoa(n+1)+` ORDER BY rn LIMIT $`+strconv.Itoa(n+2), lo, 2*rankingsapi.AroundSpan+1); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

func (r Rankings) page(ctx context.Context, base, kind string, args []any, tail string, offset, limit int) ([]rankingsapi.Row, error) {
	rows, err := r.Store.pool.Query(ctx, base+` SELECT rn, pid, sc, updated FROM r WHERE `+tail, append(append([]any{}, args...), offset, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rankingsapi.Row{}
	for rows.Next() {
		var rn int
		var pid, sc int64
		var upd time.Time
		if err := rows.Scan(&rn, &pid, &sc, &upd); err != nil {
			return nil, err
		}
		out = append(out, rankingsapi.Row{Rank: rn, PID: strconv.FormatInt(pid, 10), Score: sc,
			Display: rankingsapi.Format(kind, sc), UpdatedAt: upd})
	}
	return out, rows.Err()
}
