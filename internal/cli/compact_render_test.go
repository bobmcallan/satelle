package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/compact"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/retrieve"
	"github.com/bobmcallan/satelle/internal/store"
)

// appendOutputConfig writes an [output] table into the repo's satelle.toml
// (tempRepo already seeded [review]; TOML tables append cleanly as long as
// they don't repeat a key — output has none in common with review).
func appendOutputConfig(t *testing.T, repo, body string) {
	t.Helper()
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	existing, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, append(existing, []byte(body)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createStory(t *testing.T, title string) string {
	t.Helper()
	out, err := runRoot(t, "story", "create", "--title", title, "--status", "backlog")
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	return created.ID
}

var compactHeaderRE = regexp.MustCompile(`^\[\d+\]\{`)

func isCompactTable(s string) bool {
	return compactHeaderRE.MatchString(strings.TrimSpace(s))
}

// TestCompactModeMatrix (sty_75b76691 AC4): the four inputs to compactRequested
// — verb membership, --compact, compact_for_agents + agent env, --json —
// combine exactly as the plan describes.
func TestCompactModeMatrix(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, "\n[output]\ncompact_commands = [\"ledger-list\"]\n")

	id := createStory(t, "compact matrix fixture")
	for i := 0; i < 3; i++ {
		if out, err := runRoot(t, "ledger", "append", "--story", id, "--kind", "note", "--body", "entry"); err != nil {
			t.Fatalf("ledger append: %v\n%s", err, out)
		}
	}

	// 1. No flags, compact_for_agents unset (false) and no agent env: plain JSON.
	t.Setenv("SATELLE_SCRATCH", "")
	t.Setenv("CLAUDECODE", "")
	out, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "note")
	if err != nil {
		t.Fatalf("ledger list: %v\n%s", err, out)
	}
	if isCompactTable(out) {
		t.Errorf("default (no flags, no agent env) rendered compact, want plain JSON:\n%s", out)
	}

	// 2. --compact forces it even with no agent env.
	out, err = runRoot(t, "ledger", "list", "--story", id, "--kind", "note", "--compact")
	if err != nil {
		t.Fatalf("ledger list --compact: %v\n%s", err, out)
	}
	if !isCompactTable(out) {
		t.Errorf("--compact did not render a table header:\n%s", out)
	}

	// 3. --compact on a verb NOT in compact_commands never renders compact.
	out, err = runRoot(t, "story", "list", "--compact")
	if err != nil {
		t.Fatalf("story list --compact: %v\n%s", err, out)
	}
	if isCompactTable(out) {
		t.Errorf("--compact rendered a table for a non-configured verb (story-list):\n%s", out)
	}

	// 4. --json always wins, even over --compact.
	out, err = runRoot(t, "ledger", "list", "--story", id, "--kind", "note", "--compact", "--json")
	if err != nil {
		t.Fatalf("ledger list --compact --json: %v\n%s", err, out)
	}
	if isCompactTable(out) {
		t.Errorf("--json did not override --compact:\n%s", out)
	}
}

// TestCompactForAgentsDefaultsOnForAgentEnv (sty_75b76691 AC4): with
// compact_for_agents = true, an agent-looking caller (SATELLE_SCRATCH set)
// gets compact mode with no flag; a caller with neither agent env still gets
// plain JSON.
func TestCompactForAgentsDefaultsOnForAgentEnv(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, "\n[output]\ncompact_for_agents = true\ncompact_commands = [\"ledger-list\"]\n")

	id := createStory(t, "compact agent-default fixture")
	for i := 0; i < 3; i++ {
		if out, err := runRoot(t, "ledger", "append", "--story", id, "--kind", "note", "--body", "entry"); err != nil {
			t.Fatalf("ledger append: %v\n%s", err, out)
		}
	}

	t.Setenv("SATELLE_SCRATCH", "")
	t.Setenv("CLAUDECODE", "")
	out, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "note")
	if err != nil {
		t.Fatalf("ledger list: %v\n%s", err, out)
	}
	if isCompactTable(out) {
		t.Errorf("compact_for_agents must not compact a non-agent caller:\n%s", out)
	}

	t.Setenv("SATELLE_SCRATCH", t.TempDir())
	out, err = runRoot(t, "ledger", "list", "--story", id, "--kind", "note")
	if err != nil {
		t.Fatalf("ledger list (agent env): %v\n%s", err, out)
	}
	if !isCompactTable(out) {
		t.Errorf("compact_for_agents + SATELLE_SCRATCH did not render compact:\n%s", out)
	}
}

// assertCompactRoundTrip runs plainArgs and compactArgs (identical except for
// the JSON/compact flag), decodes the compact table, and asserts it parses
// back into EXACTLY the plain records (sty_75b76691 AC1) via json.Number-aware
// reflect.DeepEqual — the same comparison internal/compact's own table tests
// use. wantMin bounds the fixture down (a suite run may add its own ledger
// rows alongside a hand-seeded count). ignoreKeys are dropped from every
// record before comparing — for a field this package's own test harness
// cannot hold stable across the suite (see TestCompactStoryListRoundTrips).
func assertCompactRoundTrip(t *testing.T, wantMin int, ignoreKeys []string, plainArgs, compactArgs []string) {
	t.Helper()
	plain, err := runRoot(t, plainArgs...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", plainArgs, err, plain)
	}
	compactOut, err := runRoot(t, compactArgs...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", compactArgs, err, compactOut)
	}
	if !isCompactTable(compactOut) {
		t.Fatalf("%v did not render a compact table:\n%s", compactArgs, compactOut)
	}
	if len(compactOut) >= len(plain) {
		t.Errorf("compact output (%d bytes) not smaller than plain (%d bytes)", len(compactOut), len(plain))
	}

	decoded, err := compact.DecodeTable(strings.TrimRight(compactOut, "\n"), nil)
	if err != nil {
		t.Fatalf("DecodeTable: %v\n%s", err, compactOut)
	}
	var want, got []map[string]any
	if err := json.Unmarshal([]byte(plain), &want); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("unmarshal decoded: %v", err)
	}
	if len(want) < wantMin {
		t.Fatalf("fixture too small: got %d plain records, want at least %d", len(want), wantMin)
	}
	for _, rec := range want {
		for _, k := range ignoreKeys {
			delete(rec, k)
		}
	}
	for _, rec := range got {
		for _, k := range ignoreKeys {
			delete(rec, k)
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v", want, got)
	}
}

// TestCompactLedgerListRoundTrips (sty_75b76691 AC1).
func TestCompactLedgerListRoundTrips(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, "\n[output]\ncompact_commands = [\"ledger-list\"]\n")

	id := createStory(t, "compact round-trip fixture")
	for i := 0; i < 4; i++ {
		if out, err := runRoot(t, "ledger", "append", "--story", id, "--kind", "note", "--body", "entry"); err != nil {
			t.Fatalf("ledger append: %v\n%s", err, out)
		}
	}
	assertCompactRoundTrip(t, 4, nil,
		[]string{"ledger", "list", "--story", id, "--kind", "note", "--json"},
		[]string{"ledger", "list", "--story", id, "--kind", "note", "--compact"})
}

// TestCompactStoryListRoundTrips (sty_75b76691 AC1) proves the round-trip
// directly against internal/compact on `story list`'s REAL response shape,
// rather than through the CLI's --compact flag: pflag's stringSliceValue.Set
// APPENDS once its private `changed` bit is set, and stays set for the
// process lifetime, so runRoot's resetFlagState (bootstrap_test.go) — which
// resets every command's flags by re-Setting each to its DefValue — cannot
// actually restore a StringSliceVar like `story create`'s --tags to empty; it
// keeps appending "[]" (DefValue parsed as one CSV field) across the WHOLE
// cli package's test run. That pre-existing, order-dependent test-harness
// quirk can inflate "tags" enough that the table fold's own "only when
// smaller" guard (AC3) correctly declines — a false negative for THIS test's
// purpose, not a compact-rendering defect. Going straight at EncodeTable/
// DecodeTable with the CLI's actual output sidesteps the flakiness while
// still proving the claim against real data.
func TestCompactStoryListRoundTrips(t *testing.T) {
	repo := tempRepo(t)
	_ = repo

	for i := 0; i < 3; i++ {
		out, err := runRoot(t, "story", "create", "--title", "story list fixture", "--status", "backlog")
		if err != nil {
			t.Fatalf("story create: %v\n%s", err, out)
		}
	}
	plain, err := runRoot(t, "story", "list", "--status", "backlog", "--json")
	if err != nil {
		t.Fatalf("story list --json: %v\n%s", err, plain)
	}

	enc, ok := compact.EncodeTable(json.RawMessage(plain), 200, nil)
	if !ok {
		t.Fatalf("EncodeTable: story-list response is not a foldable uniform array:\n%s", plain)
	}
	dec, err := compact.DecodeTable(enc, nil)
	if err != nil {
		t.Fatalf("DecodeTable: %v\nenc=%s", err, enc)
	}
	var want, got []map[string]any
	if err := json.Unmarshal([]byte(plain), &want); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if err := json.Unmarshal(dec, &got); err != nil {
		t.Fatalf("unmarshal decoded: %v", err)
	}
	if len(want) < 3 {
		t.Fatalf("fixture too small: got %d records, want at least 3", len(want))
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v", want, got)
	}
}

// TestCompactStoryDocsRoundTrips (sty_75b76691 AC1).
func TestCompactStoryDocsRoundTrips(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, "\n[output]\ncompact_commands = [\"story-doc-list\"]\n")

	id := createStory(t, "story docs round-trip fixture")
	for _, name := range []string{"plan", "step-summary-a", "step-summary-b"} {
		if out, err := runRoot(t, "story", "attach", id, "--name", name, "--type", "output", "--body", "content for "+name); err != nil {
			t.Fatalf("story attach %s: %v\n%s", name, err, out)
		}
	}
	assertCompactRoundTrip(t, 3, nil,
		[]string{"story", "docs", id, "--json"},
		[]string{"story", "docs", id, "--compact"})
}

// TestCompactStoryMessagesRoundTrips (sty_75b76691 AC1).
func TestCompactStoryMessagesRoundTrips(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, "\n[output]\ncompact_commands = [\"story-messages\"]\n")

	id := createStory(t, "story messages round-trip fixture")
	for i := 0; i < 3; i++ {
		if out, err := runRoot(t, "story", "message", id, "--from", "executor", "--body", "status update"); err != nil {
			t.Fatalf("story message: %v\n%s", err, out)
		}
	}
	assertCompactRoundTrip(t, 3, nil,
		[]string{"story", "messages", id, "--json"},
		[]string{"story", "messages", id, "--compact"})
}

// TestCompactStoryDiffOffloadsNoiseAndRetrieves (sty_75b76691 AC2): a
// story-diff patch containing an "index" line and a noise-matched section
// (go.sum, configured via [output] noise_patterns) compacts to drop the index
// line and offload the noise hunk behind a marker that `satelle retrieve`
// resolves to the exact original bytes.
func TestCompactStoryDiffOffloadsNoiseAndRetrieves(t *testing.T) {
	repo := tempRepo(t)
	appendOutputConfig(t, repo, "\n[output]\nnoise_patterns = [\"go.sum\"]\n")

	id := createStory(t, "diff compact fixture")

	patch := "diff --git a/go.sum b/go.sum\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/go.sum\n" +
		"+++ b/go.sum\n" +
		"@@ -1,4 +1,4 @@\n" +
		"-github.com/example/one v1.0.0 h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n" +
		"-github.com/example/two v2.0.0 h1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=\n" +
		"+github.com/example/one v1.0.1 h1:cccccccccccccccccccccccccccccccccccccccccccccccc=\n" +
		"+github.com/example/two v2.0.1 h1:dddddddddddddddddddddddddddddddddddddddddddddddd=\n"

	raw, err := json.Marshal(map[string]any{
		"story_id":     id,
		"baseline_sha": "deadbeef",
		"files":        []string{"go.sum"},
		"stat":         "1 file changed",
		"patch":        patch,
	})
	if err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	off := retrieveAdapter{ctx: context.Background(), store: db.Retrieve, storyID: id}

	var cfg config.OutputConfig
	cfg.NoisePatterns = []string{"go.sum"}
	out, ok := renderCompactDiff(cfg, raw, off)
	if !ok {
		t.Fatalf("renderCompactDiff: did not fold")
	}
	if strings.Contains(out, "\nindex ") {
		t.Errorf("compact diff still carries an index line:\n%s", out)
	}
	if strings.Contains(out, "example/one v1.0.1") {
		t.Errorf("compact diff left the noise hunk body inline:\n%s", out)
	}

	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 {
		t.Fatalf("want exactly one offload marker, got %d in:\n%s", len(hashes), out)
	}

	got, err := runRoot(t, "retrieve", hashes[0])
	if err != nil {
		t.Fatalf("retrieve %s: %v\n%s", hashes[0], err, got)
	}
	if !strings.Contains(got, "example/one v1.0.1") {
		t.Errorf("satelle retrieve did not return the exact original hunk: %q", got)
	}
}

// rankFixturePatch builds a 10-file patch where each file's changed-line
// count strictly decreases (f0 the most, f9 the fewest) and every changed
// line carries enough padding that dropping the 8 smallest files under a
// MaxFiles=2 cap shrinks the patch by far more than the CLI's JSON-wrapping
// overhead — renderCompactDiff only folds when the result is provably
// smaller (AC3 shares that guard with order-3 noise-stripping).
func rankFixturePatch() string {
	sec := func(name string, n int) string {
		var b strings.Builder
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n", name, name, name, name, n, n)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "+%s line %d padded with enough filler text to make dropping this file matter\n", name, i)
		}
		return b.String()
	}
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString(sec(fmt.Sprintf("f%d.go", i), 20-2*i))
	}
	return b.String()
}

// TestCompactStoryDiffRanksWhenEnabledAndOffloadsDroppedFile (sty_918e2086
// AC3): with [output.diff_rank] enabled, renderCompactDiff applies ranking
// after noise-stripping — a file beyond max_files is dropped behind a marker
// `satelle retrieve` resolves to its exact original section.
func TestCompactStoryDiffRanksWhenEnabledAndOffloadsDroppedFile(t *testing.T) {
	tempRepo(t)
	id := createStory(t, "diff rank fixture")
	patch := rankFixturePatch()

	files := make([]string, 10)
	for i := range files {
		files[i] = fmt.Sprintf("f%d.go", i)
	}
	raw, err := json.Marshal(map[string]any{
		"story_id":     id,
		"baseline_sha": "deadbeef",
		"files":        files,
		"stat":         "10 files changed",
		"patch":        patch,
	})
	if err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	off := retrieveAdapter{ctx: context.Background(), store: db.Retrieve, storyID: id}

	var cfg config.OutputConfig
	cfg.DiffRank = config.DiffRankConfig{
		Enabled:          true,
		PassthroughLines: 0,
		MaxFiles:         2,
		MaxHunksPerFile:  5,
	}
	out, ok := renderCompactDiff(cfg, raw, off)
	if !ok {
		t.Fatalf("renderCompactDiff: did not fold")
	}
	if !strings.Contains(out, "f0.go") || !strings.Contains(out, "f1.go") {
		t.Errorf("ranked output missing kept files:\n%s", out)
	}
	for i := 2; i < 10; i++ {
		if strings.Contains(out, fmt.Sprintf("f%d.go line 0", i)) {
			t.Errorf("ranked output still carries dropped file f%d.go's body inline:\n%s", i, out)
		}
	}
	hashes := retrieve.FindHashes(out)
	if len(hashes) != 8 {
		t.Fatalf("want one offload marker per dropped file (8), got %d in:\n%s", len(hashes), out)
	}
	f9Idx := strings.Index(out, "dropped file f9.go")
	if f9Idx < 0 {
		t.Fatalf("no dropped-file marker for f9.go in:\n%s", out)
	}
	f9Hash := retrieve.FindHashes(out[f9Idx:])[0]
	got, err := runRoot(t, "retrieve", f9Hash)
	if err != nil {
		t.Fatalf("retrieve %s: %v\n%s", f9Hash, err, got)
	}
	if !strings.Contains(got, "f9.go line 0") || !strings.Contains(got, "f9.go line 1") {
		t.Errorf("satelle retrieve did not return the exact dropped file section: %q", got)
	}

	// With ranking disabled (the zero value — see TestCompactStoryDiffOffloadsNoiseAndRetrieves,
	// which uses the same zero-value DiffRank and folds on noise-stripping
	// alone), RankPatch never runs — order-3 noise-stripping is the only
	// reducer, matching this story's "disabled = today's behaviour" contract.
	var disabledCfg config.OutputConfig
	if disabledCfg.DiffRank.Enabled {
		t.Fatal("zero-value OutputConfig must have ranking disabled")
	}
}

// TestStoryDiffFullFlagBypassesCompactLikeJSON (sty_918e2086 AC3): --full
// skips compact rendering (noise-strip AND ranking) the same way --json
// always has, so its output is the raw verb response, byte-identical either
// way.
func TestStoryDiffFullFlagBypassesCompactLikeJSON(t *testing.T) {
	raw := json.RawMessage(`{"patch":"diff --git a/x.go b/x.go\nindex 111..222 100644\n+hello\n","files":["x.go"]}`)

	newCmd := func(flag string) (*cobra.Command, *bytes.Buffer) {
		cmd := &cobra.Command{Use: "diff"}
		cmd.Flags().Bool("json", false, "")
		cmd.Flags().Bool("full", false, "")
		cmd.Flags().Bool("compact", false, "")
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		if flag != "" {
			if err := cmd.Flags().Set(flag, "true"); err != nil {
				t.Fatal(err)
			}
		}
		return cmd, &buf
	}

	jsonCmd, jsonBuf := newCmd("json")
	if err := renderResponse(jsonCmd, "story-diff", "", raw); err != nil {
		t.Fatalf("renderResponse --json: %v", err)
	}
	fullCmd, fullBuf := newCmd("full")
	if err := renderResponse(fullCmd, "story-diff", "", raw); err != nil {
		t.Fatalf("renderResponse --full: %v", err)
	}
	if jsonBuf.String() != fullBuf.String() {
		t.Errorf("--full output differs from --json output:\n--json: %s\n--full: %s", jsonBuf.String(), fullBuf.String())
	}

	var want bytes.Buffer
	plainCmd, _ := newCmd("")
	plainCmd.SetOut(&want)
	if err := printJSON(plainCmd, raw); err != nil {
		t.Fatal(err)
	}
	if fullBuf.String() != want.String() {
		t.Errorf("--full output is not the raw verb response:\ngot:  %s\nwant: %s", fullBuf.String(), want.String())
	}
}
