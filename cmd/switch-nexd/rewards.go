package main

// sessionMetric names the rewards session metric of the titles whose online sessions count
// (two players sharing a gathering for REWARDS_SESSION_MIN, default 3 minutes).
var sessionMetric = map[string]string{
	"mario-kart-8-deluxe":       "kart_sessions.mk8dx",
	"splatoon-2":                "turf_sessions.s2",
	"super-smash-bros-ultimate": "brawl_sessions.ult",
}
