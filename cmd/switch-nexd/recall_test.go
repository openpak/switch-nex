package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	mk8d     = "mario-kart-8-deluxe"
	home     = "203.0.113.7"  // one household's public address
	neighbor = "198.51.100.9" // somebody else's
	traefik  = "10.0.0.249"   // what the auth stage saw before PROXY protocol
)

// testRecall is a memory-only recall on a clock the test moves.
func testRecall(t *testing.T) (*recall, *time.Time) {
	t.Helper()
	r := newRecall(24*time.Hour, "")
	now := time.Now()
	r.now = func() time.Time { return now }
	return r, &now
}

func mustBind(t *testing.T, r *recall, host string, want uint64) {
	t.Helper()
	pid, how := r.bind(mk8d, host)
	if pid != want || (how != bindLogin && how != bindReconnect) {
		t.Fatalf("bind from %s: pid=%d (%s), want %d", host, pid, how, want)
	}
}

func mustRefuse(t *testing.T, r *recall, host string, want bindVerdict) {
	t.Helper()
	if pid, how := r.bind(mk8d, host); pid != 0 || how != want {
		t.Fatalf("bind from %s: pid=%d (%s), want a refusal (%s)", host, pid, how, want)
	}
}

// The sweep's case: two consoles behind one address log in to one title in
// the same second. Their ticketless CONNECTs used to both get the second
// login's PID. Nothing in a ticketless CONNECT tells them apart, so both are
// refused — and neither is ever handed the other's account.
func TestTwoConcurrentLoginsFromOneAddressAreNeverCrossed(t *testing.T) {
	for _, loginHost := range []string{home, traefik} {
		r, _ := testRecall(t)
		r.noteLogin(mk8d, loginHost, 1001)
		r.noteLogin(mk8d, loginHost, 1002)
		mustRefuse(t, r, home, bindAmbiguous)
		mustRefuse(t, r, home, bindAmbiguous)
	}
}

// Logged in one after the other, each CONNECT takes its own login, once.
func TestEachLoginIsClaimedOnceByItsOwnConnect(t *testing.T) {
	for _, loginHost := range []string{home, traefik} {
		r, now := testRecall(t)
		r.noteLogin(mk8d, loginHost, 1001)
		mustBind(t, r, home, 1001)
		*now = now.Add(2 * time.Second)
		r.noteLogin(mk8d, loginHost, 1002)
		mustBind(t, r, home, 1002) // not 1001, whose login is spent
	}
}

// With the console's address on the login (PROXY protocol), two households
// logging in in the same second never compete for each other's login.
func TestLoginsFromDifferentAddressesPairByAddress(t *testing.T) {
	r, _ := testRecall(t)
	r.noteLogin(mk8d, home, 1001)
	r.noteLogin(mk8d, neighbor, 2002)
	mustBind(t, r, neighbor, 2002)
	mustBind(t, r, home, 1001)
}

// A login is only good for its own title and only for a short while.
func TestLoginsDoNotCrossTitlesOrOutliveTheWindow(t *testing.T) {
	r, now := testRecall(t)
	r.noteLogin("splatoon-2", traefik, 1001)
	mustRefuse(t, r, home, bindNone)

	r.noteLogin(mk8d, traefik, 1002)
	*now = now.Add(recallByTitleWindow + time.Second)
	mustRefuse(t, r, home, bindNone)
}

// A re-CONNECT with no login in flight is answered only by an address that has
// played the title as exactly one player.
func TestReconnectNeedsAnAddressWithOnePlayer(t *testing.T) {
	r, now := testRecall(t)
	r.noteLogin(mk8d, traefik, 1001)
	mustBind(t, r, home, 1001)
	*now = now.Add(10 * time.Minute)
	mustBind(t, r, home, 1001)                // reconnect: the only player seen from home
	mustRefuse(t, r, neighbor, bindNone)      // never seen there
	mustRefuse(t, r, "10.89.2.241", bindNone) // an address that says nothing proves nothing

	// A second player from the same address makes the address ambiguous.
	r.noteLogin(mk8d, traefik, 1002)
	mustBind(t, r, home, 1002)
	*now = now.Add(10 * time.Minute)
	mustRefuse(t, r, home, bindAmbiguous)

	// And the table forgets after its TTL.
	*now = now.Add(25 * time.Hour)
	mustRefuse(t, r, home, bindNone)
}

// The reconnect table survives a restart; a file in the old shape (one PID per
// address, all of them Traefik's) is ignored rather than trusted.
func TestReconnectTableSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "recall.json")

	r, _ := testRecall(t)
	r.file = file
	r.noteLogin(mk8d, home, 1001)
	r.flush()

	r2 := newRecall(24*time.Hour, file)
	if pid, how := r2.bind(mk8d, home); pid != 1001 || how != bindReconnect {
		t.Fatalf("after restart: pid=%d (%s)", pid, how)
	}

	if err := os.WriteFile(file, []byte(`{"10.0.0.249":{"pid":1001,"at":1800000000},"title:mario-kart-8-deluxe":{"pid":1001,"at":1800000000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r3 := newRecall(24*time.Hour, file)
	if pid, how := r3.bind(mk8d, traefik); pid != 0 || how != bindNone {
		t.Fatalf("old-shape file was trusted: pid=%d (%s)", pid, how)
	}
}

func TestAddressClassification(t *testing.T) {
	for host, nothing := range map[string]bool{
		"10.89.2.241": true, "127.0.0.1": true, "192.168.1.20": true, "": true,
		"145.241.199.19": false, home: false,
	} {
		if addressSaysNothing(host) != nothing {
			t.Errorf("%q: says nothing = %v", host, !nothing)
		}
	}
}
