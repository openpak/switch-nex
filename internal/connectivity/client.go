// Package connectivity is the Switch NEX boundary to shared endpoint leases.
package connectivity

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("shared connectivity unavailable")

type Candidate struct {
	Endpoint string `json:"endpoint"`
	Kind     string `json:"kind"`
}

type Key struct {
	Title      string `json:"title"`
	Session    string `json:"session"`
	Player     string `json:"player"`
	Generation string `json:"generation"`
	Component  string `json:"component"`
}

type Participant struct {
	ID         string      `json:"id"`
	Key        Key         `json:"key"`
	Candidates []Candidate `json:"candidates"`
	ExpiresAt  time.Time   `json:"expires_at"`
}

type Client struct {
	base, token string
	BootID      string
	http        *http.Client
}

func FromEnvironment() (*Client, error) {
	base, file := os.Getenv("SWITCH_NEX_CONNECTIVITY_URL"), os.Getenv("SWITCH_NEX_CONNECTIVITY_TOKEN_FILE")
	if base == "" && file == "" {
		return nil, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return nil, ErrUnavailable
	}
	token, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return nil, ErrUnavailable
	}
	return New(base, strings.TrimSpace(string(token)))
}

func New(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(token) < 32 {
		return nil, ErrUnavailable
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrUnavailable
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, BootID: hex.EncodeToString(nonce[:]), http: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) call(method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return ErrUnavailable
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, input)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return ErrUnavailable
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(out); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}

func (c *Client) Register(key Key, candidates []Candidate) (Participant, error) {
	request := struct {
		Key        Key         `json:"key"`
		Candidates []Candidate `json:"candidates"`
		TTL        int         `json:"ttl_seconds"`
	}{key, candidates, 3600}
	var out Participant
	if err := c.call("POST", "/v1/participants", request, &out); err != nil {
		return Participant{}, err
	}
	if out.ID == "" || out.Key != key || !out.ExpiresAt.After(time.Now()) || out.ExpiresAt.After(time.Now().Add(3601*time.Second)) {
		return Participant{}, ErrUnavailable
	}
	return out, nil
}

func (c *Client) Release(id string) error {
	return c.call("DELETE", "/v1/participants/"+url.PathEscape(id), nil, nil)
}

// Relay is one allocated native UDP pair relay: packets arriving from one
// bound endpoint are forwarded to the other, anything else dropped.
type Relay struct {
	ID           string    `json:"id"`
	Endpoint     string    `json:"endpoint"`
	Participants [2]string `json:"participants"`
	Bound        bool      `json:"bound"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AllocateRelay reserves an inactive pair relay for two participant lease IDs
// owned by this backend in one title/session/component. The relay forwards
// nothing until both endpoints are bound.
func (c *Client) AllocateRelay(first, second string, ttlSeconds int) (Relay, error) {
	request := struct {
		Participants [2]string `json:"participants"`
		TTL          int       `json:"ttl_seconds"`
	}{[2]string{first, second}, ttlSeconds}
	var out Relay
	if err := c.call("POST", "/v1/relays", request, &out); err != nil {
		return Relay{}, err
	}
	if out.ID == "" || out.Endpoint == "" || out.Participants != [2]string{first, second} || !out.ExpiresAt.After(time.Now()) {
		return Relay{}, ErrUnavailable
	}
	return out, nil
}

// BindRelay pins the relay's exact forwarding endpoints, in the same order as
// the allocation's participant IDs. Only these two source endpoints forward
// packets; everything else is dropped. Endpoints must be NEX-verified (ticket
// identity plus reported candidates), never guesses: a wrong bind blackholes
// the pair until expiry.
func (c *Client) BindRelay(id string, first, second string) (Relay, error) {
	request := struct {
		Endpoints [2]string `json:"endpoints"`
	}{[2]string{first, second}}
	var out Relay
	if err := c.call("POST", "/v1/relays/"+url.PathEscape(id)+"/bind", request, &out); err != nil {
		return Relay{}, err
	}
	if out.ID != id || !out.Bound {
		return Relay{}, ErrUnavailable
	}
	return out, nil
}

// ReleaseRelay closes the relay socket immediately. Failing to release leaks
// the socket until expiry; release on gathering teardown and disconnect.
func (c *Client) ReleaseRelay(id string) error {
	return c.call("DELETE", "/v1/relays/"+url.PathEscape(id), nil, nil)
}
