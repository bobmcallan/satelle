package trunk_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/trunk"
)

// Dirt in the trunk checkout that the caller attributes to another's work is
// reported with its owner and does not make the state Dirty; the compare still
// decides the state (sty_f1db1260).
func TestCheckDirtyOwnerAttributed(t *testing.T) {
	asks := 0
	owner := func(string) string { asks++; return "sty_owner" }

	t.Run("level", func(t *testing.T) {
		asks = 0
		r := testutil.NewTrunkRepos(t)
		linked := r.LinkedWorktree(t, r.Subject, "child")
		dirtySeed(t, r)

		rep := trunk.Check(context.Background(), linked, trunk.Options{DirtyOwner: owner})

		if rep.State != trunk.Level {
			t.Fatalf("state = %s, want level (%s)", rep.State, rep.Reason)
		}
		if rep.DirtyOwner != "sty_owner" || rep.DirtyCheckout != realPath(t, r.Subject) {
			t.Fatalf("owner/checkout = %q / %q", rep.DirtyOwner, rep.DirtyCheckout)
		}
		if rep.Quiet() {
			t.Fatal("an attributed dirty trunk must not be quiet")
		}
		if !strings.Contains(rep.Line(), "belong to engaged story sty_owner") {
			t.Fatalf("line = %q", rep.Line())
		}
		if asks != 1 {
			t.Fatalf("DirtyOwner asked %d times, want 1", asks)
		}
	})

	t.Run("behind is reported and the other tree is not touched", func(t *testing.T) {
		r := testutil.NewTrunkRepos(t)
		linked := r.LinkedWorktree(t, r.Subject, "child")
		dirtySeed(t, r)
		r.PublishFromPusher(t, "a.txt")
		before := take(t, r)

		rep := trunk.Check(context.Background(), linked, trunk.Options{FastForward: true, DirtyOwner: owner})

		if rep.State != trunk.Behind || rep.FastForwarded {
			t.Fatalf("state = %s, fast-forwarded = %v", rep.State, rep.FastForwarded)
		}
		if rep.DirtyOwner != "sty_owner" {
			t.Fatalf("owner = %q", rep.DirtyOwner)
		}
		if after := take(t, r); after != before {
			t.Fatalf("the owner's tree changed: %+v -> %+v", before, after)
		}
	})

	t.Run("a real divergence is still diverged", func(t *testing.T) {
		r := testutil.NewTrunkRepos(t)
		linked := r.LinkedWorktree(t, r.Subject, "child")
		r.Commit(t, r.Subject, "local.txt", "local\n")
		r.PublishFromPusher(t, "a.txt")
		dirtySeed(t, r)

		rep := trunk.Check(context.Background(), linked, trunk.Options{DirtyOwner: owner})

		if rep.State != trunk.Diverged || rep.DirtyOwner != "sty_owner" {
			t.Fatalf("state = %s, owner = %q", rep.State, rep.DirtyOwner)
		}
	})
}

// Dirt nobody accounts for stays Dirty: the callback answers nothing, the
// callback is absent, or the dirt is in the very tree Check runs in.
func TestCheckDirtyOwnerUnaccounted(t *testing.T) {
	linked := func(t *testing.T, r testutil.TrunkRepos) string { return r.LinkedWorktree(t, r.Subject, "child") }
	subject := func(_ *testing.T, r testutil.TrunkRepos) string { return r.Subject }
	cases := []struct {
		name string
		from func(*testing.T, testutil.TrunkRepos) string
		opts trunk.Options
	}{
		{"callback names no one", linked, trunk.Options{DirtyOwner: func(string) string { return "" }}},
		{"no callback", linked, trunk.Options{}},
		{"the invoking tree is the dirty one", subject, trunk.Options{DirtyOwner: func(string) string { return "sty_owner" }}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := testutil.NewTrunkRepos(t)
			dir := c.from(t, r)
			dirtySeed(t, r)

			rep := trunk.Check(context.Background(), dir, c.opts)

			if rep.State != trunk.Dirty || rep.DirtyOwner != "" {
				t.Fatalf("state = %s, owner = %q, want dirty with no owner", rep.State, rep.DirtyOwner)
			}
			if strings.Contains(rep.Line(), "belong") {
				t.Fatalf("line = %q", rep.Line())
			}
		})
	}
}

func dirtySeed(t *testing.T, r testutil.TrunkRepos) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Subject, "seed.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
