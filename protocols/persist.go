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
