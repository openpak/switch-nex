package main

// The voice-chat lookup. OpenPak's voice chat runs in the phone app, never on
// the console (as Nintendo's did), and a player's voice room is the gathering
// they are playing in. Only this process knows gatherings, and only nx-baas
// knows accounts, so the proven login joins the two: identity.go remembers the
// account behind every proven NEX PID, and this answers the website's
//
//	GET /internal/voice/gathering?account_id=<core account id>
//	X-Internal-Key: <NX_INTERNAL_KEY>
//	-> 200 {"title","gid","participants":[{"pid","account_id","nickname"}]}
//	-> 404 when the account is in no gathering on any title served here
//
// on every title's healthz port (no port of its own: the answer covers the
// whole process, so any one of them will do). A participant who logged in on
// an unproven name has an empty account_id: they play, but nobody can say who
// they are, so they cannot be let into a voice room.

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/openpak/switch-nex/protocols"
)

// member is an account as the voice room shows it.
type member struct {
	AccountID string `json:"account_id"`
	Nickname  string `json:"nickname"`
}

type voiceIndex struct {
	ids *identity

	mu   sync.Mutex
	envs []*protocols.Env
}

func (v *voiceIndex) add(e *protocols.Env) {
	v.mu.Lock()
	v.envs = append(v.envs, e)
	v.mu.Unlock()
}

type voiceParticipant struct {
	PID uint64 `json:"pid"`
	member
}

func (v *voiceIndex) gathering(w http.ResponseWriter, r *http.Request) {
	if !v.ids.enabled() || v.ids.key == "" {
		http.Error(w, "identity is not configured", http.StatusServiceUnavailable)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Key")), []byte(v.ids.key)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	account := r.URL.Query().Get("account_id")
	if account == "" {
		http.Error(w, "account_id required", http.StatusBadRequest)
		return
	}
	// One account can own several consoles; each logs in under its own PID.
	var pids []uint64
	v.ids.members.Range(func(k, val any) bool {
		if val.(member).AccountID == account {
			pids = append(pids, k.(uint64))
		}
		return true
	})
	v.mu.Lock()
	envs := append([]*protocols.Env(nil), v.envs...)
	v.mu.Unlock()
	for _, e := range envs {
		for _, pid := range pids {
			g, ok := e.GatheringOf(pid)
			if !ok {
				continue
			}
			out := struct {
				Title        string             `json:"title"`
				GID          uint32             `json:"gid"`
				Participants []voiceParticipant `json:"participants"`
			}{Title: e.Name, GID: g.GID, Participants: make([]voiceParticipant, 0, len(g.Participants))}
			for _, p := range g.Participants {
				m, _ := v.ids.members.Load(p)
				who, _ := m.(member)
				out.Participants = append(out.Participants, voiceParticipant{PID: p, member: who})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
	}
	http.Error(w, "not in a gathering", http.StatusNotFound)
}
