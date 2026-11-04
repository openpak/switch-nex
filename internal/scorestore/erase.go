package scorestore

import (
	"context"
	"fmt"
	"strings"
)

// blob namespaces whose key ends in the player's PID as unpadded lowercase hex, "<pid>" or
// "<anything>/<pid>" (protocols/persist.go). The tallies (festsum) carry no PID and the
// "recall" table belongs to the process that owns it.
var pidBlobNS = []string{"r2board", "r2common", "rkcommon", "festvote"}

// ErasePIDs removes everything the store holds for the players: their score rows (including the
// rows still waiting for an account link) and the Ranking, Ranking2 and Splatfest records keyed by
// them, in every title. It returns the number of rows removed and is safe to repeat. A nil store has
// nothing to remove.
func (s *Store) ErasePIDs(ctx context.Context, pids []uint64) (int, error) {
	if s == nil || len(pids) == 0 {
		return 0, nil
	}
	return s.be.erase(ctx, pids)
}

func pidHexes(pids []uint64) []string {
	out := make([]string, len(pids))
	for i, p := range pids {
		out[i] = fmt.Sprintf("%x", p)
	}
	return out
}

func (p *pg) erase(ctx context.Context, pids []uint64) (int, error) {
	ids := make([]int64, len(pids))
	for i, v := range pids {
		ids[i] = int64(v)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n := 0
	t, err := tx.Exec(ctx, `DELETE FROM scores.entries WHERE pid = ANY($1)`, ids)
	if err != nil {
		return 0, err
	}
	n += int(t.RowsAffected())
	t, err = tx.Exec(ctx, `DELETE FROM scores.blobs WHERE ns = ANY($1)
AND (CASE WHEN position('/' in k) = 0 THEN k ELSE split_part(k, '/', 2) END) = ANY($2)`, pidBlobNS, pidHexes(pids))
	if err != nil {
		return 0, err
	}
	n += int(t.RowsAffected())
	return n, tx.Commit(ctx)
}

func (m *memory) erase(_ context.Context, pids []uint64) (int, error) {
	set := map[uint64]bool{}
	hex := map[string]bool{}
	for _, p := range pids {
		set[p] = true
		hex[fmt.Sprintf("%x", p)] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.rows {
		if set[k.pid] {
			delete(m.rows, k)
			n++
		}
	}
	ns := map[string]bool{}
	for _, v := range pidBlobNS {
		ns[v] = true
	}
	for k := range m.kv {
		if !ns[k[1]] {
			continue
		}
		last := k[2]
		if i := strings.LastIndexByte(last, '/'); i >= 0 {
			last = last[i+1:]
		}
		if hex[last] {
			delete(m.kv, k)
			n++
		}
	}
	return n, nil
}
