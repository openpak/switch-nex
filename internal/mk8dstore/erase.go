package mk8dstore

import (
	"context"

	"github.com/openpak/switch-nex/protocols"
)

// ErasePIDs removes the players' Mario Kart 8 Deluxe data (card openpak/prds#120): profile (common
// data), score packs, the legacy score and competition rows, and every ghost/data object they
// uploaded together with its body in the bucket. A tournament a player created stays for the people
// who joined it, credited to nobody (owner 0). Scores live in the core leaderboards service and are
// erased there. It returns the rows removed or changed and is safe to repeat. blobs may be nil.
func (s *Store) ErasePIDs(ctx context.Context, pids []uint64, blobs *Blobs) (int, error) {
	ids := make([]int64, len(pids))
	for i, v := range pids {
		ids[i] = int64(v)
	}
	rows, err := s.pool.Query(ctx, `SELECT data_id FROM mk8d.objects WHERE owner = ANY($1)`, ids)
	if err != nil {
		return 0, err
	}
	var objs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		objs = append(objs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if blobs != nil {
		for _, id := range objs {
			if err := blobs.Delete(protocols.MK8DObjectKey(uint64(id))); err != nil {
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
		{`DELETE FROM mk8d.objects WHERE owner = ANY($1)`, ids},
		{`DELETE FROM mk8d.scores WHERE pid = ANY($1)`, ids},
		{`DELETE FROM mk8d.competition_scores WHERE pid = ANY($1)`, ids},
		{`DELETE FROM mk8d.common_data WHERE pid = ANY($1)`, ids},
		{`DELETE FROM mk8d.score_packs WHERE pid = ANY($1)`, ids},
		{`UPDATE mk8d.tournaments SET owner = 0 WHERE owner = ANY($1)`, ids},
	} {
		t, err := tx.Exec(ctx, q.sql, q.arg)
		if err != nil {
			return 0, err
		}
		n += int(t.RowsAffected())
	}
	return n, tx.Commit(ctx)
}
