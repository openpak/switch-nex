package smm2store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openpak/switch-nex/protocols"
)

// schema is created on start. PIDs and data ids are stored as bigint; both stay
// far below 2^63.
const schema = `
CREATE SCHEMA IF NOT EXISTS smm2;
CREATE SEQUENCE IF NOT EXISTS smm2.course_ids START 1000000;
CREATE TABLE IF NOT EXISTS smm2.users (
  pid bigint PRIMARY KEY,
  name text NOT NULL,
  clothes integer[] NOT NULL DEFAULT '{0,0,0,0}',
  mii bytea,
  region smallint NOT NULL DEFAULT 0,
  country text NOT NULL DEFAULT '',
  device text NOT NULL DEFAULT '',
  created timestamptz NOT NULL DEFAULT now(),
  updated timestamptz NOT NULL DEFAULT now(),
  code text NOT NULL DEFAULT ''
);
ALTER TABLE smm2.users ADD COLUMN IF NOT EXISTS code text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS smm2_users_code ON smm2.users (code);
CREATE TABLE IF NOT EXISTS smm2.courses (
  data_id bigint PRIMARY KEY DEFAULT nextval('smm2.course_ids'),
  owner bigint NOT NULL,
  name text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  style smallint NOT NULL DEFAULT 0,
  theme smallint NOT NULL DEFAULT 0,
  difficulty smallint NOT NULL DEFAULT 0,
  tag1 smallint NOT NULL DEFAULT 0,
  tag2 smallint NOT NULL DEFAULT 0,
  clear_condition bigint NOT NULL DEFAULT 0,
  clear_magnitude integer NOT NULL DEFAULT 0,
  status smallint NOT NULL DEFAULT 0,
  created timestamptz NOT NULL DEFAULT now(),
  updated timestamptz NOT NULL DEFAULT now(),
  plays bigint NOT NULL DEFAULT 0,
  clears bigint NOT NULL DEFAULT 0,
  attempts bigint NOT NULL DEFAULT 0,
  stars bigint NOT NULL DEFAULT 0,
  body_size bigint NOT NULL DEFAULT 0,
  raw bytea
);
CREATE INDEX IF NOT EXISTS smm2_courses_owner ON smm2.courses (owner, status);
CREATE INDEX IF NOT EXISTS smm2_courses_updated ON smm2.courses (status, updated DESC);
CREATE TABLE IF NOT EXISTS smm2.relations (
  data_id bigint NOT NULL,
  typ smallint NOT NULL,
  size bigint NOT NULL DEFAULT 0,
  created timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (data_id, typ)
);`

// Store implements protocols.SMM2Backend over Postgres.
type Store struct{ pool *pgxpool.Pool }

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

func (s *Store) PutUser(ctx context.Context, pid uint64, u *protocols.SMM2UserRecord) error {
	cl := []int32{int32(u.Clothes[0]), int32(u.Clothes[1]), int32(u.Clothes[2]), int32(u.Clothes[3])}
	_, err := s.pool.Exec(ctx, `INSERT INTO smm2.users (pid, name, clothes, mii, region, country, device, code)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (pid) DO UPDATE SET name=$2, clothes=$3, mii=$4, region=$5, country=$6,
  device=CASE WHEN $7 = '' THEN smm2.users.device ELSE $7 END, code=$8, updated=now()`,
		int64(pid), u.Name, cl, u.Mii, int16(u.Region), u.Country, u.Device, protocols.SMM2MakerCode(pid))
	return err
}

func (s *Store) GetUser(ctx context.Context, pid uint64) (*protocols.SMM2UserRecord, bool, error) {
	var (
		u  protocols.SMM2UserRecord
		cl []int32
		rg int16
	)
	err := s.pool.QueryRow(ctx, `SELECT name, clothes, mii, region, country, device FROM smm2.users WHERE pid=$1`,
		int64(pid)).Scan(&u.Name, &cl, &u.Mii, &rg, &u.Country, &u.Device)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for i := 0; i < 4 && i < len(cl); i++ {
		u.Clothes[i] = uint16(cl[i])
	}
	u.Region = uint8(rg)
	return &u, true, nil
}

func (s *Store) NewCourse(ctx context.Context, c protocols.SMM2Course) (uint64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO smm2.courses
(owner, name, description, style, theme, clear_condition, clear_magnitude, raw)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING data_id`,
		int64(c.Owner), c.Name, c.Description, int16(c.Style), int16(c.Theme),
		int64(c.ClearCondition), int32(c.ClearMagnitude), c.Raw).Scan(&id)
	return uint64(id), err
}

func (s *Store) UpdateCourse(ctx context.Context, c protocols.SMM2Course) (bool, error) {
	t, err := s.pool.Exec(ctx, `UPDATE smm2.courses SET name=$3, description=$4, style=$5, theme=$6,
clear_condition=$7, clear_magnitude=$8, updated=now() WHERE data_id=$1 AND owner=$2 AND status<>2`,
		int64(c.DataID), int64(c.Owner), c.Name, c.Description, int16(c.Style), int16(c.Theme),
		int64(c.ClearCondition), int32(c.ClearMagnitude))
	return t.RowsAffected() > 0, err
}

func (s *Store) CompleteCourse(ctx context.Context, owner, dataID uint64, bodySize uint32) (bool, error) {
	t, err := s.pool.Exec(ctx, `UPDATE smm2.courses SET status=1, body_size=$3, updated=now()
WHERE data_id=$1 AND owner=$2 AND status IN (0,1)`, int64(dataID), int64(owner), int64(bodySize))
	return t.RowsAffected() > 0, err
}

func (s *Store) SetRelation(ctx context.Context, owner, dataID uint64, typ uint8, size uint32) (bool, error) {
	t, err := s.pool.Exec(ctx, `INSERT INTO smm2.relations (data_id, typ, size)
SELECT data_id, $3, $4 FROM smm2.courses WHERE data_id=$1 AND owner=$2 AND status<>2
ON CONFLICT (data_id, typ) DO UPDATE SET size=$4`, int64(dataID), int64(owner), int16(typ), int64(size))
	if err == nil && t.RowsAffected() > 0 {
		s.pool.Exec(ctx, `UPDATE smm2.courses SET updated=now() WHERE data_id=$1`, int64(dataID))
	}
	return t.RowsAffected() > 0, err
}

func (s *Store) LatestPending(ctx context.Context, owner uint64) (uint64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT data_id FROM smm2.courses WHERE owner=$1 AND status=0
ORDER BY data_id DESC LIMIT 1`, int64(owner)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return uint64(id), err
}

func (s *Store) DeleteCourse(ctx context.Context, owner, dataID uint64) (bool, error) {
	t, err := s.pool.Exec(ctx, `UPDATE smm2.courses SET status=2, updated=now()
WHERE data_id=$1 AND owner=$2 AND status<>2`, int64(dataID), int64(owner))
	return t.RowsAffected() > 0, err
}

const courseCols = `c.data_id, c.owner, c.name, c.description, c.style, c.theme, c.difficulty, c.tag1, c.tag2,
c.clear_condition, c.clear_magnitude, c.status, c.created, c.updated, c.plays, c.clears, c.attempts, c.stars,
c.body_size, COALESCE((SELECT jsonb_object_agg(r.typ::text, r.size) FROM smm2.relations r WHERE r.data_id=c.data_id), '{}'::jsonb)`

func scanCourse(row pgx.Row) (protocols.SMM2Course, error) {
	var (
		c                                 protocols.SMM2Course
		id, owner, cc, pl, cl, at, st, bs int64
		sty, th, di, t1, t2, status, mag  int32
		created, updated                  time.Time
		rel                               map[string]int64
	)
	err := row.Scan(&id, &owner, &c.Name, &c.Description, &sty, &th, &di, &t1, &t2, &cc, &mag, &status,
		&created, &updated, &pl, &cl, &at, &st, &bs, &rel)
	if err != nil {
		return c, err
	}
	c.DataID, c.Owner = uint64(id), uint64(owner)
	c.Style, c.Theme, c.Difficulty, c.Tag1, c.Tag2 = uint8(sty), uint8(th), uint8(di), uint8(t1), uint8(t2)
	c.ClearCondition, c.ClearMagnitude, c.Status = uint32(cc), uint16(mag), int(status)
	c.Created, c.Updated = created, updated
	c.Plays, c.Clears, c.Attempts, c.Stars, c.BodySize = uint32(pl), uint32(cl), uint32(at), uint32(st), uint32(bs)
	c.Rel = map[uint8]uint32{}
	for k, v := range rel {
		var n uint8
		for _, ch := range k {
			n = n*10 + uint8(ch-'0')
		}
		c.Rel[n] = uint32(v)
	}
	return c, nil
}

func (s *Store) GetCourses(ctx context.Context, ids []uint64) (map[uint64]protocols.SMM2Course, error) {
	out := map[uint64]protocols.SMM2Course{}
	if len(ids) == 0 {
		return out, nil
	}
	arr := make([]int64, len(ids))
	for i, id := range ids {
		arr[i] = int64(id)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+courseCols+` FROM smm2.courses c WHERE c.data_id = ANY($1)`, arr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCourse(rows)
		if err != nil {
			return nil, err
		}
		out[c.DataID] = c
	}
	return out, rows.Err()
}

// Search lists complete courses; the second result is the total matching.
func (s *Store) Search(ctx context.Context, q protocols.SMM2Query) ([]protocols.SMM2Course, int, error) {
	where := []string{"c.status = 1"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if len(q.Owners) > 0 {
		o := make([]int64, len(q.Owners))
		for i, p := range q.Owners {
			o[i] = int64(p)
		}
		add("c.owner = ANY(?)", o)
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		add("(c.name ILIKE ? OR c.description ILIKE ?)", "%"+escapeLike(t)+"%")
	}
	if !q.Since.IsZero() {
		add("c.updated >= ?", q.Since)
	}
	w := strings.Join(where, " AND ")
	order := "c.created DESC, c.data_id DESC"
	switch q.Sort {
	case "stars":
		order = "c.stars DESC, c.created DESC"
	case "plays":
		order = "c.plays DESC, c.created DESC"
	case "updated":
		order = "c.updated ASC, c.data_id ASC"
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM smm2.courses c WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit, max(q.Offset, 0))
	rows, err := s.pool.Query(ctx, `SELECT `+courseCols+` FROM smm2.courses c WHERE `+w+
		` ORDER BY `+order+` LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []protocols.SMM2Course
	for rows.Next() {
		c, err := scanCourse(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (s *Store) CountByOwner(ctx context.Context, owner uint64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM smm2.courses WHERE owner=$1 AND status IN (0,1)`, int64(owner)).Scan(&n)
	return n, err
}

// Makers lists registered makers with their totals.
func (s *Store) Makers(ctx context.Context, limit, offset int) ([]protocols.SMM2Maker, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM smm2.users`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, makerSQL+` ORDER BY u.created DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []protocols.SMM2Maker
	for rows.Next() {
		m, err := scanMaker(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// Maker is one maker by PID.
func (s *Store) Maker(ctx context.Context, pid uint64) (protocols.SMM2Maker, bool, error) {
	m, err := scanMaker(s.pool.QueryRow(ctx, makerSQL+` WHERE u.pid=$1`, int64(pid)))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

const makerSQL = `SELECT u.pid, u.name, u.created,
 (SELECT count(*) FROM smm2.courses c WHERE c.owner=u.pid AND c.status=1),
 (SELECT COALESCE(sum(stars),0) FROM smm2.courses c WHERE c.owner=u.pid AND c.status=1),
 (SELECT COALESCE(sum(plays),0) FROM smm2.courses c WHERE c.owner=u.pid AND c.status=1)
 FROM smm2.users u`

func scanMaker(row pgx.Row) (protocols.SMM2Maker, error) {
	var (
		m   protocols.SMM2Maker
		pid int64
	)
	err := row.Scan(&pid, &m.Name, &m.Created, &m.Courses, &m.Stars, &m.Plays)
	m.PID = uint64(pid)
	return m, err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// MakerByCode finds a maker by the visible maker code.
func (s *Store) MakerByCode(ctx context.Context, code string) (protocols.SMM2Maker, bool, error) {
	m, err := scanMaker(s.pool.QueryRow(ctx, makerSQL+` WHERE u.code=$1`, strings.ToUpper(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

// UserNames maps PIDs to maker names.
func (s *Store) UserNames(ctx context.Context, pids []uint64) (map[uint64]string, error) {
	out := map[uint64]string{}
	if len(pids) == 0 {
		return out, nil
	}
	a := make([]int64, len(pids))
	for i, p := range pids {
		a[i] = int64(p)
	}
	rows, err := s.pool.Query(ctx, `SELECT pid, name FROM smm2.users WHERE pid = ANY($1)`, a)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p int64
		var n string
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[uint64(p)] = n
	}
	return out, rows.Err()
}
