package cli

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// sty_a21d9c07 AC3: with [hosted] push = false in the machine config, `sync push`
// and `sync workstate push` refuse naming the setting and make no hosted call,
// while `sync workstate pull` still reaches the server and succeeds.
func TestMachinePushOffRefusesPushKeepsPull(t *testing.T) {
	var mu sync.Mutex
	var rest []string
	ts, f := newFakeWorkstateServerWith(t, func(mux *http.ServeMux, _ *fakeWorkstateServer) {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			rest = append(rest, r.Method+" "+r.URL.Path)
			mu.Unlock()
			http.NotFound(w, r)
		})
	})
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	createStoryID(t, "stale machine")
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	off := false
	gc.Hosted.Push = &off
	if err := config.SaveGlobal(gc); err != nil {
		t.Fatal(err)
	}

	for _, verb := range [][]string{{"sync", "push"}, {"sync", "workstate", "push"}} {
		out, err := runRoot(t, append(verb, "--server", ts.URL)...)
		name := strings.Join(verb, " ")
		if err == nil {
			t.Fatalf("%s with push off must exit non-zero:\n%s", name, out)
		}
		if msg := err.Error(); !strings.Contains(msg, "[hosted] push = false") || !strings.Contains(msg, config.GlobalConfigPath()) {
			t.Errorf("%s refusal does not name the setting: %v", name, err)
		}
	}
	mu.Lock()
	calls := append([]string(nil), rest...)
	mu.Unlock()
	f.mu.Lock()
	applies, snaps := len(f.applies), len(f.snapReqs)
	f.mu.Unlock()
	if len(calls) != 0 || applies != 0 || snaps != 0 || f.chain.appendCount() != 0 {
		t.Fatalf("a refused push reached the server: rest %v, applies %d, snapshots %d, appends %d", calls, applies, snaps, f.chain.appendCount())
	}

	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
		t.Fatalf("pull with push off: %v\n%s", err, out)
	}
	f.mu.Lock()
	reached := len(f.snapReqs) + f.gets
	applies = len(f.applies)
	f.mu.Unlock()
	if reached == 0 {
		t.Error("the pull never reached the server")
	}
	if applies != 0 || f.chain.appendCount() != 0 {
		t.Errorf("the pull wrote to the server: applies %d, appends %d", applies, f.chain.appendCount())
	}
}
