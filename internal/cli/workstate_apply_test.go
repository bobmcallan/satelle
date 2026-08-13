package cli

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestWorkstateApplyEnabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())

	local := config.Config{Sync: map[string]string{"stories": "local", "executions": "local", "ledger": "local"}}
	ok, err := workstateApplyEnabled(local)
	if err != nil || ok {
		t.Fatalf("all local: ok=%v err=%v", ok, err)
	}

	personal := config.Config{
		Sync:   map[string]string{"all": "personal"},
		Hosted: config.HostedConfig{Server: "https://h.example", Project: "p"},
	}
	ok, err = workstateApplyEnabled(personal)
	if err != nil || ok {
		t.Fatalf("personal without cred: ok=%v err=%v", ok, err)
	}

	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: "https://h.example", AccessToken: "a", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	ok, err = workstateApplyEnabled(personal)
	if err != nil || !ok {
		t.Fatalf("personal with cred: ok=%v err=%v", ok, err)
	}
}

func TestWorkstateApplyEnabledExplicitAreas(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: "https://h.example", AccessToken: "a", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Sync:   map[string]string{"stories": "personal", "executions": "local", "ledger": "local"},
		Hosted: config.HostedConfig{Server: "https://h.example", Project: "p"},
	}
	ok, err := workstateApplyEnabled(cfg)
	if err != nil || !ok {
		t.Fatalf("stories personal: ok=%v err=%v", ok, err)
	}
}

func TestWireWorkstateApplierLocalDoesNotFire(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	var n int
	verb.SetWorkstateApplier(func(ctx context.Context, items []workitem.Item, entries []ledger.Entry) {
		n++
	})
	t.Cleanup(verb.ClearWorkstateApplier)
	wireWorkstateApplier(&app.App{Config: config.Config{
		Sync: map[string]string{"stories": "local", "executions": "local", "ledger": "local"},
	}})
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
	})
	body, _ := json.Marshal(map[string]any{"title": "LocalOnly"})
	if _, err := verb.Dispatch(context.Background(), "story-create", body); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("local config still fired applier %d times", n)
	}
}

func TestApplyWorkstateNowPostsAndSurvives500(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())

	var mu sync.Mutex
	applies := 0
	failNext := false
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	syncpb.RegisterSyncServer(gs, applyCountServer{
		fn: func(req *syncpb.ApplyRequest) (*syncpb.ApplyResponse, error) {
			mu.Lock()
			applies++
			fail := failNext
			mu.Unlock()
			if fail {
				return nil, status.Error(codes.Internal, "nope")
			}
			return &syncpb.ApplyResponse{Items: int32(len(req.GetItems()))}, nil
		},
	})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() {
		gs.Stop()
		_ = lis.Close()
		hosted.SetTestGRPCDial(nil)
	})
	hosted.SetTestGRPCDial(func(ctx context.Context, target string) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///buf",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	})

	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: "http://hosted.example", AccessToken: "tok", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Sync:   map[string]string{"all": "personal"},
		Hosted: config.HostedConfig{Server: "http://hosted.example", Project: "probe"},
	}
	a := &app.App{Config: cfg}

	var logs []string
	workstateApplyLog = func(s string) { logs = append(logs, s) }
	t.Cleanup(func() { workstateApplyLog = nil })

	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
		verb.ClearWorkstateApplier()
	})

	wireWorkstateApplier(a)
	body, err := json.Marshal(map[string]any{"title": "ApplyMe"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := verb.Dispatch(context.Background(), "story-create", body)
	if err != nil {
		t.Fatalf("create on 200: %v", err)
	}
	var created workitem.Item
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := applies
	mu.Unlock()
	if created.ID == "" || n != 1 {
		t.Fatalf("id=%s applies=%d", created.ID, n)
	}

	mu.Lock()
	failNext = true
	mu.Unlock()
	logs = nil
	body, _ = json.Marshal(map[string]any{"title": "ApplyFail"})
	raw, err = verb.Dispatch(context.Background(), "story-create", body)
	if err != nil {
		t.Fatalf("create must succeed on apply 500: %v", err)
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatal("row missing after apply 500")
	}
	if got, _ := db.Stories.Get(context.Background(), created.ID); got.Title != "ApplyFail" {
		t.Fatalf("store after 500: %+v", got)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "workstate apply") {
		t.Fatalf("expected apply log, got %v", logs)
	}
}

type applyCountServer struct {
	syncpb.UnimplementedSyncServer
	fn func(*syncpb.ApplyRequest) (*syncpb.ApplyResponse, error)
}

func (s applyCountServer) Apply(ctx context.Context, req *syncpb.ApplyRequest) (*syncpb.ApplyResponse, error) {
	return s.fn(req)
}
