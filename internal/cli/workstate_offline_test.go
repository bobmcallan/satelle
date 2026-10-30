package cli

import (
	"context"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// trimKeeps asserts that a trim holding nothing still leaves every event of id.
func trimKeeps(t *testing.T, server, repo, id string, want int) {
	t.Helper()
	cur, err := hosted.LoadWorkstateCursor(server, "probe", repo)
	if err != nil {
		t.Fatal(err)
	}
	withRepoStore(t, func(db *store.DB) {
		if _, err := verb.TrimStoryHistory(context.Background(), db.Ledger, map[string]bool{}, cur.StoryHeads); err != nil {
			t.Fatal(err)
		}
	})
	if n := localEvents(t, id); n != want {
		t.Fatalf("the trim left %d event(s) of %s, want its unpushed work kept (%d)", n, id, want)
	}
}

// sty_0dc600e1 AC4: work done while the server is unreachable is appended locally
// and kept; on reconnect, with another machine's event on the server, the push
// takes that event and re-appends this machine's after it, recording no conflict.
func TestOfflineWorkRebasesOnReconnect(t *testing.T) {
	defer hosted.SetChainRetryDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})()
	ts, f, repo := hostedModeRepo(t)
	id := cacheStory(t, f, ts.URL, repo, "worked offline", "backlog", time.Now().Add(-72*time.Hour))
	published := localIDs(t, id)

	// Offline: the appends land locally and the push cannot reach the chain.
	appendLocal(t, id, "offline one")
	appendLocal(t, id, "offline two")
	down := status.Error(codes.Unavailable, "connection refused")
	f.chain.mu.Lock()
	f.chain.transient = map[string][]error{id: {down, down, down, down}}
	f.chain.mu.Unlock()
	if out, err := pushRaw(t, ts.URL); err == nil {
		t.Fatalf("a push with the chain unreachable must fail:\n%s", out)
	}
	local := localIDs(t, id)
	if len(local) < len(published)+2 || !slices.Equal(local[:len(published)], published) {
		t.Fatalf("the local chain %v must be the published %v and the offline events after it", local, published)
	}
	trimKeeps(t, ts.URL, repo, id, len(local))

	// Another machine moves the story on while this one is offline.
	other := ledger.Entry{ID: ledger.NewID(), StoryID: id, BaseID: published[len(published)-1], Kind: ledger.KindComment, Actor: "alice", Body: "made elsewhere", CreatedAt: time.Now().UTC()}
	f.chain.seed("probe", id, hostedEvent(t, other))

	out, err := pushRaw(t, ts.URL)
	if err != nil {
		t.Fatalf("the push on reconnect: %v\n%s", err, out)
	}
	want := append(append(slices.Clone(published), other.ID), local[len(published):]...)
	if got := f.chain.stored("probe", id); !slices.Equal(got, want) {
		t.Errorf("the server's chain = %v, want the other machine's event then this one's: %v", got, want)
	}
	if got := localIDs(t, id); !slices.Equal(got, want) {
		t.Errorf("the local chain = %v, want %v", got, want)
	}
	for _, body := range []string{"offline one", "offline two"} {
		if !f.chain.carries(body) {
			t.Errorf("no Append carried %q", body)
		}
	}
	if c, ok := storyConflicts(t, repo)[id]; ok {
		t.Errorf("a rebase recorded a conflict: %v", c)
	}
	if got := storyHeads(t, ts.URL, "probe", repo)[id]; got != want[len(want)-1] {
		t.Errorf("head = %s, want the last re-appended event %s", got, want[len(want)-1])
	}
}
