package smm2store

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The AWS documentation's SigV4 presigned GET example: the signature below is
// the one AWS publishes for these inputs.
func TestPresignMatchesAWSExample(t *testing.T) {
	b := NewBlobs(S3Config{Access: "AKIAIOSFODNN7EXAMPLE", Secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Bucket: "examplebucket"})
	b.now = func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }
	got := b.presignPath("GET", "https://examplebucket.s3.amazonaws.com", "/test.txt", 86400*time.Second)
	want := "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if got != want {
		t.Fatalf("presigned URL\n got %s\nwant %s", got, want)
	}
}

// The AWS documentation's POST policy example (SigV4): policy and signature.
func TestPostPolicySignature(t *testing.T) {
	b := NewBlobs(S3Config{Access: "AKIAIOSFODNN7EXAMPLE", Secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Bucket: "examplebucket", PublicURL: "https://x"})
	// AWS's example policy, signed with its example key.
	pol := "eyAiZXhwaXJhdGlvbiI6ICIyMDE1LTEyLTMwVDEyOjAwOjAwLjAwMFoiLA0KICAiY29uZGl0aW9ucyI6IFsNCiAgICB7ImJ1Y2tldCI6ICJzaWd2NGV4YW1wbGVidWNrZXQifSwNCiAgICBbInN0YXJ0cy13aXRoIiwgIiRrZXkiLCAidXNlci91c2VyMS8iXSwNCiAgICB7ImFjbCI6ICJwdWJsaWMtcmVhZCJ9LA0KICAgIHsic3VjY2Vzc19hY3Rpb25fcmVkaXJlY3QiOiAiaHR0cDovL3NpZ3Y0ZXhhbXBsZWJ1Y2tldC5zMy5hbWF6b25hd3MuY29tL3N1Y2Nlc3NmdWxfdXBsb2FkLmh0bWwifSwNCiAgICBbInN0YXJ0cy13aXRoIiwgIiRDb250ZW50LVR5cGUiLCAiaW1hZ2UvIl0sDQogICAgeyJ4LWFtei1tZXRhLXV1aWQiOiAiMTQzNjUxMjM2NTEyNzQifSwNCiAgICB7IngtYW16LXNlcnZlci1zaWRlLWVuY3J5cHRpb24iOiAiQUVTMjU2In0sDQogICAgWyJzdGFydHMtd2l0aCIsICIkeC1hbXotbWV0YS10YWciLCAiIl0sDQoNCiAgICB7IngtYW16LWNyZWRlbnRpYWwiOiAiQUtJQUlPU0ZPRE5ON0VYQU1QTEUvMjAxNTEyMjkvdXMtZWFzdC0xL3MzL2F3czRfcmVxdWVzdCJ9LA0KICAgIHsieC1hbXotYWxnb3JpdGhtIjogIkFXUzQtSE1BQy1TSEEyNTYifSwNCiAgICB7IngtYW16LWRhdGUiOiAiMjAxNTEyMjlUMDAwMDAwWiIgfQ0KICBdDQp9"
	sig := hex.EncodeToString(hmacSHA(b.signingKey("20151229"), pol))
	if sig != "8afdbf4008c03f22c2cd3cdb72e4afbb1f6a588f3255ac628749a66d7f09699e" {
		t.Fatalf("policy signature %s", sig)
	}
}

func TestPostFormFields(t *testing.T) {
	b := NewBlobs(S3Config{Access: "ak", Secret: "sk", Bucket: "nex-smm2", PublicURL: "https://ds.example/"})
	b.now = func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }
	url, fields, err := b.PostForm("course/5.bcd", 1000, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://ds.example/nex-smm2/" {
		t.Fatalf("url %q", url)
	}
	m := map[string]string{}
	for _, f := range fields {
		m[f[0]] = f[1]
	}
	if m["key"] != "course/5.bcd" || m["x-amz-algorithm"] != "AWS4-HMAC-SHA256" || m["x-amz-date"] != "20261101T120000Z" {
		t.Fatalf("fields %v", m)
	}
	raw, err := base64.StdEncoding.DecodeString(m["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Expiration string
		Conditions []any
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Expiration != "2026-11-01T13:00:00.000Z" || len(p.Conditions) != 7 {
		t.Fatalf("policy %s (%v)", raw, err)
	}
	if !strings.Contains(string(raw), `"content-length-range",0,1000`) {
		t.Fatalf("size limit missing: %s", raw)
	}
	if m["x-amz-signature"] != hex.EncodeToString(hmacSHA(b.signingKey("20261101"), m["policy"])) {
		t.Fatal("signature does not cover the policy")
	}
}
