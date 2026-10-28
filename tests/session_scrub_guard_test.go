package tests

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// sessionEnvReaders are the functions that read the running harness's session
// identity from the process environment: agentcli.DetectSessionHarnesses (and
// InLoopHarnessFromEnv, which wraps it), agentcli.SessionIDFromEnv,
// config.ResolveSession and the cli session anchor (anchorFromEnv). Add a new
// env reader here.
var sessionEnvReaders = regexp.MustCompile(`\b(DetectSessionHarnesses|InLoopHarnessFromEnv|SessionIDFromEnv|ResolveSession|anchorFromEnv)\(`)

// TestEveryEnvReadingPackageScrubsInTestMain (sty_a37fa9fe): a package whose
// code or tests read the harness session from the environment must start its
// suite from a bare shell, so the suite gives one answer whichever agent session
// runs it. A new package that starts reading the session without a TestMain
// scrub fails here.
func TestEveryEnvReadingPackageScrubsInTestMain(t *testing.T) {
	// Anchor the repo root to this file, not the working directory: the
	// integration build's TestMain moves the process into a temp dir.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate session_scrub_guard_test.go")
	}
	root := filepath.Dir(filepath.Dir(file))
	self := "session_scrub_guard_test.go"
	readers := map[string]bool{} // directory -> reads the session env
	scrubs := map[string]bool{}  // directory -> a _test.go TestMain scrubs
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".satelle", "testdata", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || (d.Name() == self && filepath.Dir(path) == filepath.Join(root, "tests")) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		dir := filepath.Dir(path)
		if sessionEnvReaders.MatchString(src) {
			readers[dir] = true
		}
		if strings.HasSuffix(path, "_test.go") && strings.Contains(src, "func TestMain(") && strings.Contains(src, "ScrubSessionEnv()") {
			scrubs[dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for dir := range readers {
		if !scrubs[dir] {
			rel, _ := filepath.Rel(root, dir)
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)
	if len(readers) == 0 {
		t.Fatal("found no package reading the session env: the reader list or the walk is wrong")
	}
	for _, rel := range missing {
		t.Errorf("%s reads the harness session from the environment but has no TestMain calling agentcli.ScrubSessionEnv()", rel)
	}
}
