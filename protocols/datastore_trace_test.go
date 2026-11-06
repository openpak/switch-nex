package protocols

import (
	"strings"
	"testing"
)

func TestMaskBody(t *testing.T) {
	b := []byte{1, 0x78, 0x56, 0x34, 0x12, 0, 'S', 'e', 'c', 'r', 'e', 't', 'N', 'a', 'm', 'e', 0, 9}
	got := maskBody(b, 0x12345678)
	if got != "01[pid]00[str:10]0009" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(maskBody(b, 0x12345678), "Secret") {
		t.Fatal("leak")
	}
}
