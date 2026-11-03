package scorestore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PIDs are stored as bigint; a pid above 2^63 (a 16-hex BAAS user id) wraps and unwraps
// losslessly through the int64 cast.
const schema = `
CREATE SCHEMA IF NOT EXISTS scores;
CREATE TABLE IF NOT EXISTS scores.entries (
  title text NOT NULL,
  board text NOT NULL,
  pid bigint NOT NULL,
  score bigint NOT NULL,
  ord smallint NOT NULL DEFAULT 1,
  created timestamptz NOT NULL DEFAULT now(),
  updated timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (title, board, pid)
);
CREATE INDEX IF NOT EXISTS scores_entries_asc ON scores.entries (title, board, score, updated);
`

// migrate creates the schema; the advisory lock keeps the title processes, which all open
// the store at startup, from racing each other on CREATE.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7010)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, schema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type pg struct{ pool *pgxpool.Pool }

func (p *pg) submit(ctx context.Context, title string, e entry, overwrite bool) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO scores.entries (title, board, pid, score, ord) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (title, board, pid) DO UPDATE SET score = $4, ord = $5, updated = now()
WHERE $6::boolean OR ($5::smallint = 0 AND $4::bigint < scores.entries.score) OR ($5::smallint = 1 AND $4::bigint > scores.entries.score)`,
		title, e.board, int64(e.pid), e.score, int16(e.order), overwrite)
	return err
}

func (p *pg) add(ctx context.Context, title string, e entry) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO scores.entries (title, board, pid, score, ord) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (title, board, pid) DO UPDATE SET score = scores.entries.score + $4, updated = now()`,
		title, e.board, int64(e.pid), e.score, int16(e.order))
	return err
}

func (p *pg) boards(ctx context.Context, title string) ([]boardInfo, error) {
	rows, err := p.pool.Query(ctx, `SELECT board, max(ord) FROM scores.entries WHERE title = $1 GROUP BY board`, title)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []boardInfo
	for rows.Next() {
		var b boardInfo
		var o int16
		if err := rows.Scan(&b.id, &o); err != nil {
			return nil, err
		}
		b.order = uint8(o)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (p *pg) list(ctx context.Context, title, board string) ([]entry, error) {
	rows, err := p.pool.Query(ctx, `SELECT pid, score, ord, updated FROM scores.entries WHERE title = $1 AND board = $2
ORDER BY CASE WHEN ord = 0 THEN score ELSE -score END, updated, pid LIMIT $3`, title, board, maxRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []entry
	for rows.Next() {
		var pid, score int64
		var o int16
		var upd time.Time
		if err := rows.Scan(&pid, &score, &o, &upd); err != nil {
			return nil, err
		}
		out = append(out, entry{board: board, pid: uint64(pid), score: score, order: uint8(o), updated: upd})
	}
	return out, rows.Err()
}
