package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// `satelle trunk publish` through the real command (sty_6af229f1): the repo
// under test is the subject clone of a local bare remote, another clone plays
// the machine that pushed first, and the proof is the repo's [trunk] prove.

func TestTrunkPublishRecordsTheCombinedHeadOnTheStory(t *testing.T) {
	slices := map[string]func(testutil.TrunkRepos, *testing.T){
		"linear": func(r testutil.TrunkRepos, t *testing.T) { r.LinearSlice(t) },
		"epic":   func(r testutil.TrunkRepos, t *testing.T) { r.EpicSlice(t) },
	}
	for name, build := range slices {
		t.Run(name, func(t *testing.T) {
			seen := filepath.Join(t.TempDir(), "proved-sha")
			prove := filepath.Join(t.TempDir(), "prove.sh")
			body := fmt.Sprintf("test -f other.txt || exit 1\ngit rev-parse HEAD > %s\n", seen)
			if err := os.WriteFile(prove, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			repo, r, id := trunkEngageRepo(t, fmt.Sprintf("[trunk]\nprove = 'sh %s'\npublish_rounds = 2\n", prove))
			build(r, t)
			r.PublishFromPusher(t, "other.txt")

			out, err := runRoot(t, "trunk", "publish", "--story", id, "--json")
			if err != nil {
				t.Fatalf("publish: %v\n%s", err, out)
			}
			head := r.Head(t, repo)
			pushed, combined := jsonField(t, out, "pushed"), jsonField(t, out, "combined")
			if got := r.RemoteHead(t); pushed != got || combined != got || head != got {
				t.Fatalf("pushed=%s combined=%s subject=%s remote=%s, want all equal", pushed, combined, head, got)
			}
			if b, _ := os.ReadFile(seen); strings.TrimSpace(string(b)) != combined {
				t.Errorf("the configured proof ran on %q, want the combined head %s", b, combined)
			}

			led, err := runRoot(t, "ledger", "list", "--story", id)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"trunk_publish", "satelle: trunk published " + pushed, "combined " + combined} {
				if !strings.Contains(led, want) {
					t.Errorf("ledger lacks %q:\n%s", want, led)
				}
			}
		})
	}
}

func TestTrunkPublishFailsNamedWhenNothingProvesTheHead(t *testing.T) {
	repo, r, id := trunkEngageRepo(t, "")
	r.LinearSlice(t)
	before := r.RemoteHead(t)

	out, err := runRoot(t, "trunk", "publish", "--story", id)
	if err == nil || !strings.Contains(err.Error(), "no configured proof") {
		t.Fatalf("publish error = %v, want one naming the missing proof\n%s", err, out)
	}
	if r.RemoteHead(t) != before {
		t.Error("the remote moved though no proof was configured")
	}
	if got := r.Head(t, repo); got == before {
		t.Error("the release commit vanished from the subject")
	}
	led, lerr := runRoot(t, "ledger", "list", "--story", id)
	if lerr != nil || !strings.Contains(led, "trunk_publish") {
		t.Errorf("a failed publish writes no ledger row: %v\n%s", lerr, led)
	}
}
