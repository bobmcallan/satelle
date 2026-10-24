package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

type stubSync struct {
	syncpb.UnimplementedSyncServer
	mu        sync.Mutex
	auths     []string
	locs      []string
	applies   int
	snaps     int
	refreshes int // Sync.Refresh RPCs received; the CLI must never send one
	kind      string
	fail      []error
	apply     func(*syncpb.ApplyRequest) (*syncpb.ApplyResponse, error)
	snap      func(*syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error)
}

func (s *stubSync) recordAuth(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	vals := md.Get("authorization")
	if len(vals) == 0 {
		s.auths = append(s.auths, "")
	} else {
		s.auths = append(s.auths, vals[0])
	}
	locs := md.Get(LocationHeader)
	if len(locs) == 0 {
		s.locs = append(s.locs, "")
	} else {
		s.locs = append(s.locs, locs[0])
	}
}

func (s *stubSync) nextFail() error {
	if len(s.fail) == 0 {
		return nil
	}
	err := s.fail[0]
	s.fail = s.fail[1:]
	return err
}

func (s *stubSync) Apply(ctx context.Context, req *syncpb.ApplyRequest) (*syncpb.ApplyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordAuth(ctx)
	s.applies++
	if err := s.nextFail(); err != nil {
		return nil, err
	}
	if s.apply != nil {
		return s.apply(req)
	}
	return &syncpb.ApplyResponse{Items: int32(len(req.GetItems())), Ledger: int32(len(req.GetLedger()))}, nil
}

func (s *stubSync) Snapshot(ctx context.Context, req *syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordAuth(ctx)
	s.snaps++
	s.kind = req.GetKind()
	if err := s.nextFail(); err != nil {
		return nil, err
	}
	if s.snap != nil {
		return s.snap(req)
	}
	return &syncpb.SnapshotResponse{}, nil
}

func (s *stubSync) Refresh(ctx context.Context, req *syncpb.RefreshRequest) (*syncpb.RefreshResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshes++
	return nil, status.Error(codes.Unimplemented, "the CLI refreshes at the provider, not here")
}

func newTestGRPCClient(t *testing.T, stub *stubSync, store Store, httpClient *http.Client) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	syncpb.RegisterSyncServer(gs, stub)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() {
		gs.Stop()
		_ = lis.Close()
	})
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	c := NewClient("http://hosted.example", store, httpClient)
	c.dialGRPC = func(ctx context.Context, target string) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///buf",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
	return c
}

// snapshotAll runs one Snapshot pass and gathers every page's rows.
func snapshotAll(c *Client, project, kind string, opts SnapshotOptions) ([]WorkstateItem, []WorkstateLedgerRow, SnapshotResult, error) {
	var items []WorkstateItem
	var rows []WorkstateLedgerRow
	res, err := c.Snapshot(context.Background(), project, kind, opts, func(p SnapshotPage) error {
		items = append(items, p.Items...)
		rows = append(rows, p.Ledger...)
		return nil
	})
	return items, rows, res, err
}

func fatalHTTP(t *testing.T) *http.Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP %s %s", r.Method, r.URL.Path)
		http.Error(w, "no REST", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	return ts.Client()
}

func TestApplyUsesGRPC(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	var seen *syncpb.ApplyRequest
	stub := &stubSync{apply: func(req *syncpb.ApplyRequest) (*syncpb.ApplyResponse, error) {
		seen = req
		return &syncpb.ApplyResponse{Items: 1, Ledger: 0}, nil
	}}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	res, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Items != 1 {
		t.Fatalf("items = %d", res.Items)
	}
	if seen == nil || seen.GetProject() != "probe" {
		t.Fatalf("request = %+v", seen)
	}
	if len(seen.GetItems()) != 1 || string(seen.GetItems()[0].GetRecord()) != `{"id":"sty_1"}` {
		t.Fatalf("record = %q", seen.GetItems()[0].GetRecord())
	}
	if seen.GetItems()[0].GetId() != "sty_1" {
		t.Fatalf("promoted id = %q", seen.GetItems()[0].GetId())
	}
	if len(stub.auths) != 1 || stub.auths[0] != "Bearer tok" {
		t.Fatalf("auth = %v", stub.auths)
	}
}

func TestApplySendsLocationMetadata(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	stub := &stubSync{}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	c.SetLocation("loc_grpc_apply_bbbbbbbb")
	if _, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if len(stub.locs) != 1 || stub.locs[0] != "loc_grpc_apply_bbbbbbbb" {
		t.Fatalf("location metadata = %v", stub.locs)
	}
}

func TestSnapshotSendsLocationMetadata(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	stub := &stubSync{}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	c.SetLocation("loc_grpc_snap_cccccccc")
	if _, _, _, err := snapshotAll(c, "probe", "", SnapshotOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(stub.locs) != 1 || stub.locs[0] != "loc_grpc_snap_cccccccc" {
		t.Fatalf("location metadata = %v", stub.locs)
	}
}

func TestApplyOmitsLocationWhenUnset(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	stub := &stubSync{}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	if _, err := c.Apply(context.Background(), "probe", WorkstateIngest{}); err != nil {
		t.Fatal(err)
	}
	if len(stub.locs) != 1 || stub.locs[0] != "" {
		t.Fatalf("unset location leaked metadata %v", stub.locs)
	}
}

// sty_40853d02: a "held by location" refusal from a server that still has holds
// is an ordinary Apply failure — the client has no hold concept to map it to.
func TestMapGRPCErrHeldRefusalIsPlainError(t *testing.T) {
	st := status.New(codes.FailedPrecondition, "workstate: sty_h held by location loc_other_bbbb (desk), last seen 2026-09-01T00:00:00Z")
	err := mapGRPCErr("Apply", st.Err())
	if err == nil || !strings.Contains(err.Error(), "held by location loc_other_bbbb") {
		t.Fatalf("err = %v, want the server's message carried through", err)
	}
}

func TestApplyTransportError(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	stub := &stubSync{fail: []error{status.Error(codes.Internal, "nope")}}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	_, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if err == nil {
		t.Fatal("expected apply error")
	}
}

func TestSnapshotUsesGRPC(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	stub := &stubSync{snap: func(req *syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error) {
		return &syncpb.SnapshotResponse{
			Items: []*syncpb.WorkItem{{Id: "sty_1", Kind: "story", Title: "T", Record: []byte(`{"id":"sty_1"}`)}},
		}, nil
	}}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	items, rows, res, err := snapshotAll(c, "probe", "story", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 1 || res.SyncToken != "" {
		t.Fatalf("result = %+v, want one page and no token from an unpaged server", res)
	}
	if len(items) != 1 || items[0].ID != "sty_1" {
		t.Fatalf("snapshot items = %+v", items)
	}
	if len(rows) != 0 {
		t.Fatalf("ledger = %+v, want none", rows)
	}
	if stub.kind != "story" {
		t.Fatalf("kind = %q", stub.kind)
	}
	if len(stub.auths) != 1 || stub.auths[0] != "Bearer tok" {
		t.Fatalf("auth = %v", stub.auths)
	}
}

func TestSnapshotTransportError(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	stub := &stubSync{fail: []error{status.Error(codes.Internal, "boom")}}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	_, _, _, err := snapshotAll(c, "probe", "", SnapshotOptions{})
	if err == nil {
		t.Fatal("expected snapshot error")
	}
}

func TestApplyRefreshesOnUnauthenticated(t *testing.T) {
	prov := newFakeProvider(t)
	store := &memStore{}
	cred := prov.credential("http://hosted.example")
	cred.DisplayName, cred.Email, cred.PrincipalID = "A", "a@b.c", "u1"
	_ = store.Save(cred)
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "unauthenticated")}}
	c := newTestGRPCClient(t, stub, store, guardedClient(t))

	res, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Items != 1 {
		t.Fatalf("items = %d", res.Items)
	}
	if stub.applies != 2 {
		t.Fatalf("applies = %d", stub.applies)
	}
	// AC4: the refresh reached the provider, never satelle-server's Sync.Refresh.
	if stub.refreshes != 0 {
		t.Fatalf("Sync.Refresh RPCs = %d, want 0", stub.refreshes)
	}
	if n := len(prov.refreshes()); n != 1 {
		t.Fatalf("provider refreshes = %d, want 1", n)
	}
	prov.wantRefresh(t, 0, "refresh-1")
	if len(stub.auths) != 2 || stub.auths[0] != "Bearer access-1" || stub.auths[1] != "Bearer access-2" {
		t.Fatalf("auths = %v", stub.auths)
	}
	got, err := store.Load("http://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "refresh-2" || got.DisplayName != "A" || got.Email != "a@b.c" || got.PrincipalID != "u1" {
		t.Fatalf("rotated cred = %+v", got)
	}
	if got.TokenEndpoint != prov.tokenURL() || got.ClientID != "client-abc" || got.Resource != "https://api.example/" {
		t.Fatalf("discovery fields lost across rotation: %+v", got)
	}
}

func TestSnapshotRefreshesOnUnauthenticated(t *testing.T) {
	prov := newFakeProvider(t)
	store := &memStore{}
	cred := prov.credential("http://hosted.example")
	cred.DisplayName, cred.Email, cred.PrincipalID = "A", "a@b.c", "u1"
	_ = store.Save(cred)
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "unauthenticated")}}
	c := newTestGRPCClient(t, stub, store, guardedClient(t))
	_, _, _, err := snapshotAll(c, "probe", "", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stub.snaps != 2 {
		t.Fatalf("snaps = %d", stub.snaps)
	}
	if stub.refreshes != 0 || len(prov.refreshes()) != 1 {
		t.Fatalf("Sync.Refresh = %d, provider refreshes = %d; want 0 and 1", stub.refreshes, len(prov.refreshes()))
	}
	prov.wantRefresh(t, 0, "refresh-1")
	got, err := store.Load("http://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "refresh-2" || got.DisplayName != "A" {
		t.Fatalf("rotated cred = %+v", got)
	}
}

// AC6: an Unauthenticated whose message starts "unknown_subject: " is shown
// with the server's message, with one RPC, no provider refresh, no retry.
func TestApplyUnknownSubjectIsShownNotRetried(t *testing.T) {
	const msg = "Sign in at https://satelle.dev first, then retry."
	prov := newFakeProvider(t)
	store := &memStore{}
	_ = store.Save(prov.credential("http://hosted.example"))
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "unknown_subject: "+msg)}}
	c := newTestGRPCClient(t, stub, store, guardedClient(t))
	_, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	var us *UnknownSubjectError
	if !errors.As(err, &us) || us.Message != msg || !strings.Contains(err.Error(), msg) {
		t.Fatalf("err = %v, want UnknownSubjectError with the server's message", err)
	}
	if errors.Is(err, ErrLoginRequired) {
		t.Errorf("an unknown subject must not tell the user to run satelle login: %v", err)
	}
	if stub.applies != 1 || stub.refreshes != 0 || len(prov.refreshes()) != 0 {
		t.Fatalf("applies=%d Sync.Refresh=%d provider refreshes=%d; want 1, 0, 0", stub.applies, stub.refreshes, len(prov.refreshes()))
	}
}

// A different Unauthenticated message is not unknown_subject: it refreshes.
func TestApplyOtherUnauthenticatedMessageStillRefreshes(t *testing.T) {
	prov := newFakeProvider(t)
	store := &memStore{}
	_ = store.Save(prov.credential("http://hosted.example"))
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "sign in at satelle.dev first")}}
	c := newTestGRPCClient(t, stub, store, guardedClient(t))
	if _, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if stub.applies != 2 || len(prov.refreshes()) != 1 {
		t.Fatalf("applies=%d provider refreshes=%d; want 2 and 1", stub.applies, len(prov.refreshes()))
	}
}

// A credential from before discovery has no token endpoint: login is required
// and nothing is sent anywhere.
func TestApplyCredentialWithoutTokenEndpointIsLoginRequired(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "access-1", RefreshToken: "refresh-1"})
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "unauthenticated")}}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	_, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v, want ErrLoginRequired", err)
	}
	if stub.refreshes != 0 {
		t.Fatalf("Sync.Refresh RPCs = %d, want 0", stub.refreshes)
	}
}

func TestSnapshotAcceptsLargeMessage(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	pad := strings.Repeat("a", 128<<10)
	items := make([]*syncpb.WorkItem, 40)
	for i := range items {
		rec := fmt.Sprintf(`{"id":"sty_%d","pad":%q}`, i, pad)
		items[i] = &syncpb.WorkItem{Id: fmt.Sprintf("sty_%d", i), Kind: "story", Record: []byte(rec)}
	}
	resp := &syncpb.SnapshotResponse{Items: items}
	if n := proto.Size(resp); n <= 4<<20 {
		t.Fatalf("fixture encoded size %d, want > 4 MiB", n)
	}
	stub := &stubSync{snap: func(*syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error) {
		return resp, nil
	}}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	got, _, _, err := snapshotAll(c, "probe", "", SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 40 {
		t.Fatalf("items = %d, want 40", len(got))
	}
	if got[0].ID != "sty_0" || len(got[0].Record) == 0 {
		t.Fatalf("first = %+v", got[0])
	}
}

func TestSnapshotResourceExhaustedNamesCap(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r"})
	stub := &stubSync{fail: []error{status.Error(codes.ResourceExhausted, "grpc: received message larger than max (25136676 vs. 4194304)")}}
	c := newTestGRPCClient(t, stub, store, fatalHTTP(t))
	_, _, _, err := snapshotAll(c, "probe", "", SnapshotOptions{})
	if err == nil {
		t.Fatal("expected snapshot error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Snapshot") || !strings.Contains(msg, "64 MiB") {
		t.Fatalf("error = %q, want Snapshot and 64 MiB", msg)
	}
}

func TestApplyRefreshFailureIsLoginRequired(t *testing.T) {
	prov := newFakeProvider(t)
	prov.deny = true
	store := &memStore{}
	_ = store.Save(prov.credential("http://hosted.example"))
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "unauthenticated")}}
	c := newTestGRPCClient(t, stub, store, guardedClient(t))
	_, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v", err)
	}
	if stub.applies != 1 {
		t.Fatalf("applies = %d", stub.applies)
	}
	if len(prov.refreshes()) != 1 || stub.refreshes != 0 {
		t.Fatalf("provider refreshes = %d, Sync.Refresh = %d; want 1 and 0", len(prov.refreshes()), stub.refreshes)
	}
}

func TestApplyNoCredential(t *testing.T) {
	stub := &stubSync{}
	c := newTestGRPCClient(t, stub, &memStore{}, fatalHTTP(t))
	_, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v", err)
	}
	if stub.applies != 0 {
		t.Fatalf("applies = %d", stub.applies)
	}
}

func TestGRPCTarget(t *testing.T) {
	addr, secure, err := grpcTarget("https://satelle.dev")
	if err != nil || addr != "satelle.dev:443" || !secure {
		t.Fatalf("https = %s %v %v", addr, secure, err)
	}
	addr, secure, err = grpcTarget("http://127.0.0.1:8788")
	if err != nil || addr != "127.0.0.1:8788" || secure {
		t.Fatalf("http = %s %v %v", addr, secure, err)
	}
	if _, _, err := grpcTarget("ftp://x"); err == nil {
		t.Fatal("expected scheme error")
	}
}
