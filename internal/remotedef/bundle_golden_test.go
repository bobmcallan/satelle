package remotedef

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden rewrites testdata/bundle_golden.json. Run it only to capture the
// pre-lift bytes; a later encoding change must not be "fixed" by regenerating.
var updateGolden = flag.Bool("update-bundle-golden", false, "rewrite testdata/bundle_golden.json")

// goldenBundleSHA256 is the sha256 of testdata/bundle_golden.json, captured from
// Bundle before its encoding moved to internal/filebundle. Both pins fail on any
// change of file order, JSON escaping, field order or format number.
const goldenBundleSHA256 = "94a271ab1045f676b500035dfc507a07ea797bc90139dac7ac0d36f02b2598dd"

// goldenFixture is a fixed definition across every definition root. The
// non-ASCII text and the <>& characters pin json.Marshal's HTML escaping, and
// the .local file pins that it never enters a bundle.
func goldenFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"remote.toml":            "name = \"golden\"\n# café — ✓\n",
		"workflows/step.toml":    "[a]\nnote = \"x < y > z & w\"\n",
		"workflows/done.toml":    "done = true\n",
		"skills/zeta.md":         "# Zeta\n\n### Heading\n\ncafé — ✓ <b>&amp;</b>\n",
		"skills/alpha.md":        "# Alpha\n\nline one\r\nline two\n",
		"skills/nested/deep.md":  "deep <> & ✓\n",
		"principles/one.md":      "# One\n\n日本語 — text\n",
		"skills/secret.local.md": "never bundled\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBundleBytesArePinned(t *testing.T) {
	got, err := Bundle(goldenFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if *updateGolden {
		if err := os.WriteFile(filepath.Join("testdata", "bundle_golden.json"), got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(filepath.Join("testdata", "bundle_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Bundle bytes changed:\n got %s\nwant %s", got, want)
	}
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != goldenBundleSHA256 {
		t.Errorf("Bundle sha256 = %x, want %s", sum, goldenBundleSHA256)
	}
}
