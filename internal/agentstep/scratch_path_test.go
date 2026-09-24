package agentstep

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// Worst-case chromedp user-data-dir socket beneath a scratch dir: MkdirTemp's
// suffix is a uint32, up to 10 digits. Linux sun_path is 108 bytes incl. NUL, so
// 107 is the longest usable path; 91 leaves 16 bytes of headroom.
const (
	chromedpWorstSuffix = "/chromedp-runner4294967295/SingletonSocket"
	socketPathBudget    = 91
)

func TestScratchSocketPathBudget(t *testing.T) {
	for _, tc := range []struct {
		name, story string
		dirLen      int
	}{
		{"story", "sty_0123abcd", 47},
		{"adhoc", "", 49},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(scratchParentIn("/tmp", "/some/repo", tc.story), dispatchID())
			if len(dir) != tc.dirLen {
				t.Errorf("scratch dir %s is %d bytes, want %d", dir, len(dir), tc.dirLen)
			}
			if n := len(dir + chromedpWorstSuffix); n > socketPathBudget {
				t.Errorf("worst-case socket path is %d bytes, budget %d", n, socketPathBudget)
			}
		})
	}
}

func TestDispatchIDShape(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}$`)
	if id := dispatchID(); !re.MatchString(id) {
		t.Errorf("dispatchID %q is not 8 hex", id)
	}
}

func TestNewScratchUniqueIsolated0700(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	repo := "/some/repo"
	seen := map[string]bool{}
	for range 200 {
		dir, err := newScratch(repo, "sty_uniq0001")
		if err != nil {
			t.Fatal(err)
		}
		if seen[dir] {
			t.Fatalf("duplicate scratch dir %s", dir)
		}
		seen[dir] = true
		if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("scratch %s: mode/err = %v %v", dir, fi, err)
		}
	}
	a, _ := newScratch(repo, "sty_iso00001")
	b, _ := newScratch(repo, "sty_iso00002")
	if filepath.Dir(a) == filepath.Dir(b) {
		t.Errorf("different stories share a parent: %s", filepath.Dir(a))
	}
	x, _ := newScratch(repo, "")
	y, _ := newScratch(repo, "")
	if filepath.Dir(x) == filepath.Dir(y) {
		t.Errorf("adhoc dispatches share a leg: %s", filepath.Dir(x))
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(x)), "adhoc-") {
		t.Errorf("adhoc leg = %s", filepath.Dir(x))
	}
	if !strings.HasPrefix(x, os.TempDir()+string(os.PathSeparator)) {
		t.Errorf("scratch %s not under temp root %s", x, os.TempDir())
	}
}

func TestNewScratchRetriesOnCollision(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	repo, story := "/some/repo", "sty_coll0001"
	parent := scratchParentIn(os.TempDir(), repo, story)
	taken := filepath.Join(parent, "deadbeef")
	if err := os.MkdirAll(taken, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taken, "marker"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ids := []string{"deadbeef", "deadbeef", "cafef00d"}
	orig := newDispatchID
	t.Cleanup(func() { newDispatchID = orig })
	newDispatchID = func() string { id := ids[0]; ids = ids[1:]; return id }

	dir, err := newScratch(repo, story)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "cafef00d" {
		t.Errorf("got %s, want retry to cafef00d", dir)
	}
	if _, err := os.Stat(filepath.Join(taken, "marker")); err != nil {
		t.Errorf("existing dispatch dir was disturbed: %v", err)
	}
}

func TestScratchTidyIsSiblingAndSurvivesFinish(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	repo := "/some/repo"
	for _, story := range []string{"sty_sib00001", ""} {
		dir, err := newScratch(repo, story)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(config.ScratchEnv, dir)
		t.Setenv("TMPDIR", dir)
		leg := filepath.Base(filepath.Dir(dir))
		tidy := config.TidyDir(repo, leg)
		if want := filepath.Join(filepath.Dir(dir), "tidy"); tidy != want {
			t.Fatalf("TidyDir = %s, want %s", tidy, want)
		}
		if err := os.MkdirAll(tidy, 0o700); err != nil {
			t.Fatal(err)
		}
		finishScratch(dir, false)
		if _, err := os.Stat(tidy); err != nil {
			t.Errorf("tidy lost with dispatch: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("dispatch dir not removed: %v", err)
		}
		// keep=true leaves it; a later RemoveAll of the kept dir spares tidy.
		kept, _ := newScratch(repo, story)
		finishScratch(kept, true)
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("kept scratch removed: %v", err)
		}
		_ = os.RemoveAll(kept)
		if _, err := os.Stat(tidy); err != nil {
			t.Errorf("cleanup of kept scratch touched tidy: %v", err)
		}
	}
}

func TestScratchChromedpSocketBinds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sun_path budget is a Linux limit")
	}
	if fi, err := os.Stat("/tmp"); err != nil || !fi.IsDir() {
		t.Skip("/tmp unavailable")
	}
	for _, story := range []string{"sty_sock0001", ""} {
		t.Setenv("TMPDIR", "/tmp")
		dir, err := newScratch(t.TempDir(), story)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(dir)) })
		t.Setenv("TMPDIR", dir)
		runner := filepath.Join(os.TempDir(), "chromedp-runner4294967295")
		if err := os.MkdirAll(runner, 0o700); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(runner, "SingletonSocket")
		if sock != dir+chromedpWorstSuffix || len(sock) > socketPathBudget {
			t.Fatalf("socket path %s (%d bytes) is not the AC1-bounded path", sock, len(sock))
		}
		l, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatalf("listen on %s: %v", sock, err)
		}
		l.Close()
	}
}
