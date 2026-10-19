package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestRemoteLivePublishListFetch repeats publish, list and fetch against a real
// server. It needs satelle-server's `remote` kind, a team workspace the signed-in
// account can publish to, and a stored sign-in, so it runs only when
// SATELLE_LIVE_SERVER and SATELLE_LIVE_WORKSPACE are set.
func TestRemoteLivePublishListFetch(t *testing.T) {
	server, team := os.Getenv("SATELLE_LIVE_SERVER"), os.Getenv("SATELLE_LIVE_WORKSPACE")
	if server == "" || team == "" {
		t.Skip("set SATELLE_LIVE_SERVER and SATELLE_LIVE_WORKSPACE to run against a live team workspace")
	}
	repo := tempRepo(t)
	t.Chdir(repo)
	name := "live-" + strings.ToLower(time.Now().UTC().Format("20060102t150405"))
	run := func(args ...string) string {
		t.Helper()
		out, err := runRoot(t, append(args, "--server", server, "--workspace", team)...)
		if err != nil {
			t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	if _, err := runRoot(t, "remote", "create", name); err != nil {
		t.Fatal(err)
	}
	if out := run("remote", "publish", name); !strings.Contains(out, "config/remotes/"+name) {
		t.Errorf("publish: %s", out)
	}
	if out := run("remote", "list"); !strings.Contains(out, name) {
		t.Errorf("list does not show %s:\n%s", name, out)
	}
	into := filepath.Join(t.TempDir(), "fetched")
	run("remote", "fetch", name, "--version", "1", "--into", into)
	want := readTree(t, filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), name))
	for rel, got := range readTree(t, into) {
		if want[rel] != got {
			t.Errorf("%s differs after the live round trip", rel)
		}
	}
}
