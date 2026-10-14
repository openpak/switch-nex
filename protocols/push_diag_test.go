package protocols

import "testing"

func TestDiagEnabled(t *testing.T) {
	for list, want := range map[string]bool{"": false, "duck-game": false, "arms, mario-party-superstars": true, "all": true} {
		if got := diagEnabled("mario-party-superstars", list); got != want {
			t.Errorf("%q: got %v", list, got)
		}
	}
}
