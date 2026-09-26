// Package stationurl implements the NEX StationURL string used in Switch-era
// LoginEx responses ("prudp:/k=v;k=v;…"). Parameters are a dictionary: the
// order on the wire does not matter to the client.
package switchnex

import (
	"fmt"
	"strconv"
	"strings"
)

// URL is a parsed StationURL. Scheme is "prudp" (or "prudps"); the rest are
// string/int parameters carried as key=value pairs.
type URL struct {
	Scheme  string
	Params  map[string]string
	IntVals map[string]uint64
	order   []string
}

// Empty returns a bare scheme-only URL. It renders as "prudp:/" — the shape the
// LoginEx special-station field carries when no special station is in use. The
// trailing ":/" is part of it; the bare word "prudp" is a different string.
func Empty(scheme string) *URL {
	if scheme == "" {
		scheme = "prudp"
	}
	return &URL{Scheme: scheme, Params: map[string]string{}, IntVals: map[string]uint64{}}
}

// EmptyString renders the bare scheme-only station URL.
func EmptyString(scheme string) string { return Empty(scheme).String() }

// Parse reads "scheme:/k=v;k=v" back into a URL. An unparseable string yields an
// empty URL rather than an error: a station the console sent in a shape we do
// not recognise is not a reason to drop the connection.
func Parse(s string) *URL {
	u := &URL{Params: map[string]string{}, IntVals: map[string]uint64{}}
	i := strings.Index(s, ":/")
	if i < 0 {
		return u
	}
	u.Scheme = s[:i]
	for _, field := range strings.Split(s[i+2:], ";") {
		if k, v, ok := strings.Cut(field, "="); ok {
			u.Set(k, v)
		}
	}
	return u
}

// Get returns a parameter as a string, whichever way it was set.
func (u *URL) Get(k string) string {
	if v, ok := u.Params[k]; ok {
		return v
	}
	if v, ok := u.IntVals[k]; ok {
		return strconv.FormatUint(v, 10)
	}
	return ""
}

// Has reports whether a parameter is present.
func (u *URL) Has(k string) bool {
	_, s := u.Params[k]
	_, i := u.IntVals[k]
	return s || i
}

// Remove drops a parameter from both the map and the key order. Leaving it in
// the order would re-emit it as "k=", and the console distinguishes a missing
// parameter from an empty one when it picks a LAN candidate.
func (u *URL) Remove(k string) {
	if !u.Has(k) {
		return
	}
	delete(u.Params, k)
	delete(u.IntVals, k)
	for i, kk := range u.order {
		if kk == k {
			u.order = append(u.order[:i], u.order[i+1:]...)
			break
		}
	}
}

// GetInt returns a parameter as uint64, or 0 if not present or not an integer.
// Both sides have to be read, the way Get does: Parse puts every field in
// Params as a string and only SetInt ever fills IntVals. Reading IntVals alone
// returned 0 for every PARSED station, so hasValidReplaceURL never fired and
// GetSessionURLs burned its whole 2.5s wait on every join — which is what shut
// Kirby's joiner out of the host's ~13s room window (2026-09-26).
func (u *URL) GetInt(k string) uint64 {
	if v, ok := u.IntVals[k]; ok {
		return v
	}
	n, _ := strconv.ParseUint(u.Params[k], 10, 64)
	return n
}

// Copy returns an independent copy, key order included.
func (u *URL) Copy() *URL {
	c := &URL{Scheme: u.Scheme, Params: map[string]string{}, IntVals: map[string]uint64{}, order: append([]string(nil), u.order...)}
	for k, v := range u.Params {
		c.Params[k] = v
	}
	for k, v := range u.IntVals {
		c.IntVals[k] = v
	}
	return c
}

// Station URL type flags. The public station a title advertises carries either
// the Wii U-era BehindNAT|Public (0x03) or, on Pia 5.19 and later, the extra
// Switch bit as well (0x0B) — per title, following the title's Pia version.
const (
	FlagBehindNAT uint64 = 0x01
	FlagPublic    uint64 = 0x02
	FlagSwitch    uint64 = 0x08
)

// New returns a scheme-defaulted URL with the secure-endpoint parameters set.
func New(address string, port uint16) *URL {
	return NewScheme("prudp", address, port)
}

// NewScheme is New with the station scheme named: titles differ ("prudp" or the
// secure "prudps"), and the scheme is part of the string the console parses.
func NewScheme(scheme, address string, port uint16) *URL {
	if scheme == "" {
		scheme = "prudp"
	}
	u := &URL{Scheme: scheme, Params: map[string]string{}, IntVals: map[string]uint64{}}
	u.Set("address", address)
	u.SetInt("port", uint64(port))
	// Values verified accepted by MK8D 3.0.5 (see SPEC.md StationURL).
	u.SetInt("CID", 1)
	u.SetInt("PID", 2)     // secure endpoint principal id
	u.SetInt("sid", 1)     // stream id
	u.SetInt("stream", 10) // RVSecure stream type
	u.SetInt("type", 2)    // public
	return u
}

func (u *URL) Set(k, v string) {
	if _, ok := u.Params[k]; !ok {
		u.order = append(u.order, k)
	}
	u.Params[k] = v
	delete(u.IntVals, k)
}

func (u *URL) SetInt(k string, v uint64) {
	if _, ok := u.IntVals[k]; !ok {
		u.order = append(u.order, k)
	}
	u.IntVals[k] = v
	delete(u.Params, k)
}

// Address returns the address parameter.
func (u *URL) Address() string { return u.Params["address"] }

// Port returns the port parameter.
func (u *URL) Port() uint64 { return u.IntVals["port"] }

// String renders "scheme:/k=v;k=v;…" (int parameters rendered as decimal).
func (u *URL) String() string {
	parts := make([]string, 0, len(u.order))
	for _, k := range u.order {
		if v, ok := u.Params[k]; ok {
			parts = append(parts, k+"="+v)
			continue
		}
		parts = append(parts, k+"="+strconv.FormatUint(u.IntVals[k], 10))
	}
	return fmt.Sprintf("%s:/%s", u.Scheme, strings.Join(parts, ";"))
}
