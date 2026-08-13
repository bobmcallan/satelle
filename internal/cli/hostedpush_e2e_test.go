package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/serve"
	"github.com/bobmcallan/satelle/internal/web"
)

// TestMutatingCLIDoesNotDialHosted proves sty_c526753a AC1: a story create
// against an in-process mirror with the hosted-push hook wired records the
// repo as a push target, and the mutating CLI process itself makes no Apply
// request to the fake hosted server.
func TestMutatingCLIDoesNotDialHosted(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, "[review]\ngate_create = false\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	ms, err := mirror.Open(filepath.Join(t.TempDir(), "mirror.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Close() })

	var mu sync.Mutex
	var pushed []string
	pusher := &serve.Pusher{
		Debounce: 5 * time.Millisecond,
		Timeout:  time.Second,
		Resolve: func(_ context.Context, _ string) (string, bool) {
			return repo, true
		},
		Push: func(_ context.Context, repoPath string) error {
			mu.Lock()
			pushed = append(pushed, repoPath)
			mu.Unlock()
			return nil
		},
	}
	webSrv := web.NewMirrorHooks(ms, "test", pusher.Notify)
	srv := httptest.NewServer(webSrv.Handler)
	t.Cleanup(srv.Close)
	t.Setenv(EnvServerEndpoint, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go pusher.Run(ctx)

	out, err := runRoot(t, "story", "create",
		"--title", "Hosted via satelled",
		"--body", "CLI must not Apply",
		"--acceptance", "1. push recorded, no hosted call from create",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	if id, _ := created["id"].(string); id == "" {
		t.Fatalf("no story id in %s", out)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(pushed)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	got := append([]string(nil), pushed...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("ingest did not trigger a hosted push")
	}
	if got[0] != repo {
		t.Fatalf("push target = %q, want %q", got[0], repo)
	}
	if f.postCount("probe") != 0 {
		t.Fatalf("mutating CLI process posted %d Apply(s) to hosted; want 0", f.postCount("probe"))
	}
}
