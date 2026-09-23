package compact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// TestPackageCarriesNoFilenamePattern asserts the constitution's "no filename
// compiled into the binary" rule (sty_75b76691 AC2): the noise/lockfile glob
// list is authored configuration, never a literal in this package's source.
func TestPackageCarriesNoFilenamePattern(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(b))
		for _, forbidden := range []string{"go.sum", "package-lock", "yarn.lock", "cargo.lock"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("%s: contains hardcoded lockfile pattern %q — noise patterns are authored configuration, not compiled", e.Name(), forbidden)
			}
		}
	}
}

const samplePatch = `diff --git a/internal/foo/foo.go b/internal/foo/foo.go
index 1111111..2222222 100644
--- a/internal/foo/foo.go
+++ b/internal/foo/foo.go
@@ -1,3 +1,4 @@
 package foo
+// added
 func Foo() {}

diff --git a/go.sum b/go.sum
index 3333333..4444444 100644
--- a/go.sum
+++ b/go.sum
@@ -1,6 +1,6 @@
-github.com/example/one v1.0.0 h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=
-github.com/example/two v2.0.0 h1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=
-github.com/example/three v3.0.0 h1:ccccccccccccccccccccccccccccccccccccccccccccccc=
+github.com/example/one v1.0.1 h1:dddddddddddddddddddddddddddddddddddddddddddddddd=
+github.com/example/two v2.0.1 h1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee=
+github.com/example/three v3.0.1 h1:fffffffffffffffffffffffffffffffffffffffffffffff=
`

func TestCompactPatchDropsIndexLines(t *testing.T) {
	out := CompactPatch(samplePatch, nil, nil)
	if strings.Contains(out, "\nindex ") || strings.HasPrefix(out, "index ") {
		t.Errorf("CompactPatch left an index line:\n%s", out)
	}
	// Everything else survives with no offloader.
	if !strings.Contains(out, "diff --git a/internal/foo/foo.go") || !strings.Contains(out, "+// added") {
		t.Errorf("CompactPatch dropped more than index lines:\n%s", out)
	}
}

func TestCompactPatchEmptyUnchanged(t *testing.T) {
	if got := CompactPatch("", nil, nil); got != "" {
		t.Errorf("CompactPatch(\"\") = %q, want \"\"", got)
	}
}

func TestCompactPatchOffloadsNoiseSectionAndResolves(t *testing.T) {
	store := newMemStore()
	out := CompactPatch(samplePatch, []string{"go.sum"}, store)

	if strings.Contains(out, "example/one v1.0.1") {
		t.Errorf("CompactPatch left go.sum hunk body inline:\n%s", out)
	}
	if !strings.Contains(out, "diff --git a/go.sum b/go.sum") {
		t.Errorf("CompactPatch dropped the go.sum file header:\n%s", out)
	}
	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 {
		t.Fatalf("expected exactly one offload marker, got %d in:\n%s", len(hashes), out)
	}
	original, err := store.Get(hashes[0])
	if err != nil {
		t.Fatalf("resolve offloaded hunk: %v", err)
	}
	wantHunk := samplePatch[strings.Index(samplePatch, "@@ -1,6 +1,6 @@"):]
	if string(original) != wantHunk {
		t.Errorf("offloaded bytes are not byte-identical to the original hunk:\ngot:  %q\nwant: %q", original, wantHunk)
	}
	// The untouched foo.go section keeps its hunk body inline.
	if !strings.Contains(out, "+// added") {
		t.Errorf("CompactPatch touched the non-noise section:\n%s", out)
	}
}

const whitespaceOnlyPatch = `diff --git a/x.go b/x.go
index 1111111..2222222 100644
--- a/x.go
+++ b/x.go
@@ -1,6 +1,6 @@
-	line one reindented from a tab to four spaces below
-	line two reindented from a tab to four spaces below
-	line three reindented from a tab to four spaces below
+    line one reindented from a tab to four spaces below
+    line two reindented from a tab to four spaces below
+    line three reindented from a tab to four spaces below
`

func TestCompactPatchOffloadsWhitespaceOnlyHunk(t *testing.T) {
	store := newMemStore()
	out := CompactPatch(whitespaceOnlyPatch, nil, store)
	if strings.Contains(out, "reindented from a tab") {
		t.Errorf("CompactPatch left whitespace-only hunk body inline:\n%s", out)
	}
	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 {
		t.Fatalf("expected one offload marker, got %d", len(hashes))
	}
	original, err := store.Get(hashes[0])
	if err != nil {
		t.Fatal(err)
	}
	wantHunk := whitespaceOnlyPatch[strings.Index(whitespaceOnlyPatch, "@@ -1,6 +1,6 @@"):]
	if string(original) != wantHunk {
		t.Errorf("offloaded bytes are not byte-identical to the original hunk:\ngot:  %q\nwant: %q", original, wantHunk)
	}
}

const realContentChangePatch = `diff --git a/x.go b/x.go
index 1111111..2222222 100644
--- a/x.go
+++ b/x.go
@@ -1,2 +1,2 @@
-old behaviour
+new behaviour
`

func TestCompactPatchLeavesRealContentHunkAlone(t *testing.T) {
	store := newMemStore()
	out := CompactPatch(realContentChangePatch, nil, store)
	if !strings.Contains(out, "-old behaviour") || !strings.Contains(out, "+new behaviour") {
		t.Errorf("CompactPatch touched a real content change:\n%s", out)
	}
	if len(store.blobs) != 0 {
		t.Errorf("CompactPatch offloaded a real content change (%d blobs stored)", len(store.blobs))
	}
}

func TestCompactPatchNilOffloaderNeverOffloads(t *testing.T) {
	out := CompactPatch(samplePatch, []string{"go.sum"}, nil)
	if strings.Contains(out, "index ") {
		t.Errorf("index line survived with nil offloader:\n%s", out)
	}
	if !strings.Contains(out, "example/one v1.0.1") {
		t.Errorf("nil offloader must leave noise sections inline:\n%s", out)
	}
}

// mnemonicPrefixPatch is what `git diff` emits with diff.mnemonicPrefix=true
// (c/ for the "commit"/pre-image side, w/ for the working tree side) instead
// of the usual a/ b/. sty_75b76691 AC2: gitDiffSince now forces a/ b/ on its
// own patches, but CompactPatch must still recognise noise sections in a
// patch that reaches it some other way, from a machine where that config is
// set.
const mnemonicPrefixPatch = `diff --git c/go.sum w/go.sum
index 3333333..4444444 100644
--- c/go.sum
+++ w/go.sum
@@ -1,6 +1,6 @@
-github.com/example/one v1.0.0 h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=
-github.com/example/two v2.0.0 h1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=
-github.com/example/three v3.0.0 h1:ccccccccccccccccccccccccccccccccccccccccccccccc=
+github.com/example/one v1.0.1 h1:dddddddddddddddddddddddddddddddddddddddddddddddd=
+github.com/example/two v2.0.1 h1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee=
+github.com/example/three v3.0.1 h1:fffffffffffffffffffffffffffffffffffffffffffffff=
`

func TestCompactPatchOffloadsNoiseSectionWithMnemonicPrefixHeaders(t *testing.T) {
	store := newMemStore()
	out := CompactPatch(mnemonicPrefixPatch, []string{"go.sum"}, store)

	if strings.Contains(out, "example/one v1.0.1") {
		t.Errorf("c/ w/ mnemonic-prefix go.sum hunk body left inline:\n%s", out)
	}
	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 {
		t.Fatalf("expected exactly one offload marker, got %d in:\n%s", len(hashes), out)
	}
	original, err := store.Get(hashes[0])
	if err != nil {
		t.Fatalf("resolve offloaded hunk: %v", err)
	}
	wantHunk := mnemonicPrefixPatch[strings.Index(mnemonicPrefixPatch, "@@ -1,6 +1,6 @@"):]
	if string(original) != wantHunk {
		t.Errorf("offloaded bytes are not byte-identical to the original hunk:\ngot:  %q\nwant: %q", original, wantHunk)
	}
}

// noprefixPatch is what `git diff` emits with diff.noprefix=true — no a/ b/
// (or c/ w/) segment at all on either the "diff --git" line or the ---/+++
// paths.
const noprefixPatch = `diff --git go.sum go.sum
index 3333333..4444444 100644
--- go.sum
+++ go.sum
@@ -1,3 +1,3 @@
-github.com/example/one v1.0.0 h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=
+github.com/example/one v1.0.1 h1:dddddddddddddddddddddddddddddddddddddddddddddddd=
`

func TestCompactPatchOffloadsNoiseSectionWithNoPrefixHeaders(t *testing.T) {
	store := newMemStore()
	out := CompactPatch(noprefixPatch, []string{"go.sum"}, store)

	if strings.Contains(out, "example/one v1.0.1") {
		t.Errorf("no-prefix go.sum hunk body left inline:\n%s", out)
	}
	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 {
		t.Fatalf("expected exactly one offload marker, got %d in:\n%s", len(hashes), out)
	}
	original, err := store.Get(hashes[0])
	if err != nil {
		t.Fatalf("resolve offloaded hunk: %v", err)
	}
	wantHunk := noprefixPatch[strings.Index(noprefixPatch, "@@ -1,3 +1,3 @@"):]
	if string(original) != wantHunk {
		t.Errorf("offloaded bytes are not byte-identical to the original hunk:\ngot:  %q\nwant: %q", original, wantHunk)
	}
}

// noprefixNestedPatch: diff.noprefix=true on a NESTED path. A path glob
// (containing "/") must match the full path, so this proves the no-prefix
// fallback in parsePathLine/stripDiffPrefix does not eat "internal/" as if it
// were an a/-style prefix segment — a real regression stripOnePrefix's
// original "strip any leading segment" version had.
const noprefixNestedPatch = `diff --git internal/vendor/go.sum internal/vendor/go.sum
index 3333333..4444444 100644
--- internal/vendor/go.sum
+++ internal/vendor/go.sum
@@ -1,3 +1,3 @@
-github.com/example/one v1.0.0 h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=
+github.com/example/one v1.0.1 h1:dddddddddddddddddddddddddddddddddddddddddddddddd=
`

func TestCompactPatchNoPrefixNestedPathKeepsLeadingDirectory(t *testing.T) {
	store := newMemStore()
	out := CompactPatch(noprefixNestedPatch, []string{"internal/vendor/go.sum"}, store)
	if strings.Contains(out, "example/one v1.0.1") {
		t.Errorf("noise section stayed inline — the no-prefix fallback likely mis-stripped the real leading directory \"internal/\":\n%s", out)
	}
	hashes := retrieve.FindHashes(out)
	if len(hashes) != 1 {
		t.Fatalf("expected exactly one offload marker, got %d in:\n%s", len(hashes), out)
	}
}

// TestParsePathLineKeepsLeadingDirectoryUnderNoPrefix is the narrower unit
// test for the same bug: a plain nested path with no recognised single-letter
// diff prefix must come back unchanged, not missing its first segment.
func TestParsePathLineKeepsLeadingDirectoryUnderNoPrefix(t *testing.T) {
	got := parsePathLine("+++ internal/vendor/go.sum\n", "+++ ")
	if got != "internal/vendor/go.sum" {
		t.Errorf("parsePathLine(no-prefix nested) = %q, want \"internal/vendor/go.sum\"", got)
	}
}

// TestParsePathLineStripsKnownMnemonicPrefixes: the flip side — a real
// single-letter diff prefix is still stripped.
func TestParsePathLineStripsKnownMnemonicPrefixes(t *testing.T) {
	for _, prefix := range []string{"a", "b", "c", "i", "o", "w"} {
		got := parsePathLine("+++ "+prefix+"/go.sum\n", "+++ ")
		if got != "go.sum" {
			t.Errorf("parsePathLine(%s/go.sum) = %q, want \"go.sum\"", prefix, got)
		}
	}
}

func TestMatchesAnyBasenameVsPathGlob(t *testing.T) {
	if !matchesAny("vendor/mod/go.sum", []string{"go.sum"}) {
		t.Errorf("basename glob should match nested go.sum")
	}
	if matchesAny("vendor/mod/go.sum", []string{"internal/*/go.sum"}) {
		t.Errorf("path glob with wrong prefix should not match")
	}
	if !matchesAny("internal/x/go.sum", []string{"internal/*/go.sum"}) {
		t.Errorf("path glob should match internal/x/go.sum")
	}
}
