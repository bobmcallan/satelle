package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

type stubSync struct {
	syncpb.UnimplementedSyncServer
	mu      sync.Mutex
	auths   []string
	applies int
	snaps   int
	kind    string
	fail    []error
	apply   func(*syncpb.ApplyRequest) (*syncpb.ApplyResponse, error)
	snap    func(*syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error)
}

func (s *stubSync) recordAuth(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	vals := md.Get("authorization")
	if len(vals) == 0 {
		s.auths = append(s.auths, "")
		return
	}
	s.auths = append(s.auths, vals[0])
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
	items, rows, err := c.Snapshot(context.Background(), "probe", "story")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "sty_1" {
		t.Fatalf("snapshot items = %+v", items)
	}
	if rows == nil {
		t.Fatal("ledger nil")
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
	_, _, err := c.Snapshot(context.Background(), "probe", "")
	if err == nil {
		t.Fatal("expected snapshot error")
	}
}

func TestApplyRefreshesOnUnauthenticated(t *testing.T) {
	store := &memStore{}
	_ = store.Save(Credential{
		ServerURL: "http://hosted.example", AccessToken: "access-1", RefreshToken: "refresh-1",
		DisplayName: "A", Email: "a@b.c", PrincipalID: "u1",
	})
	var refreshPosts int
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		refreshPosts++
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "refresh-1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP %s %s", r.Method, r.URL.Path)
		http.Error(w, "nope", 500)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Client.server must match the stored credential AND the refresh URL.
	// newTestGRPCClient hardcodes http://hosted.example — override after
	// construction and store against ts.URL.
	store = &memStore{}
	_ = store.Save(Credential{
		ServerURL: ts.URL, AccessToken: "access-1", RefreshToken: "refresh-1",
		DisplayName: "A", Email: "a@b.c", PrincipalID: "u1",
	})
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "unauthenticated")}}
	c := newTestGRPCClient(t, stub, store, ts.Client())
	c.server = ts.URL

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
	if refreshPosts != 1 {
		t.Fatalf("refresh posts = %d", refreshPosts)
	}
	if len(stub.auths) != 2 || stub.auths[0] != "Bearer access-1" || stub.auths[1] != "Bearer access-2" {
		t.Fatalf("auths = %v", stub.auths)
	}
	got, err := store.Load(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "refresh-2" || got.DisplayName != "A" || got.Email != "a@b.c" || got.PrincipalID != "u1" {
		t.Fatalf("rotated cred = %+v", got)
	}
}

func TestApplyRefreshFailureIsLoginRequired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "access-1", RefreshToken: "refresh-1"})
	stub := &stubSync{fail: []error{status.Error(codes.Unauthenticated, "unauthenticated")}}
	c := newTestGRPCClient(t, stub, store, ts.Client())
	c.server = ts.URL
	_, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v", err)
	}
	if stub.applies != 1 {
		t.Fatalf("applies = %d", stub.applies)
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
