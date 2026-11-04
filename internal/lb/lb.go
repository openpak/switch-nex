// Package lb is the Switch NEX servers' client of the neutral core: the leaderboards service
// (scores) and the account core's links (NEX PID <-> account id). Adapters keep the wire and the
// core keeps the data (adapter-guide.md): no score table lives in switch-nex.
//
// A PID is the Switch BAAS user id; the account core files it as link namespace "switch", subject
// "%016x" (the same identity the reward events use).
package lb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	pb "github.com/openpak/switch-nex/internal/leaderboardspb"
)

// Network is the leaderboards network name of Switch titles.
const Network = "switch"

// maxRecv is the largest reply the client accepts (the default 4 MB is too small for a page of
// entries with info blobs).
const maxRecv = 16 << 20

// ErrNoLink means the PID or account has no active link in the account core.
var ErrNoLink = errors.New("lb: no active link")

// Identity maps PIDs to accounts and back.
type Identity interface {
	Account(ctx context.Context, pid uint64) (string, error)
	PID(ctx context.Context, account string) (uint64, error)
}

// Client is the leaderboards gRPC client plus the identity map.
type Client struct {
	LB pb.LeaderboardsClient
	ID Identity
}

// Dial connects both services. keys are sent as gRPC metadata x-internal-key.
func Dial(lbAddr, lbKey, coreAddr, coreKey string) (*Client, error) {
	llc, err := dial(lbAddr, lbKey)
	if err != nil {
		return nil, err
	}
	cc, err := dial(coreAddr, coreKey)
	if err != nil {
		return nil, err
	}
	return &Client{LB: pb.NewLeaderboardsClient(llc), ID: &links{c: accountv1.NewLinksClient(cc), m: map[string]cached{}}}, nil
}

func dial(addr, key string, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	if addr == "" || key == "" {
		return nil, fmt.Errorf("lb: address and key are required (%q)", addr)
	}
	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecv)),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, inv grpc.UnaryInvoker, o ...grpc.CallOption) error {
			return inv(metadata.AppendToOutgoingContext(ctx, "x-internal-key", key), method, req, reply, cc, o...)
		}),
	}, extra...)
	return grpc.NewClient(addr, opts...)
}

type cached struct {
	v   string
	exp time.Time
}

type links struct {
	c  accountv1.LinksClient
	mu sync.Mutex
	m  map[string]cached
}

func (l *links) get(k string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.m[k]
	if !ok || time.Now().After(c.exp) {
		return "", false
	}
	return c.v, true
}

func (l *links) put(k, v string, ttl time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 50000 {
		l.m = map[string]cached{}
	}
	l.m[k] = cached{v, time.Now().Add(ttl)}
}

func subject(pid uint64) string { return fmt.Sprintf("%016x", pid) }

// Account resolves a PID to its account id. Only positive answers are cached (ten minutes) so a
// player who links later is picked up at once.
func (l *links) Account(ctx context.Context, pid uint64) (string, error) {
	k := "p" + subject(pid)
	if v, ok := l.get(k); ok {
		return v, nil
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := l.c.GetLinkBySubject(rctx, &accountv1.GetLinkBySubjectRequest{Namespace: Network, SubjectId: subject(pid)})
	if status.Code(err) == codes.NotFound {
		return "", ErrNoLink
	}
	if err != nil {
		return "", err
	}
	if resp.GetState() != accountv1.LinkState_LINK_STATE_ACTIVE || resp.GetAccountId() == "" {
		return "", ErrNoLink
	}
	a := strings.ToLower(resp.GetAccountId())
	l.put(k, a, 10*time.Minute)
	return a, nil
}

// PID resolves an account to its Switch PID.
func (l *links) PID(ctx context.Context, account string) (uint64, error) {
	k := "a" + account
	if v, ok := l.get(k); ok {
		n, _ := strconv.ParseUint(v, 16, 64)
		return n, nil
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := l.c.GetActiveLinkByAccount(rctx, &accountv1.GetActiveLinkByAccountRequest{Namespace: Network, AccountId: account})
	if status.Code(err) == codes.NotFound {
		return 0, ErrNoLink
	}
	if err != nil {
		return 0, err
	}
	n, perr := strconv.ParseUint(resp.GetSubjectId(), 16, 64)
	if perr != nil {
		return 0, ErrNoLink
	}
	l.put(k, resp.GetSubjectId(), 10*time.Minute)
	return n, nil
}

// Title is one game's place in the core: the neutral key, the id the Switch knows it by and its name.
type Title struct {
	Key, Native, Name string
}

// Ref builds a board reference for a title.
func (t Title) Ref(board string) *pb.BoardRef {
	return &pb.BoardRef{Network: Network, Title: t.Native, Board: board}
}

// MapTitle registers the title (neutral key + Switch application id) before any data exists.
func (c *Client) MapTitle(ctx context.Context, t Title) error {
	_, err := c.LB.MapTitle(ctx, &pb.MapTitleRequest{Title: t.Key, Name: t.Name, Network: Network, NativeId: t.Native})
	return err
}

// Account returns the account for a PID.
func (c *Client) Account(ctx context.Context, pid uint64) (string, error) {
	return c.ID.Account(ctx, pid)
}

// All pages a whole board (best first), up to max entries.
func (c *Client) All(ctx context.Context, ref *pb.BoardRef, max int) ([]*pb.Entry, int64, error) {
	var out []*pb.Entry
	var total int64
	for len(out) < max {
		r, err := c.LB.Range(ctx, &pb.RangeRequest{Ref: ref, Start: int64(len(out) + 1), Limit: 100})
		if err != nil {
			return nil, 0, err
		}
		total = r.GetTotal()
		out = append(out, r.GetEntries()...)
		if len(r.GetEntries()) < 100 {
			break
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, total, nil
}
