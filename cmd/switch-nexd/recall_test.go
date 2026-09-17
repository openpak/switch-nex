package main

import (
	"testing"
	"time"
)

// Behind a NAT the login and the secure CONNECT arrive from different internal
// addresses; the title's recent login pairs them, and only while it is recent.
func TestRecallByTitleWhenTheAddressSaysNothing(t *testing.T) {
	r := newRecall(time.Hour, "")
	r.remember("10.89.2.224", 42)
	r.remember(titleKey("mario-kart-8-deluxe"), 42)

	if _, ok := r.take("10.89.2.241"); ok {
		t.Fatal("a different address recalled by host")
	}
	if pid, ok := r.takeRecent(titleKey("mario-kart-8-deluxe"), recallByTitleWindow); !ok || pid != 42 {
		t.Fatalf("by title: pid=%d ok=%v", pid, ok)
	}
	if _, ok := r.takeRecent(titleKey("splatoon-2"), recallByTitleWindow); ok {
		t.Fatal("another title's login was recalled")
	}
	r.m[titleKey("mario-kart-8-deluxe")] = recallEntry{PID: 42, At: time.Now().Add(-time.Minute).Unix()}
	if _, ok := r.takeRecent(titleKey("mario-kart-8-deluxe"), recallByTitleWindow); ok {
		t.Fatal("a stale login was recalled")
	}

	if !addressSaysNothing("10.89.2.241") || !addressSaysNothing("127.0.0.1") || addressSaysNothing("145.241.199.19") {
		t.Fatal("private/public address classification")
	}
}
