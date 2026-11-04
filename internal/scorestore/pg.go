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
ALTER TABLE scores.entries ADD COLUMN IF NOT EXISTS uid bigint NOT NULL DEFAULT 0;
ALTER TABLE scores.entries ADD COLUMN IF NOT EXISTS misc bigint NOT NULL DEFAULT 0;
ALTER TABLE scores.entries ADD COLUMN IF NOT EXISTS latest boolean NOT NULL DEFAULT false;
ALTER TABLE scores.entries ADD COLUMN IF NOT EXISTS is_add boolean NOT NULL DEFAULT false;
ALTER TABLE scores.entries ADD COLUMN IF NOT EXISTS migrated boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS scores_entries_asc ON scores.entries (title, board, score, updated);
CREATE TABLE IF NOT EXISTS scores.blobs (
  title text NOT NULL,
  ns text NOT NULL,
  k text NOT NULL,
  v bytea NOT NULL,
  updated timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (title, ns, k)
);
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

// A write to a row that was already imported starts a new pending row state (migrated = false), so
// a later upload of an unlinked-again player is imported too; the import is idempotent per board rule.
func (p *pg) submit(ctx context.Context, title string, e entry, overwrite bool) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO scores.entries (title, board, pid, score, ord, uid, misc, latest, is_add) VALUES ($1,$2,$3,$4,$5,$7,$8,$6,false)
ON CONFLICT (title, board, pid) DO UPDATE SET score = $4, ord = $5, uid = $7, misc = $8, latest = $6, is_add = false, migrated = false, updated = now()
WHERE $6::boolean OR scores.entries.migrated OR ($5::smallint = 0 AND $4::bigint < scores.entries.score) OR ($5::smallint = 1 AND $4::bigint > scores.entries.score)`,
		title, e.board, int64(e.pid), e.score, int16(e.order), overwrite, e.uid, e.misc)
	return err
}

func (p *pg) add(ctx context.Context, title string, e entry) error {
	// A migrated row's sum is already in the core: start the pending sum again from this delta.
	_, err := p.pool.Exec(ctx, `INSERT INTO scores.entries (title, board, pid, score, ord, is_add) VALUES ($1,$2,$3,$4,$5,true)
ON CONFLICT (title, board, pid) DO UPDATE SET score = CASE WHEN scores.entries.migrated THEN $4 ELSE scores.entries.score + $4 END,
  is_add = true, migrated = false, updated = now()`,
		title, e.board, int64(e.pid), e.score, int16(e.order))
	return err
}

func (p *pg) pending(ctx context.Context, title string, limit int) ([]pending, error) {
	rows, err := p.pool.Query(ctx, `SELECT board, pid, score, ord, uid, misc, latest, is_add, updated FROM scores.entries
WHERE title = $1 AND NOT migrated ORDER BY updated, board, pid LIMIT $2`, title, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pending
	for rows.Next() {
		var e entry
		var pid int64
		var o int16
		if err := rows.Scan(&e.board, &pid, &e.score, &o, &e.uid, &e.misc, &e.latest, &e.add, &e.updated); err != nil {
			return nil, err
		}
		e.pid, e.order = uint64(pid), uint8(o)
		out = append(out, pending{title, e})
	}
	return out, rows.Err()
}

// markMigrated flags the row as imported only if it was not rewritten since it was read.
func (p *pg) markMigrated(ctx context.Context, title string, e entry) error {
	_, err := p.pool.Exec(ctx, `UPDATE scores.entries SET migrated = true WHERE title=$1 AND board=$2 AND pid=$3 AND updated = $4`,
		title, e.board, int64(e.pid), e.updated)
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

func (p *pg) putBlob(ctx context.Context, title, ns, k string, v []byte) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO scores.blobs (title, ns, k, v) VALUES ($1,$2,$3,$4)
ON CONFLICT (title, ns, k) DO UPDATE SET v = $4, updated = now()`, title, ns, k, v)
	return err
}

func (p *pg) deleteBlob(ctx context.Context, title, ns, k string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM scores.blobs WHERE title=$1 AND ns=$2 AND k=$3`, title, ns, k)
	return err
}

func (p *pg) blobs(ctx context.Context, title, ns string) (map[string][]byte, error) {
	rows, err := p.pool.Query(ctx, `SELECT k, v FROM scores.blobs WHERE title=$1 AND ns=$2`, title, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (p *pg) seed(ctx context.Context, title string, e entry) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO scores.entries (title, board, pid, score, ord, uid, misc) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (title, board, pid) DO NOTHING`, title, e.board, int64(e.pid), e.score, int16(e.order), e.uid, e.misc)
	return err
}
