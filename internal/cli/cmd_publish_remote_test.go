package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publishTeamRepo is a repo bound to the team workspace "Acme", signed in to a
// fake publish server, so a publish verb reaches the remote refusal rather than
// failing on workspace resolution.
func publishTeamRepo(t *testing.T) (repo string, serverURL string, store *fakePublishStore) {
	t.Helper()
	ts, store := newFakePublishServerStore(t)
	seedCred(t, ts.URL)
	repo = syncConfigRepo(t, "[hosted]\nworkspace = \"Acme\"\n")
	pointAt(t, repo)
	return repo, ts.URL, store
}

const remotePushPointer = `satelle remote publish`
const remoteAdoptPointer = `satelle remote fetch <name> --version N`

func TestPublishPushRefusesKindRemote(t *testing.T) {
	repo, url, store := publishTeamRepo(t)
	// The file does not exist: a refusal that came before any read says so.
	cmd, _ := testCmd()
	err := runPublishPush(cmd, url, "", "remote", "", false, []string{"skills/x.md"})
	if err == nil || !strings.Contains(err.Error(), remotePushPointer) {
		t.Fatalf("--kind remote must be refused with a pointer to %q, got: %v", remotePushPointer, err)
	}
	if got := store.requests.Load(); got != 0 {
		t.Errorf("refusal made %d request(s)", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "adoptions.json")); !os.IsNotExist(err) {
		t.Errorf("nothing may be recorded: %v", err)
	}
}

func TestPublishPushRefusesRemotesPath(t *testing.T) {
	_, url, store := publishTeamRepo(t)
	for _, p := range []string{"config/remotes/x/remote.toml", ".satelle/config/remotes/x", "config/remotes"} {
		// None of these files exist: a refusal that came before any read says so.
		cmd, _ := testCmd()
		err := runPublishPush(cmd, url, "", "", "", false, []string{p})
		if err == nil || !strings.Contains(err.Error(), remotePushPointer) {
			t.Errorf("%s must be refused with a pointer to %q, got: %v", p, remotePushPointer, err)
		}
	}
	if got := store.requests.Load(); got != 0 {
		t.Errorf("refusal made %d request(s)", got)
	}
}

func TestPublishAdoptRefusesRemotesPath(t *testing.T) {
	repo, url, store := publishTeamRepo(t)
	cmd, _ := testCmd()
	err := runPublishAdopt(cmd, url, "", 0, "config/remotes/x")
	if err == nil || !strings.Contains(err.Error(), remoteAdoptPointer) {
		t.Fatalf("a remotes path must be refused with a pointer to %q, got: %v", remoteAdoptPointer, err)
	}
	if got := store.requests.Load(); got != 0 {
		t.Errorf("path refusal made %d request(s), want 0", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "config")); !os.IsNotExist(err) {
		t.Errorf("no local file may be written: %v", err)
	}
}

func TestPublishAdoptRefusesKindRemote(t *testing.T) {
	repo, url, store := publishTeamRepo(t)
	// A remote published at an ordinary path: only its kind gives it away.
	store.put(fakeTeamWS, "skills/x.md", "remote", "x", "user-1", []byte(`{"format":1,"files":[]}`))
	cmd, _ := testCmd()
	err := runPublishAdopt(cmd, url, "", 0, "skills/x.md")
	if err == nil || !strings.Contains(err.Error(), remoteAdoptPointer) {
		t.Fatalf("kind remote must be refused with a pointer to %q, got: %v", remoteAdoptPointer, err)
	}
	if got := store.requests.Load(); got == 0 {
		t.Error("the kind is only known after the fetch, so the content must have been requested")
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "skills", "x.md")); !os.IsNotExist(err) {
		t.Errorf("no local file may be written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "adoptions.json")); !os.IsNotExist(err) {
		t.Errorf("no adoption may be recorded: %v", err)
	}
}
