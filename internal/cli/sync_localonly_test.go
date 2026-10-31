package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// hostedCalls counts every call a fake server received: Snapshot, Apply and the
// chain's Read, Append and Subscribe, plus any HTTP request to a route it lacks.
func hostedCalls(f *fakeWorkstateServer, http *atomic.Int32) int {
	f.mu.Lock()
	n := f.gets + len(f.applies)
	f.mu.Unlock()
	f.chain.mu.Lock()
	n += len(f.chain.appends) + len(f.chain.subscribes)
	for _, r := range f.chain.reads {
		n += r
	}
	f.chain.mu.Unlock()
	return n + int(http.Load())
}

// sty_0dc600e1 AC6: a user who is not signed in, and a signed-in user in a repo
// with no [sync] project (whose directory slug ResolveBoundProject still names),
// keep the whole local store: snapshot, pull, push, follow and an open of an old
// story drop no history and make no hosted call.
func TestLocalOnlyKeepsTheWholeStoreAndCallsNoServer(t *testing.T) {
	for name, fx := range map[string]struct {
		toml     string
		signedIn bool
	}{
		"not signed in":                {toml: hostedToml},
		"signed in, no [sync] project": {toml: strings.Replace(hostedToml, "project = \"probe\"\n", "", 1), signedIn: true},
	} {
		t.Run(name, func(t *testing.T) {
			var stray atomic.Int32
			ts, f := newFakeWorkstateServerWith(t, func(mux *http.ServeMux, _ *fakeWorkstateServer) {
				mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
					stray.Add(1)
					http.NotFound(w, nil)
				})
			})
			repo := workstateRepo(t, fx.toml)
			if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
				t.Fatal(err)
			}
			if fx.signedIn {
				seedCredAs(t, ts.URL, "usr_me")
			}
			old := cacheStory(t, f, ts.URL, repo, "old", "backlog", time.Now().Add(-72*time.Hour).UTC())
			appendLocal(t, old, "made here")
			before := localEvents(t, old)
			calls := hostedCalls(f, &stray)

			for _, args := range [][]string{
				{"sync", "workstate", "snapshot"},
				{"sync", "workstate", "pull"},
				{"sync", "workstate", "push"},
				{"sync", "workstate", "follow"},
				{"story", "get", old},
			} {
				out, err := runRoot(t, args...)
				if fx.signedIn && err != nil {
					t.Errorf("%v must be a no-op in an unbound repo: %v\n%s", args, err, out)
				}
				if fx.signedIn && args[0] == "sync" && !strings.Contains(out, "repo not bound") {
					t.Errorf("%v does not say the repo is not bound:\n%s", args, out)
				}
			}
			if n := localEvents(t, old); n != before {
				t.Errorf("the old story holds %d event(s), want all %d kept", n, before)
			}
			if hasMark(t, old, ledger.MarkHistoryRemote) {
				t.Error("a local-only store marked a story's history remote")
			}
			if n := hostedCalls(f, &stray); n != calls {
				t.Errorf("a local-only checkout made %d hosted call(s)", n-calls)
			}
		})
	}
}

// sty_0dc600e1 AC6: satelle init binds a checkout that synced under its directory
// slug with no [sync] project, and leaves one with no saved hosted state unbound.
func TestInitBindsACheckoutThatSyncedUnderItsSlug(t *testing.T) {
	unbound := strings.Replace(hostedToml, "project = \"probe\"\n", "", 1)
	for name, saved := range map[string]bool{"saved state": true, "no saved state": false} {
		t.Run(name, func(t *testing.T) {
			ts, _ := newFakeWorkstateServer(t)
			repo := workstateRepo(t, unbound)
			if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
				t.Fatal(err)
			}
			cfgPath := filepath.Join(repo, config.DefaultDataDir, config.ConfigName)
			cfg, _, err := config.Load(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			slug := config.ResolveBoundProject(cfg, repo)
			if saved {
				if err := hosted.SaveStoryHeads(ts.URL, slug, repo, map[string]string{"sty_x": "evt_x"}); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runRoot(t, "init")
			if err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			cfg, _, err = config.Load(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			got := cfg.SyncProject()
			switch {
			case saved && got != slug:
				raw, _ := os.ReadFile(cfgPath)
				t.Errorf("[sync] project = %q, want the slug %q it synced under:\n%s\n%s", got, slug, raw, out)
			case !saved && got != "":
				t.Errorf("a checkout with no saved hosted state was bound to %q", got)
			}
		})
	}
}
