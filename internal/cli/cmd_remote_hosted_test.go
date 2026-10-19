package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// remoteTeamRepo is a repo bound to the team workspace "Acme" on a fake publish
// server, signed in, with the cwd set to it.
func remoteTeamRepo(t *testing.T) (repo string, ts *httptest.Server, store *fakePublishStore) {
	t.Helper()
	repo = tempRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"),
		[]byte("[review]\ngate_create = false\n[hosted]\nworkspace = \"Acme\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, store = newFakePublishServerStore(t)
	seedCred(t, ts.URL)
	t.Chdir(repo)
	return repo, ts, store
}

// fakeTeamWS is the id the fake server gives the workspace named "Acme".
const fakeTeamWS = "ws-team"

func remoteRun(t *testing.T, ts *httptest.Server, args ...string) string {
	t.Helper()
	out, err := runRoot(t, append(args, "--server", ts.URL)...)
	if err != nil {
		t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func remoteDir(repo, name string) string {
	return filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), name)
}

// readTree returns every file under dir by slash path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func relabel(t *testing.T, repo, name, to string) string {
	t.Helper()
	p := filepath.Join(remoteDir(repo, name), "remote.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(string(b), `label = "your-host-label"`, `label = "`+to+`"`, 1)
	if err := os.WriteFile(p, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
	return next
}

func TestRemotePublish(t *testing.T) {
	repo, ts, store := remoteTeamRepo(t)
	if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatal(err)
	}
	out := remoteRun(t, ts, "remote", "publish", "edge")
	if !strings.Contains(out, "published config/remotes/edge v1") {
		t.Errorf("publish must print the new version, got:\n%s", out)
	}
	pv, n, ok := store.get(fakeTeamWS, "config/remotes/edge", 0)
	if !ok || n != 1 || pv.kind != "remote" {
		t.Fatalf("PUT: ok=%v version=%d kind=%q", ok, n, pv.kind)
	}
	if want, err := remotedef.Bundle(remoteDir(repo, "edge")); err != nil || string(pv.content) != string(want) {
		t.Errorf("published bytes are not the definition's bundle (err=%v)", err)
	}
}

func TestRemotePublishRefusesInvalid(t *testing.T) {
	repo, ts, store := remoteTeamRepo(t)
	if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(remoteDir(repo, "edge"), "remote.toml")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(b, []byte("\n[host.env]\nAPI_TOKEN = \"hunter2\"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	validateOut, verr := runRoot(t, "remote", "validate", "edge")
	if verr == nil || !strings.Contains(validateOut, "FAIL") {
		t.Fatalf("a credential value must fail validation:\n%s", validateOut)
	}

	out, err := runRoot(t, "remote", "publish", "edge", "--server", ts.URL)
	if err == nil {
		t.Fatalf("publish must exit non-zero:\n%s", out)
	}
	if out != validateOut {
		t.Errorf("publish must print validate's findings.\nvalidate:\n%s\npublish:\n%s", validateOut, out)
	}
	if got := store.requests.Load(); got != 0 {
		t.Errorf("a refused definition reached the network: %d request(s)", got)
	}
}

func TestRemotePublishFetchRoundTrip(t *testing.T) {
	repo, ts, _ := remoteTeamRepo(t)
	if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(remoteDir(repo, "edge"), "skills", "remote-work.md")
	b, _ := os.ReadFile(skill)
	if err := os.WriteFile(skill, append(b, []byte("\nbytes: é — ✓\r\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	remoteRun(t, ts, "remote", "publish", "edge")

	if out := remoteRun(t, ts, "remote", "list"); !strings.Contains(out, "edge\tv1\tuser-1") {
		t.Errorf("list must show the remote with its latest version:\n%s", out)
	}

	into := filepath.Join(t.TempDir(), "fetched")
	remoteRun(t, ts, "remote", "fetch", "edge", "--version", "1", "--into", into)
	want, got := readTree(t, remoteDir(repo, "edge")), readTree(t, into)
	if len(want) == 0 {
		t.Fatal("the source remote has no files")
	}
	if len(got) != len(want) {
		t.Fatalf("fetched %d file(s), want %d", len(got), len(want))
	}
	for rel, w := range want {
		if got[rel] != w {
			t.Errorf("%s differs after the round trip", rel)
		}
	}

	if _, err := runRoot(t, "remote", "fetch", "edge", "--into", into, "--server", ts.URL); err == nil {
		t.Error("fetch must refuse a non-empty target without --force")
	}
	remoteRun(t, ts, "remote", "fetch", "edge", "--into", into, "--force")
}

func TestRemoteVersions(t *testing.T) {
	repo, ts, store := remoteTeamRepo(t)
	if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatal(err)
	}
	original := readTree(t, remoteDir(repo, "edge"))
	remoteRun(t, ts, "remote", "publish", "edge")

	if out := remoteRun(t, ts, "remote", "publish", "edge"); !strings.Contains(out, "unchanged at v1") {
		t.Errorf("unchanged content must report the existing version:\n%s", out)
	}
	if _, n, _ := store.get(fakeTeamWS, "config/remotes/edge", 0); n != 1 {
		t.Fatalf("unchanged content created a version: head is v%d", n)
	}

	relabel(t, repo, "edge", "second-label")
	if out := remoteRun(t, ts, "remote", "publish", "edge"); !strings.Contains(out, "published config/remotes/edge v2") {
		t.Errorf("changed content must create v2:\n%s", out)
	}

	into := filepath.Join(t.TempDir(), "v1")
	remoteRun(t, ts, "remote", "fetch", "edge", "--version", "1", "--into", into)
	got := readTree(t, into)
	for rel, w := range original {
		if got[rel] != w {
			t.Errorf("v1 fetched after v2 exists differs in %s", rel)
		}
	}
}

func TestRemoteFetchRecordsAdoption(t *testing.T) {
	repo, ts, _ := remoteTeamRepo(t)
	dataDir := filepath.Join(repo, ".satelle")
	if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatal(err)
	}
	remoteRun(t, ts, "remote", "publish", "edge")
	bundle, err := remotedef.Bundle(remoteDir(repo, "edge"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bundle)

	remoteRun(t, ts, "remote", "fetch", "edge", "--version", "1", "--into", filepath.Join(t.TempDir(), "x"))
	recs, err := loadAdoptions(dataDir)
	if err != nil || len(recs) != 1 {
		t.Fatalf("adoption records: %+v err=%v", recs, err)
	}
	r := recs[0]
	if r.Workspace != "Acme" || r.Path != "config/remotes/edge" || r.Version != 1 || r.Kind != "remote" || r.BlobSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("record = %+v, want Acme config/remotes/edge v1 remote %x", r, sum)
	}

	// A second publish makes check report it; --update writes the files of the
	// new version (never the bundle as one file) and moves the record.
	v2 := relabel(t, repo, "edge", "second-label")
	remoteRun(t, ts, "remote", "publish", "edge")
	if out := remoteRun(t, ts, "publish", "check"); !strings.Contains(out, "local v1 → remote v2") {
		t.Errorf("check must report the newer remote version:\n%s", out)
	}
	if err := os.RemoveAll(remoteDir(repo, "edge")); err != nil {
		t.Fatal(err)
	}
	remoteRun(t, ts, "publish", "check", "--update")
	if got, _ := os.ReadFile(filepath.Join(remoteDir(repo, "edge"), "remote.toml")); string(got) != v2 {
		t.Errorf("--update must write v2's remote.toml, got:\n%s", got)
	}
	recs, _ = loadAdoptions(dataDir)
	if len(recs) != 1 || recs[0].Version != 2 || recs[0].BlobSHA256 == hex.EncodeToString(sum[:]) {
		t.Errorf("record after update: %+v", recs)
	}
}
