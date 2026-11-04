package main

// Account erasure (card openpak/prds#120). DELETE /internal/accounts/{account_id}/data on every
// title's healthz listener (the container network only, never routed by Traefik), with
// "Authorization: Bearer $ACCOUNT_ERASE_KEY". It removes everything this server keeps for the
// account's Switch consoles: the PIDs come from the account core's links (namespace "switch",
// subject = the PID as 16 hex digits), the data from the score store (all titles), the Super Mario
// Maker 2 and Mario Kart 8 Deluxe stores of the title that owns them, and the in-memory copies of
// this process. See docs/erasure.md for what is erased and what is kept.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	"github.com/openpak/switch-nex/internal/mk8dstore"
	"github.com/openpak/switch-nex/internal/scorestore"
	"github.com/openpak/switch-nex/internal/smm2store"
	"github.com/openpak/switch-nex/protocols"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// pidLister names the PIDs an account has on the Switch network (hex link subjects, parsed).
type pidLister interface {
	SwitchPIDs(ctx context.Context, account string) ([]uint64, error)
}

type coreLinks struct{ c accountv1.LinksClient }

func (l coreLinks) SwitchPIDs(ctx context.Context, account string) ([]uint64, error) {
	r, err := l.c.ListAccountLinks(ctx, &accountv1.ListAccountLinksRequest{AccountId: account, Namespace: "switch"})
	if err != nil {
		return nil, err
	}
	var out []uint64
	for _, link := range r.GetLinks() {
		if n, err := strconv.ParseUint(strings.TrimSpace(link.GetSubjectId()), 16, 64); err == nil && n != 0 {
			out = append(out, n)
		}
	}
	return out, nil
}

// newCoreLinksFromEnv dials the account core with SWITCH_NEX_CORE_ADDR/_KEY (the pair the score
// importer uses), or MK8D_CORE_ADDR/_KEY. nil when neither is set.
func newCoreLinksFromEnv() pidLister {
	get := func(k string) string { return os.ExpandEnv(os.Getenv(k)) }
	addr, key := get("SWITCH_NEX_CORE_ADDR"), get("SWITCH_NEX_CORE_KEY")
	if addr == "" || key == "" {
		addr, key = get("MK8D_CORE_ADDR"), get("MK8D_CORE_KEY")
	}
	if addr == "" || key == "" {
		return nil
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, inv grpc.UnaryInvoker, o ...grpc.CallOption) error {
			ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(ctx, "x-internal-key", key), 10*time.Second)
			defer cancel()
			return inv(ctx, method, req, reply, cc, o...)
		}))
	if err != nil {
		return nil
	}
	return coreLinks{accountv1.NewLinksClient(conn)}
}

// eraser holds what an erasure needs from the process.
type eraser struct {
	key   string // ACCOUNT_ERASE_KEY; empty: the endpoint answers 503
	core  pidLister
	ids   *identity
	voice *voiceIndex // the envs of this process
	recal *recall
	sm2   *smm2store.Store
	sm2b  *smm2store.Blobs
	mk    *mk8dstore.Store
	mkb   *mk8dstore.Blobs
	score func() (*scorestore.Store, error)

	mu sync.Mutex // one erasure at a time
}

// Erase removes everything this process holds for the core account and returns the rows removed.
func (e *eraser) Erase(ctx context.Context, account string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.core == nil {
		return 0, errors.New("account core is not configured (SWITCH_NEX_CORE_ADDR/_KEY)")
	}
	set := map[uint64]bool{}
	listed, err := e.core.SwitchPIDs(ctx, account)
	if err != nil {
		return 0, err
	}
	for _, p := range listed {
		set[p] = true
	}
	// PIDs this process saw log in for the account (a link the core has already lost).
	if e.ids != nil {
		e.ids.members.Range(func(k, v any) bool {
			if v.(member).AccountID == account {
				set[k.(uint64)] = true
			}
			return true
		})
	}
	pids := make([]uint64, 0, len(set))
	for p := range set {
		pids = append(pids, p)
	}
	rows := 0
	if len(pids) > 0 {
		if e.score != nil {
			st, err := e.score()
			if err != nil {
				return 0, err
			}
			n, err := st.ErasePIDs(ctx, pids)
			if err != nil {
				return 0, fmt.Errorf("scores: %w", err)
			}
			rows += n
		}
		if e.sm2 != nil {
			n, err := e.sm2.ErasePIDs(ctx, pids, e.sm2b)
			if err != nil {
				return 0, fmt.Errorf("smm2: %w", err)
			}
			rows += n
		}
		if e.mk != nil {
			n, err := e.mk.ErasePIDs(ctx, pids, e.mkb)
			if err != nil {
				return 0, fmt.Errorf("mk8d: %w", err)
			}
			rows += n
		}
	}
	// Memory last: the durable rows are gone, so nothing re-hydrates them.
	if e.voice != nil {
		e.voice.mu.Lock()
		envs := append([]*protocols.Env(nil), e.voice.envs...)
		e.voice.mu.Unlock()
		for _, env := range envs {
			env.ForgetPIDs(pids)
		}
	}
	if e.ids != nil {
		e.ids.forget(account, pids)
	}
	if e.recal != nil {
		e.recal.forget(pids)
	}
	return rows, nil
}

// handler serves the erase route.
func (e *eraser) handler(w http.ResponseWriter, r *http.Request) {
	if e == nil || e.key == "" {
		http.Error(w, "erasure is not configured", http.StatusServiceUnavailable)
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(got), []byte(e.key)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	account := r.PathValue("account_id")
	if !uuidRE.MatchString(account) {
		http.Error(w, "account_id must be a UUID", http.StatusBadRequest)
		return
	}
	account = strings.ToLower(account)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	n, err := e.Erase(ctx, account)
	if err != nil {
		slog.Error("account erase failed", "service", "switch-nex", "err", err.Error())
		http.Error(w, "erase failed", http.StatusBadGateway)
		return
	}
	slog.Info("account erased", "account", account, "rows", n, "service", "switch-nex")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"service": "switch-nex", "rows": n}) //nolint:errcheck
}

// register mounts the route on a healthz mux.
func (e *eraser) register(mux *http.ServeMux) {
	if e == nil {
		return
	}
	mux.HandleFunc("DELETE /internal/accounts/{account_id}/data", e.handler)
}
