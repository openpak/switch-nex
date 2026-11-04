package lb

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
)

type stubLinks struct {
	accountv1.LinksClient
	calls int
}

func (s *stubLinks) GetLinkBySubject(_ context.Context, r *accountv1.GetLinkBySubjectRequest, _ ...grpc.CallOption) (*accountv1.GetLinkBySubjectResponse, error) {
	s.calls++
	if r.Namespace != "switch" {
		return nil, status.Error(codes.InvalidArgument, "ns")
	}
	if r.SubjectId == "00000000000000ff" {
		return &accountv1.GetLinkBySubjectResponse{AccountId: "AAAA", State: accountv1.LinkState_LINK_STATE_ACTIVE}, nil
	}
	return nil, status.Error(codes.NotFound, "none")
}

func (s *stubLinks) GetActiveLinkByAccount(_ context.Context, r *accountv1.GetActiveLinkByAccountRequest, _ ...grpc.CallOption) (*accountv1.GetLinkBySubjectResponse, error) {
	if r.AccountId == "aaaa" {
		return &accountv1.GetLinkBySubjectResponse{SubjectId: "00000000000000ff"}, nil
	}
	return nil, status.Error(codes.NotFound, "none")
}

func TestLinksResolveBothWays(t *testing.T) {
	s := &stubLinks{}
	l := &links{c: s, m: map[string]cached{}}
	ctx := context.Background()
	a, err := l.Account(ctx, 0xff)
	if err != nil || a != "aaaa" {
		t.Fatalf("account %q %v", a, err)
	}
	if _, err := l.Account(ctx, 0xff); err != nil || s.calls != 1 {
		t.Fatalf("second lookup should be cached: calls %d %v", s.calls, err)
	}
	if _, err := l.Account(ctx, 7); !errors.Is(err, ErrNoLink) {
		t.Fatalf("unlinked: %v", err)
	}
	if _, err := l.Account(ctx, 7); !errors.Is(err, ErrNoLink) || s.calls != 3 {
		t.Fatalf("a missing link must not be cached: calls %d", s.calls)
	}
	if p, err := l.PID(ctx, "aaaa"); err != nil || p != 0xff {
		t.Fatalf("pid %d %v", p, err)
	}
	if _, err := l.PID(ctx, "bbbb"); !errors.Is(err, ErrNoLink) {
		t.Fatalf("pid unlinked: %v", err)
	}
}
