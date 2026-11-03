package main

import "testing"

func TestTrimToken(t *testing.T) {
	clean := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.abc-_DEF"
	for name, in := range map[string]string{
		"clean": clean,
		"junk":  clean + "\xff\xfd\x00stale.buffer\x19",
		"nul":   clean + "\x00",
	} {
		if got := trimToken(in); got != clean {
			t.Errorf("%s: trimToken = %q, want %q", name, got, clean)
		}
	}
}
