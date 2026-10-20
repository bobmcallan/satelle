package remotenode

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/remotedef"
)

// refusedPushURL is every clone's push URL. No git transport answers to the
// scheme, so a push fails before it reaches any network: a node reads its repos
// and never writes them back.
const refusedPushURL = "refused://this-clone-is-read-only"

// FetchRepos fetches each repo at its ref over https into reposDir/<name> and
// returns what it fetched. The clone has no credential and a push URL that
// refuses. env is added to git's environment (a test's trust roots, a proxy).
func FetchRepos(ctx context.Context, reposDir string, repos []remotedef.Repo, env []string) ([]FetchedRepo, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		return nil, err
	}
	var out []FetchedRepo
	for _, r := range repos {
		got, err := fetchRepo(ctx, reposDir, r, env)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}

func fetchRepo(ctx context.Context, reposDir string, r remotedef.Repo, env []string) (FetchedRepo, error) {
	if u, err := url.Parse(r.URL); err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return FetchedRepo{}, fmt.Errorf("repo %s: only an https URL without a credential is fetched", redactedHost(r.URL))
	}
	if r.Ref == "" || strings.HasPrefix(r.Ref, "-") {
		return FetchedRepo{}, fmt.Errorf("repo %s: %q is not a usable ref", r.URL, r.Ref)
	}
	name := remotedef.RepoName(r.URL)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return FetchedRepo{}, fmt.Errorf("repo %s: no usable directory name", r.URL)
	}
	dest := filepath.Join(reposDir, name)
	if err := os.RemoveAll(dest); err != nil {
		return FetchedRepo{}, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return FetchedRepo{}, err
	}
	steps := [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", r.URL},
		{"fetch", "-q", "--depth", "1", "origin", r.Ref},
		{"-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"},
		{"remote", "set-url", "--push", "origin", refusedPushURL},
	}
	for _, step := range steps {
		if _, err := git(ctx, dest, env, step...); err != nil {
			return FetchedRepo{}, fmt.Errorf("repo %s at %s: git %s: %w", r.URL, r.Ref, step[0], err)
		}
	}
	commit, err := git(ctx, dest, env, "rev-parse", "HEAD")
	if err != nil {
		return FetchedRepo{}, fmt.Errorf("repo %s: %w", r.URL, err)
	}
	return FetchedRepo{Name: name, URL: r.URL, Ref: r.Ref, Commit: strings.TrimSpace(commit)}, nil
}

// git runs one git command in dir, over https only and never prompting.
func git(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	full := append([]string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append(env, "GIT_TERMINAL_PROMPT=0")...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// redactedHost names a URL by host alone, so a malformed one that carried a
// credential is never echoed.
func redactedHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "(unparseable url)"
}
