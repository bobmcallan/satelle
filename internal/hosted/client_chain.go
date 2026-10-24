package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

// ChainEvent is one story event as Chain/Append carries it. WorkflowVersion is the
// event's workflow_hash and Payload the ledger entry's JSON, which the server
// stores as it came.
type ChainEvent struct {
	ID              string
	BaseID          string
	WorkflowVersion string
	Payload         json.RawMessage
}

// MaxChainReplyBytes is the largest reply the client reads from Chain/Append: an
// accepted Append echoes every stored event and a StaleBase detail echoes every
// event the caller has not seen. It is set on each call, which takes precedence
// over a dial default, and sits well above grpc-go's 4 MiB default and the 3 MiB
// the CLI sends per call.
const MaxChainReplyBytes = 16 << 20

func chainCallOpts() []grpc.CallOption {
	return []grpc.CallOption{grpc.MaxCallRecvMsgSize(MaxChainReplyBytes)}
}

// AppendFrameSize is the encoded size of an Append request that carries no events.
func AppendFrameSize(project, storyID, base string) int {
	return proto.Size(&syncpb.AppendRequest{Project: project, StoryId: storyID, BaseId: base})
}

// AppendEventSize is the encoded size one event adds to an Append request.
func AppendEventSize(storyID string, e ChainEvent) int {
	return proto.Size(&syncpb.AppendRequest{Events: []*syncpb.Event{newEvent(storyID, e)}})
}

func newEvent(storyID string, e ChainEvent) *syncpb.Event {
	return &syncpb.Event{
		Id: e.ID, StoryId: storyID, BaseId: e.BaseID,
		WorkflowVersion: e.WorkflowVersion, Payload: e.Payload,
	}
}

// StaleBaseError is the refusal of an Append whose base is no longer the story's
// latest event: nothing was stored. LatestID is the server's latest event and
// Missing counts the events after Base that this machine has not seen. The
// caller reads the story again and decides; nothing here merges or retries.
type StaleBaseError struct {
	StoryID  string
	Base     string
	LatestID string
	Missing  int
}

func (e *StaleBaseError) Error() string {
	return fmt.Sprintf("hosted: story %s moved since %s: server is at %s (%d event(s) not seen)",
		e.StoryID, e.Since(), e.LatestID, e.Missing)
}

// Since names the base the Append was made from, "its start" when it was empty.
func (e *StaleBaseError) Since() string {
	if e.Base == "" {
		return "its start"
	}
	return e.Base
}

// ChainConn is one connection to the server's Chain service, shared by the
// Appends of a push.
type ChainConn struct {
	c    *Client
	conn *grpc.ClientConn
	cli  syncpb.ChainClient
}

// OpenChain dials the server's Chain service. The caller closes it. A checkout
// with no credential for the server is not connected to it: that is
// ErrLoginRequired before anything is dialled.
func (c *Client) OpenChain(ctx context.Context) (*ChainConn, error) {
	if _, err := c.store.Load(c.server); err != nil {
		if errors.Is(err, ErrNoCredential) {
			return nil, ErrLoginRequired
		}
		return nil, err
	}
	conn, err := c.openGRPC(ctx)
	if err != nil {
		return nil, err
	}
	return &ChainConn{c: c, conn: conn, cli: syncpb.NewChainClient(conn)}, nil
}

// Close releases the connection.
func (cc *ChainConn) Close() error { return cc.conn.Close() }

// AppendStory stores events after base on the story's chain and returns the new
// latest event id. base is the latest event this machine saw for the story,
// empty when it saw none. A stale base returns *StaleBaseError; any other
// refusal is mapped like every other hosted gRPC error.
func (cc *ChainConn) AppendStory(ctx context.Context, project, storyID, base string, events []ChainEvent) (string, error) {
	req := &syncpb.AppendRequest{Project: project, StoryId: storyID, BaseId: base}
	for _, e := range events {
		req.Events = append(req.Events, newEvent(storyID, e))
	}
	var out *syncpb.AppendResponse
	err := cc.c.withGRPCAuth(ctx, func(ctx context.Context) error {
		return retryTransient(ctx, func() (rpcErr error) {
			out, rpcErr = cc.cli.Append(ctx, req, chainCallOpts()...)
			return rpcErr
		})
	})
	if err != nil {
		if sb := staleBaseFromStatus(err); sb != nil {
			return "", &StaleBaseError{StoryID: storyID, Base: base, LatestID: sb.GetLatestId(), Missing: len(sb.GetMissing())}
		}
		if bareStaleBase(err) {
			// The refusal's detail echoes every event the caller has not seen and
			// can be too large for the HTTP/2 trailers, so it arrives without it. The
			// story's latest event is read instead and decides the same way.
			// A read that fails too leaves the refusal as it came.
			if stale, rerr := cc.staleBaseByRead(ctx, project, storyID, base); rerr == nil && stale != nil {
				return "", stale
			}
		}
		return "", cc.c.mapSyncErr("Append", err)
	}
	return out.GetLatestId(), nil
}

// chainRetryDelays are the waits before each retry of a call that failed
// transiently: three retries, then the failure is reported. A var so tests run
// without waiting.
var chainRetryDelays = []time.Duration{500 * time.Millisecond, 2 * time.Second, 5 * time.Second}

// SetChainRetryDelays replaces the waits between retries and returns a func that
// restores them (tests only).
func SetChainRetryDelays(d []time.Duration) (restore func()) {
	prev := chainRetryDelays
	chainRetryDelays = d
	return func() { chainRetryDelays = prev }
}

// transientChainErr reports a failure of the transport rather than a decision of
// the server: the server or a proxy in front of it is unavailable (including an
// HTTP 502, which grpc-go reports as Unavailable), or reset the stream with
// ENHANCE_YOUR_CALM, which grpc-go reports as ResourceExhausted.
func transientChainErr(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable:
		return true
	case codes.ResourceExhausted:
		return strings.Contains(st.Message(), "ENHANCE_YOUR_CALM")
	}
	return false
}

// retryTransient runs call, repeating it after each chainRetryDelays wait while it
// fails transiently. An Append that did apply before its reply was lost is safe to
// repeat: the repeat is refused as a stale base whose latest event is one this
// checkout holds, which the caller resumes from.
func retryTransient(ctx context.Context, call func() error) error {
	err := call()
	for _, d := range chainRetryDelays {
		if err == nil || !transientChainErr(err) {
			return err
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		err = call()
	}
	return err
}

// bareStaleBase reports a FailedPrecondition that carries no StaleBase detail.
func bareStaleBase(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.FailedPrecondition && staleBaseFromStatus(err) == nil
}

// staleBaseByRead learns where the story stands by reading the events after base.
// It returns nil when the story is still at base, so the refusal was not a stale
// base. A base the server does not know is read again from the start.
func (cc *ChainConn) staleBaseByRead(ctx context.Context, project, storyID, base string) (*StaleBaseError, error) {
	read := func(after string) (*syncpb.ReadResponse, error) {
		var out *syncpb.ReadResponse
		err := cc.c.withGRPCAuth(ctx, func(ctx context.Context) error {
			return retryTransient(ctx, func() (rpcErr error) {
				out, rpcErr = cc.cli.Read(ctx, &syncpb.ReadRequest{Project: project, StoryId: storyID, AfterId: after}, chainCallOpts()...)
				return rpcErr
			})
		})
		return out, err
	}
	out, err := read(base)
	missing := 0
	if err == nil {
		missing = len(out.GetEvents())
	} else if base != "" {
		first := err
		if out, err = read(""); err != nil {
			return nil, errors.Join(first, err)
		}
		missing = len(out.GetEvents())
		for i, e := range out.GetEvents() {
			if e.GetId() == base {
				missing = len(out.GetEvents()) - i - 1
				break
			}
		}
	} else {
		return nil, err
	}
	latest := out.GetLatestId()
	if latest == "" {
		if evs := out.GetEvents(); len(evs) > 0 {
			latest = evs[len(evs)-1].GetId()
		}
	}
	if latest == "" || latest == base {
		return nil, nil
	}
	return &StaleBaseError{StoryID: storyID, Base: base, LatestID: latest, Missing: missing}, nil
}

// staleBaseFromStatus returns the StaleBase detail of a FailedPrecondition
// status, or nil for any other error.
func staleBaseFromStatus(err error) *syncpb.StaleBase {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		return nil
	}
	for _, d := range st.Details() {
		if sb, ok := d.(*syncpb.StaleBase); ok {
			return sb
		}
	}
	return nil
}
