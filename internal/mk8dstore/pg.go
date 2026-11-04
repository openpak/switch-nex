// Package mk8dstore is the durable side of Mario Kart 8 Deluxe: the Postgres
// ranking, competition, tournament and ghost store, plus the read API the
// OpenKart site consumes. Ghost bodies live in S3 (MinIO) through the same
// presigner Super Mario Maker 2 uses (smm2store.Blobs).
package mk8dstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
  migrated boolean NOT NULL DEFAULT false,
  PRIMARY KEY (category, pid)
);
ALTER TABLE mk8d.scores ADD COLUMN IF NOT EXISTS migrated boolean NOT NULL DEFAULT false;
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
  migrated boolean NOT NULL DEFAULT false,
  PRIMARY KEY (competition, season, pid)
);
ALTER TABLE mk8d.competition_scores ADD COLUMN IF NOT EXISTS migrated boolean NOT NULL DEFAULT false;
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
// Scores live in the core leaderboards service (Core); the mk8d.scores and mk8d.competition_scores
// tables are the legacy copy, read once by MigrateLegacy and never written again.
type Store struct {
	pool *pgxpool.Pool
	Core *Scores
}

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

// UploadScore stores a score in the core; see Scores.UploadScore.
func (s *Store) UploadScore(ctx context.Context, pid, uniqueID uint64, sc protocols.RankingScore) (bool, error) {
	if s.Core == nil {
		return false, errNoCore
	}
	return s.Core.UploadScore(ctx, pid, uniqueID, sc)
}

var errNoCore = errors.New("mk8d: leaderboards core is not configured")

// Ranking answers a board read from the core; the common data of each row comes from here.
func (s *Store) Ranking(ctx context.Context, q protocols.RankQuery) ([]protocols.RankRow, int, error) {
	if s.Core == nil {
		return nil, 0, errNoCore
	}
	rows, total, err := s.Core.Ranking(ctx, q)
	if err != nil || len(rows) == 0 {
		return rows, total, err
	}
	pids := make([]uint64, len(rows))
	for i, r := range rows {
		pids[i] = r.PID
	}
	cd, err := s.CommonData(ctx, pids)
	if err != nil {
		return nil, 0, err
	}
	for i := range rows {
		rows[i].CommonData = cd[rows[i].PID]
	}
	return rows, total, nil
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

// CompetitionUpload stores a competition score in the core; the last upload of a player in
// a season wins (the console sends its running total). An unknown competition
// id gets a placeholder tournament row so it shows on the site.
func (s *Store) CompetitionUpload(ctx context.Context, pid uint64, p protocols.CompetitionUpload) error {
	if s.Core == nil {
		return errNoCore
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO mk8d.tournaments (id, season) VALUES ($1,$2)
ON CONFLICT (id) DO UPDATE SET season = greatest(mk8d.tournaments.season, $2), updated=now()`, int64(p.ID), int64(max(p.Season, 1))); err != nil {
		return err
	}
	return s.Core.CompetitionUpload(ctx, pid, p)
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
	if s.Core == nil {
		return nil, errNoCore
	}
	cur, ok, err := s.currentSeason(ctx, id)
	if err != nil || !ok {
		return nil, err
	}
	first := max(cur-int64(n), 1)
	var out []protocols.CompetitionSeason
	for season := first; season <= cur; season++ {
		scores, total, err := s.Core.Season(ctx, id, uint32(season), top)
		if err != nil {
			return nil, err
		}
		out = append(out, protocols.CompetitionSeason{Season: uint32(season), TeamScores: make([]uint32, 4), Scores: scores, Participants: uint32(total)})
	}
	return out, nil
}

// CompetitionList lists the tournaments, most players first (players of the current season).
func (s *Store) CompetitionList(ctx context.Context, offset, limit int) ([]protocols.CompetitionSummary, error) {
	if s.Core == nil {
		return nil, errNoCore
	}
	rows, err := s.pool.Query(ctx, `SELECT id, season FROM mk8d.tournaments WHERE NOT deleted ORDER BY id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	var all []protocols.CompetitionSummary
	var seasons []int64
	for rows.Next() {
		var id, season int64
		if err := rows.Scan(&id, &season); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, protocols.CompetitionSummary{ID: uint32(id), TeamScores: make([]uint32, 4)})
		seasons = append(seasons, season)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range all {
		n, err := s.Core.Participants(ctx, all[i].ID, uint32(seasons[i]))
		if err != nil {
			return nil, err
		}
		all[i].Participants = uint32(n)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Participants > all[j].Participants })
	if offset >= len(all) {
		return nil, nil
	}
	return all[offset:min(offset+limit, len(all))], nil
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
