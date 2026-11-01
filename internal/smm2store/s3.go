// Package smm2store is the durable side of Super Mario Maker 2: the Postgres
// course store, the S3 (MinIO) presigner and the read API the course site uses.
package smm2store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3Config points at the bucket. Endpoint is what this process dials; PublicURL
// is the base the console is told (the signature binds the host, so a URL for
// the console is signed for PublicURL, not Endpoint).
type S3Config struct {
	Endpoint  string // e.g. http://127.0.0.1:19000
	PublicURL string // e.g. https://smm2-ds.openpak.org
	Bucket    string
	Access    string
	Secret    string
	Region    string // default us-east-1
}

// Blobs implements protocols.SMM2Blobs over S3 with hand-rolled SigV4, so the
// image takes no S3 client dependency.
type Blobs struct {
	cfg  S3Config
	http *http.Client
	now  func() time.Time
}

// NewBlobs builds the presigner.
func NewBlobs(cfg S3Config) *Blobs {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	return &Blobs{cfg: cfg, http: &http.Client{Timeout: 5 * time.Second}, now: time.Now}
}

func hmacSHA(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func (b *Blobs) signingKey(date string) []byte {
	k := hmacSHA([]byte("AWS4"+b.cfg.Secret), date)
	k = hmacSHA(k, b.cfg.Region)
	k = hmacSHA(k, "s3")
	return hmacSHA(k, "aws4_request")
}

// qEscape escapes a query component: like uriEscape but "/" is encoded too.
func qEscape(s string) string { return strings.ReplaceAll(uriEscape(s), "/", "%2F") }

func uriEscape(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~/", c) >= 0 {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

// presign is a query-string SigV4 URL for method on key against base.
func (b *Blobs) presign(method, base, key string, ttl time.Duration) string {
	return b.presignPath(method, base, "/"+b.cfg.Bucket+"/"+key, ttl)
}

func (b *Blobs) presignPath(method, base, path string, ttl time.Duration) string {
	u, _ := url.Parse(base)
	t := b.now().UTC()
	date, stamp := t.Format("20060102"), t.Format("20060102T150405Z")
	scope := date + "/" + b.cfg.Region + "/s3/aws4_request"
	q := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    b.cfg.Access + "/" + scope,
		"X-Amz-Date":          stamp,
		"X-Amz-Expires":       fmt.Sprint(int(ttl.Seconds())),
		"X-Amz-SignedHeaders": "host",
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = qEscape(k) + "=" + qEscape(q[k])
	}
	canonQuery := strings.Join(parts, "&")
	canon := method + "\n" + uriEscape(path) + "\n" + canonQuery + "\nhost:" + u.Host + "\n\nhost\nUNSIGNED-PAYLOAD"
	sum := sha256.Sum256([]byte(canon))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	sig := hex.EncodeToString(hmacSHA(b.signingKey(date), toSign))
	return u.Scheme + "://" + u.Host + uriEscape(path) + "?" + canonQuery + "&X-Amz-Signature=" + sig
}

// GetURL is a presigned GET the console can fetch.
func (b *Blobs) GetURL(key string, ttl time.Duration) string {
	return b.presign("GET", b.cfg.PublicURL, key, ttl)
}

// internalGet is a presigned GET this process fetches (the read API's thumbnails).
func (b *Blobs) internalGet(key string) string {
	return b.presign("GET", b.cfg.Endpoint, key, time.Minute)
}

// Size HEADs the object.
func (b *Blobs) Size(key string) (int64, bool) {
	req, _ := http.NewRequest("HEAD", b.presign("HEAD", b.cfg.Endpoint, key, time.Minute), nil)
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, false
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, false
	}
	return resp.ContentLength, true
}

// PostForm builds an S3 POST policy (SigV4) for key limited to maxSize bytes.
// The console POSTs a multipart form with these fields followed by the file.
func (b *Blobs) PostForm(key string, maxSize uint32, ttl time.Duration) (string, [][2]string, error) {
	t := b.now().UTC()
	date, stamp := t.Format("20060102"), t.Format("20060102T150405Z")
	cred := b.cfg.Access + "/" + date + "/" + b.cfg.Region + "/s3/aws4_request"
	policy := map[string]any{
		"expiration": t.Add(ttl).Format("2006-01-02T15:04:05.000Z"),
		"conditions": []any{
			map[string]string{"bucket": b.cfg.Bucket},
			map[string]string{"key": key},
			map[string]string{"x-amz-algorithm": "AWS4-HMAC-SHA256"},
			map[string]string{"x-amz-credential": cred},
			map[string]string{"x-amz-date": stamp},
			[]any{"content-length-range", 0, maxSize},
			[]any{"starts-with", "$Content-Type", ""},
		},
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return "", nil, err
	}
	pol := base64.StdEncoding.EncodeToString(raw)
	sig := hex.EncodeToString(hmacSHA(b.signingKey(date), pol))
	return b.cfg.PublicURL + "/" + b.cfg.Bucket + "/", [][2]string{
		{"key", key},
		{"x-amz-algorithm", "AWS4-HMAC-SHA256"},
		{"x-amz-credential", cred},
		{"x-amz-date", stamp},
		{"policy", pol},
		{"x-amz-signature", sig},
	}, nil
}

// EnsureBucket creates the bucket when it does not exist yet, as the nex titles
// do for their nex-* buckets. A bucket that is already there is fine.
func (b *Blobs) EnsureBucket() error {
	req, _ := http.NewRequest("PUT", b.presignPath("PUT", b.cfg.Endpoint, "/"+b.cfg.Bucket, time.Minute), nil)
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode == 200 || resp.StatusCode == 409 && strings.Contains(string(body), "BucketAlreadyOwnedByYou") {
		return nil
	}
	return fmt.Errorf("create bucket %s: %s %s", b.cfg.Bucket, resp.Status, strings.TrimSpace(string(body)))
}
