package smm2store

import (
	"context"

	"github.com/openpak/switch-nex/protocols"
)

// ErasePIDs removes the players' Super Mario Maker 2 data (card openpak/prds#120): their maker
// profile, their unpublished and deleted courses with the bodies and relations in the bucket, and
// the replay/thumbnail relations of those courses. A course the player published stays in Course World
// (other players have it saved), credited to nobody: its owner becomes 0, the id of no player. It
// returns the rows removed or changed and is safe to repeat. blobs may be nil (no bucket configured).
func (s *Store) ErasePIDs(ctx context.Context, pids []uint64, blobs *Blobs) (int, error) {
	ids := make([]int64, len(pids))
	for i, v := range pids {
		ids[i] = int64(v)
	}
	// Courses to delete: everything but published (status 1).
	rows, err := s.pool.Query(ctx, `SELECT data_id FROM smm2.courses WHERE owner = ANY($1) AND status <> 1`, ids)
	if err != nil {
		return 0, err
	}
	var courses []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		courses = append(courses, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if blobs != nil && len(courses) > 0 {
		rel, err := s.pool.Query(ctx, `SELECT data_id, typ FROM smm2.relations WHERE data_id = ANY($1)`, courses)
		if err != nil {
			return 0, err
		}
		type relKey struct {
			id  int64
			typ int16
		}
		var rels []relKey
		for rel.Next() {
			var k relKey
			if err := rel.Scan(&k.id, &k.typ); err != nil {
				rel.Close()
				return 0, err
			}
			rels = append(rels, k)
		}
		rel.Close()
		if err := rel.Err(); err != nil {
			return 0, err
		}
		for _, k := range rels {
			if err := blobs.Delete(protocols.SMM2RelationKey(uint64(k.id), uint8(k.typ))); err != nil {
				return 0, err
			}
		}
		for _, id := range courses {
			if err := blobs.Delete(protocols.SMM2BodyKey(uint64(id))); err != nil {
				return 0, err
			}
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n := 0
	for _, q := range []struct {
		sql string
		arg any
	}{
		{`DELETE FROM smm2.relations WHERE data_id = ANY($1)`, courses},
		{`DELETE FROM smm2.courses WHERE data_id = ANY($1)`, courses},
		{`UPDATE smm2.courses SET owner = 0 WHERE owner = ANY($1)`, ids},
		{`DELETE FROM smm2.users WHERE pid = ANY($1)`, ids},
	} {
		t, err := tx.Exec(ctx, q.sql, q.arg)
		if err != nil {
			return 0, err
		}
		n += int(t.RowsAffected())
	}
	return n, tx.Commit(ctx)
}
