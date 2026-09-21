package main

import "testing"

// A numeric login username IS the principal (ACNH logs in with its account
// id: "12297829382472990721"). Hashing it mints a second identity that
// nothing resolving by id — FindMatchmakeSessionByParticipant island visits
// included — can ever find.
func TestPidForNumericUsernameIsVerbatim(t *testing.T) {
	for _, tc := range []struct {
		username string
		want     uint64
	}{
		{"12297829382472990721", 12297829382472990721},
		{"12297829382472990722", 12297829382472990722},
		{"1", 1},
	} {
		if got := pidFor(tc.username); got != tc.want {
			t.Errorf("pidFor(%q) = %d, want %d", tc.username, got, tc.want)
		}
	}
}

// Opaque names and zero keep the stable hash: every other title logs in
// with a name, and 0 is nobody.
func TestPidForNameStillHashes(t *testing.T) {
	a, b := pidFor("hansolo"), pidFor("hansolo")
	if a == 0 || a != b {
		t.Fatalf("name hash unstable: %d vs %d", a, b)
	}
	if got := pidFor("leia"); got == a {
		t.Fatalf("distinct names collide: %d", got)
	}
	if got := pidFor("0"); got == 0 {
		t.Fatalf("zero username must not become PID 0")
	}
	if got := pidFor(""); got == 0 {
		t.Fatalf("empty username must not become PID 0")
	}
	// The hash space (0x6C000000 | 24 bits) cannot swallow a numeric id:
	// full-width ids live nowhere near it.
	if got := pidFor("Leia"); got < 0x6C000000 || got > 0x6CFFFFFF {
		t.Fatalf("hashed PID %#x outside the hash range", got)
	}
}
