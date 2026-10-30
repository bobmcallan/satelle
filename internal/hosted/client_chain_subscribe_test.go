package hosted

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

// subscribeChain is a stubChain whose Subscribe answers each call in turn.
type subscribeChain struct {
	stubChain
	calls     []*syncpb.SubscribeRequest
	subscribe func(call int, req *syncpb.SubscribeRequest, stream grpc.ServerStreamingServer[syncpb.SubscribeResponse]) error
}

func (s *subscribeChain) Subscribe(req *syncpb.SubscribeRequest, stream grpc.ServerStreamingServer[syncpb.SubscribeResponse]) error {
	s.calls = append(s.calls, req)
	return s.subscribe(len(s.calls), req, stream)
}

func openSubscribeChain(t *testing.T, stub *subscribeChain) *ChainConn {
	t.Helper()
	return openStubChainServer(t, stub)
}

type delivered struct{ story, id string }

// sty_0dc600e1 AC3: Subscribe carries the project and the resume map, and each
// streamed event reaches on with its story.
func TestSubscribeStoriesDeliversEachEvent(t *testing.T) {
	stub := &subscribeChain{subscribe: func(_ int, req *syncpb.SubscribeRequest, stream grpc.ServerStreamingServer[syncpb.SubscribeResponse]) error {
		return stream.Send(&syncpb.SubscribeResponse{Event: &syncpb.Event{Id: "evt_2", StoryId: "sty_a", BaseId: "evt_1", Payload: []byte(`{"id":"evt_2"}`)}})
	}}
	cc := openSubscribeChain(t, stub)
	var got []delivered
	err := cc.SubscribeStories(context.Background(), "probe", map[string]string{"sty_a": "evt_1"}, func(story string, ev ChainEvent) error {
		if ev.BaseID != "evt_1" || string(ev.Payload) != `{"id":"evt_2"}` {
			t.Errorf("event = %+v", ev)
		}
		got = append(got, delivered{story, ev.ID})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (delivered{"sty_a", "evt_2"}) {
		t.Errorf("delivered %v, want evt_2 of sty_a", got)
	}
	if req := stub.calls[0]; req.GetProject() != "probe" || !maps.Equal(req.GetResume(), map[string]string{"sty_a": "evt_1"}) {
		t.Errorf("request = %+v", req)
	}
}

// sty_0dc600e1 AC3: a stream cut transiently is reopened from the last event
// handled, so nothing is delivered twice.
func TestSubscribeStoriesResumesAfterATransientCut(t *testing.T) {
	defer SetChainRetryDelays([]time.Duration{time.Millisecond})()
	stub := &subscribeChain{subscribe: func(call int, _ *syncpb.SubscribeRequest, stream grpc.ServerStreamingServer[syncpb.SubscribeResponse]) error {
		if call == 1 {
			_ = stream.Send(&syncpb.SubscribeResponse{Event: &syncpb.Event{Id: "evt_1", StoryId: "sty_a"}})
			return status.Error(codes.Unavailable, "proxy went away")
		}
		return stream.Send(&syncpb.SubscribeResponse{Event: &syncpb.Event{Id: "evt_2", StoryId: "sty_a", BaseId: "evt_1"}})
	}}
	cc := openSubscribeChain(t, stub)
	var got []string
	if err := cc.SubscribeStories(context.Background(), "probe", nil, func(_ string, ev ChainEvent) error {
		got = append(got, ev.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "evt_1" || got[1] != "evt_2" {
		t.Errorf("delivered %v, want evt_1 then evt_2 once each", got)
	}
	if len(stub.calls) != 2 || stub.calls[1].GetResume()["sty_a"] != "evt_1" {
		t.Errorf("the reopened stream must resume after evt_1: %+v", stub.calls)
	}
}

// sty_0dc600e1 AC3: a server without Subscribe is ErrChainUnsupported, and an error
// from on ends the subscription as it came.
func TestSubscribeStoriesUnsupportedAndConsumerErrors(t *testing.T) {
	bare := openSubscribeChain(t, &subscribeChain{subscribe: func(int, *syncpb.SubscribeRequest, grpc.ServerStreamingServer[syncpb.SubscribeResponse]) error {
		return status.Error(codes.Unimplemented, "method Subscribe not implemented")
	}})
	if err := bare.SubscribeStories(context.Background(), "probe", nil, func(string, ChainEvent) error { return nil }); !errors.Is(err, ErrChainUnsupported) {
		t.Errorf("err = %v, want ErrChainUnsupported", err)
	}

	stop := errors.New("the consumer stopped")
	cc := openSubscribeChain(t, &subscribeChain{subscribe: func(_ int, _ *syncpb.SubscribeRequest, stream grpc.ServerStreamingServer[syncpb.SubscribeResponse]) error {
		return stream.Send(&syncpb.SubscribeResponse{Event: &syncpb.Event{Id: "evt_1", StoryId: "sty_a"}})
	}})
	if err := cc.SubscribeStories(context.Background(), "probe", nil, func(string, ChainEvent) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("err = %v, want the consumer's error", err)
	}
}
