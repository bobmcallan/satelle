package hosted

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

type stubChain struct {
	syncpb.UnimplementedChainServer
	append func(*syncpb.AppendRequest) (*syncpb.AppendResponse, error)
	read   func(*syncpb.ReadRequest) (*syncpb.ReadResponse, error)
}

func (s *stubChain) Read(_ context.Context, req *syncpb.ReadRequest) (*syncpb.ReadResponse, error) {
	if s.read == nil {
		return nil, status.Error(codes.Unimplemented, "no read")
	}
	return s.read(req)
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

func staleBaseStub(t *testing.T, sb *syncpb.StaleBase) *stubChain {
	t.Helper()
	return &stubChain{append: func(*syncpb.AppendRequest) (*syncpb.AppendResponse, error) {
		st, err := status.New(codes.FailedPrecondition, "stale base").WithDetails(sb)
		if err != nil {
			t.Fatal(err)
		}
		return nil, st.Err()
	}}
}

// sty_e292e7f5 AC2: a detail carrying missing_count reports it, even when the
// bounded detail lists fewer events.
func TestAppendStoryStaleBaseUsesMissingCount(t *testing.T) {
	var listed []*syncpb.Event
	for i := 0; i < 16; i++ {
		listed = append(listed, &syncpb.Event{Id: fmt.Sprintf("evt_%d", i+1)})
	}
	cc := openStubChain(t, staleBaseStub(t, &syncpb.StaleBase{LatestId: "evt_40", Missing: listed, MissingCount: 40}))
	_, err := cc.AppendStory(context.Background(), "probe", "sty_a", "", oneEvent)
	var stale *StaleBaseError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *StaleBaseError", err)
	}
	if stale.Missing != 40 || !strings.Contains(stale.Error(), "40 event(s) not seen") {
		t.Fatalf("stale = %+v, err = %v, want Missing 40", stale, err)
	}
}

// sty_e292e7f5 AC3: an older server sends no missing_count; the list length stands.
func TestAppendStoryStaleBaseWithoutMissingCount(t *testing.T) {
	cc := openStubChain(t, staleBaseStub(t, &syncpb.StaleBase{
		LatestId: "evt_3", Missing: []*syncpb.Event{{Id: "evt_1"}, {Id: "evt_2"}, {Id: "evt_3"}},
	}))
	_, err := cc.AppendStory(context.Background(), "probe", "sty_a", "", oneEvent)
	var stale *StaleBaseError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *StaleBaseError", err)
	}
	if stale.Missing != 3 {
		t.Fatalf("stale = %+v, want Missing 3", stale)
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

// sty_9bdf08c4 AC11: the frame and per-event sizes add up to the encoded request,
// which is what lets the CLI keep every Append under its cap.
func TestAppendSizesAddUpToTheRequest(t *testing.T) {
	events := []ChainEvent{
		{ID: "evt_1", BaseID: "", WorkflowVersion: "wf", Payload: []byte(`{"a":1}`)},
		{ID: "evt_2", BaseID: "evt_1", Payload: make([]byte, 300)},
	}
	req := &syncpb.AppendRequest{Project: "probe", StoryId: "sty_a", BaseId: "evt_0"}
	size := AppendFrameSize("probe", "sty_a", "evt_0")
	for _, e := range events {
		req.Events = append(req.Events, newEvent("sty_a", e))
		size += AppendEventSize("sty_a", e)
	}
	if want := proto.Size(req); size != want {
		t.Fatalf("sizes add up to %d, the request encodes to %d", size, want)
	}
}

// sty_9bdf08c4 AC11: a reply well past grpc-go's 4 MiB default is read — an accepted
// Append's echo and a StaleBase detail both.
func TestAppendStoryReadsRepliesPastTheGRPCDefault(t *testing.T) {
	big := &syncpb.Event{Id: "evt_big", StoryId: "sty_a", Payload: make([]byte, 5<<20)}
	cc := openStubChain(t, &stubChain{append: func(req *syncpb.AppendRequest) (*syncpb.AppendResponse, error) {
		if req.GetBaseId() == "evt_1" {
			st, err := status.New(codes.FailedPrecondition, "stale base").WithDetails(&syncpb.StaleBase{LatestId: "evt_big", Missing: []*syncpb.Event{big}})
			if err != nil {
				t.Fatal(err)
			}
			return nil, st.Err()
		}
		return &syncpb.AppendResponse{LatestId: "evt_big", Appended: []*syncpb.Event{big}}, nil
	}})
	if latest, err := cc.AppendStory(context.Background(), "probe", "sty_a", "", oneEvent); err != nil || latest != "evt_big" {
		t.Fatalf("an accepted Append with a 5 MiB echo: latest = %q, err = %v", latest, err)
	}
	_, err := cc.AppendStory(context.Background(), "probe", "sty_a", "evt_1", oneEvent)
	var stale *StaleBaseError
	if !errors.As(err, &stale) || stale.LatestID != "evt_big" || stale.Missing != 1 {
		t.Fatalf("a StaleBase with a 5 MiB detail: err = %v", err)
	}
}
