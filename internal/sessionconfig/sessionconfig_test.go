package sessionconfig

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	_ "modernc.org/sqlite"
)

func indexed(t *testing.T, roots map[string]string, defaults ...docindex.Doc) *docindex.Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := docindex.Migrate(db); err != nil {
		t.Fatal(err)
	}
	st := docindex.New(db)
	st.SetDefaults(defaults)
	st.SetRoots(roots)
	if _, err := st.Sync(context.Background(), roots, time.Now()); err != nil {
		t.Fatal(err)
	}
	return st
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectPathsAreRelativeToTheKindRootAndExcludeWhatNeverTravels(t *testing.T) {
	tmp := t.TempDir()
	// skills lives under an overridden root, as [substrate_roots] would set it.
	roots := map[string]string{
		"skills":    filepath.Join(tmp, "elsewhere", "skills"),
		"workflows": filepath.Join(tmp, "data", "workflows"),
		"documents": filepath.Join(tmp, "data", "documents"),
	}
	write(t, filepath.Join(roots["skills"], "alpha.md"), "# A\n\n### Heading\n")
	write(t, filepath.Join(roots["skills"], "nested", "deep.md"), "deep\n")
	write(t, filepath.Join(roots["skills"], "mine.local.md"), "machine\n")
	write(t, filepath.Join(roots["workflows"], "step.toml"), "x = 1\n")
	write(t, filepath.Join(roots["documents"], "notes.md"), "not configuration\n")
	st := indexed(t, roots, docindex.Doc{Kind: "principles", Name: "base", Body: "# Base\n"})

	items, err := Collect(context.Background(), st, roots)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Path)
	}
	want := []string{"principles/base.md", "skills/alpha.md", "skills/nested/deep.md", "workflows/step.toml"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v (embedded kept, documents and .local dropped, sorted)", got, want)
	}
}

func TestBundleRoundTripKeepsHeadingsAndRecomputesTheRecordedDigests(t *testing.T) {
	tmp := t.TempDir()
	roots := map[string]string{"skills": filepath.Join(tmp, "skills")}
	write(t, filepath.Join(roots["skills"], "alpha.md"), "# A\n\n### Heading\n\n---\n\ncafé — ✓ <b>&</b>\n")
	st := indexed(t, roots)
	items, err := Collect(context.Background(), st, roots)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := BindingsJSON(config.AgentsConfig{Executor: config.AgentBinding{Command: "in-loop", Env: map[string]string{"TOKEN": "secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bindings), "secret") {
		t.Errorf("an env value must not enter the bindings: %s", bindings)
	}
	blob, err := encode(items, bindings)
	if err != nil {
		t.Fatal(err)
	}
	sd, bd := SubstrateDigest(items), BindingsDigest(bindings)
	b, err := Parse([]byte(Render("sty_x", BundleName(sd, bd), sd, bd, blob)))
	if err != nil {
		t.Fatal(err)
	}
	if !b.Verified() {
		t.Errorf("recomputed digests %s/%s differ from recorded %s/%s", b.Substrate, b.BindingsSH, sd, bd)
	}
	if len(b.Entries) != 2 || b.Entries[0].Path != BindingsPath || b.Entries[1].Content != items[0].Body {
		t.Errorf("entries = %+v, want bindings.json then skills/alpha.md with the body intact", b.Entries)
	}
}

func TestParseRefusesAnUnsafePath(t *testing.T) {
	for _, p := range []string{"../x", "skills/../../x", "/etc/x", "skills/x.local.md", "documents/n.md", "bare.md"} {
		file := "---\nstory: s\ntype: config-bundle\nname: n\n---\n\n" + `{"format":1,"files":[{"path":"` + p + `","content":"a"}]}` + "\n"
		if _, err := Parse([]byte(file)); err == nil {
			t.Errorf("path %q must be refused", p)
		}
	}
}

func TestStampKeepsOneBundlePerDigestPairAndReportsAFailure(t *testing.T) {
	tmp := t.TempDir()
	roots := map[string]string{"skills": filepath.Join(tmp, "skills")}
	write(t, filepath.Join(roots["skills"], "alpha.md"), "# A\n")
	st := indexed(t, roots)
	have := map[string]bool{}
	puts := 0
	var warned error
	src := &Source{
		Docs: st, Roots: roots,
		Bindings: func() ([]byte, error) { return []byte(`{"a":1}`), nil },
		Has:      func(_ context.Context, story, name string) bool { return have[story+"/"+name] },
		Put: func(_ context.Context, story, name, _, _ string, _ []byte) error {
			puts++
			have[story+"/"+name] = true
			return nil
		},
		Warn: func(err error) { warned = err },
	}
	in := ledger.AppendInput{StoryID: "sty_a", Kind: ledger.KindAgentInvocation}
	first, err := src.Stamp(context.Background(), in)
	if err != nil || first[KeySubstrate] == "" || first[KeyBindings] == "" {
		t.Fatalf("stamp = %v, %v", first, err)
	}
	if _, err := src.Stamp(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Errorf("puts = %d, want one bundle for one digest pair", puts)
	}
	if _, err := src.Stamp(context.Background(), ledger.AppendInput{Kind: ledger.KindAgentInvocation}); err != nil || puts != 1 {
		t.Errorf("a row with no story keeps no bundle (puts=%d, err=%v)", puts, err)
	}

	src.Put = func(context.Context, string, string, string, string, []byte) error { return os.ErrPermission }
	write(t, filepath.Join(roots["skills"], "beta.md"), "# B\n")
	if _, err := st.Sync(context.Background(), roots, time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err := src.Stamp(context.Background(), in)
	if err != nil || out[KeySubstrate] == first[KeySubstrate] || warned == nil {
		t.Errorf("a failed bundle write is reported and the stamp still returned: %v %v warned=%v", out, err, warned)
	}
}
