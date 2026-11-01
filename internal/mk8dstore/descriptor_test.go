package mk8dstore

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"
)

func chunk(id uint8, data []byte) []byte {
	return append([]byte{id, byte(len(data) >> 8), byte(len(data))}, data...)
}

func utf16be(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = binary.BigEndian.AppendUint16(b, u)
	}
	return b
}

func TestParseMetadataNameAndDescription(t *testing.T) {
	meta := []byte{0x5a, 0x5a}
	meta = append(meta, chunk(2, utf16be("Mushroom Cup Cup"))...)
	meta = append(meta, chunk(4, utf16be("race fast"))...)
	meta = append(meta, 255)
	name, desc := parseMetadata(meta)
	if name != "Mushroom Cup Cup" || desc != "race fast" {
		t.Fatalf("%q %q", name, desc)
	}
	for _, bad := range [][]byte{nil, {1, 2, 3}, {0x5a, 0x5a, 2, 0xFF, 0xFF}} {
		if n, d := parseMetadata(bad); n != "" || d != "" {
			t.Fatalf("garbage parsed: %q %q", n, d)
		}
	}
}

func TestParseDescriptorGarbage(t *testing.T) {
	if d := ParseDescriptor([]byte{1, 2}); d.Name != "" || d.Attributes == nil {
		t.Fatalf("%+v", d)
	}
}

// The body endpoint streams the ghost from object storage; here S3 is a stub.
func TestBlobsFetch(t *testing.T) {
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/nex-mk8d/ghost/") || r.URL.Query().Get("X-Amz-Signature") == "" {
			http.Error(w, "no", 403)
			return
		}
		w.Write([]byte("ghost-bytes"))
	}))
	defer s3.Close()
	b := NewBlobs(S3Config{Endpoint: s3.URL, PublicURL: "http://pub", Bucket: "nex-mk8d", Access: "a", Secret: "s"})
	if got, ok := b.Fetch("ghost/1.bin", 1<<20); !ok || string(got) != "ghost-bytes" {
		t.Fatalf("%q %v", got, ok)
	}
	if got, _ := b.Fetch("ghost/1.bin", 5); string(got) != "ghost" {
		t.Fatalf("limit not applied: %q", got)
	}
}
