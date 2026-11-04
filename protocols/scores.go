package protocols

// Persisting the scores the generic protocols upload (internal/scorestore), so the rankings
// contract can serve them. Only fields the code parses are stored: the category and score of
// RankingScoreData (Ranking 0x70) and of Ranking2ScoreData (Ranking2 0x7A). What the game
// receives in answers is not changed by any of this.

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/openpak/switch-nex/internal/scorestore"
)

// BoardRanking2 is the board id of a Ranking2 category; Ranking categories use the plain
// decimal number and Splatfests "fest<id>".
func BoardRanking2(category uint32) string { return "r2-" + strconv.FormatUint(uint64(category), 10) }

// BoardFestival is the board id of a Splatoon 2 Splatfest.
func BoardFestival(id uint32) string { return fmt.Sprintf("fest%d", id) }

func scoreCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// keepScore stores one RankingScoreData for the player. A body whose order or update mode is
// outside the known values is not a score we read faithfully and is skipped.
// Order 0 is ascending (lowest is best), 1 descending; update mode 1 overwrites, 0 keeps the best.
func keepScore(env *Env, pid uint64, sd RankingScore) {
	if env.Scores == nil || sd.OrderBy > 1 || sd.UpdateMode > 1 {
		return
	}
	ctx, cancel := scoreCtx()
	defer cancel()
	if err := env.Scores.Submit(ctx, env.Name, strconv.FormatUint(uint64(sd.Category), 10), pid, int64(sd.Score), sd.OrderBy, sd.UpdateMode == 1); err != nil {
		log.Printf("[%s] scores: store: %v", env.Name, err)
	}
}

// keepRanking2Score stores one Ranking2 PutScore entry: higher is better, best kept (the
// order the in-memory board already used).
func keepRanking2Score(env *Env, pid uint64, category, score uint32, uid, misc uint64) {
	if env.Scores == nil {
		return
	}
	ctx, cancel := scoreCtx()
	defer cancel()
	if err := env.Scores.SubmitFull(ctx, env.Name, BoardRanking2(category), pid, int64(score), scorestore.Desc, false, int64(uid), int64(misc)); err != nil {
		log.Printf("[%s] scores: store: %v", env.Name, err)
	}
}

// keepFestivalScore adds a player's Splatfest contribution to the festival's board.
func keepFestivalScore(env *Env, pid uint64, festival, score uint32) {
	if env.Scores == nil {
		return
	}
	ctx, cancel := scoreCtx()
	defer cancel()
	if err := env.Scores.Add(ctx, env.Name, BoardFestival(festival), pid, int64(score), scorestore.Desc); err != nil {
		log.Printf("[%s] scores: store: %v", env.Name, err)
	}
}
