package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
		req.Events = append(req.Events, &syncpb.Event{
			Id: e.ID, StoryId: storyID, BaseId: e.BaseID,
			WorkflowVersion: e.WorkflowVersion, Payload: append([]byte(nil), e.Payload...),
		})
	}
	var out *syncpb.AppendResponse
	err := cc.c.withGRPCAuth(ctx, func(ctx context.Context) error {
		var rpcErr error
		out, rpcErr = cc.cli.Append(ctx, req, syncCallOpts()...)
		return rpcErr
	})
	if err != nil {
		if sb := staleBaseFromStatus(err); sb != nil {
			return "", &StaleBaseError{StoryID: storyID, Base: base, LatestID: sb.GetLatestId(), Missing: len(sb.GetMissing())}
		}
		return "", cc.c.mapSyncErr("Append", err)
	}
	return out.GetLatestId(), nil
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
