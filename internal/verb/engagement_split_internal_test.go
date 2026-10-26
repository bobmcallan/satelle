package verb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// splitRangeByTrunk keeps a commit when it names the story or the remote trunk does
// not hold it, drops it when the trunk holds it and it names another story, and
// reports no foreign commit when nothing in range is dropped (sty_659c4133).
func TestSplitRangeByTrunk(t *testing.T) {
	r := testutil.NewTrunkRepos(t)
	base := r.Head(t, r.Subject)
	commit := func(dir, file, subject string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r.Git(t, dir, "add", file)
		r.Git(t, dir, "commit", "--quiet", "-m", subject)
		return r.Head(t, dir)
	}
	ctx := context.Background()
	const id = "sty_mine0001"

	t.Run("empty range", func(t *testing.T) {
		own, foreign := splitRangeByTrunk(ctx, r.Subject, base, id)
		if len(own) != 0 || foreign {
			t.Fatalf("own=%v foreign=%v", own, foreign)
		}
	})

	// A commit another story published, brought in by a fast-forward.
	commit(r.Pusher, "other.go", "feat: other (sty_other)")
	r.Git(t, r.Pusher, "push", "--quiet", "origin", "main")
	r.Git(t, r.Subject, "pull", "--quiet", "--ff-only")
	mine := commit(r.Subject, "mine.go", "fix: mine ("+id+")")
	unpushed := commit(r.Subject, "wip.go", "wip, no story named")

	t.Run("published and naming another story is foreign; unpublished is own", func(t *testing.T) {
		own, foreign := splitRangeByTrunk(ctx, r.Subject, base, id)
		if !foreign || strings.Join(own, ",") != mine+","+unpushed {
			t.Fatalf("own=%v foreign=%v, want [%s %s] true", own, foreign, mine, unpushed)
		}
	})

	t.Run("a commit naming the story stays when the trunk holds it", func(t *testing.T) {
		r.Git(t, r.Subject, "push", "--quiet", "origin", "main")
		own, foreign := splitRangeByTrunk(ctx, r.Subject, base, id)
		// Both commits are now published; only the one naming the story stays.
		if !foreign || strings.Join(own, ",") != mine {
			t.Fatalf("own=%v foreign=%v, want [%s] true", own, foreign, mine)
		}
	})

	t.Run("no remote trunk to compare with", func(t *testing.T) {
		r.Git(t, r.Subject, "remote", "remove", "origin")
		own, foreign := splitRangeByTrunk(ctx, r.Subject, base, id)
		if len(own) != 0 || foreign {
			t.Fatalf("own=%v foreign=%v, want the whole-range diff left to gitDiffSince", own, foreign)
		}
	})
}
