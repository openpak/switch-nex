package main

// Identity: turning a console into an OpenPak account.
//
// A Switch has no NEX password. It logs in with a BAAS id token, and nx-baas is
// the only thing that can prove one — it holds the signing keys and this server
// deliberately does not. So the login carries a token, we hand that token to
// nx-baas, and nx-baas answers with the account behind it.
//
// Without this the login name IS the identity: whatever string arrives becomes
// a PID by hashing, so anyone who can reach the auth port can be anybody, and
// nx-baas never learns who is playing. The name is a claim; the token is proof.
//
// Two shapes arrive here. An emulator binds the account by adding an `openpak`
// claim only nx-baas can sign, which is the strong proof. A real console sends
// a plain BAAS id token with no such claim, so the whole token goes over and is
// proved there. We send both fields and let nx-baas decide which it can stand
// behind.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/switch-nex/rmc"
)

// errTokenRejected means the token itself did not prove out: expired, or not
// ours. The console reacts to that by fetching a fresh token and retrying,
// which is the right move. errNoAccount means the token was genuine but names
// nobody we know — retrying cannot fix that, and answering the retryable code
// instead puts the console in a refresh loop.
var (
	errTokenRejected = errors.New("id token not proven")
	errNoAccount     = errors.New("no account for this token")
)

// player is what nx-baas answers with.
type player struct {
	PID        uint64   `json:"pid"`
	BaasUserID string   `json:"baas_user_id"`
	Nickname   string   `json:"nickname"`
	Country    string   `json:"country"`
	Friends    []player `json:"friends"`
}

// nexPID is the principal id a Switch title expects for this account: the
// network service account id itself (the 16-hex `baas_user_id`), as a
// number. That is what the game logs in with as its username (ACNH sends
// "12297829382472990721" = 0xaaaaaaaaaaaa0001, its NSA id), what its own
// web API calls the user, and what it compares friends' NSA ids against
// when an island is friends-only. nx-baas's minted `pid` is the fallback
// for an account without one.
func (p player) nexPID() uint64 {
	if id, err := strconv.ParseUint(p.BaasUserID, 16, 64); err == nil && id != 0 {
		return id
	}
	return p.PID
}

func (p player) friendPIDs() []uint64 {
	out := make([]uint64, 0, len(p.Friends))
	for _, f := range p.Friends {
		if id := f.nexPID(); id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// identity resolves consoles to accounts through nx-baas. A zero value (no URL
// configured) resolves nothing and reports that it is disabled, which is what
// keeps this optional while the fleet is migrated.
type identity struct {
	url    string
	key    string
	client *http.Client
}

func newIdentity(url, key string) *identity {
	return &identity{
		url: strings.TrimRight(url, "/"),
		key: key,
		// A login is on the console's critical path and it gives up long
		// before any generous timeout would: better a clean refusal we can
		// see in the log than a console waiting on us.
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (i *identity) enabled() bool { return i != nil && i.url != "" }

// resolve proves a BAAS id token and returns the account behind it.
func (i *identity) resolve(idToken string) (*player, error) {
	query := map[string]any{"id_token": idToken}
	if claim := openpakClaim(idToken); claim != "" {
		query["openpak"] = claim
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, i.url+"/internal/switch/identity", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", i.key)

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errTokenRejected
	case http.StatusNotFound:
		return nil, errNoAccount
	default:
		return nil, fmt.Errorf("nx-baas identity: %s", resp.Status)
	}

	var p player
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	if p.PID == 0 || p.BaasUserID == "" {
		return nil, fmt.Errorf("nx-baas identity: incomplete reply")
	}
	return &p, nil
}

// openpakClaim pulls the nx-baas-signed binding claim out of a BAAS id token, or
// returns "" when there is none. Empty is the NORMAL case for a retail console
// and never an error here: it only means nx-baas has to prove the whole token
// itself. A client that sends the bare claim instead of a JWT passes through.
func openpakClaim(idToken string) string {
	if strings.HasPrefix(idToken, "nx2.") {
		return idToken
	}
	segments := strings.Split(idToken, ".")
	if len(segments) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(segments[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		OpenPak string `json:"openpak"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.OpenPak
}

// authenticationToken pulls the BAAS id token out of the login's extra data.
//
// The parameter is a DataHolder — a named object wrapped in two length
// prefixes — carrying an AuthenticationInfo:
//
//	String identifier ("AuthenticationInfo")
//	u32    length, u32 bufferLength
//	  Data base structure header (u8 version, u32 length = 0; no fields)
//	  structure header (u8 version, u32 content length)
//	    String Token          <- the JWT
//	    u32 NGSVersion, u32 TokenType, u32 ServerVersion
//
// A login carrying no token, or a holder naming something else, returns false:
// there is nothing to prove, and a Switch title has no password path to fall
// back to.
func authenticationToken(r *rmc.Reader) (string, bool) {
	name, err := r.String()
	if err != nil || name != "AuthenticationInfo" {
		return "", false
	}
	if _, err := r.U32(); err != nil { // holder length
		return "", false
	}
	if _, err := r.U32(); err != nil { // buffer length
		return "", false
	}
	if _, _, err := r.StructHeader(); err != nil { // the empty Data base
		return "", false
	}
	if _, _, err := r.StructHeader(); err != nil { // AuthenticationInfo itself
		return "", false
	}
	token, err := r.String()
	if err != nil || token == "" {
		return "", false
	}
	return token, true
}

// loginResultCode maps a resolution failure onto the code the console acts on.
func loginResultCode(err error) uint32 {
	if errors.Is(err, errTokenRejected) {
		// Retryable: the console fetches a fresh token and comes back.
		return rmc.ResultAuthTokenExpired
	}
	// Not retryable. Answering the retryable code here is what turns an
	// unknown account into a console retrying forever.
	return rmc.ResultAuthValidationFail
}
