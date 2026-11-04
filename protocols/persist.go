package protocols

// Durable copies of the state the answers to the game are built from: the Ranking2 boards and
// common data, the generic Ranking common data and the Splatoon 2 Splatfest votes and tallies.
// They live in the score store's blob table (Postgres when SWITCH_NEX_DATABASE_URL is set), are
// written through when they change and loaded once, on the first request that needs them, so a
// restart does not wipe what players see in game.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
)

const (
	nsR2Board  = "r2board"  // key "<category>/<pid>" -> nexUniqueID, misc u64, score u32
	nsR2Common = "r2common" // key "<pid>" -> JSON
	nsRkCommon = "rkcommon" // key "<pid>" -> raw blob
	nsFestVote = "festvote" // key "<festival>/<pid>" -> team u8
	nsFestSum  = "festsum"  // key "<festival>" -> 3 x u64 team scores
	nsNotif    = "notif"    // key "<type>/<pid>" -> JSON notificationEvent (what a player last published)
	nsTourn    = "tourn"    // key "<id>/<owner pid>" -> JSON tournament (the non-MK8D in-memory store)
	nsTournSeq = "tournseq" // key "next" -> u32, the highest tournament id ever handed out
)

func (e *Env) putBlob(ns, k string, v []byte) {
	if e.Scores == nil {
		return
	}
	ctx, cancel := scoreCtx()
	defer cancel()
	if err := e.Scores.PutBlob(ctx, e.Name, ns, k, v); err != nil {
		log.Printf("[%s] persist: %s: %v", e.Name, ns, err)
	}
}

func (e *Env) deleteBlob(ns, k string) {
	if e.Scores == nil {
		return
	}
	ctx, cancel := scoreCtx()
	defer cancel()
	if err := e.Scores.DeleteBlob(ctx, e.Name, ns, k); err != nil {
		log.Printf("[%s] persist: %s: %v", e.Name, ns, err)
	}
}

// hydrate loads the persisted state into the in-memory structures once.
func (e *Env) hydrate() {
	e.persistOnce.Do(func() {
		if e.Scores == nil {
			return
		}
		ctx, cancel := scoreCtx()
		defer cancel()
		n := 0
		if m, err := e.Scores.Blobs(ctx, e.Name, nsR2Board); err == nil {
			for k, v := range m {
				if e.Scores.HasCore() {
					// The core holds the boards: file the old blob as a local row (once) for the importer.
					cat, pidS, ok := strings.Cut(k, "/")
					c, err1 := strconv.ParseUint(cat, 10, 32)
					pid, err2 := strconv.ParseUint(pidS, 16, 64)
					if ok && err1 == nil && err2 == nil && len(v) == 20 {
						if err := e.Scores.SeedPending(ctx, e.Name, BoardRanking2(uint32(c)), pid, int64(binary.BigEndian.Uint32(v[16:])),
							int64(binary.BigEndian.Uint64(v)), int64(binary.BigEndian.Uint64(v[8:]))); err != nil {
							log.Printf("[%s] persist: seed r2board: %v", e.Name, err)
						}
					}
					continue
				}
				cat, pidS, ok := strings.Cut(k, "/")
				c, err1 := strconv.ParseUint(cat, 10, 32)
				pid, err2 := strconv.ParseUint(pidS, 16, 64)
				if !ok || err1 != nil || err2 != nil || len(v) != 20 {
					continue
				}
				e.r2common.load(uint32(c), r2Entry{pid: pid, nexUniqueID: binary.BigEndian.Uint64(v), misc: binary.BigEndian.Uint64(v[8:]), score: binary.BigEndian.Uint32(v[16:])})
				n++
			}
		} else {
			log.Printf("[%s] persist: load r2board: %v", e.Name, err)
		}
		if m, err := e.Scores.Blobs(ctx, e.Name, nsR2Common); err == nil {
			for k, v := range m {
				pid, err := strconv.ParseUint(k, 16, 64)
				var j r2CommonJSON
				if err != nil || json.Unmarshal(v, &j) != nil {
					continue
				}
				e.r2common.put(pid, r2Common{userName: j.Name, mii: j.Mii, binaryData: j.Bin})
				n++
			}
		}
		if m, err := e.Scores.Blobs(ctx, e.Name, nsRkCommon); err == nil {
			for k, v := range m {
				if pid, err := strconv.ParseUint(k, 16, 64); err == nil {
					e.ranking.putMem(pid, v)
					n++
				}
			}
		}
		if m, err := e.Scores.Blobs(ctx, e.Name, nsFestVote); err == nil {
			for k, v := range m {
				f, pidS, ok := strings.Cut(k, "/")
				id, err1 := strconv.ParseUint(f, 10, 32)
				pid, err2 := strconv.ParseUint(pidS, 16, 64)
				if !ok || err1 != nil || err2 != nil || len(v) != 1 || v[0] >= fest2Teams {
					continue
				}
				e.festivals.mu.Lock()
				if t := e.festivals.tally(uint32(id), true); t != nil {
					t.votes[pid] = v[0]
				}
				e.festivals.mu.Unlock()
				n++
			}
		}
		if m, err := e.Scores.Blobs(ctx, e.Name, nsFestSum); err == nil {
			for k, v := range m {
				id, err := strconv.ParseUint(k, 10, 32)
				if err != nil || len(v) != 8*fest2Teams {
					continue
				}
				e.festivals.mu.Lock()
				if t := e.festivals.tally(uint32(id), true); t != nil {
					for i := range t.scores {
						t.scores[i] = binary.BigEndian.Uint64(v[8*i:])
					}
				}
				e.festivals.mu.Unlock()
				n++
			}
		}
		if m, err := e.Scores.Blobs(ctx, e.Name, nsNotif); err == nil {
			e.friendsMu.Lock()
			if e.notifData == nil {
				e.notifData = map[uint64]map[uint32]notificationEvent{}
			}
			for k, v := range m {
				var ev notificationEvent
				_, pidS, ok := strings.Cut(k, "/")
				pid, err := strconv.ParseUint(pidS, 16, 64)
				if !ok || err != nil || json.Unmarshal(v, &ev) != nil {
					continue
				}
				if e.notifData[pid] == nil {
					e.notifData[pid] = map[uint32]notificationEvent{}
				}
				e.notifData[pid][ev.Type] = ev
				n++
			}
			e.friendsMu.Unlock()
		} else {
			log.Printf("[%s] persist: load notif: %v", e.Name, err)
		}
		e.tournaments.mu.Lock()
		if m, err := e.Scores.Blobs(ctx, e.Name, nsTourn); err == nil {
			if e.tournaments.byID == nil {
				e.tournaments.byID = map[uint32]tournament{}
			}
			for _, v := range m {
				var j tournamentJSON
				if json.Unmarshal(v, &j) != nil {
					continue
				}
				e.tournaments.byID[j.ID] = tournament{id: j.ID, owner: j.Owner, version: j.Version, blob: j.Blob}
				e.tournaments.next = max(e.tournaments.next, j.ID)
				n++
			}
		} else {
			log.Printf("[%s] persist: load tournaments: %v", e.Name, err)
		}
		if m, err := e.Scores.Blobs(ctx, e.Name, nsTournSeq); err == nil {
			if v := m["next"]; len(v) == 4 {
				e.tournaments.next = max(e.tournaments.next, binary.BigEndian.Uint32(v))
			}
		}
		e.tournaments.mu.Unlock()
		if n > 0 {
			log.Printf("[%s] persist: restored %d records", e.Name, n)
		}
	})
}

type r2CommonJSON struct {
	Name string `json:"n"`
	Mii  []byte `json:"m"`
	Bin  []byte `json:"b"`
}

func (e *Env) keepR2Board(category uint32, en r2Entry) {
	if e.Scores.HasCore() {
		return
	}
	v := make([]byte, 20)
	binary.BigEndian.PutUint64(v, en.nexUniqueID)
	binary.BigEndian.PutUint64(v[8:], en.misc)
	binary.BigEndian.PutUint32(v[16:], en.score)
	e.putBlob(nsR2Board, fmt.Sprintf("%d/%x", category, en.pid), v)
}

func (e *Env) keepR2Common(pid uint64, c r2Common) {
	b, _ := json.Marshal(r2CommonJSON{Name: c.userName, Mii: c.mii, Bin: c.binaryData})
	e.putBlob(nsR2Common, fmt.Sprintf("%x", pid), b)
}

func (e *Env) keepFestVote(id uint32, pid uint64, team uint8) {
	e.putBlob(nsFestVote, fmt.Sprintf("%d/%x", id, pid), []byte{team})
}

func (e *Env) keepFestSum(id uint32, scores [fest2Teams]uint64) {
	v := make([]byte, 8*fest2Teams)
	for i, s := range scores {
		binary.BigEndian.PutUint64(v[8*i:], s)
	}
	e.putBlob(nsFestSum, strconv.FormatUint(uint64(id), 10), v)
}

func (e *Env) keepNotif(pid uint64, ev notificationEvent) {
	b, _ := json.Marshal(ev)
	e.putBlob(nsNotif, fmt.Sprintf("%d/%x", ev.Type, pid), b)
}

type tournamentJSON struct {
	ID      uint32 `json:"i"`
	Owner   uint64 `json:"o"`
	Version uint8  `json:"v"`
	Blob    []byte `json:"b"`
}

// keepTournament writes a tournament and the id counter, so an id is never handed out twice.
func (e *Env) keepTournament(t tournament) {
	b, _ := json.Marshal(tournamentJSON{ID: t.id, Owner: t.owner, Version: t.version, Blob: t.blob})
	e.putBlob(nsTourn, fmt.Sprintf("%d/%x", t.id, t.owner), b)
	seq := make([]byte, 4)
	binary.BigEndian.PutUint32(seq, t.id)
	e.putBlob(nsTournSeq, "next", seq)
}

func (e *Env) dropTournament(t tournament) {
	e.deleteBlob(nsTourn, fmt.Sprintf("%d/%x", t.id, t.owner))
}
