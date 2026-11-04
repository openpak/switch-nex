package protocols

// ForgetPIDs drops everything this title keeps in memory about the given principals: the
// sessions-side caches (friends, published notification data, title version, NAT class), the
// Ranking and Ranking2 records, the Splatfest votes, the Super Mario Maker 2 profile cache and the
// tournaments they created. It is the in-process half of account erasure; the durable half is
// scorestore.ErasePIDs and the title stores. Safe to repeat.
func (e *Env) ForgetPIDs(pids []uint64) {
	if len(pids) == 0 {
		return
	}
	e.friendsMu.Lock()
	for _, p := range pids {
		delete(e.friends, p)
		delete(e.notifData, p)
	}
	e.friendsMu.Unlock()
	for _, p := range pids {
		e.appVersions.Delete(p)
	}
	e.nat.mu.Lock()
	for _, p := range pids {
		delete(e.nat.m, p)
	}
	e.nat.mu.Unlock()

	e.ranking.mu.Lock()
	for _, p := range pids {
		delete(e.ranking.common, p)
	}
	e.ranking.mu.Unlock()

	e.r2common.mu.Lock()
	for _, p := range pids {
		delete(e.r2common.data, p)
		for _, b := range e.r2common.boards {
			delete(b, p)
		}
	}
	e.r2common.mu.Unlock()

	e.festivals.mu.Lock()
	for _, t := range e.festivals.fests {
		for _, p := range pids {
			delete(t.votes, p)
		}
	}
	e.festivals.mu.Unlock()

	e.smm2.mu.Lock()
	for _, p := range pids {
		delete(e.smm2.users, p)
	}
	e.smm2.mu.Unlock()

	e.tournaments.mu.Lock()
	for id, t := range e.tournaments.byID {
		for _, p := range pids {
			if t.owner == p {
				delete(e.tournaments.byID, id)
				break
			}
		}
	}
	e.tournaments.mu.Unlock()
}
