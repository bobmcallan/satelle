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
// Apply batches ride in one message; Snapshot is paged (DefaultSnapshotPageSize),
// so a page stays far under the cap however large the project grows
// (satelle.dev/satelle was 24 MiB unpaged on 2026-08-25).
const MaxSyncMessageBytes = 64 << 20

// DefaultSnapshotPageSize is the page size Snapshot asks for when the caller
// sets none: at most this many items and this many ledger rows per response.
// The server caps it further. A var so tests can shrink it.
var DefaultSnapshotPageSize int32 = 500

// maxSnapshotPages bounds one pass so a server that never ends it cannot loop
// the client forever.
const maxSnapshotPages = 100000

// ErrSinceTokenRejected is returned by Snapshot when the server refuses a
// since_token or page_token the request carried (InvalidArgument,
// FailedPrecondition or OutOfRange). The caller falls back to a full pass.
var ErrSinceTokenRejected = errors.New("hosted: snapshot token rejected")

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
		return WorkstateIngestResult{}, c.mapSyncErr("Apply", err)
	}
	return WorkstateIngestResult{Items: int(out.GetItems()), Ledger: int(out.GetLedger())}, nil
}

// SnapshotOptions steers one Snapshot pass. SinceToken is the sync_token of a
// previous completed pass (only rows changed since are returned); PageToken
// resumes a pass from a page boundary (send the SinceToken that pass began
// with beside it). PageSize 0 means DefaultSnapshotPageSize.
type SnapshotOptions struct {
	SinceToken string
	PageToken  string
	PageSize   int32
}

// SnapshotPage is one response of a pass. Index counts from 1 within the call.
type SnapshotPage struct {
	Items         []WorkstateItem
	Ledger        []WorkstateLedgerRow
	NextPageToken string // empty on the last page
	Index         int
}

// SnapshotResult closes a pass: the last page's sync_token (empty when the
// server issues none) and how many pages this call fetched.
type SnapshotResult struct {
	SyncToken string
	Pages     int
}

// Snapshot pulls work-state through Sync/Snapshot one bounded page at a time,
// following next_page_token to the end of the pass. Each page is handed to
// onPage as it arrives and is not retained, so the caller can apply and
// checkpoint it and client memory stays bounded by the page size; an onPage
// error stops the pass and is returned as is. A row can repeat across pages or
// passes (the server never misses one), so onPage must apply idempotently.
func (c *Client) Snapshot(ctx context.Context, project, kind string, opts SnapshotOptions, onPage func(SnapshotPage) error) (SnapshotResult, error) {
	conn, err := c.openGRPC(ctx)
	if err != nil {
		return SnapshotResult{}, err
	}
	defer conn.Close()
	cli := syncpb.NewSyncClient(conn)
	size := opts.PageSize
	if size <= 0 {
		size = DefaultSnapshotPageSize
	}
	token := opts.PageToken
	seen := map[string]bool{token: true}
	var res SnapshotResult
	for {
		req := &syncpb.SnapshotRequest{
			Project: project, Kind: kind, PageSize: size,
			PageToken: token, SinceToken: opts.SinceToken,
		}
		var out *syncpb.SnapshotResponse
		err = c.withGRPCAuth(ctx, cli, func(ctx context.Context) error {
			var rpcErr error
			out, rpcErr = cli.Snapshot(ctx, req, syncCallOpts()...)
			return rpcErr
		})
		if err != nil {
			if (token != "" || opts.SinceToken != "") && tokenRejectedCode(status.Code(err)) {
				return res, fmt.Errorf("%w: %s", ErrSinceTokenRejected, status.Convert(err).Message())
			}
			return res, c.mapSyncErr("Snapshot", err)
		}
		res.Pages++
		page := SnapshotPage{
			Items:         workstateItemsFromProto(out.GetItems()),
			Ledger:        workstateLedgerFromProto(out.GetLedger()),
			NextPageToken: out.GetNextPageToken(),
			Index:         res.Pages,
		}
		if err := onPage(page); err != nil {
			return res, err
		}
		if page.NextPageToken == "" {
			res.SyncToken = out.GetSyncToken()
			return res, nil
		}
		if seen[page.NextPageToken] || res.Pages >= maxSnapshotPages {
			return res, fmt.Errorf("hosted: grpc Snapshot: pass does not end (page %d, token %q repeats or exceeds %d pages)",
				res.Pages, page.NextPageToken, maxSnapshotPages)
		}
		seen[page.NextPageToken] = true
		token = page.NextPageToken
	}
}

// tokenRejectedCode reports whether a status code means the server refused a
// token the request carried, rather than failed.
func tokenRejectedCode(c codes.Code) bool {
	return c == codes.InvalidArgument || c == codes.FailedPrecondition || c == codes.OutOfRange
}

func workstateItemsFromProto(in []*syncpb.WorkItem) []WorkstateItem {
	items := make([]WorkstateItem, 0, len(in))
	for _, it := range in {
		items = append(items, WorkstateItem{
			ID: it.GetId(), Kind: it.GetKind(), Type: it.GetType(),
			Status: it.GetStatus(), Title: it.GetTitle(), Origin: it.GetOrigin(),
			Record: json.RawMessage(it.GetRecord()),
		})
	}
	return items
}

func workstateLedgerFromProto(in []*syncpb.LedgerEntry) []WorkstateLedgerRow {
	ledger := make([]WorkstateLedgerRow, 0, len(in))
	for _, e := range in {
		ledger = append(ledger, WorkstateLedgerRow{
			ID: e.GetId(), StoryID: e.GetStoryId(), Kind: e.GetKind(),
			Type: e.GetType(), Origin: e.GetOrigin(),
			Record: json.RawMessage(e.GetRecord()),
		})
	}
	return ledger
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
	err = fn(c.callCtx(ctx, cred.AccessToken))
	if err == nil || status.Code(err) != codes.Unauthenticated {
		return err
	}
	if cred.IsSession() {
		return ErrSessionTokenRefused
	}
	rotated, rErr := c.refreshOverGRPC(ctx, cli, cred)
	if rErr != nil {
		return rErr
	}
	return fn(c.callCtx(ctx, rotated.AccessToken))
}

// refreshOverGRPC rotates tokens via Sync.Refresh on the same connection as
// Apply/Snapshot. The RPC is sent on the bare ctx — no bearer — because the
// access token has just been rejected; the refresh token in the request is
// the credential.
func (c *Client) refreshOverGRPC(ctx context.Context, cli syncpb.SyncClient, cred Credential) (Credential, error) {
	out, err := cli.Refresh(c.withLocation(ctx), &syncpb.RefreshRequest{RefreshToken: cred.RefreshToken}, syncCallOpts()...)
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

func (c *Client) withLocation(ctx context.Context) context.Context {
	if c.location != "" {
		return metadata.AppendToOutgoingContext(ctx, LocationHeader, c.location)
	}
	return ctx
}

// callCtx is the authenticated outgoing context for Apply/Snapshot:
// bearer plus, when set, x-satelle-location. Used on both the first attempt
// and the post-refresh retry.
func (c *Client) callCtx(ctx context.Context, token string) context.Context {
	return c.withLocation(withBearer(ctx, token))
}

// mapSyncErr is mapGRPCErr with the session-token reading of NotFound: under a
// session token the project is outside the token's scope.
func (c *Client) mapSyncErr(op string, err error) error {
	if status.Code(err) == codes.NotFound {
		if scope := c.sessionScopeErr(); scope != nil {
			return scope
		}
	}
	return mapGRPCErr(op, err)
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
		if held := heldErrorFromStatus(st); held != nil {
			return held
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

// heldErrorFromStatus maps a server held refusal onto *HeldError without a
// proto change (sty_f6cff549 AC4). Sniffs FailedPrecondition/Aborted plus
// "held" in the message; extracts location id when the wording matches.
func heldErrorFromStatus(st *status.Status) *HeldError {
	if st == nil {
		return nil
	}
	switch st.Code() {
	case codes.FailedPrecondition, codes.Aborted, codes.PermissionDenied:
	default:
		return nil
	}
	msg := st.Message()
	if !strings.Contains(strings.ToLower(msg), "held") {
		return nil
	}
	h := &HeldError{}
	// "held by location loc_xxx" — best-effort parse, never fail the mapping.
	if i := strings.Index(msg, "location "); i >= 0 {
		rest := msg[i+len("location "):]
		id := strings.Fields(rest)
		if len(id) > 0 {
			h.Hold.LocationID = strings.Trim(id[0], "(),")
		}
	}
	return h
}
