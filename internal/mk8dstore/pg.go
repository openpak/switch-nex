// Package mk8dstore is the durable side of Mario Kart 8 Deluxe: the Postgres
// ranking, competition, tournament and ghost store, plus the read API the
// OpenKart site consumes. Ghost bodies live in S3 (MinIO) through the same
// presigner Super Mario Maker 2 uses (smm2store.Blobs).
package mk8dstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openpak/switch-nex/protocols"
)

// PIDs and unique ids are stored as bigint: PIDs stay far below 2^63, and a
// unique id that does not is wrapped by the int64 cast, which is lossless.
const schema = `
CREATE SCHEMA IF NOT EXISTS mk8d;
CREATE SEQUENCE IF NOT EXISTS mk8d.object_ids START 1000000;
CREATE SEQUENCE IF NOT EXISTS mk8d.tournament_ids START 1;
CREATE TABLE IF NOT EXISTS mk8d.scores (
  category bigint NOT NULL,
  pid bigint NOT NULL,
  unique_id bigint NOT NULL DEFAULT 0,
  score bigint NOT NULL,
  order_by smallint NOT NULL DEFAULT 0,
  update_mode smallint NOT NULL DEFAULT 0,
  groups bytea NOT NULL DEFAULT '',
  param bigint NOT NULL DEFAULT 0,
  created timestamptz NOT NULL DEFAULT now(),
  updated timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (category, pid)
);
CREATE INDEX IF NOT EXISTS mk8d_scores_board ON mk8d.scores (category, score, updated);
CREATE INDEX IF NOT EXISTS mk8d_scores_unique ON mk8d.scores (unique_id);
CREATE TABLE IF NOT EXISTS mk8d.common_data (
  pid bigint PRIMARY KEY,
  unique_id bigint NOT NULL DEFAULT 0,
  data bytea NOT NULL,
  updated timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS mk8d.score_packs (
  pid bigint NOT NULL,
  category bigint NOT NULL,
  meta bytea NOT NULL,
  updated timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (pid, category)
);
CREATE TABLE IF NOT EXISTS mk8d.tournaments (
  id bigint PRIMARY KEY,
  owner bigint NOT NULL DEFAULT 0,
  version smallint NOT NULL DEFAULT 0,
  blob bytea,
  name text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  community_code text NOT NULL DEFAULT '',
  attributes bigint[] NOT NULL DEFAULT '{}',
  starts timestamptz,
  ends timestamptz,
  season bigint NOT NULL DEFAULT 1,
  deleted boolean NOT NULL DEFAULT false,
  created timestamptz NOT NULL DEFAULT now(),
  updated timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS mk8d.competition_scores (
  competition bigint NOT NULL,
  season bigint NOT NULL,
  pid bigint NOT NULL,
  score bigint NOT NULL,
  team smallint NOT NULL DEFAULT 0,
  team_score bigint NOT NULL DEFAULT 0,
  metadata bytea NOT NULL DEFAULT '',
  updated timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (competition, season, pid)
);
CREATE TABLE IF NOT EXISTS mk8d.objects (
  data_id bigint PRIMARY KEY DEFAULT nextval('mk8d.object_ids'),
  owner bigint NOT NULL,
  name text NOT NULL DEFAULT '',
  size bigint NOT NULL DEFAULT 0,
  data_type integer NOT NULL DEFAULT 0,
  meta bytea NOT NULL DEFAULT '',
  tags text[] NOT NULL DEFAULT '{}',
  slot integer NOT NULL DEFAULT -1,
  refer_data_id bigint NOT NULL DEFAULT 0,
  flag bigint NOT NULL DEFAULT 0,
  permission smallint NOT NULL DEFAULT 0,
  status smallint NOT NULL DEFAULT 0,
  created timestamptz NOT NULL DEFAULT now(),
  updated timestamptz NOT NULL DEFAULT now(),
  raw bytea
);
CREATE INDEX IF NOT EXISTS mk8d_objects_owner ON mk8d.objects (owner, status);
CREATE INDEX IF NOT EXISTS mk8d_objects_type ON mk8d.objects (data_type, status, updated DESC);`

// Store implements protocols.MK8DBackend over Postgres.
type Store struct{ pool *pgxpool.Pool }

var _ protocols.MK8DBackend = (*Store)(nil)

// Open connects and creates the schema.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

func i64(v uint64) int64 { return int64(v) }

// bs keeps a nil byte slice from becoming NULL in a NOT NULL column.
func bs(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// UploadScore stores a score. Update semantics: update mode 1 always
// overwrites; any other mode keeps the best score, where best is the lowest
// (time trials, the MK8D board) - ascending order.
func (s *Store) UploadScore(ctx context.Context, pid, uniqueID uint64, sc protocols.RankingScore) (bool, error) {
	t, err := s.pool.Exec(ctx, `INSERT INTO mk8d.scores (category, pid, unique_id, score, order_by, update_mode, groups, param)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (category, pid) DO UPDATE SET unique_id=$3, score=$4, order_by=$5, update_mode=$6, groups=$7, param=$8, updated=now()
WHERE $6 = 1 OR $4 < mk8d.scores.score`,
		int64(sc.Category), i64(pid), i64(uniqueID), int64(sc.Score), int16(sc.OrderBy), int16(sc.UpdateMode), bs(sc.Groups), i64(sc.Param))
	return t.RowsAffected() > 0, err
}

// Ranking answers a board read. Ranks are global (the player's place on the
// whole board), also for by-PID and by-unique-id lists.
func (s *Store) Ranking(ctx context.Context, q protocols.RankQuery) ([]protocols.RankRow, int, error) {
	length := q.Length
	if length == 0 || length > 255 {
		length = 20
	}
	args := []any{int64(q.Category)}
	filter := ""
	if q.GroupIndex != 0xFF {
		args = append(args, int(q.GroupIndex), int(q.GroupNum))
		filter = " AND length(groups) > $2 AND get_byte(groups, $2) = $3"
	}
	base := `WITH r AS (SELECT s.*, row_number() OVER w AS rn, rank() OVER (ORDER BY score) AS rk
FROM mk8d.scores s WHERE category = $1` + filter + ` WINDOW w AS (ORDER BY score, updated, pid))`
	var total int
	if err := s.pool.QueryRow(ctx, base+` SELECT count(*) FROM r`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := `r.rk`
	if q.Calc == 1 {
		order = `r.rn`
	}
	var cond, tail string
	switch {
	case q.PIDs != nil:
		ids := make([]int64, len(q.PIDs))
		for i, p := range q.PIDs {
			ids[i] = i64(p)
		}
		args = append(args, ids)
		cond = fmt.Sprintf(" WHERE r.pid = ANY($%d)", len(args))
		tail = fmt.Sprintf(" ORDER BY r.rn LIMIT %d", length)
	case q.UniqueIDs != nil:
		ids := make([]int64, len(q.UniqueIDs))
		for i, p := range q.UniqueIDs {
			ids[i] = i64(p)
		}
		args = append(args, ids)
		cond = fmt.Sprintf(" WHERE r.unique_id = ANY($%d)", len(args))
		tail = fmt.Sprintf(" ORDER BY r.rn LIMIT %d", length)
	case q.Mode == protocols.RankModeSelf:
		args = append(args, i64(q.Self))
		cond = fmt.Sprintf(" WHERE r.pid = $%d", len(args))
		tail = " LIMIT 1"
	case q.Mode == protocols.RankModeAround:
		args = append(args, i64(q.Self))
		cond = fmt.Sprintf(` WHERE r.rn >= (SELECT greatest(1, rn - %d) FROM r WHERE pid = $%d)`, length/2, len(args))
		tail = fmt.Sprintf(" ORDER BY r.rn LIMIT %d", length)
	default:
		off := min(q.Offset, protocols.RankingMaxOffset)
		if off+length > protocols.RankingMaxOffset {
			length = protocols.RankingMaxOffset - off
		}
		cond = fmt.Sprintf(" WHERE r.rn > %d", off)
		tail = fmt.Sprintf(" ORDER BY r.rn LIMIT %d", length)
	}
	rows, err := s.pool.Query(ctx, base+` SELECT r.pid, r.unique_id, `+order+`, r.category, r.score, r.groups, r.param,
COALESCE(c.data, ''::bytea), r.updated FROM r LEFT JOIN mk8d.common_data c ON c.pid = r.pid`+cond+tail, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []protocols.RankRow
	for rows.Next() {
		var (
			r                        protocols.RankRow
			pid, uid, ord, cat, scor int64
			param                    int64
		)
		if err := rows.Scan(&pid, &uid, &ord, &cat, &scor, &r.Groups, &param, &r.CommonData, &r.Updated); err != nil {
			return nil, 0, err
		}
		r.PID, r.UniqueID, r.Order, r.Category, r.Score, r.Param = uint64(pid), uint64(uid), uint32(ord), uint32(cat), uint32(scor), uint64(param)
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *Store) PutCommonData(ctx context.Context, pid, uniqueID uint64, blob []byte) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO mk8d.common_data (pid, unique_id, data) VALUES ($1,$2,$3)
ON CONFLICT (pid) DO UPDATE SET unique_id=$2, data=$3, updated=now()`, i64(pid), i64(uniqueID), blob)
	return err
}

func (s *Store) CommonData(ctx context.Context, pids []uint64) (map[uint64][]byte, error) {
	out := map[uint64][]byte{}
	if len(pids) == 0 {
		return out, nil
	}
	ids := make([]int64, len(pids))
	for i, p := range pids {
		ids[i] = i64(p)
	}
	rows, err := s.pool.Query(ctx, `SELECT pid, data FROM mk8d.common_data WHERE pid = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var d []byte
		if err := rows.Scan(&pid, &d); err != nil {
			return nil, err
		}
		out[uint64(pid)] = d
	}
	return out, rows.Err()
}

func (s *Store) PutScorePack(ctx context.Context, pid uint64, category uint32, meta []byte) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO mk8d.score_packs (pid, category, meta) VALUES ($1,$2,$3)
ON CONFLICT (pid, category) DO UPDATE SET meta=$3, updated=now()`, i64(pid), int64(category), bs(meta))
	return err
}

func (s *Store) ScorePacks(ctx context.Context, pids []uint64, category uint32) (map[uint64][]byte, error) {
	out := map[uint64][]byte{}
	ids := make([]int64, len(pids))
	for i, p := range pids {
		ids[i] = i64(p)
	}
	rows, err := s.pool.Query(ctx, `SELECT pid, meta FROM mk8d.score_packs WHERE category=$1 AND pid = ANY($2)`, int64(category), ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var d []byte
		if err := rows.Scan(&pid, &d); err != nil {
			return nil, err
		}
		out[uint64(pid)] = d
	}
	return out, rows.Err()
}

// --- competitions ---------------------------------------------------------

// CompetitionUpload stores a competition score; the last upload of a player in
// a season wins (the console sends its running total). An unknown competition
// id gets a placeholder tournament row so it shows on the site.
func (s *Store) CompetitionUpload(ctx context.Context, pid uint64, p protocols.CompetitionUpload) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO mk8d.tournaments (id, season) VALUES ($1,$2)
ON CONFLICT (id) DO UPDATE SET season = greatest(mk8d.tournaments.season, $2), updated=now()`, int64(p.ID), int64(max(p.Season, 1))); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mk8d.competition_scores (competition, season, pid, score, team, team_score, metadata)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (competition, season, pid) DO UPDATE SET score=$4, team=$5, team_score=$6, metadata=$7, updated=now()`,
		int64(p.ID), int64(p.Season), i64(pid), int64(p.Score), int16(p.TeamID), int64(p.TeamScore), bs(p.Metadata)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) currentSeason(ctx context.Context, id uint32) (int64, bool, error) {
	var season int64
	err := s.pool.QueryRow(ctx, `SELECT season FROM mk8d.tournaments WHERE id=$1 AND NOT deleted`, int64(id)).Scan(&season)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return season, err == nil, err
}

// CompetitionScores answers seasons current-n .. current (n+1 seasons), top
// scores first, ranks assigned by score.
func (s *Store) CompetitionScores(ctx context.Context, id uint32, n uint32, top int) ([]protocols.CompetitionSeason, error) {
	cur, ok, err := s.currentSeason(ctx, id)
	if err != nil || !ok {
		return nil, err
	}
	first := max(cur-int64(n), 1)
	var out []protocols.CompetitionSeason
	for season := first; season <= cur; season++ {
		cs := protocols.CompetitionSeason{Season: uint32(season), TeamScores: make([]uint32, 4)}
		rows, err := s.pool.Query(ctx, `SELECT pid, score, updated, team, metadata FROM mk8d.competition_scores
WHERE competition=$1 AND season=$2 ORDER BY score DESC, updated, pid LIMIT $3`, int64(id), season, top)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sc protocols.CompetitionScore
			var pid, score int64
			var team int16
			if err := rows.Scan(&pid, &score, &sc.Updated, &team, &sc.Metadata); err != nil {
				rows.Close()
				return nil, err
			}
			sc.Rank, sc.PID, sc.Score, sc.TeamID = uint32(len(cs.Scores)+1), uint64(pid), uint32(score), uint8(team)
			cs.Scores = append(cs.Scores, sc)
		}
		rows.Close()
		var n int64
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM mk8d.competition_scores WHERE competition=$1 AND season=$2`,
			int64(id), season).Scan(&n); err != nil {
			return nil, err
		}
		cs.Participants = uint32(n)
		out = append(out, cs)
	}
	return out, nil
}

func (s *Store) CompetitionList(ctx context.Context, offset, limit int) ([]protocols.CompetitionSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id, (SELECT count(DISTINCT pid) FROM mk8d.competition_scores c WHERE c.competition=t.id) AS n
FROM mk8d.tournaments t WHERE NOT t.deleted ORDER BY n DESC, t.id OFFSET $1 LIMIT $2`, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocols.CompetitionSummary
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out = append(out, protocols.CompetitionSummary{ID: uint32(id), Participants: uint32(n), TeamScores: make([]uint32, 4)})
	}
	return out, rows.Err()
}

// --- tournaments (matchmaking descriptors) --------------------------------

func (s *Store) SaveTournament(ctx context.Context, owner uint64, version uint8, blob []byte) (uint32, error) {
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT nextval('mk8d.tournament_ids')`).Scan(&id); err != nil {
		return 0, err
	}
	// A console that sent its own object id keeps it only when free; ours wins.
	blob = protocols.TournamentWithID(blob, uint32(id))
	d := ParseDescriptor(blob)
	_, err := s.pool.Exec(ctx, `INSERT INTO mk8d.tournaments (id, owner, version, blob, name, description, community_code, attributes, starts, ends)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (id) DO UPDATE SET owner=$2, version=$3, blob=$4, name=$5, description=$6, community_code=$7, attributes=$8,
  starts=$9, ends=$10, deleted=false, updated=now()`,
		id, i64(owner), int16(version), blob, d.Name, d.Description, d.Code, d.Attributes, d.Start, d.End)
	return uint32(id), err
}

func (s *Store) ListTournaments(ctx context.Context, limit int) ([]protocols.MK8DTournament, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, owner, version, blob FROM mk8d.tournaments
WHERE NOT deleted AND blob IS NOT NULL ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocols.MK8DTournament
	for rows.Next() {
		var t protocols.MK8DTournament
		var id, owner int64
		var v int16
		if err := rows.Scan(&id, &owner, &v, &t.Blob); err != nil {
			return nil, err
		}
		t.ID, t.Owner, t.Version = uint32(id), uint64(owner), uint8(v)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTournament(ctx context.Context, id uint32, owner uint64) (bool, error) {
	t, err := s.pool.Exec(ctx, `UPDATE mk8d.tournaments SET deleted=true, updated=now() WHERE id=$1 AND owner=$2 AND NOT deleted`,
		int64(id), i64(owner))
	return t.RowsAffected() > 0, err
}

// --- ghosts ---------------------------------------------------------------

func (s *Store) NewObject(ctx context.Context, o protocols.MK8DObject) (uint64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO mk8d.objects (owner, name, size, data_type, meta, tags, slot, refer_data_id, flag, permission, raw)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING data_id`,
		i64(o.Owner), o.Name, int64(o.Size), int32(o.DataType), bs(o.Meta), nonNil(o.Tags), int32(o.Slot), int64(o.ReferDataID),
		int64(o.Flag), int16(o.Permission), o.Raw).Scan(&id)
	return uint64(id), err
}

func nonNil(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

// CompleteObject makes a pending object visible. An object in a persistence
// slot replaces the owner's previous object in that slot.
func (s *Store) CompleteObject(ctx context.Context, owner, id uint64, size uint32) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var slot int32
	err = tx.QueryRow(ctx, `UPDATE mk8d.objects SET status=1, size=CASE WHEN $3 > 0 THEN $3 ELSE size END, updated=now()
WHERE data_id=$1 AND owner=$2 AND status IN (0,1) RETURNING slot`, int64(id), i64(owner), int64(size)).Scan(&slot)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if slot >= 0 {
		if _, err := tx.Exec(ctx, `UPDATE mk8d.objects SET status=2, updated=now()
WHERE owner=$1 AND slot=$2 AND data_id<>$3 AND status<>2`, i64(owner), slot, int64(id)); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

func (s *Store) DeleteObject(ctx context.Context, owner, id uint64) (bool, error) {
	t, err := s.pool.Exec(ctx, `UPDATE mk8d.objects SET status=2, updated=now() WHERE data_id=$1 AND owner=$2 AND status<>2`,
		int64(id), i64(owner))
	return t.RowsAffected() > 0, err
}

const objCols = `data_id, owner, name, size, data_type, meta, tags, slot, refer_data_id, flag, permission, status, created, updated`

func scanObject(row pgx.Row) (protocols.MK8DObject, error) {
	var (
		o                            protocols.MK8DObject
		id, owner, size, refer, flag int64
		dt, slot                     int32
		perm, status                 int16
		created, updated             time.Time
	)
	if err := row.Scan(&id, &owner, &o.Name, &size, &dt, &o.Meta, &o.Tags, &slot, &refer, &flag, &perm, &status, &created, &updated); err != nil {
		return o, err
	}
	o.DataID, o.Owner, o.Size, o.DataType, o.Slot = uint64(id), uint64(owner), uint32(size), uint16(dt), int(slot)
	o.ReferDataID, o.Flag, o.Permission, o.Status, o.Created, o.Updated = uint32(refer), uint32(flag), uint8(perm), int(status), created, updated
	return o, nil
}

func (s *Store) Objects(ctx context.Context, ids []uint64) (map[uint64]protocols.MK8DObject, error) {
	out := map[uint64]protocols.MK8DObject{}
	if len(ids) == 0 {
		return out, nil
	}
	in := make([]int64, len(ids))
	for i, v := range ids {
		in[i] = int64(v)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+objCols+` FROM mk8d.objects WHERE data_id = ANY($1) AND status<>2`, in)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out[o.DataID] = o
	}
	return out, rows.Err()
}

func (s *Store) SlotObject(ctx context.Context, owner uint64, slot uint16) (protocols.MK8DObject, bool, error) {
	o, err := scanObject(s.pool.QueryRow(ctx, `SELECT `+objCols+` FROM mk8d.objects
WHERE owner=$1 AND slot=$2 AND status=1 ORDER BY data_id DESC LIMIT 1`, i64(owner), int32(slot)))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, false, nil
	}
	return o, err == nil, err
}

func (s *Store) SearchObjects(ctx context.Context, q protocols.MK8DSearch) ([]protocols.MK8DObject, int, error) {
	where, args := "status=1 AND permission=0", []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND "+cond, len(args))
	}
	if len(q.Owners) > 0 {
		ids := make([]int64, len(q.Owners))
		for i, p := range q.Owners {
			ids[i] = i64(p)
		}
		add("owner = ANY($%d)", ids)
	}
	if len(q.DataTypes) > 0 {
		ts := make([]int32, len(q.DataTypes))
		for i, t := range q.DataTypes {
			ts[i] = int32(t)
		}
		add("data_type = ANY($%d)", ts)
	}
	if len(q.Tags) > 0 {
		add("tags @> $%d", q.Tags)
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM mk8d.objects WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, q.Limit, q.Offset)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT `+objCols+` FROM mk8d.objects WHERE `+where+
		` ORDER BY updated DESC, data_id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []protocols.MK8DObject
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}
