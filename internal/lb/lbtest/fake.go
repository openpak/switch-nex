// Package lbtest is an in-memory stand-in for the core: a leaderboards client that follows the
// real service's aggregation, rank and position rules, and a PID <-> account map.
package lbtest

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/openpak/switch-nex/internal/lb"
	pb "github.com/openpak/switch-nex/internal/leaderboardspb"
)

type key struct{ title, board string }

// Fake implements pb.LeaderboardsClient.
type Fake struct {
	mu      sync.Mutex
	Boards  map[key]*pb.Policy
	entries map[key][]*pb.Entry
	Mapped  map[string]string // native -> neutral key
	tick    int64
	Down    bool
	Calls   int
}

func New() *Fake {
	return &Fake{Boards: map[key]*pb.Policy{}, entries: map[key][]*pb.Entry{}, Mapped: map[string]string{}}
}

var _ pb.LeaderboardsClient = (*Fake)(nil)

func k(r *pb.BoardRef) key { return key{r.GetTitle(), r.GetBoard()} }

func (f *Fake) ranked(kk key) []*pb.Entry {
	asc := f.Boards[kk] != nil && f.Boards[kk].Order == "asc"
	es := append([]*pb.Entry(nil), f.entries[kk]...)
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Score != es[j].Score {
			return (es[i].Score < es[j].Score) == asc
		}
		if es[i].UpdatedUnixMs != es[j].UpdatedUnixMs {
			return es[i].UpdatedUnixMs < es[j].UpdatedUnixMs
		}
		return es[i].Account < es[j].Account
	})
	out := make([]*pb.Entry, len(es))
	for i, e := range es {
		c := proto.Clone(e).(*pb.Entry)
		c.Position, c.Rank = int64(i+1), int64(i+1)
		if i > 0 && out[i-1].Score == c.Score {
			c.Rank = out[i-1].Rank
		}
		out[i] = c
	}
	return out
}

func (f *Fake) fail() error {
	f.Calls++
	if f.Down {
		return fmt.Errorf("fake leaderboards down")
	}
	return nil
}

func (f *Fake) Submit(_ context.Context, req *pb.SubmitRequest, _ ...grpc.CallOption) (*pb.SubmitResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	kk := k(req.Ref)
	if f.Boards[kk] == nil {
		f.Boards[kk] = req.CreatePolicy
		if f.Boards[kk] == nil {
			f.Boards[kk] = &pb.Policy{}
		}
	}
	p := f.Boards[kk]
	var cur *pb.Entry
	for _, e := range f.entries[kk] {
		if e.Account == req.Account && e.Sub == req.Sub {
			cur = e
		}
	}
	changed := cur == nil
	if cur != nil {
		better := req.Score > cur.Score
		if p.Order == "asc" {
			better = req.Score < cur.Score
		}
		changed = better || p.Aggregation == "latest" || p.Aggregation == "force"
	}
	f.tick++
	if changed {
		e := &pb.Entry{Account: req.Account, Sub: req.Sub, Score: req.Score, Columns: req.Columns, Comment: req.Comment,
			Info: req.Info, Network: req.Ref.Network, UpdatedUnixMs: f.tick}
		if cur != nil {
			cur.Score, cur.Columns, cur.Comment, cur.Info, cur.Network, cur.UpdatedUnixMs = e.Score, e.Columns, e.Comment, e.Info, e.Network, e.UpdatedUnixMs
		} else {
			f.entries[kk] = append(f.entries[kk], e)
		}
	}
	var entry *pb.Entry
	for _, e := range f.ranked(kk) {
		if e.Account == req.Account && e.Sub == req.Sub {
			entry = e
		}
	}
	return &pb.SubmitResponse{Changed: changed, Entry: entry, Total: int64(len(f.entries[kk]))}, nil
}

func (f *Fake) Range(_ context.Context, req *pb.RangeRequest, _ ...grpc.CallOption) (*pb.RangeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	all := f.ranked(k(req.Ref))
	lo := min(max(int(req.Start)-1, 0), len(all))
	n := int(req.Limit)
	if n <= 0 {
		n = 25
	}
	return &pb.RangeResponse{Entries: all[lo:min(lo+min(n, 100), len(all))], Total: int64(len(all))}, nil
}

func (f *Fake) ByAccounts(_ context.Context, req *pb.ByAccountsRequest, _ ...grpc.CallOption) (*pb.RangeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, a := range req.Accounts {
		want[a] = true
	}
	all := f.ranked(k(req.Ref))
	out := &pb.RangeResponse{Total: int64(len(all))}
	for _, e := range all {
		if want[e.Account] {
			out.Entries = append(out.Entries, e)
		}
	}
	return out, nil
}

func (f *Fake) Total(_ context.Context, req *pb.TotalRequest, _ ...grpc.CallOption) (*pb.TotalResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	return &pb.TotalResponse{Total: int64(len(f.entries[k(req.Ref)]))}, nil
}

func (f *Fake) UpsertBoard(_ context.Context, req *pb.UpsertBoardRequest, _ ...grpc.CallOption) (*pb.BoardInfoResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	f.Boards[k(req.Ref)] = req.Policy
	return &pb.BoardInfoResponse{}, nil
}

func (f *Fake) MapTitle(_ context.Context, req *pb.MapTitleRequest, _ ...grpc.CallOption) (*pb.MapTitleResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	f.Mapped[req.NativeId] = req.Title
	return &pb.MapTitleResponse{Title: req.Title}, nil
}

func (f *Fake) ListBoards(_ context.Context, req *pb.ListBoardsRequest, _ ...grpc.CallOption) (*pb.ListBoardsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	var ids []string
	for kk := range f.Boards {
		if kk.title == req.Title {
			ids = append(ids, kk.board)
		}
	}
	sort.Strings(ids)
	out := &pb.ListBoardsResponse{}
	for _, id := range ids {
		out.Boards = append(out.Boards, &pb.Board{Title: req.Title, Id: id, Policy: f.Boards[key{req.Title, id}]})
	}
	return out, nil
}

func (f *Fake) FriendRanks(context.Context, *pb.FriendRanksRequest, ...grpc.CallOption) (*pb.RangeResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *Fake) BoardInfo(context.Context, *pb.BoardInfoRequest, ...grpc.CallOption) (*pb.BoardInfoResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *Fake) SetData(context.Context, *pb.SetDataRequest, ...grpc.CallOption) (*pb.SetDataResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *Fake) GetData(context.Context, *pb.GetDataRequest, ...grpc.CallOption) (*pb.GetDataResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *Fake) Delete(context.Context, *pb.DeleteRequest, ...grpc.CallOption) (*pb.DeleteResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *Fake) ResetBoard(context.Context, *pb.ResetBoardRequest, ...grpc.CallOption) (*pb.ResetBoardResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// IDs is a fake PID <-> account map. Accounts are made up as UUIDs from the PID.
type IDs struct {
	mu   sync.Mutex
	byP  map[uint64]string
	byA  map[string]uint64
	Auto bool // link every PID asked about
}

func NewIDs() *IDs { return &IDs{byP: map[uint64]string{}, byA: map[string]uint64{}} }

// Link gives a PID an account and returns it.
func (m *IDs) Link(pid uint64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.byP[pid]; ok {
		return a
	}
	a := fmt.Sprintf("00000000-0000-4000-8000-%012x", pid)
	m.byP[pid], m.byA[a] = a, pid
	return a
}

func (m *IDs) Account(_ context.Context, pid uint64) (string, error) {
	m.mu.Lock()
	a, ok := m.byP[pid]
	m.mu.Unlock()
	if ok {
		return a, nil
	}
	if m.Auto {
		return m.Link(pid), nil
	}
	return "", lb.ErrNoLink
}

func (m *IDs) PID(_ context.Context, a string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.byA[a]; ok {
		return p, nil
	}
	return 0, lb.ErrNoLink
}
