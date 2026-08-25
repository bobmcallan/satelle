package hosted

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

// MaxSyncMessageBytes caps a single unary Sync message (Apply / Snapshot /
// Refresh) at 64 MiB, well over grpc-go's 4 MiB MaxCallRecvMsgSize default.
// Snapshot is unpaged, so a whole project's work-state rides in one message
// (satelle.dev/satelle was 24 MiB on 2026-08-25).
const MaxSyncMessageBytes = 64 << 20

func syncCallOpts() []grpc.CallOption {
	return []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(MaxSyncMessageBytes),
		grpc.MaxCallSendMsgSize(MaxSyncMessageBytes),
	}
}

// testGRPCDial, when set, is used by every Client that has no per-client
// dialer. CLI integration tests install a bufconn fake here.
var testGRPCDial func(ctx context.Context, target string) (*grpc.ClientConn, error)

// SetTestGRPCDial installs a process-wide test dialer. Pass nil to clear.
func SetTestGRPCDial(fn func(ctx context.Context, target string) (*grpc.ClientConn, error)) {
	testGRPCDial = fn
}

// Apply posts a work-state batch via gRPC Sync/Apply. Callers keep the
// WorkstateIngest shape; the transport is no longer REST.
func (c *Client) Apply(ctx context.Context, project string, batch WorkstateIngest) (WorkstateIngestResult, error) {
	if batch.Items == nil {
		batch.Items = []json.RawMessage{}
	}
	if batch.Ledger == nil {
		batch.Ledger = []json.RawMessage{}
	}
	req, err := ingestToApplyRequest(project, batch)
	if err != nil {
		return WorkstateIngestResult{}, err
	}
	conn, err := c.openGRPC(ctx)
	if err != nil {
		return WorkstateIngestResult{}, err
	}
	defer conn.Close()
	cli := syncpb.NewSyncClient(conn)
	var out *syncpb.ApplyResponse
	err = c.withGRPCAuth(ctx, cli, func(ctx context.Context) error {
		var rpcErr error
		out, rpcErr = cli.Apply(ctx, req, syncCallOpts()...)
		return rpcErr
	})
	if err != nil {
		return WorkstateIngestResult{}, mapGRPCErr("Apply", err)
	}
	return WorkstateIngestResult{Items: int(out.GetItems()), Ledger: int(out.GetLedger())}, nil
}

// Snapshot pulls current work-state via one gRPC Sync/Snapshot RPC.
func (c *Client) Snapshot(ctx context.Context, project, kind string) (items []WorkstateItem, ledger []WorkstateLedgerRow, err error) {
	conn, err := c.openGRPC(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	cli := syncpb.NewSyncClient(conn)
	var out *syncpb.SnapshotResponse
	err = c.withGRPCAuth(ctx, cli, func(ctx context.Context) error {
		var rpcErr error
		out, rpcErr = cli.Snapshot(ctx, &syncpb.SnapshotRequest{Project: project, Kind: kind}, syncCallOpts()...)
		return rpcErr
	})
	if err != nil {
		return nil, nil, mapGRPCErr("Snapshot", err)
	}
	items = make([]WorkstateItem, 0, len(out.GetItems()))
	for _, it := range out.GetItems() {
		items = append(items, WorkstateItem{
			ID: it.GetId(), Kind: it.GetKind(), Type: it.GetType(),
			Status: it.GetStatus(), Title: it.GetTitle(), Origin: it.GetOrigin(),
			Record: json.RawMessage(it.GetRecord()),
		})
	}
	ledger = make([]WorkstateLedgerRow, 0, len(out.GetLedger()))
	for _, e := range out.GetLedger() {
		ledger = append(ledger, WorkstateLedgerRow{
			ID: e.GetId(), StoryID: e.GetStoryId(), Kind: e.GetKind(),
			Type: e.GetType(), Origin: e.GetOrigin(),
			Record: json.RawMessage(e.GetRecord()),
		})
	}
	return items, ledger, nil
}

func ingestToApplyRequest(project string, batch WorkstateIngest) (*syncpb.ApplyRequest, error) {
	req := &syncpb.ApplyRequest{Project: project}
	for _, raw := range batch.Items {
		it, err := protoWorkItem(raw)
		if err != nil {
			return nil, err
		}
		req.Items = append(req.Items, it)
	}
	for _, raw := range batch.Ledger {
		e, err := protoLedgerEntry(raw)
		if err != nil {
			return nil, err
		}
		req.Ledger = append(req.Ledger, e)
	}
	return req, nil
}

func protoWorkItem(raw json.RawMessage) (*syncpb.WorkItem, error) {
	var p struct {
		ID, Kind, Type, Status, Title, Origin string
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("hosted: encode workstate item: %w", err)
		}
	}
	return &syncpb.WorkItem{
		Id: p.ID, Kind: p.Kind, Type: p.Type, Status: p.Status,
		Title: p.Title, Origin: p.Origin, Record: append([]byte(nil), raw...),
	}, nil
}

func protoLedgerEntry(raw json.RawMessage) (*syncpb.LedgerEntry, error) {
	var p struct {
		ID, Kind, Type, Origin string
		StoryID                string `json:"story_id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("hosted: encode workstate ledger: %w", err)
		}
	}
	return &syncpb.LedgerEntry{
		Id: p.ID, StoryId: p.StoryID, Kind: p.Kind, Type: p.Type,
		Origin: p.Origin, Record: append([]byte(nil), raw...),
	}, nil
}

func (c *Client) openGRPC(ctx context.Context) (*grpc.ClientConn, error) {
	target, secure, err := grpcTarget(c.server)
	if err != nil {
		return nil, err
	}
	if c.dialGRPC != nil {
		return c.dialGRPC(ctx, target)
	}
	if testGRPCDial != nil {
		return testGRPCDial(ctx, target)
	}
	var creds credentials.TransportCredentials
	if secure {
		creds = credentials.NewTLS(&tls.Config{ServerName: hostnameOf(target)})
	} else {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("hosted: grpc dial %s: %w", target, err)
	}
	return conn, nil
}

// grpcTarget maps a hosted HTTP origin onto a gRPC dial address.
// https → host:443 (or explicit port) with TLS; http → host:80 (or explicit
// port) insecure. No extra config knob.
func grpcTarget(server string) (addr string, secure bool, err error) {
	u, err := url.Parse(server)
	if err != nil || strings.TrimSpace(u.Hostname()) == "" {
		return "", false, fmt.Errorf("hosted: grpc target: invalid server %q", server)
	}
	port := u.Port()
	switch strings.ToLower(u.Scheme) {
	case "https":
		if port == "" {
			port = "443"
		}
		return net.JoinHostPort(u.Hostname(), port), true, nil
	case "http":
		if port == "" {
			port = "80"
		}
		return net.JoinHostPort(u.Hostname(), port), false, nil
	default:
		return "", false, fmt.Errorf("hosted: grpc target: unsupported scheme %q", u.Scheme)
	}
}

func hostnameOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func (c *Client) withGRPCAuth(ctx context.Context, cli syncpb.SyncClient, fn func(ctx context.Context) error) error {
	cred, err := c.store.Load(c.server)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			return ErrLoginRequired
		}
		return err
	}
	err = fn(withBearer(ctx, cred.AccessToken))
	if err == nil || status.Code(err) != codes.Unauthenticated {
		return err
	}
	rotated, rErr := c.refreshOverGRPC(ctx, cli, cred)
	if rErr != nil {
		return rErr
	}
	return fn(withBearer(ctx, rotated.AccessToken))
}

// refreshOverGRPC rotates tokens via Sync.Refresh on the same connection as
// Apply/Snapshot. The RPC is sent on the bare ctx — no bearer — because the
// access token has just been rejected; the refresh token in the request is
// the credential.
func (c *Client) refreshOverGRPC(ctx context.Context, cli syncpb.SyncClient, cred Credential) (Credential, error) {
	out, err := cli.Refresh(ctx, &syncpb.RefreshRequest{RefreshToken: cred.RefreshToken}, syncCallOpts()...)
	if err != nil {
		return Credential{}, fmt.Errorf("%w (refresh failed: %v)", ErrLoginRequired, err)
	}
	if out.GetAccessToken() == "" || out.GetRefreshToken() == "" {
		return Credential{}, fmt.Errorf("%w (refresh failed: missing access or refresh token)", ErrLoginRequired)
	}
	tok := tokenResponse{
		AccessToken:  out.GetAccessToken(),
		RefreshToken: out.GetRefreshToken(),
		ExpiresIn:    out.GetExpiresIn(),
	}
	return c.persistRotated(cred, tok)
}

func withBearer(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

func mapGRPCErr(op string, err error) error {
	if err == nil || errors.Is(err, ErrLoginRequired) {
		return err
	}
	if st, ok := status.FromError(err); ok {
		if st.Code() == codes.ResourceExhausted && isMessageSizeErr(st.Message()) {
			return fmt.Errorf("hosted: grpc %s: message exceeds the %d MiB satelle Sync cap: %s",
				op, MaxSyncMessageBytes>>20, st.Message())
		}
		return fmt.Errorf("hosted: grpc %s: %s", op, st.Message())
	}
	return fmt.Errorf("hosted: grpc %s: %w", op, err)
}

// isMessageSizeErr sniffs grpc-go's message-size wording. That text is not a
// stable API; a miss falls through to the generic mapGRPCErr message.
func isMessageSizeErr(msg string) bool {
	return strings.Contains(msg, "larger than max")
}
