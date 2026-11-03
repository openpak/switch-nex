package main

import (
	"context"
	"fmt"
	"log"
	"os"
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

// configureScores persists the scores the title's Ranking and Ranking2 services receive and
// serves them through the OpenPak rankings contract.
//
//	SWITCH_NEX_DATABASE_URL  postgres://... (schema "scores", created on start); unset keeps
//	                         scores in memory, lost on restart
//	RANKINGS_ADDR            contract listener; "auto" is :<title base+5>; unset is off
//
// Mario Kart 8 Deluxe has its own store (configureMK8D) and is left alone, as is any title
// that offers neither Ranking nor Ranking2.
func configureScores(env *protocols.Env, base int) {
	if env.Name == "mario-kart-8-deluxe" || !(env.Ranking || env.Ranking2) {
		return
	}
	dsn := os.ExpandEnv(os.Getenv("SWITCH_NEX_DATABASE_URL"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := scorestore.Open(ctx, dsn)
	if err != nil {
		log.Printf("[%s] scores: %v (keeping scores in memory)", env.Name, err)
		st = scorestore.NewMemory()
	}
	env.Scores = st
	addr := os.ExpandEnv(os.Getenv("RANKINGS_ADDR"))
	if addr == "auto" {
		addr = fmt.Sprintf(":%d", base+rankingsPortOffset)
	}
	names := boardNames[env.Name]
	src := scorestore.Source{Store: st, Title: env.Name, Name: func(b string) string { return names[b] }}
	rankingsapi.Serve(addr, "switch", src)
	log.Printf("[%s] scores: durable=%v rankings=%q", env.Name, st.Durable(), addr)
}
