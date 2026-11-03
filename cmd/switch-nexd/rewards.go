package main

import "github.com/openpak/switch-nex/protocols"

// sessionMetric names the rewards family metric of the titles whose online sessions also count
// toward a series (two players sharing a gathering for REWARDS_SESSION_MIN, default 3 minutes).
var sessionMetric = map[string]string{
	"mario-kart-8-deluxe":       "kart_sessions.mk8dx",
	"splatoon-2":                "turf_sessions.s2",
	"super-smash-bros-ultimate": "brawl_sessions.ult",
}

// gameSessionMetric is the per-title session metric every title with a matchmaking store
// reports: game_sessions.<title name as in the titles list>.
func gameSessionMetric(name string) string { return "game_sessions." + name }

// sessionMetrics lists the metrics a title's sessions are reported under: its family metric
// when it has one, and game_sessions.<name> for every title with a matchmaking store. Titles
// without one (no gatherings: Super Mario Odyssey, TMNT Shredder's Revenge) report none.
func sessionMetrics(name string, env *protocols.Env) []string {
	if env.Match == nil {
		return nil
	}
	var out []string
	if m := sessionMetric[name]; m != "" {
		out = append(out, m)
	}
	return append(out, gameSessionMetric(name))
}
