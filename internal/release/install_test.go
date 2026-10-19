package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

func readRepoFile(t *testing.T, rel ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, rel...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var urlHost = regexp.MustCompile(`https?://([A-Za-z0-9.-]+)`)

// TestInstallScriptNamesOnlyTheOrigins: the hosts scripts/install.sh builds URLs
// on are exactly Origins — neither a host release does not own, nor an origin
// the script forgot — so the egress derived from this package covers the script.
func TestInstallScriptNamesOnlyTheOrigins(t *testing.T) {
	got := map[string]bool{}
	for _, m := range urlHost.FindAllStringSubmatch(readRepoFile(t, "scripts", "install.sh"), -1) {
		got[m[1]] = true
	}
	var gotList []string
	for h := range got {
		gotList = append(gotList, h)
	}
	sort.Strings(gotList)
	want := Origins()
	sort.Strings(want)
	if strings.Join(gotList, ",") != strings.Join(want, ",") {
		t.Errorf("install.sh names hosts %v, release.Origins is %v", gotList, want)
	}
	all := map[string]bool{}
	for _, h := range Hosts() {
		all[h] = true
	}
	for _, h := range want {
		if !all[h] {
			t.Errorf("Hosts() lacks origin %s", h)
		}
	}
}

// TestUpdateBuildsItsURLsFromThisPackage: cmd_update spells no release host.
func TestUpdateBuildsItsURLsFromThisPackage(t *testing.T) {
	src := readRepoFile(t, "internal", "cli", "cmd_update.go")
	for _, host := range append(Origins(), redirectHosts...) {
		for i, line := range strings.Split(src, "\n") {
			if strings.Contains(line, "://"+host) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("cmd_update.go:%d names %s; build the URL with package release", i+1, host)
			}
		}
	}
	if !strings.Contains(src, "release.DownloadBase(") || !strings.Contains(src, "release.LatestAPI(") || !strings.Contains(src, "release.APIBase(") {
		t.Error("cmd_update.go must build its release URLs with release.DownloadBase, LatestAPI and APIBase")
	}
}

func TestURLBuilders(t *testing.T) {
	if got, want := DownloadBase("o/r"), "https://github.com/o/r/releases/download"; got != want {
		t.Errorf("DownloadBase = %s", got)
	}
	if got, want := LatestAPI("o/r"), "https://api.github.com/repos/o/r/releases/latest"; got != want {
		t.Errorf("LatestAPI = %s", got)
	}
	if got, want := APIBase("o/r"), "https://api.github.com/repos/o/r/releases"; got != want {
		t.Errorf("APIBase = %s", got)
	}
	if got, want := InstallScriptURL("o/r", "", "1.2.3"), "https://github.com/o/r/releases/latest/download/install.sh"; got != want {
		t.Errorf("InstallScriptURL (no base) = %s", got)
	}
	if got, want := InstallScriptURL("o/r", "http://mirror/dl", "1.2.3"), "http://mirror/dl/v1.2.3/install.sh"; got != want {
		t.Errorf("InstallScriptURL (base) = %s", got)
	}
}

// fakeReleases is a release server publishing one CLI release under the asset
// names release.yml gives them: <artifact>-v<ver>-<goos>-<goarch> and the
// <asset>.sha256 beside it. It records every path requested.
type fakeReleases struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []string
}

func newFakeReleases(t *testing.T, version, asset string, shaFor func(asset []byte) string) *fakeReleases {
	t.Helper()
	// The names below are release.yml's; fail loudly if it renames them.
	wf := readRepoFile(t, ".github", "workflows", "release.yml")
	for _, frag := range []string{
		`OUT="${{ matrix.artifact }}-v${VER}-${GOOS}-${GOARCH}"`,
		`sha256sum "${OUT}" > "${OUT}.sha256"`,
	} {
		if !strings.Contains(wf, frag) {
			t.Fatalf("release.yml no longer contains %s; update this fake release server", frag)
		}
	}
	name := fmt.Sprintf("satelle-v%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
	body := []byte(asset)
	f := &fakeReleases{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, r.URL.Path)
		f.mu.Unlock()
		switch r.URL.Path {
		case "/v" + version + "/" + name:
			_, _ = w.Write(body)
		case "/v" + version + "/" + name + ".sha256":
			fmt.Fprintf(w, "%s  %s\n", shaFor(body), name)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeReleases) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hits...)
}

func realSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// runInstall runs scripts/install.sh with args against base, installing into a
// fresh directory it returns.
func runInstall(t *testing.T, base string, args ...string) (installDir, out string, err error) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("install.sh supports linux and darwin")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("install.sh supports amd64 and arm64")
	}
	for _, tool := range []string{"sh", "curl"} {
		if _, lerr := exec.LookPath(tool); lerr != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	script, _ := filepath.Abs(filepath.Join("..", "..", "scripts", "install.sh"))
	installDir = filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), "SATELLE_INSTALL_DIR="+installDir, "SATELLE_RELEASE_BASE="+base, "HOME="+t.TempDir())
	b, err := cmd.CombinedOutput()
	return installDir, string(b), err
}

// TestPinnedInstall: install.sh <version> downloads <base>/v<version>/<asset> and
// its .sha256, nothing else — no release lookup — and installs that exact build.
func TestPinnedInstall(t *testing.T) {
	for _, arg := range []string{"1.2.3", "v1.2.3"} {
		t.Run(arg, func(t *testing.T) {
			f := newFakeReleases(t, "1.2.3", "#!/bin/sh\necho satelle 1.2.3\n", realSum)
			dir, out, err := runInstall(t, f.srv.URL, arg)
			if err != nil {
				t.Fatalf("install.sh %s: %v\n%s", arg, err, out)
			}
			got, rerr := os.ReadFile(filepath.Join(dir, "satelle"))
			if rerr != nil || string(got) != "#!/bin/sh\necho satelle 1.2.3\n" {
				t.Fatalf("installed satelle = %q, %v", got, rerr)
			}
			if fi, _ := os.Stat(filepath.Join(dir, "satelle")); fi == nil || fi.Mode()&0o111 == 0 {
				t.Error("the installed binary must be executable")
			}
			name := fmt.Sprintf("satelle-v1.2.3-%s-%s", runtime.GOOS, runtime.GOARCH)
			want := []string{"/v1.2.3/" + name, "/v1.2.3/" + name + ".sha256"}
			if hits := f.paths(); strings.Join(hits, " ") != strings.Join(want, " ") {
				t.Errorf("requests = %v, want exactly %v (a pin makes no release or API lookup)", hits, want)
			}
			if _, serr := os.Stat(filepath.Join(dir, "satelled")); serr == nil {
				t.Error("a pinned install is the CLI alone")
			}
		})
	}
}

// TestPinnedInstallRefusesAChecksumMismatch installs nothing.
func TestPinnedInstallRefusesAChecksumMismatch(t *testing.T) {
	f := newFakeReleases(t, "1.2.3", "tampered", func([]byte) string { return realSum([]byte("something else")) })
	dir, out, err := runInstall(t, f.srv.URL, "1.2.3")
	if err == nil {
		t.Fatalf("a checksum mismatch must fail:\n%s", out)
	}
	if !strings.Contains(out, "sha256 mismatch") {
		t.Errorf("the refusal must say so:\n%s", out)
	}
	if entries, rerr := os.ReadDir(dir); rerr == nil && len(entries) > 0 {
		t.Errorf("nothing may be installed, found %v", entries)
	}
}

func TestPinnedInstallRefusesAMalformedVersion(t *testing.T) {
	f := newFakeReleases(t, "1.2.3", "x", realSum)
	for _, bad := range []string{"1.2.3;rm", "../x", "latest"} {
		if _, out, err := runInstall(t, f.srv.URL, bad); err == nil {
			t.Errorf("version %q must be refused:\n%s", bad, out)
		}
	}
	if hits := f.paths(); len(hits) != 0 {
		t.Errorf("a refused version must request nothing, got %v", hits)
	}
}
