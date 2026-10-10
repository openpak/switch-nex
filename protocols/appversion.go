package protocols

import (
	"github.com/openpak/switch-nex/redact"
	"log"
	"strings"
)

// Different versions of a title never share a match.
//
// A Switch title logs in with a BAAS id token whose nintendo.av claim is the
// installed title version, %08x of the u32 (MK8D 3.0.5 is 00150000), the same
// value aauth saw. Nintendo ran one game server per title across every patch
// and kept versions apart in matchmaking; so does this: a gathering takes the
// version of the player who created it, and a player of another version is
// neither auto-matched into it, shown it by a browse, nor let in by its id.
//
// Only what can be read decides: a login without a token, or a version that
// is not 8 hex digits (an aauth that failed leaves nx-baas's "0033" default),
// is unknown, and unknown matches everything, as before.

// SetAppVersion records a principal's title version as its login named it.
func (e *Env) SetAppVersion(pid uint64, av string) {
	if !IsAppVersion(av) {
		e.appVersions.Delete(pid)
		return
	}
	e.appVersions.Store(pid, strings.ToLower(av))
}

// AppVersion answers a principal's title version, "" when unknown.
func (e *Env) AppVersion(pid uint64) string {
	if v, ok := e.appVersions.Load(pid); ok {
		return v.(string)
	}
	return ""
}

// IsAppVersion reports whether av is the console's %08x title version.
func IsAppVersion(av string) bool {
	if len(av) != 8 {
		return false
	}
	for _, c := range av {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// versionFits reports whether a principal may meet a gathering: same version,
// or either side unknown.
func (e *Env) versionFits(g *gathering, pid uint64) bool {
	have := e.AppVersion(pid)
	return g.version == "" || have == "" || g.version == have
}

// sameVersionOpen is the open sessions a principal may be shown.
func (e *Env) sameVersionOpen(pid uint64) []*Session {
	var out []*Session
	for _, g := range e.Match.all() {
		if !g.sess.OpenParticipation {
			continue
		}
		if !e.versionFits(g, pid) {
			log.Printf("[%s] matchmake: gid=%d hidden from pid=%s (version %s, theirs %s)", e.Name, g.sess.GID, redact.Tag(pid), g.version, e.AppVersion(pid))
			continue
		}
		out = append(out, g.sess)
	}
	return out
}
