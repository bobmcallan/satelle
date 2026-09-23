package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/retrieve"
	"github.com/bobmcallan/satelle/internal/store"
)

// TestCheckLogCompressorRetrieves (sty_ef930f81 AC2): checkLogCompressor(a)
// reduces a long failing-check log through the SAME retrieval store every
// other offload path shares, and `satelle retrieve <hash>` on its footer
// marker resolves back to the exact original log.
func TestCheckLogCompressorRetrieves(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, "\n[output.check_log]\n"+
		"enabled = true\n"+
		"passthrough_lines = 10\n"+
		"context_lines = 1\n"+
		"head_lines = 2\n"+
		"tail_lines = 2\n"+
		"keep_patterns = [\"^--- FAIL\"]\n")

	id := createStory(t, "check-log compressor fixture")

	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("noise line\n")
	}
	b.WriteString("--- FAIL: TestSomething (0.00s)\n")
	for i := 0; i < 20; i++ {
		b.WriteString("more noise\n")
	}
	log := b.String()

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	cfgPath := repo + "/.satelle/satelle.toml"
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	a := &app.App{Config: cfg, Store: db}
	compress := checkLogCompressor(a)
	out := compress(context.Background(), id, log)

	if !strings.Contains(out, "--- FAIL: TestSomething (0.00s)") {
		t.Fatalf("compressed notes dropped the failure line:\n%s", out)
	}
	if strings.Count(out, "noise line") == 20 {
		t.Errorf("compression did not reduce the noise at all:\n%s", out)
	}

	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 {
		t.Fatalf("want exactly one retrieval marker, got %d in:\n%s", len(hashes), out)
	}

	got, err := runRoot(t, "retrieve", hashes[0])
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if !strings.Contains(got, log) {
		t.Errorf("retrieve did not return the exact original log:\ngot: %q\nwant substring: %q", got, log)
	}
}
