package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/openpak/switch-nex/internal/lb"
	"log"
	"os"
	"sync"
	"time"

	"github.com/openpak/switch-nex/internal/rankingsapi"
	"github.com/openpak/switch-nex/internal/scorestore"
	"github.com/openpak/switch-nex/protocols"
)

// rankingsPortOffset places the rankings listener of "auto" next to the title's other ports
// (auth base, secure base+1, healthz base+2, Eagle base+3), the way Mario Kart 8 Deluxe's
// :22855 sits at its base 22850 + 5.
const rankingsPortOffset = 5

// boardNames are the human names of the boards the game's own documentation or capture has
// established; every other board is "Board N".
var boardNames = map[string]map[string]string{
	"mario-golf-super-rush": {"26": "Ranked match points"}, // console 2026-09-21: category 26 after a ranked match
}

// allowMemory is the explicit local-development switch: SWITCH_NEX_ALLOW_MEMORY=1 lets a
// title that wants a database run without one (its state is then lost on restart).
func allowMemory() bool { return os.Getenv("SWITCH_NEX_ALLOW_MEMORY") == "1" }

const (
	dbAttempts = 4
	dbWait     = 3 * time.Second
)

// openWithRetry runs open up to attempts times, wait apart; the last error is returned.
func openWithRetry(what string, attempts int, wait time.Duration, open func(ctx context.Context) error) error {
	var err error
	for i := 1; i <= attempts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = open(ctx)
		cancel()
		if err == nil {
			return nil
		}
		log.Printf("%s: attempt %d/%d: %v", what, i, attempts, err)
		if i < attempts {
			time.Sleep(wait)
		}
	}
	return err
}

// requireDB says what a title that needs a database does when it has none: refuse to start,
// unless SWITCH_NEX_ALLOW_MEMORY=1. Silently falling back to memory is how a restart once wiped
// every score.
func requireDB(title, what string, err error) {
	if allowMemory() {
		log.Printf("[%s] %s: %v (SWITCH_NEX_ALLOW_MEMORY=1: running without a database, state is lost on restart)", title, what, err)
		return
	}
	log.Fatalf("[%s] %s: %v: refusing to start without a database (set SWITCH_NEX_ALLOW_MEMORY=1 for local development)", title, what, err)
}

// openScoreStore opens the score store for dsn. An empty dsn is an error unless memory is allowed.
func openScoreStore(dsn string, attempts int, wait time.Duration) (*scorestore.Store, error) {
	if dsn == "" {
		if allowMemory() {
			return scorestore.NewMemory(), nil
		}
		return nil, errors.New("SWITCH_NEX_DATABASE_URL is not set")
	}
	var st *scorestore.Store
	err := openWithRetry("scores", attempts, wait, func(ctx context.Context) (e error) {
		st, e = scorestore.Open(ctx, dsn)
		return e
	})
	if err != nil {
		if allowMemory() {
			return scorestore.NewMemory(), nil
		}
		return nil, err
	}
	return st, nil
}

// sharedScores is the one score store of the process (one small pool, however many titles it serves).
var sharedScores struct {
	once sync.Once
	st   *scorestore.Store
	err  error
}

func scoreStore() (*scorestore.Store, error) {
	sharedScores.once.Do(func() {
		sharedScores.st, sharedScores.err = openScoreStore(os.ExpandEnv(os.Getenv("SWITCH_NEX_DATABASE_URL")), dbAttempts, dbWait)
	})
	return sharedScores.st, sharedScores.err
}

// configureScores persists the scores the title's Ranking and Ranking2 services receive and
// serves them through the OpenPak rankings contract.
//
//	SWITCH_NEX_DATABASE_URL  postgres://... (schema "scores", created on start). Unset or
//	                         unreachable, a title with Ranking/Ranking2 refuses to start,
//	                         unless SWITCH_NEX_ALLOW_MEMORY=1 (scores then live in memory)
//	RANKINGS_ADDR            contract listener; "auto" is :<title base+5>; unset is off
//
// Mario Kart 8 Deluxe has its own store (configureMK8D) and is left alone, as is any title
// that offers neither Ranking nor Ranking2.
func configureScores(env *protocols.Env, base int) {
	if env.Name == "mario-kart-8-deluxe" || !(env.Ranking || env.Ranking2) {
		return
	}
	st, err := scoreStore()
	if err != nil {
		requireDB(env.Name, "scores", err)
		st = scorestore.NewMemory()
	}
	env.Scores = st
	if useCoreScores(env, st) {
		log.Printf("[%s] scores: durable=%v, in the core leaderboards (rankings contract: the core's /t/%s)", env.Name, st.Durable(), env.Name)
		return
	}
	// No core configured (local development, or a unit not rolled yet): the old local store and contract.
	addr := os.ExpandEnv(os.Getenv("RANKINGS_ADDR"))
	if addr == "auto" {
		addr = fmt.Sprintf(":%d", base+rankingsPortOffset)
	}
	names := boardNames[env.Name]
	src := scorestore.Source{Store: st, Title: env.Name, Name: func(b string) string { return names[b] }}
	rankingsapi.Serve(addr, "switch", src)
	log.Printf("[%s] scores: durable=%v rankings=%q", env.Name, st.Durable(), addr)
}

// useCoreScores connects the title's scores to the core leaderboards when SWITCH_NEX_LEADERBOARDS_ADDR
// (and _KEY, SWITCH_NEX_CORE_ADDR, _KEY) are set, registers the title and starts the importer that
// moves local rows of players that gained an account link. Reports whether the core is in use.
//
//	SWITCH_NEX_LEADERBOARDS_ADDR, SWITCH_NEX_LEADERBOARDS_KEY   core leaderboards gRPC and its internal key
//	SWITCH_NEX_CORE_ADDR, SWITCH_NEX_CORE_KEY                    account core gRPC and its internal key
func useCoreScores(env *protocols.Env, st *scorestore.Store) bool {
	get := func(k string) string { return os.ExpandEnv(os.Getenv(k)) }
	if get("SWITCH_NEX_LEADERBOARDS_ADDR") == "" {
		return false
	}
	c, err := lb.Dial(get("SWITCH_NEX_LEADERBOARDS_ADDR"), get("SWITCH_NEX_LEADERBOARDS_KEY"), get("SWITCH_NEX_CORE_ADDR"), get("SWITCH_NEX_CORE_KEY"))
	if err != nil {
		requireDB(env.Name, "leaderboards core", err)
		return false
	}
	core := &scorestore.Core{C: c, Name: func(title, board string) string { return boardNames[title][board] }}
	if err := openWithRetry("scores title map", dbAttempts, dbWait, func(ctx context.Context) error { return core.Register(ctx, env.Name) }); err != nil {
		requireDB(env.Name, "leaderboards core", err)
		return false
	}
	st.UseCore(core)
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if _, err := st.Import(ctx, env.Name); err != nil {
				log.Printf("[%s] scores: import into the core: %v", env.Name, err)
			}
			cancel()
			time.Sleep(10 * time.Minute)
		}
	}()
	return true
}
