package remotedef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleIsDeterministicAndUnbundlesByteForByte(t *testing.T) {
	dir := scaffolded(t)
	a, err := Bundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := Bundle(dir); string(a) != string(b) {
		t.Fatal("the same definition must bundle to the same bytes")
	}

	into := filepath.Join(t.TempDir(), "out")
	if err := Unbundle(into, a, false); err != nil {
		t.Fatal(err)
	}
	if b, err := Bundle(into); err != nil || string(a) != string(b) {
		t.Errorf("a bundle must survive unbundle and re-bundle unchanged (err=%v)", err)
	}
	if errs := Validate(into); len(errs) != 0 {
		t.Errorf("an unbundled remote must still validate: %v", errs)
	}
}

func TestBundleNeverCarriesALocalFile(t *testing.T) {
	dir := scaffolded(t)
	if err := os.WriteFile(filepath.Join(dir, "workflows", "agents.local.toml"), []byte("machine = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Bundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "agents.local") || strings.Contains(string(b), "machine") {
		t.Errorf("a .local file entered the bundle: %s", b)
	}
}

func TestBundleRefusesNonUTF8(t *testing.T) {
	dir := scaffolded(t)
	if err := os.WriteFile(filepath.Join(dir, "skills", "bin.md"), []byte{0xff, 0xfe, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Bundle(dir); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("non-UTF-8 content must be refused, got %v", err)
	}
}

func TestUnbundleRefusesUnsafeBundles(t *testing.T) {
	for name, blob := range map[string]string{
		"parent":     `{"format":1,"files":[{"path":"../x","content":"a"}]}`,
		"nested up":  `{"format":1,"files":[{"path":"skills/../../x","content":"a"}]}`,
		"absolute":   `{"format":1,"files":[{"path":"/etc/x","content":"a"}]}`,
		"local":      `{"format":1,"files":[{"path":"workflows/agents.local.toml","content":"a"}]}`,
		"outside":    `{"format":1,"files":[{"path":"notes.md","content":"a"}]}`,
		"backslash":  `{"format":1,"files":[{"path":"skills\\x.md","content":"a"}]}`,
		"format":     `{"format":2,"files":[]}`,
		"not a json": `nope`,
	} {
		dir := filepath.Join(t.TempDir(), "out")
		if err := Unbundle(dir, []byte(blob), false); err == nil {
			t.Errorf("%s: must be refused", name)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s: nothing may be written for a refused bundle (err=%v)", name, err)
		}
	}
}

func TestUnbundleOverwriteReplacesTheDefinitionButKeepsLocalFiles(t *testing.T) {
	dir := scaffolded(t)
	blob, err := Bundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Unbundle(dir, blob, false); err == nil {
		t.Error("a non-empty target must be refused without overwrite")
	}
	stale := filepath.Join(dir, "skills", "stale.md")
	local := filepath.Join(dir, "workflows", "agents.local.toml")
	for _, p := range []string{stale, local} {
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Unbundle(dir, blob, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("overwrite must remove a definition file the bundle does not carry")
	}
	if _, err := os.Stat(local); err != nil {
		t.Error("overwrite must never remove a .local file")
	}
}
