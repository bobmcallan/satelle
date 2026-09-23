package compact

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// TestPackageCarriesNoLanguagePattern asserts the constitution's "no gate as
// code" / "language-neutral" rule (sty_ef930f81 AC1): this package's SOURCE
// (not its tests) never hardcodes a Go/test-runner pattern (FAIL, panic,
// goroutine, a `file.go:N:M:` literal) — every such pattern arrives through
// LogConfig, authored in internal/config.
func TestPackageCarriesNoLanguagePattern(t *testing.T) {
	b, err := os.ReadFile("logs.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"fail", "panic", "goroutine", ".go:"}
	for i, line := range strings.Split(string(b), "\n") {
		code := line
		if idx := strings.Index(code, "//"); idx >= 0 {
			code = code[:idx]
		}
		lower := strings.ToLower(code)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("logs.go:%d: contains hardcoded pattern %q outside a comment — every language pattern must arrive via LogConfig: %q", i+1, f, line)
			}
		}
	}
}

func TestCompressLog_PassthroughVerbatim(t *testing.T) {
	log := "line 1\nline 2\nline 3\n"
	if got := CompressLog(log, LogConfig{}, newMemStore()); got != log {
		t.Fatalf("passthrough log changed: got %q, want %q", got, log)
	}
}

// TestCompressLog_NoPatterns is AC3: with no patterns configured (and sizes
// left at their zero-value mechanism fallbacks), a log over the passthrough
// threshold is reduced to head + omission marker + tail + footer + marker.
func TestCompressLog_NoPatterns(t *testing.T) {
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, fmt.Sprintf("line %03d", i))
	}
	log := strings.Join(lines, "\n") + "\n"
	off := newMemStore()
	got := CompressLog(log, LogConfig{}, off)

	for i := 0; i < DefaultHeadLines; i++ {
		want := fmt.Sprintf("line %03d", i)
		if !strings.Contains(got, want) {
			t.Errorf("missing head line %q in:\n%s", want, got)
		}
	}
	for i := 500 - DefaultTailLines; i < 500; i++ {
		want := fmt.Sprintf("line %03d", i)
		if !strings.Contains(got, want) {
			t.Errorf("missing tail line %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "lines omitted]") {
		t.Errorf("no omission marker in:\n%s", got)
	}
	if !strings.Contains(got, "log compressed: kept") {
		t.Errorf("no footer in:\n%s", got)
	}
	hash, kind, size, ok := extractMarker(t, got)
	if !ok {
		t.Fatalf("footer carries no retrieval marker: %q", got)
	}
	if kind != "log" || size != len(log) {
		t.Errorf("marker kind/size = %q/%d, want log/%d", kind, size, len(log))
	}
	raw, err := off.Get(hash)
	if err != nil || string(raw) != log {
		t.Errorf("retrieve(%s) = %q, %v — want the exact original log", hash, raw, err)
	}
	// A middle line — evidence a naive tailLines(out, 40) would have kept
	// nothing from the head — must NOT appear literally in the notes.
	if strings.Contains(got, "line 250\n") {
		t.Errorf("middle line unexpectedly survived head/tail compression:\n%s", got)
	}
}

// TestCompressLog_UnwiredEngineFallback mirrors the fallback runCheck uses
// when no compressor is wired (compact.CompressLog(out, LogConfig{}, nil)):
// no offloader at all still yields bounded head/tail notes with no marker.
func TestCompressLog_UnwiredEngineFallback(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("line %03d", i))
	}
	log := strings.Join(lines, "\n") + "\n"
	got := CompressLog(log, LogConfig{}, nil)
	if !strings.Contains(got, "line 000") || !strings.Contains(got, "line 099") {
		t.Fatalf("head/tail fallback missing boundary lines: %q", got)
	}
	if strings.Contains(got, "full log:") {
		t.Errorf("no offloader was given — footer must not carry a marker: %q", got)
	}
}

// goPatterns mirrors this repo's [output.check_log] Go configuration
// (.satelle/satelle.toml, sty_ef930f81) — the SAME values, so a test that
// passes here is evidence the shipped config behaves as documented.
func goPatterns() LogConfig {
	return LogConfig{
		PassthroughLines: 60,
		ContextLines:     3,
		HeadLines:        20,
		TailLines:        20,
		MaxKeptLines:     400,
		KeepPatterns: []string{
			`^--- FAIL`,
			`^FAIL\b`,
			`^panic:`,
			`^fatal error:`,
			`\.go:\d+:\d+:`,
			`^\s+\S+\.go:\d+:`,
			`^OBJECTIVE \d+ FAILED`,
			`^(ok|FAIL)\s+\S+\s+[\d.]+s`,
		},
		WarnPatterns:    []string{`(?i)^warning:`},
		TraceStart:      `^goroutine \d+ \[`,
		TraceFrame:      `^(\S.*\(.*\)|\s+/.*\.go:\d+.*)$`,
		TraceAppFrame:   `github.com/bobmcallan/satelle`,
		TraceKeepFrames: 3,
	}
}

// TestCompressLog_GoPatterns is AC2: with this repo's Go configuration, the
// notes of a failing check always contain every --- FAIL line, every panic:
// with its collapsed goroutine trace (first frames + app frames + a collapse
// marker), every build-error line, plus a correct footer whose marker
// resolves through the same store to the exact original log.
func TestCompressLog_GoPatterns(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "info: doing step %d\n", i)
	}
	b.WriteString("--- FAIL: TestFoo (0.00s)\n")
	b.WriteString("FAIL\n")
	b.WriteString("FAIL\tsomepkg\t0.01s\n")
	b.WriteString("panic: boom: something bad\n")
	b.WriteString("\n")
	b.WriteString("goroutine 1 [running]:\n")
	b.WriteString("testing.tRunner(0x1)\n")                           // frame idx0 — kept (< 3)
	b.WriteString("\t/usr/lib/go/src/testing/testing.go:1000 +0x1\n") // frame idx1 — kept (< 3)
	b.WriteString("testing.tRunner.func1()\n")                        // frame idx2 — kept (< 3)
	// A dropped run long enough to outrun ±3 context from its neighbours on
	// both sides (16 lines > 2*ContextLines), so it genuinely collapses.
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "somepkg.filler%d()\n", i)
		fmt.Fprintf(&b, "\t/somepkg/filler%d.go:%d +0x1\n", i, i)
	}
	b.WriteString("github.com/bobmcallan/satelle/internal/foo.Bar(...)\n")                     // app frame — kept
	b.WriteString("\t/home/build/github.com/bobmcallan/satelle/internal/foo/foo.go:42 +0x1\n") // app frame — kept
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "otherpkg.filler%d()\n", i)
		fmt.Fprintf(&b, "\t/otherpkg/filler%d.go:%d +0x1\n", i, i)
	}
	b.WriteString("./internal/foo/foo.go:10:5: undefined: x\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "info: doing later step %d\n", i)
	}
	log := b.String()

	off := newMemStore()
	got := CompressLog(log, goPatterns(), off)

	for _, want := range []string{
		"--- FAIL: TestFoo (0.00s)",
		"FAIL\tsomepkg\t0.01s",
		"panic: boom: something bad",
		"goroutine 1 [running]:",
		"testing.tRunner(0x1)",
		"/usr/lib/go/src/testing/testing.go:1000 +0x1",
		"testing.tRunner.func1()",
		"github.com/bobmcallan/satelle/internal/foo.Bar(...)",
		"/home/build/github.com/bobmcallan/satelle/internal/foo/foo.go:42 +0x1",
		"./internal/foo/foo.go:10:5: undefined: x",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing always-keep line %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "frames collapsed]") {
		t.Errorf("no trace-collapse marker in:\n%s", got)
	}

	hash, kind, size, ok := extractMarker(t, got)
	if !ok {
		t.Fatalf("footer carries no retrieval marker: %q", got)
	}
	if kind != "log" || size != len(log) {
		t.Errorf("marker kind/size = %q/%d, want log/%d", kind, size, len(log))
	}
	raw, err := off.Get(hash)
	if err != nil || string(raw) != log {
		t.Errorf("retrieve(%s) did not return the exact original log: %v", hash, err)
	}
}

// TestCompressLog_WarnDedup asserts a repeated warn_patterns line survives
// once, annotated with its repeat count, instead of once per occurrence.
func TestCompressLog_WarnDedup(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- FAIL: TestFoo (0.00s)\n")
	for i := 0; i < 80; i++ {
		b.WriteString("warning: deprecated flag -foo\n")
		fmt.Fprintf(&b, "info: step %d\n", i)
	}
	log := b.String()
	got := CompressLog(log, goPatterns(), newMemStore())
	if n := strings.Count(got, "warning: deprecated flag -foo"); n != 1 {
		t.Fatalf("warn line appears %d times, want exactly 1 (deduplicated): %s", n, got)
	}
	if !strings.Contains(got, "warning: deprecated flag -foo (x80)") {
		t.Errorf("surviving warn line missing its repeat count: %s", got)
	}
	if !strings.Contains(got, "duplicate warnings") {
		t.Errorf("footer missing duplicate-warning count: %s", got)
	}
}

// oldTailLines reproduces the old tailLines(s, 40) truncation this story
// replaces (internal/agentstep/engine.go, deleted), so AC5's fixtures can
// prove a line it would have dropped now survives CompressLog.
func oldTailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TestCompressLog_Fixtures is AC5: captured go test/go build failures where
// the failure line sits well outside tailLines(out, 40)'s window. Each case
// asserts the failure lines survive CompressLog under this repo's Go
// configuration, and that the SAME lines are absent from the old tail(40)
// reproduction — proving this story fixes a real, not hypothetical, drop.
func TestCompressLog_Fixtures(t *testing.T) {
	cfg := goPatterns()
	// This fixture's module (fixturegen/...) is not this repo's own module,
	// so trace_app_frame never matches it — only the first TraceKeepFrames
	// trace lines survive intact, the rest collapse. That still exercises
	// the collapse path; app-frame-survives-past-the-cap is covered by
	// TestCompressLog_GoPatterns above.
	cfg.TraceAppFrame = ""

	cases := []struct {
		name      string
		file      string
		mustKeep  []string
		wantTrace bool
	}{
		{
			name: "early fail followed by 200+ lines",
			file: "go_test_early_fail.txt",
			mustKeep: []string{
				"--- FAIL: TestEarlyFailure (0.00s)",
				"FAIL\tfixturegen/pkg1\t0.002s",
			},
		},
		{
			name: "panic with a long goroutine dump",
			file: "go_test_panic.txt",
			mustKeep: []string{
				"--- FAIL: TestPanicsDeep (0.00s)",
				"panic: boom: unexpected nil config [recovered, repanicked]",
				"goroutine 6 [running]:",
			},
			wantTrace: true,
		},
		{
			name: "build error buried in surrounding noise",
			file: "go_build_error.txt",
			mustKeep: []string{
				"pkgbuild/bad.go:4:9: undefined: undefinedSymbol",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			fixture := string(raw)
			oldTail := oldTailLines(fixture, 40)
			got := CompressLog(fixture, cfg, newMemStore())

			for _, want := range tc.mustKeep {
				if !strings.Contains(got, want) {
					t.Errorf("%s: CompressLog dropped %q, want kept:\n%s", tc.file, want, got)
				}
				if strings.Contains(oldTail, want) {
					t.Errorf("%s: fixture is not a valid regression case — the OLD tailLines(out,40) already kept %q", tc.file, want)
				}
			}
			if tc.wantTrace && !strings.Contains(got, "frames collapsed]") {
				t.Errorf("%s: no trace-collapse marker in:\n%s", tc.file, got)
			}
			gotLines := len(strings.Split(got, "\n"))
			fixtureLines := len(strings.Split(strings.TrimRight(fixture, "\n"), "\n"))
			if gotLines >= fixtureLines {
				t.Errorf("%s: compressed notes (%d lines) not shorter than the fixture (%d lines)", tc.file, gotLines, fixtureLines)
			}
		})
	}
}

// extractMarker finds retrieve's MarkerKind marker in s and parses it.
func extractMarker(t *testing.T, s string) (hash, kind string, size int, ok bool) {
	t.Helper()
	idx := strings.Index(s, "<<ccr:")
	if idx < 0 {
		return "", "", 0, false
	}
	end := strings.Index(s[idx:], ">>")
	if end < 0 {
		return "", "", 0, false
	}
	return retrieve.ParseMarkerKind(s[idx : idx+end+2])
}
