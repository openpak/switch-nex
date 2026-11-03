package main

import (
	"testing"

	"github.com/openpak/switch-nex/protocols"
)

// "auto" puts a title's rankings listener at base+5; no other title may use that port for its
// own listeners (auth base, secure base+1, healthz base+2, Eagle base+3, rankings base+5).
func TestRankingsAutoPortsAreFree(t *testing.T) {
	used := map[int]string{}
	for _, tt := range titles {
		for off := 0; off <= 3; off++ {
			used[tt.base+off] = tt.name
		}
	}
	for _, tt := range titles {
		if o, clash := used[tt.base+rankingsPortOffset]; clash {
			t.Errorf("%s rankings port %d collides with %s", tt.name, tt.base+rankingsPortOffset, o)
		}
	}
}

func TestConfigureScoresPerTitle(t *testing.T) {
	t.Setenv("SWITCH_NEX_DATABASE_URL", "")
	t.Setenv("RANKINGS_ADDR", "")
	for _, tt := range titles {
		env := protocols.NewEnv(tt.name, "127.0.0.1", tt.base)
		tt.env(env)
		configureScores(env, tt.base)
		want := (env.Ranking || env.Ranking2) && tt.name != "mario-kart-8-deluxe"
		if (env.Scores != nil) != want {
			t.Errorf("%s: scores=%v ranking=%v ranking2=%v", tt.name, env.Scores != nil, env.Ranking, env.Ranking2)
		}
		if env.Scores != nil && env.Scores.Durable() {
			t.Errorf("%s: durable without a database url", tt.name)
		}
	}
}
