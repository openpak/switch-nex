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
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// errTokenRejected means the token itself did not prove out: expired, or not
// ours. The console reacts to that by fetching a fresh token and retrying,
// which is the right move. errNoAccount means the token was genuine but names
// nobody we know — retrying cannot fix that, and answering the retryable code
// instead puts the console in a refresh loop.
//
// errBanned means nx-baas proved the console but the account is banned or
// being deleted (403 account_banned / account_deleted). It is refused even
// when identity is not enforced: falling back to the login name would let a
// banned player straight back in.
var (
	errTokenRejected = errors.New("id token not proven")
	errNoAccount     = errors.New("no account for this token")
	errBanned        = errors.New("account banned")
)

// player is what nx-baas answers with.
type player struct {
	PID        uint64   `json:"pid"`
	BaasUserID string   `json:"baas_user_id"`
	Nickname   string   `json:"nickname"`
	Country    string   `json:"country"`
	Friends    []player `json:"friends"`
	// AccountID is the core account behind the console, when nx-baas knows it.
	// Voice chat maps a gathering's participants to accounts with it.
	AccountID string `json:"account_id"`
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

	// accounts maps a proven login's NEX PID to nx-baas's own pid for the
	// account, so WatchBans can ask about a connected player again.
	accounts sync.Map // uint64 -> uint64

	// members maps a proven login's NEX PID to who it is on OpenPak, for the
	// voice lookup (voice.go). Only proven logins land here: an unproven name
	// is nobody's account.
	members sync.Map // uint64 -> member
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
	p, err := i.post(query)
	if err != nil {
		return nil, err
	}
	i.accounts.Store(p.nexPID(), p.PID)
	if p.AccountID != "" {
		i.members.Store(p.nexPID(), member{AccountID: p.AccountID, Nickname: p.Nickname})
	}
	return p, nil
}

// stillAllowed re-asks nx-baas about a player who logged in earlier. It
// returns errBanned for a locked-out account, nil for one still allowed, and
// any other error when nx-baas could not answer (the session is kept then).
func (i *identity) stillAllowed(nexPID uint64) error {
	v, ok := i.accounts.Load(nexPID)
	if !ok {
		return nil // not a proven login: nothing to ask about
	}
	_, err := i.post(map[string]any{"pid": v.(uint64)})
	return err
}

func (i *identity) post(query map[string]any) (*player, error) {
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
	case http.StatusForbidden:
		return nil, errBanned
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

// appClaims reads the title id and installed version out of a BAAS id
// token's nintendo claim (ai, av): what aauth was told when the console asked
// for the token. The token is proved separately; this only reads it.
func appClaims(idToken string) (ai, av string) {
	segments := strings.Split(idToken, ".")
	if len(segments) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(segments[1], "="))
	if err != nil {
		return "", ""
	}
	var claims struct {
		Nintendo struct {
			AI string `json:"ai"`
			AV string `json:"av"`
		} `json:"nintendo"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return "", ""
	}
	return claims.Nintendo.AI, claims.Nintendo.AV
}

// outdated asks nx-baas whether a title version is older than the latest.
// It is the table aauth refuses with (2124-3001), so the two never disagree.
// Anything that cannot be answered is not outdated: an nx-baas outage must
// not lock out a player the console already let online.
func (i *identity) outdated(ai, av string) (bool, string) {
	if !i.enabled() || ai == "" || av == "" {
		return false, ""
	}
	q := url.Values{"title": {ai}, "version": {av}}
	req, err := http.NewRequest(http.MethodGet, i.url+"/internal/title-version?"+q.Encode(), nil)
	if err != nil {
		return false, ""
	}
	req.Header.Set("X-Internal-Key", i.key)
	resp, err := i.client.Do(req)
	if err != nil {
		log.Printf("version check %s %s: %v (allowed)", ai, av, err)
		return false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("version check %s %s: nx-baas %s (allowed)", ai, av, resp.Status)
		return false, ""
	}
	var v struct {
		Latest         string `json:"latest"`
		UpdateRequired bool   `json:"update_required"`
	}
	if json.NewDecoder(resp.Body).Decode(&v) != nil {
		return false, ""
	}
	return v.UpdateRequired, v.Latest
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
	if errors.Is(err, errBanned) {
		// RendezVous::AccountDisabled: the NEX result for a disabled account.
		return rmc.ResultAccountDisabled
	}
	if errors.Is(err, errTokenRejected) {
		// Retryable: the console fetches a fresh token and comes back.
		return rmc.ResultAuthTokenExpired
	}
	// Not retryable. Answering the retryable code here is what turns an
	// unknown account into a console retrying forever.
	return rmc.ResultAuthValidationFail
}

// banSweepEvery is how often watchBans re-asks about connected players.
// nx-baas caches an active answer for at most 30 s, so a ban reaches a live
// session within a minute.
const banSweepEvery = 30 * time.Second

// watchBans drops secure connections whose account nx-baas now refuses as
// banned. Only a positive "banned" drops a session; an nx-baas outage keeps
// it. The console's next login is refused with RendezVous::AccountDisabled.
func (i *identity) watchBans(ep *prudplite.Endpoint) {
	if !i.enabled() {
		return
	}
	for {
		time.Sleep(banSweepEvery)
		i.sweepBans(ep)
	}
}

func (i *identity) sweepBans(ep *prudplite.Endpoint) {
	checked := map[uint64]bool{}
	for _, c := range ep.Connections() {
		pid := c.PID
		if pid == 0 {
			continue
		}
		banned, seen := checked[pid]
		if !seen {
			banned = errors.Is(i.stillAllowed(pid), errBanned)
			checked[pid] = banned
		}
		if banned {
			log.Printf("[%s] pid=%d is banned, dropping its connection", ep.Name, pid)
			c.Drop()
		}
	}
}
