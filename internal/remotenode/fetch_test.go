package remotenode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode/gittest"
)

// AC5: the node fetches a declared repo at its ref over https into a clone whose
// push URL refuses. The server is a local smart-HTTP git server (git's
// http-backend behind net/http/cgi), so the fetch needs no network beyond it.
func TestFetchReposOverHTTPSIsReadOnly(t *testing.T) {
	g := gittest.New(t)
	dir := t.TempDir()

	got, err := FetchRepos(context.Background(), dir, []remotedef.Repo{{URL: g.URL, Ref: "main"}}, g.Env)
	if err != nil {
		t.Fatalf("FetchRepos: %v", err)
	}
	if len(got) != 1 || got[0].Name != "tool" || got[0].Commit != g.Head || got[0].Ref != "main" {
		t.Fatalf("fetched = %+v, want tool at %s", got, g.Head)
	}
	clone := filepath.Join(dir, "tool")
	if b, _ := os.ReadFile(filepath.Join(clone, "README")); string(b) != "two\n" {
		t.Errorf("working tree README = %q", b)
	}
	if gittest.Git(t, clone, "rev-parse", "HEAD") != g.Head {
		t.Error("the clone is not at the fetched commit")
	}

	// A push refuses, and the served repository is untouched.
	gittest.Git(t, clone, "commit", "-q", "--allow-empty", "-m", "local change")
	cmd := exec.Command("git", "push", "origin", "HEAD:refs/heads/pushed")
	cmd.Dir = clone
	cmd.Env = append(os.Environ(), g.Env...)
	out, perr := cmd.CombinedOutput()
	if perr == nil {
		t.Fatalf("a push from the node's clone must fail:\n%s", out)
	}
	if !strings.Contains(string(out), "refused") {
		t.Errorf("the push must fail on the clone's own refusing push URL, not on the server:\n%s", out)
	}
	if refs := gittest.Git(t, g.Bare, "for-each-ref", "refs/heads/"); strings.Contains(refs, "pushed") {
		t.Errorf("the push reached the served repo:\n%s", refs)
	}
	if pushURL := gittest.Git(t, clone, "remote", "get-url", "--push", "origin"); pushURL != refusedPushURL {
		t.Errorf("push URL = %q, want the refusing one", pushURL)
	}

	// A tag pins an older commit than the branch tip.
	got, err = FetchRepos(context.Background(), t.TempDir(), []remotedef.Repo{{URL: g.URL, Ref: "v1"}}, g.Env)
	if err != nil || len(got) != 1 || got[0].Commit != g.Tagged {
		t.Fatalf("fetching v1 = %+v, %v; want commit %s", got, err, g.Tagged)
	}
}

func TestFetchReposRefusesWhatIsNotPlainHTTPS(t *testing.T) {
	for _, tc := range []struct{ name, url, ref string }{
		{"http", "http://git.example.com/a/b.git", "main"},
		{"ssh", "ssh://git@git.example.com/a/b.git", "main"},
		{"file", "file:///tmp/x.git", "main"},
		{"credential", "https://user:secret@git.example.com/a/b.git", "main"},
		{"option ref", "https://git.example.com/a/b.git", "--upload-pack=x"},
		{"no ref", "https://git.example.com/a/b.git", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := FetchRepos(context.Background(), dir, []remotedef.Repo{{URL: tc.url, Ref: tc.ref}}, nil)
			if err == nil {
				t.Fatal("must refuse")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("the error echoes the credential: %v", err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) > 0 {
				t.Errorf("a refused repo must not create a clone: %v", entries)
			}
		})
	}
}

func TestFetchReposWithNoReposDoesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repos")
	if got, err := FetchRepos(context.Background(), dir, nil, nil); err != nil || got != nil {
		t.Errorf("= %v, %v", got, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("no repos, no directory")
	}
}
