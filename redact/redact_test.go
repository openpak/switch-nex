package redact

import (
	"fmt"
	"strings"
	"testing"
)

func TestTagHidesTheValue(t *testing.T) {
	for _, v := range []any{uint64(1234567890), "Some Nickname", "tok-abc123secret"} {
		line := fmt.Sprintf("login pid=%s", Tag(v))
		if strings.Contains(line, fmt.Sprint(v)) {
			t.Fatalf("%q leaked into %q", v, line)
		}
		if Tag(v) != Tag(v) {
			t.Fatalf("tag for %v is not stable", v)
		}
	}
	if Tag(1) == Tag(2) {
		t.Fatal("two values share a tag")
	}
}
