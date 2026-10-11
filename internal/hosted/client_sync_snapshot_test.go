package hosted

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

// pagedSnapshotStub serves n pages of one item and one ledger row each, with
// page tokens "p2".."pn" and sync_token "sync-1" on the last page.
func pagedSnapshotStub(n int) *stubSync {
	return &stubSync{snap: func(req *syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error) {
		page := 1
		if tok := req.GetPageToken(); tok != "" {
			_, _ = fmt.Sscanf(tok, "p%d", &page)
		}
		resp := &syncpb.SnapshotResponse{
			Items:  []*syncpb.WorkItem{{Id: fmt.Sprintf("sty_%d", page), Kind: "story", Title: "T"}},
			Ledger: []*syncpb.LedgerEntry{{Id: fmt.Sprintf("led_%d", page), Kind: "note"}},
		}
		if page < n {
			resp.NextPageToken = fmt.Sprintf("p%d", page+1)
		} else {
			resp.SyncToken = "sync-1"
		}
		return resp, nil
	}}
}

func snapshotTestClient(t *testing.T, stub *stubSync) *Client {
	t.Helper()
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	return newTestGRPCClient(t, stub, store, fatalHTTP(t))
}

// recordSnapshotRequests wraps the stub's handler to keep a copy of every request.
func recordSnapshotRequests(stub *stubSync) *[]*syncpb.SnapshotRequest {
	reqs := &[]*syncpb.SnapshotRequest{}
	inner := stub.snap
	stub.snap = func(req *syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error) {
		*reqs = append(*reqs, proto.Clone(req).(*syncpb.SnapshotRequest))
		return inner(req)
	}
	return reqs
}

// AC1: every request carries a page size, successive requests carry the
// previous page's token, and every row of every page reaches the caller once.
func TestSnapshotFollowsPages(t *testing.T) {
	stub := pagedSnapshotStub(3)
	reqs := recordSnapshotRequests(stub)
	c := snapshotTestClient(t, stub)

	var indexes []int
	var items []WorkstateItem
	var rows []WorkstateLedgerRow
	res, err := c.Snapshot(context.Background(), "probe", "", SnapshotOptions{SinceToken: "since-0"}, func(p SnapshotPage) error {
		indexes = append(indexes, p.Index)
		items = append(items, p.Items...)
		rows = append(rows, p.Ledger...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 3 || res.SyncToken != "sync-1" {
		t.Fatalf("result = %+v, want 3 pages and the last page's sync_token", res)
	}
	if len(*reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(*reqs))
	}
	for i, wantTok := range []string{"", "p2", "p3"} {
		r := (*reqs)[i]
		if r.GetPageSize() <= 0 {
			t.Errorf("request %d page_size = %d, want > 0", i, r.GetPageSize())
		}
		if r.GetPageToken() != wantTok {
			t.Errorf("request %d page_token = %q, want %q", i, r.GetPageToken(), wantTok)
		}
		if r.GetSinceToken() != "since-0" {
			t.Errorf("request %d since_token = %q, want since-0", i, r.GetSinceToken())
		}
	}
	if len(items) != 3 || items[0].ID != "sty_1" || items[1].ID != "sty_2" || items[2].ID != "sty_3" {
		t.Fatalf("items = %+v", items)
	}
	if len(rows) != 3 || rows[0].ID != "led_1" || rows[1].ID != "led_2" || rows[2].ID != "led_3" {
		t.Fatalf("ledger = %+v", rows)
	}
	if fmt.Sprint(indexes) != "[1 2 3]" {
		t.Fatalf("page indexes = %v", indexes)
	}
}

func TestSnapshotHonoursExplicitPageSizeAndResume(t *testing.T) {
	stub := pagedSnapshotStub(3)
	reqs := recordSnapshotRequests(stub)
	c := snapshotTestClient(t, stub)
	items, _, res, err := snapshotAll(c, "probe", "", SnapshotOptions{PageSize: 7, PageToken: "p2"})
	if err != nil {
		t.Fatal(err)
	}
	if first := (*reqs)[0]; first.GetPageSize() != 7 || first.GetPageToken() != "p2" {
		t.Fatalf("first request = %+v", first)
	}
	if res.Pages != 2 || len(items) != 2 || items[0].ID != "sty_2" {
		t.Fatalf("resumed pass = %+v items=%+v", res, items)
	}
}

func TestSnapshotOnPageErrorStopsThePass(t *testing.T) {
	stub := pagedSnapshotStub(4)
	c := snapshotTestClient(t, stub)
	boom := errors.New("apply failed")
	res, err := c.Snapshot(context.Background(), "probe", "", SnapshotOptions{}, func(p SnapshotPage) error {
		if p.Index == 2 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the callback's error unchanged", err)
	}
	if res.Pages != 2 || stub.snaps != 2 {
		t.Fatalf("pages = %d, requests = %d, want the pass to stop at page 2", res.Pages, stub.snaps)
	}
}

// AC5: a refused token is told apart from a failure so the caller can fall back.
func TestSnapshotRejectedTokenIsDistinguished(t *testing.T) {
	for _, code := range []codes.Code{codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange} {
		stub := &stubSync{fail: []error{status.Error(code, "token expired")}}
		_, _, _, err := snapshotAll(snapshotTestClient(t, stub), "probe", "", SnapshotOptions{SinceToken: "old"})
		if !errors.Is(err, ErrSinceTokenRejected) || !strings.Contains(err.Error(), "token expired") {
			t.Errorf("%v with a since_token: err = %v, want ErrSinceTokenRejected naming the server's message", code, err)
		}
		stub = &stubSync{fail: []error{status.Error(code, "bad request")}}
		_, _, _, err = snapshotAll(snapshotTestClient(t, stub), "probe", "", SnapshotOptions{})
		if err == nil || errors.Is(err, ErrSinceTokenRejected) {
			t.Errorf("%v with no token: err = %v, want a plain failure", code, err)
		}
	}
}

func TestSnapshotRefusesAPassThatNeverEnds(t *testing.T) {
	stub := &stubSync{snap: func(*syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error) {
		return &syncpb.SnapshotResponse{NextPageToken: "same"}, nil
	}}
	_, _, _, err := snapshotAll(snapshotTestClient(t, stub), "probe", "", SnapshotOptions{})
	if err == nil || !strings.Contains(err.Error(), "does not end") {
		t.Fatalf("err = %v, want a repeated-token error", err)
	}
	if stub.snaps > 3 {
		t.Fatalf("requests = %d, the loop guard should stop at the repeat", stub.snaps)
	}
}
