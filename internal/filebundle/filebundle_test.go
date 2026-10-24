package filebundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeKeepsTheCallersOrderAndIsDeterministic(t *testing.T) {
	in := []Entry{{"z/last.md", "z"}, {"a/first.md", "a"}, {"m.md", "m"}}
	a, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := Encode(in); string(a) != string(b) {
		t.Fatal("the same entries must encode to the same bytes")
	}
	got, err := Decode(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("entry %d = %v, want %v: Encode must not reorder", i, got[i], in[i])
		}
	}
}

func TestEncodeNoEntriesIsAnEmptyList(t *testing.T) {
	b, err := Encode(nil)
	if err != nil || string(b) != `{"format":1,"files":[]}` {
		t.Errorf("Encode(nil) = %s, %v", b, err)
	}
}

func TestEncodeRefusesLocalAndNonUTF8(t *testing.T) {
	if _, err := Encode([]Entry{{"skills/x.local.md", "a"}}); err == nil {
		t.Error("a .local entry must be refused")
	}
	if _, err := Encode([]Entry{{"skills/bin.md", string([]byte{0xff, 0xfe})}}); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("non-UTF-8 content must be refused, got %v", err)
	}
}

func TestRoundTripKeepsMarkdownHeadingsAndMarkup(t *testing.T) {
	in := []Entry{{"skills/a.md", "# A\n\n### Heading\n\ncafé — ✓ <b>&amp;</b>\r\nend"}}
	blob, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(blob, []string{"skills"})
	if err != nil || len(out) != 1 || out[0] != in[0] {
		t.Errorf("round trip = %v, %v", out, err)
	}
}

func TestDecodeRefusesAnUnknownFormatAndNonJSON(t *testing.T) {
	_, err := Decode([]byte(`{"format":2,"files":[]}`), nil)
	var fe *FormatError
	if !errors.As(err, &fe) || fe.Got != 2 || fe.Want != Format {
		t.Errorf("format 2 must give a *FormatError, got %v", err)
	}
	_, err = Decode([]byte(`nope`), nil)
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("non-JSON must match ErrMalformed, got %v", err)
	}
}

func TestDecodeRefusesNonUTF8Content(t *testing.T) {
	blob := append([]byte(`{"format":1,"files":[{"path":"a","content":"`), 0xff, 0xfe)
	blob = append(blob, []byte(`"}]}`)...)
	if _, err := Decode(blob, nil); !errors.Is(err, ErrMalformed) {
		t.Errorf("non-UTF-8 bytes must be refused, not silently replaced, got %v", err)
	}
}

func TestCheckPath(t *testing.T) {
	roots := []string{"remote.toml", "skills"}
	for p, ok := range map[string]bool{
		"skills/a.md":          true,
		"remote.toml":          true,
		"skills":               true,
		"../x":                 false,
		"skills/../../x":       false,
		"/etc/x":               false,
		`skills\x.md`:          false,
		"skills//x.md":         false,
		"":                     false,
		"skills/x.local.md":    false,
		"notes.md":             false,
		"skillset/a.md":        false,
		"skills/./a.md":        false,
		"skills/nested/b.md":   true,
		"skills/a.local/b.md":  false,
		"skills/local/keep.md": true,
	} {
		if err := CheckPath(p, roots); (err == nil) != ok {
			t.Errorf("CheckPath(%q) = %v, want ok=%v", p, err, ok)
		}
	}
	if err := CheckPath("anything/goes.md", nil); err != nil {
		t.Errorf("no roots must accept any plain path: %v", err)
	}
}

func TestDecodeChecksEveryPathBeforeReturningAny(t *testing.T) {
	blob := `{"format":1,"files":[{"path":"skills/ok.md","content":"a"},{"path":"../x","content":"b"}]}`
	if out, err := Decode([]byte(blob), []string{"skills"}); err == nil || out != nil {
		t.Errorf("a bad path must refuse the whole bundle, got %v, %v", out, err)
	}
}

func TestWriteRefusesANonEmptyDirWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, []Entry{{"skills/a.md", "a"}}, false, nil); err == nil {
		t.Error("a non-empty dir must be refused without overwrite")
	}
}

func TestWriteChecksEveryPathBeforeWritingAnything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	err := Write(dir, []Entry{{"skills/a.md", "a"}, {"../escape", "b"}}, false, nil)
	if err == nil {
		t.Fatal("a `..` path must be refused")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("nothing may be written for a refused bundle (err=%v)", statErr)
	}
}

func TestWriteOverwriteRemovesOwnedStaleFilesButNeverALocalFile(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "skills", "stale.md")
	local := filepath.Join(dir, "skills", "mine.local.md")
	for _, p := range []string{stale, local} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	owned := func(rel string) bool { return true }
	if err := Write(dir, []Entry{{"skills/a.md", "a"}}, true, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("an owned stale file must be removed")
	}
	if _, err := os.Stat(local); err != nil {
		t.Error("a .local file must never be removed")
	}
}

func TestWriteOverwriteLeavesFilesThePredicateDoesNotOwn(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(foreign, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	owned := func(rel string) bool { return strings.HasPrefix(rel, "skills/") }
	if err := Write(dir, []Entry{{"skills/a.md", "a"}}, true, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("a file the predicate does not own must stay")
	}
}
