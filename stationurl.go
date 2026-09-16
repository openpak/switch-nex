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

// New returns a prudp-scheme URL with the parameters set.
func New(address string, port uint16) *URL {
	u := &URL{Scheme: "prudp", Params: map[string]string{}, IntVals: map[string]uint64{}}
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
