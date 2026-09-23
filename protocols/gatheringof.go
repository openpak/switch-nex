package protocols

// GatheringOf: the one question voice chat asks a title server. OpenPak's
// voice runs on the phone app, never on the console, so the room a player
// talks in is the gathering they are playing in right now; the website asks
// here which one that is and who else is in it.

// GatheringView is a copy of one live gathering's identity and members, safe
// to hold after the title's lock is released.
type GatheringView struct {
	GID          uint32
	Participants []uint64
}

// GatheringOf returns the gathering pid is participating in. A console that
// is in several at once (a lobby and the match it spawned) is answered with
// the newest one — the highest gid, since ids only count up within a
// process — which is the one it is playing in.
func (e *Env) GatheringOf(pid uint64) (GatheringView, bool) {
	if e == nil || e.Match == nil || pid == 0 {
		return GatheringView{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var best *gathering
	for _, g := range e.Match.all() {
		if g.participants[pid] && (best == nil || g.sess.GID > best.sess.GID) {
			best = g
		}
	}
	if best == nil {
		return GatheringView{}, false
	}
	v := GatheringView{GID: best.sess.GID, Participants: make([]uint64, 0, len(best.participants))}
	for p, in := range best.participants {
		if in {
			v.Participants = append(v.Participants, p)
		}
	}
	return v, true
}
