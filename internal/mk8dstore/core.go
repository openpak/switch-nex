package mk8dstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/openpak/switch-nex/internal/lb"
	pb "github.com/openpak/switch-nex/internal/leaderboardspb"
	"github.com/openpak/switch-nex/protocols"
)

// Scores keeps every Mario Kart 8 Deluxe score in the core leaderboards service (card #63,
// adapter-guide.md): the time-trial courses are boards "0".."N" (ascending times, ms) and a
// tournament season is board "t<c>s<s>" (descending points, the last upload wins). This package keeps
// the wire (RankingRankData, competition infos); the core keeps the data, so a restart, or a second
// copy of this server, loses nothing.
//
// Entry layout: score = the time or points; columns "uid" (the NEX unique id) and "param"
// (RankingScoreData param) or "team" and "team_score"; info = the group bytes (time trials) or
// the competition metadata. An entry's account is the PID's account (account core link, namespace
// "switch"); an upload from a PID with no link is acknowledged and not stored.
type Scores struct {
	C *lb.Client
	T lb.Title

	mu   sync.Mutex
	agg  map[string]string // board -> aggregation last declared by this process
	part map[string]partial
}

type partial struct {
	n   int
	exp time.Time
}

// MK8DTitle is the core's record of the game: neutral key and Switch application id.
var MK8DTitle = lb.Title{Key: "mario-kart-8-deluxe", Native: "0100152000022000", Name: "Mario Kart 8 Deluxe"}

// maxScan bounds a read that has to look at a whole board (group filter, unique-id lists).
const maxScan = 10000

func courseBoard(c uint32) string { return strconv.FormatUint(uint64(c), 10) }

func (s *Scores) coursePolicy(c uint32, overwrite bool) *pb.Policy {
	agg := "best"
	if overwrite {
		agg = "latest"
	}
	return &pb.Policy{Name: "Course " + courseBoard(c), Kind: "time", Order: "asc", Aggregation: agg, Unit: "ms"}
}

func tournamentPolicy(c, season uint32) *pb.Policy {
	return &pb.Policy{Name: "Tournament " + strconv.FormatUint(uint64(c), 10), Kind: "score", Order: "desc", Aggregation: "latest",
		Season: strconv.FormatUint(uint64(season), 10)}
}

// Register maps the title and is called once at start, before any score exists.
func (s *Scores) Register(ctx context.Context) error { return s.C.MapTitle(ctx, s.T) }

func (s *Scores) upload(ctx context.Context, pid uint64, board string, score int64, cols map[string]int64, info []byte, pol *pb.Policy) (*pb.SubmitResponse, error) {
	acct, err := s.C.Account(ctx, pid)
	if errors.Is(err, lb.ErrNoLink) {
		log.Printf("mk8d: score from a PID with no account link skipped (board %s)", board)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.declare(ctx, board, pol); err != nil {
		return nil, err
	}
	return s.C.LB.Submit(ctx, &pb.SubmitRequest{Ref: s.T.Ref(board), Account: acct, Score: score, Columns: cols, Info: info, CreatePolicy: pol})
}

// declare tells the core a board's rules the first time this process writes it, and again when the
// game's update mode for it changes (the aggregation is a property of the board, the game's update
// mode of the upload).
func (s *Scores) declare(ctx context.Context, board string, pol *pb.Policy) error {
	s.mu.Lock()
	if s.agg == nil {
		s.agg = map[string]string{}
	}
	same := s.agg[board] == pol.Aggregation
	s.mu.Unlock()
	if same {
		return nil
	}
	if _, err := s.C.LB.UpsertBoard(ctx, &pb.UpsertBoardRequest{Ref: s.T.Ref(board), Policy: pol, TitleName: s.T.Name}); err != nil {
		return err
	}
	s.mu.Lock()
	s.agg[board] = pol.Aggregation
	s.mu.Unlock()
	return nil
}

// UploadScore stores a time. Update mode 1 always overwrites, any other mode keeps the lowest.
func (s *Scores) UploadScore(ctx context.Context, pid, uniqueID uint64, sc protocols.RankingScore) (bool, error) {
	resp, err := s.upload(ctx, pid, courseBoard(sc.Category), int64(sc.Score),
		map[string]int64{"uid": int64(uniqueID), "param": int64(sc.Param)}, sc.Groups, s.coursePolicy(sc.Category, sc.UpdateMode == 1))
	if err != nil || resp == nil {
		return false, err
	}
	return resp.GetChanged(), nil
}

// CompetitionUpload stores a tournament score; the last upload of a player in a season wins.
func (s *Scores) CompetitionUpload(ctx context.Context, pid uint64, p protocols.CompetitionUpload) error {
	season := max(p.Season, 1)
	_, err := s.upload(ctx, pid, tournamentID(p.ID, season), int64(p.Score),
		map[string]int64{"team": int64(p.TeamID), "team_score": int64(p.TeamScore)}, p.Metadata, tournamentPolicy(p.ID, season))
	return err
}

func tournamentID(c, s uint32) string { return fmt.Sprintf("t%ds%d", c, s) }

type rankedEntry struct {
	e      *pb.Entry
	rn, rk int64
}

// filterRank applies the group filter and recomputes ranks inside it (the game ranks within the
// group it asked for).
func filterRank(es []*pb.Entry, keep func(*pb.Entry) bool) []rankedEntry {
	var out []rankedEntry
	for _, e := range es {
		if !keep(e) {
			continue
		}
		r := rankedEntry{e: e, rn: int64(len(out) + 1)}
		r.rk = r.rn
		if n := len(out); n > 0 && out[n-1].e.Score == e.Score {
			r.rk = out[n-1].rk
		}
		out = append(out, r)
	}
	return out
}

// Ranking answers a board read. Ranks are the board's own (standard: ties share, ordinal: unique).
func (s *Scores) Ranking(ctx context.Context, q protocols.RankQuery) ([]protocols.RankRow, int, error) {
	length := int64(q.Length)
	if length == 0 || length > 255 {
		length = 20
	}
	ref := s.T.Ref(courseBoard(q.Category))
	var rows []rankedEntry
	total := 0
	switch {
	case q.GroupIndex != 0xFF || q.UniqueIDs != nil:
		// The core has no group or unique-id index: read the board (bounded) and filter here.
		all, _, err := s.C.All(ctx, ref, maxScan)
		if err != nil {
			return nil, 0, err
		}
		uids := map[int64]bool{}
		for _, u := range q.UniqueIDs {
			uids[int64(u)] = true
		}
		filtered := filterRank(all, func(e *pb.Entry) bool {
			if q.GroupIndex != 0xFF && !(len(e.Info) > int(q.GroupIndex) && e.Info[q.GroupIndex] == q.GroupNum) {
				return false
			}
			return true
		})
		total = len(filtered)
		sel := filtered[:0:0]
		switch {
		case q.UniqueIDs != nil:
			for _, r := range filtered {
				if uids[r.e.Columns["uid"]] {
					sel = append(sel, r)
				}
			}
			rows = capRows(sel, length)
		case q.PIDs != nil:
			rows = capRows(s.pickPIDs(ctx, filtered, q.PIDs), length)
		case q.Mode == protocols.RankModeSelf, q.Mode == protocols.RankModeAround:
			rows = s.selfWindow(ctx, filtered, q, length)
		default:
			off := min(int64(q.Offset), protocols.RankingMaxOffset)
			length = min(length, protocols.RankingMaxOffset-off)
			if off < int64(len(filtered)) {
				rows = capRows(filtered[off:], length)
			}
		}
	case q.PIDs != nil:
		accts := s.accounts(ctx, q.PIDs)
		resp, err := s.C.LB.ByAccounts(ctx, &pb.ByAccountsRequest{Ref: ref, Accounts: accts})
		if err != nil {
			return nil, 0, err
		}
		total = int(resp.GetTotal())
		for _, e := range resp.GetEntries() {
			rows = append(rows, rankedEntry{e: e, rn: e.Position, rk: e.Rank})
		}
		rows = capRows(rows, length)
	case q.Mode == protocols.RankModeSelf || q.Mode == protocols.RankModeAround:
		acct, err := s.C.Account(ctx, q.Self)
		if err != nil {
			if errors.Is(err, lb.ErrNoLink) {
				return nil, 0, nil
			}
			return nil, 0, err
		}
		me, err := s.C.LB.ByAccounts(ctx, &pb.ByAccountsRequest{Ref: ref, Accounts: []string{acct}})
		if err != nil {
			return nil, 0, err
		}
		total = int(me.GetTotal())
		if len(me.GetEntries()) == 0 {
			return nil, total, nil
		}
		self := me.GetEntries()[0]
		if q.Mode == protocols.RankModeSelf {
			rows = []rankedEntry{{e: self, rn: self.Position, rk: self.Rank}}
			break
		}
		resp, err := s.C.LB.Range(ctx, &pb.RangeRequest{Ref: ref, Start: max(1, self.Position-length/2), Limit: min(length, 100)})
		if err != nil {
			return nil, 0, err
		}
		for _, e := range resp.GetEntries() {
			rows = append(rows, rankedEntry{e: e, rn: e.Position, rk: e.Rank})
		}
	default:
		off := min(int64(q.Offset), protocols.RankingMaxOffset)
		if off+length > protocols.RankingMaxOffset {
			length = protocols.RankingMaxOffset - off
		}
		for got := int64(0); got < length; {
			resp, err := s.C.LB.Range(ctx, &pb.RangeRequest{Ref: ref, Start: off + got + 1, Limit: min(length-got, 100)})
			if err != nil {
				return nil, 0, err
			}
			total = int(resp.GetTotal())
			for _, e := range resp.GetEntries() {
				rows = append(rows, rankedEntry{e: e, rn: e.Position, rk: e.Rank})
			}
			if len(resp.GetEntries()) == 0 || int64(len(resp.GetEntries())) < min(length-got, 100) {
				break
			}
			got += int64(len(resp.GetEntries()))
		}
	}
	return s.render(ctx, q, rows), total, nil
}

func capRows(r []rankedEntry, n int64) []rankedEntry {
	if int64(len(r)) > n {
		return r[:n]
	}
	return r
}

func (s *Scores) accounts(ctx context.Context, pids []uint64) []string {
	var out []string
	for _, p := range pids {
		if a, err := s.C.Account(ctx, p); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func (s *Scores) pickPIDs(ctx context.Context, in []rankedEntry, pids []uint64) []rankedEntry {
	want := map[string]bool{}
	for _, a := range s.accounts(ctx, pids) {
		want[a] = true
	}
	var out []rankedEntry
	for _, r := range in {
		if want[r.e.Account] {
			out = append(out, r)
		}
	}
	return out
}

func (s *Scores) selfWindow(ctx context.Context, in []rankedEntry, q protocols.RankQuery, length int64) []rankedEntry {
	acct, err := s.C.Account(ctx, q.Self)
	if err != nil {
		return nil
	}
	for i, r := range in {
		if r.e.Account != acct {
			continue
		}
		if q.Mode == protocols.RankModeSelf {
			return []rankedEntry{r}
		}
		return capRows(in[max(0, i-int(length/2)):], length)
	}
	return nil
}

// render turns entries into the wire rows; a row whose account has no PID cannot be shown to a console.
func (s *Scores) render(ctx context.Context, q protocols.RankQuery, rows []rankedEntry) []protocols.RankRow {
	out := make([]protocols.RankRow, 0, len(rows))
	for _, r := range rows {
		pid, err := s.C.ID.PID(ctx, r.e.Account)
		if err != nil {
			continue
		}
		order := r.rk
		if q.Calc == 1 {
			order = r.rn
		}
		out = append(out, protocols.RankRow{PID: pid, UniqueID: uint64(r.e.Columns["uid"]), Order: uint32(order), Category: q.Category,
			Score: uint32(r.e.Score), Groups: r.e.Info, Param: uint64(r.e.Columns["param"]), Updated: time.UnixMilli(r.e.UpdatedUnixMs)})
	}
	return out
}

// Season returns the top scores of one tournament season and how many players are on it.
func (s *Scores) Season(ctx context.Context, comp, season uint32, top int) ([]protocols.CompetitionScore, int, error) {
	ref := s.T.Ref(tournamentID(comp, season))
	var out []protocols.CompetitionScore
	total := 0
	for len(out) < top {
		resp, err := s.C.LB.Range(ctx, &pb.RangeRequest{Ref: ref, Start: int64(len(out) + 1), Limit: int64(min(top-len(out), 100))})
		if err != nil {
			return nil, 0, err
		}
		total = int(resp.GetTotal())
		for _, e := range resp.GetEntries() {
			pid, err := s.C.ID.PID(ctx, e.Account)
			if err != nil {
				continue
			}
			out = append(out, protocols.CompetitionScore{Rank: uint32(e.Position), PID: pid, Score: uint32(e.Score), Updated: time.UnixMilli(e.UpdatedUnixMs),
				TeamID: uint8(e.Columns["team"]), Metadata: e.Info})
		}
		if len(resp.GetEntries()) < 100 {
			break
		}
	}
	for i := range out {
		out[i].Rank = uint32(i + 1)
	}
	return out, total, nil
}

// Participants is the number of distinct players across seasons 1..cur of a tournament. The count
// is derived from the core and cached for half a minute (the game asks for the whole list at once).
func (s *Scores) Participants(ctx context.Context, comp, cur uint32) (int, error) {
	k := tournamentID(comp, cur)
	s.mu.Lock()
	if p, ok := s.part[k]; ok && time.Now().Before(p.exp) {
		s.mu.Unlock()
		return p.n, nil
	}
	s.mu.Unlock()
	seen := map[string]bool{}
	for season := uint32(1); season <= cur; season++ {
		es, _, err := s.C.All(ctx, s.T.Ref(tournamentID(comp, season)), 2000)
		if err != nil {
			return 0, err
		}
		for _, e := range es {
			seen[e.Account] = true
		}
	}
	s.mu.Lock()
	if s.part == nil {
		s.part = map[string]partial{}
	}
	s.part[k] = partial{len(seen), time.Now().Add(30 * time.Second)}
	s.mu.Unlock()
	return len(seen), nil
}

// CourseCount is one course with the number of ranked players.
type CourseCount struct {
	Course int64
	Ranked int
}

// Courses lists every time-trial board that holds a score.
func (s *Scores) Courses(ctx context.Context) ([]CourseCount, error) {
	bs, err := s.C.LB.ListBoards(ctx, &pb.ListBoardsRequest{Network: lb.Network, Title: s.T.Native})
	if err != nil {
		return nil, err
	}
	var out []CourseCount
	for _, b := range bs.GetBoards() {
		n, err := strconv.ParseInt(b.GetId(), 10, 64)
		if err != nil {
			continue // a tournament board
		}
		t, err := s.C.LB.Total(ctx, &pb.TotalRequest{Ref: s.T.Ref(b.GetId())})
		if err != nil {
			return nil, err
		}
		if t.GetTotal() > 0 {
			out = append(out, CourseCount{n, int(t.GetTotal())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Course < out[j].Course })
	return out, nil
}

// CoursePage is a window of one course's board.
type CoursePage struct {
	Rank    int
	PID     uint64
	Score   int64
	Updated time.Time
}

func (s *Scores) CoursePage(ctx context.Context, course int64, offset, limit int) ([]CoursePage, int, error) {
	resp, err := s.C.LB.Range(ctx, &pb.RangeRequest{Ref: s.T.Ref(strconv.FormatInt(course, 10)), Start: int64(offset + 1), Limit: int64(min(limit, 100))})
	if err != nil {
		return nil, 0, err
	}
	var out []CoursePage
	for _, e := range resp.GetEntries() {
		pid, err := s.C.ID.PID(ctx, e.Account)
		if err != nil {
			continue
		}
		out = append(out, CoursePage{int(e.Position), pid, e.Score, time.UnixMilli(e.UpdatedUnixMs)})
	}
	return out, int(resp.GetTotal()), nil
}

// PlayerRank is a player's place on a course.
type PlayerRank struct {
	Course int64
	Score  int64
	Rank   int64
	Of     int64
}

// Player lists the player's place on every course.
func (s *Scores) Player(ctx context.Context, pid uint64) ([]PlayerRank, error) {
	acct, err := s.C.Account(ctx, pid)
	if errors.Is(err, lb.ErrNoLink) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cs, err := s.Courses(ctx)
	if err != nil {
		return nil, err
	}
	var out []PlayerRank
	for _, c := range cs {
		r, err := s.C.LB.ByAccounts(ctx, &pb.ByAccountsRequest{Ref: s.T.Ref(strconv.FormatInt(c.Course, 10)), Accounts: []string{acct}})
		if err != nil {
			return nil, err
		}
		if len(r.GetEntries()) > 0 {
			e := r.GetEntries()[0]
			out = append(out, PlayerRank{c.Course, e.Score, e.Rank, r.GetTotal()})
		}
	}
	return out, nil
}

// PlayerScore is one player's time on one course.
func (s *Scores) PlayerScore(ctx context.Context, course int64, pid uint64) (int64, bool) {
	acct, err := s.C.Account(ctx, pid)
	if err != nil {
		return 0, false
	}
	r, err := s.C.LB.ByAccounts(ctx, &pb.ByAccountsRequest{Ref: s.T.Ref(strconv.FormatInt(course, 10)), Accounts: []string{acct}})
	if err != nil || len(r.GetEntries()) == 0 {
		return 0, false
	}
	return r.GetEntries()[0].Score, true
}

// has reports whether the PID's account already has an entry on a board. A PID with no link has none.
func (s *Scores) has(ctx context.Context, pid uint64, board string) (bool, error) {
	acct, err := s.C.Account(ctx, pid)
	if errors.Is(err, lb.ErrNoLink) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	r, err := s.C.LB.ByAccounts(ctx, &pb.ByAccountsRequest{Ref: s.T.Ref(board), Accounts: []string{acct}})
	return len(r.GetEntries()) > 0, err
}
