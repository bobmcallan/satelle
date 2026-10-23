package hosted

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

type stubChain struct {
	syncpb.UnimplementedChainServer
	append func(*syncpb.AppendRequest) (*syncpb.AppendResponse, error)
}

func (s *stubChain) Append(_ context.Context, req *syncpb.AppendRequest) (*syncpb.AppendResponse, error) {
	return s.append(req)
}

func openStubChain(t *testing.T, stub *stubChain) *ChainConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	syncpb.RegisterChainServer(gs, stub)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() {
		gs.Stop()
		_ = lis.Close()
	})
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	c := NewClient("http://hosted.example", store, fatalHTTP(t))
	c.dialGRPC = func(ctx context.Context, target string) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///buf",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
	cc, err := c.OpenChain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

var oneEvent = []ChainEvent{{ID: "evt_2", BaseID: "evt_1", WorkflowVersion: "wf-hash", Payload: []byte(`{"id":"evt_2"}`)}}

func TestAppendStoryCarriesTheBaseAndTheEvents(t *testing.T) {
	var seen *syncpb.AppendRequest
	cc := openStubChain(t, &stubChain{append: func(req *syncpb.AppendRequest) (*syncpb.AppendResponse, error) {
		seen = req
		return &syncpb.AppendResponse{LatestId: "evt_2"}, nil
	}})
	latest, err := cc.AppendStory(context.Background(), "probe", "sty_a", "evt_1", oneEvent)
	if err != nil || latest != "evt_2" {
		t.Fatalf("latest = %q, err = %v", latest, err)
	}
	ev := seen.GetEvents()[0]
	if seen.GetProject() != "probe" || seen.GetStoryId() != "sty_a" || seen.GetBaseId() != "evt_1" ||
		ev.GetId() != "evt_2" || ev.GetStoryId() != "sty_a" || ev.GetBaseId() != "evt_1" ||
		ev.GetWorkflowVersion() != "wf-hash" || string(ev.GetPayload()) != `{"id":"evt_2"}` {
		t.Fatalf("request = %+v", seen)
	}
}

// sty_9bdf08c4 AC3: a FailedPrecondition carrying StaleBase is the stale-base
// refusal, with the server's latest event and how many the client has not seen.
func TestAppendStoryMapsStaleBase(t *testing.T) {
	cc := openStubChain(t, &stubChain{append: func(*syncpb.AppendRequest) (*syncpb.AppendResponse, error) {
		st, err := status.New(codes.FailedPrecondition, "stale base").WithDetails(&syncpb.StaleBase{
			LatestId: "evt_9", Missing: []*syncpb.Event{{Id: "evt_8"}, {Id: "evt_9"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return nil, st.Err()
	}})
	_, err := cc.AppendStory(context.Background(), "probe", "sty_a", "", oneEvent)
	var stale *StaleBaseError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *StaleBaseError", err)
	}
	if stale.StoryID != "sty_a" || stale.Base != "" || stale.LatestID != "evt_9" || stale.Missing != 2 || stale.Since() != "its start" {
		t.Fatalf("stale = %+v", stale)
	}
}

// sty_9bdf08c4 AC8: every other refusal goes through the shared gRPC error mapping
// and is not mistaken for a stale base — a FailedPrecondition without the detail
// included.
func TestAppendStoryMapsOtherRefusals(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		msg  string
	}{
		{codes.PermissionDenied, "viewers may not append"},
		{codes.InvalidArgument, "workflow_version is empty"},
		{codes.FailedPrecondition, "story is archived"},
	} {
		cc := openStubChain(t, &stubChain{append: func(*syncpb.AppendRequest) (*syncpb.AppendResponse, error) {
			return nil, status.Error(tc.code, tc.msg)
		}})
		_, err := cc.AppendStory(context.Background(), "probe", "sty_a", "evt_1", oneEvent)
		var stale *StaleBaseError
		if err == nil || errors.As(err, &stale) {
			t.Fatalf("%v: err = %v, want a plain refusal", tc.code, err)
		}
		if !strings.Contains(err.Error(), "grpc Append: "+tc.msg) {
			t.Errorf("%v: err = %v, want the mapped message", tc.code, err)
		}
	}
}
