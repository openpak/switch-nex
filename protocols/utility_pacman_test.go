package protocols

import "testing"

// The client refuses the table unless keys 0-9, 20 and 21 are all present.
func TestPacMan99IntegerSettingsHasEveryKeyTheClientChecks(t *testing.T) {
	m, code := PacMan99IntegerSettings(0)
	if code != 0 {
		t.Fatal(code)
	}
	for _, k := range []uint16{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 20, 21} {
		if _, ok := m[k]; !ok {
			t.Fatalf("key %d missing", k)
		}
	}
	if m[9] < 2 || m[21] < 2 {
		t.Fatal("a lobby must never start below two players")
	}
}
