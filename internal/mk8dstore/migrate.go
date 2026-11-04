package mk8dstore

import (
	"context"
	"log"
	"time"

	"github.com/openpak/switch-nex/protocols"
)

// MigrateStats is what one MigrateLegacy pass did.
type MigrateStats struct {
	Scores, Competitions int // rows moved to the core
	Superseded           int // rows not sent: the player already has a newer entry on a last-upload-wins board
	NoLink               int // rows left behind: the PID has no account link (retried on the next start)
}

// MigrateLegacy moves the rows of the old mk8d.scores and mk8d.competition_scores tables into the
// core leaderboards service. It never deletes: a moved row is only marked migrated, a row whose PID
// has no account link stays as it is and is tried again on the next start. Rows go oldest first, so
// the core's write order keeps the tie order the game saw. Safe to run on every start; a second pass
// finds nothing to do.
func (s *Store) MigrateLegacy(ctx context.Context) (MigrateStats, error) {
	var st MigrateStats
	if s.Core == nil {
		return st, errNoCore
	}
	rows, err := s.pool.Query(ctx, `SELECT category, pid, unique_id, score, update_mode, groups, param, updated FROM mk8d.scores WHERE NOT migrated ORDER BY updated, category, pid`)
	if err != nil {
		return st, err
	}
	type srow struct {
		cat, pid, uid, score, param int64
		mode                        int16
		groups                      []byte
		updated                     time.Time
	}
	var sr []srow
	for rows.Next() {
		var r srow
		if err := rows.Scan(&r.cat, &r.pid, &r.uid, &r.score, &r.mode, &r.groups, &r.param, &r.updated); err != nil {
			rows.Close()
			return st, err
		}
		sr = append(sr, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, r := range sr {
		// The stored row is the player's best (or last, in update mode 1): sent as such. On a
		// last-upload-wins board an entry already in the core is newer than this row.
		if r.mode == 1 {
			if has, err := s.Core.has(ctx, uint64(r.pid), courseBoard(uint32(r.cat))); err != nil {
				return st, err
			} else if has {
				if _, err := s.pool.Exec(ctx, `UPDATE mk8d.scores SET migrated = true WHERE category=$1 AND pid=$2`, r.cat, r.pid); err != nil {
					return st, err
				}
				st.Superseded++
				continue
			}
		}
		resp, err := s.Core.upload(ctx, uint64(r.pid), courseBoard(uint32(r.cat)), r.score,
			map[string]int64{"uid": r.uid, "param": r.param}, r.groups, s.Core.coursePolicy(uint32(r.cat), r.mode == 1))
		if err != nil {
			return st, err
		}
		if resp == nil {
			st.NoLink++
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE mk8d.scores SET migrated = true WHERE category=$1 AND pid=$2`, r.cat, r.pid); err != nil {
			return st, err
		}
		st.Scores++
	}

	crows, err := s.pool.Query(ctx, `SELECT competition, season, pid, score, team, team_score, metadata FROM mk8d.competition_scores WHERE NOT migrated ORDER BY updated, competition, season, pid`)
	if err != nil {
		return st, err
	}
	type crow struct {
		comp, season, pid, score, ts int64
		team                         int16
		meta                         []byte
	}
	var cr []crow
	for crows.Next() {
		var r crow
		if err := crows.Scan(&r.comp, &r.season, &r.pid, &r.score, &r.team, &r.ts, &r.meta); err != nil {
			crows.Close()
			return st, err
		}
		cr = append(cr, r)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return st, err
	}
	for _, r := range cr {
		if has, err := s.Core.has(ctx, uint64(r.pid), tournamentID(uint32(r.comp), uint32(r.season))); err != nil {
			return st, err
		} else if has {
			if _, err := s.pool.Exec(ctx, `UPDATE mk8d.competition_scores SET migrated = true WHERE competition=$1 AND season=$2 AND pid=$3`, r.comp, r.season, r.pid); err != nil {
				return st, err
			}
			st.Superseded++
			continue
		}
		resp, err := s.Core.upload(ctx, uint64(r.pid), tournamentID(uint32(r.comp), uint32(r.season)), r.score,
			map[string]int64{"team": int64(r.team), "team_score": r.ts}, r.meta, tournamentPolicy(uint32(r.comp), uint32(r.season)))
		if err != nil {
			return st, err
		}
		if resp == nil {
			st.NoLink++
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE mk8d.competition_scores SET migrated = true WHERE competition=$1 AND season=$2 AND pid=$3`, r.comp, r.season, r.pid); err != nil {
			return st, err
		}
		st.Competitions++
	}
	log.Printf("mk8d: legacy scores moved to the core: %d course rows, %d tournament rows, %d superseded by newer core entries, %d left (no account link)", st.Scores, st.Competitions, st.Superseded, st.NoLink)
	return st, nil
}

var _ protocols.MK8DBackend = (*Store)(nil)
