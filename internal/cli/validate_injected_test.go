package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// memDocs is an in-memory DocGetter for the report core.
type memDocs struct{ docs []docindex.Doc }

func (m memDocs) List(_ context.Context, kind string) ([]docindex.Doc, error) {
	var out []docindex.Doc
	for _, d := range m.docs {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m memDocs) Get(_ context.Context, kind, name string) (docindex.Doc, error) {
	for _, d := range m.docs {
		if d.Kind == kind && d.Name == name {
			return d, nil
		}
	}
	return docindex.Doc{}, docindex.ErrNotFound
}

// injectedFixture is a small repo: three resident principles big enough that the
// claude limit truncates and the grok limit does not, a constitution, a reviewer
// skill, a performer skill and the shipped route.
func injectedFixture(t *testing.T, agentsTOML string) injectedEnv {
	t.Helper()
	pad := func(n int) string { return strings.Repeat("rule text. ", n) }
	res := func(name string, n int) docindex.Doc {
		return docindex.Doc{Kind: "principles", Name: name,
			Body: "---\nname: " + name + "\ntags: [principles:session]\n---\n# " + name + "\n" + pad(n)}
	}
	done, _ := embeddedDefault("workflows", "done")
	step, _ := embeddedDefault("workflows", "step")
	// The repo overlay owns the gate list (routemerge): it moves the step summary
	// onto a named binding, the way an authored repo does.
	overlay := "[[gate]]\nskill = \"satelle-step-summary\"\nagent = \"reviewer-summary\"\nfor = [\"*\"]\n"
	workflows := []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: done, Embedded: true},
		{Kind: "workflows", Name: "step", Body: step, Embedded: true},
		{Kind: "workflows", Name: "step", Body: overlay},
	}
	docs := memDocs{docs: []docindex.Doc{
		res("p-one", 300), res("p-two", 300), res("p-three", 300),
		{Kind: "skills", Name: "satelle-story-intent-review", Body: "---\nname: satelle-story-intent-review\ntags: [type:skill, type:reviewer]\n---\n" + pad(50)},
		{Kind: "skills", Name: "satelle-step-summary", Body: "---\nname: satelle-step-summary\ntags: [type:skill, type:reviewer]\n---\n" + pad(20)},
	}}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflows", "agents.toml"), []byte(agentsTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	agents, err := config.LoadAgents(dir)
	return injectedEnv{
		Docs: docs, Workflows: workflows, Constitution: "constitution body. " + pad(40),
		ConstPath: ".satelle/constitution.md", Agents: agents, AgentsErr: err,
	}
}

const injectedAgentsTOML = `
[reviewer]
command = "claude -p --append-system-prompt {system}"
tools = "Read,Grep,Glob"

[reviewer-summary]
role = "reviewer"
command = "grok --print {system}"
tools = "read_file,grep"
principles = "none"
`

func rowFor(t *testing.T, rows []injectedRow, prefix string) injectedRow {
	t.Helper()
	for _, r := range rows {
		if strings.HasPrefix(r.Label, prefix) {
			return r
		}
	}
	t.Fatalf("no row labelled %q in %+v", prefix, rows)
	return injectedRow{}
}

// AC3: one driver row per configured in-loop harness, each under its own limit,
// and its figure IS the deterministic SessionStart assembly the hook calls.
func TestInjectedDriverRowsAreOnePerHarnessAndMatchTheHookAssembly(t *testing.T) {
	env := injectedFixture(t, injectedAgentsTOML)
	rows := injectedRows(context.Background(), env)

	claude, grok := rowFor(t, rows, "driver[claude]"), rowFor(t, rows, "driver[grok]")
	always := selectAlwaysDocs(mustList(t, env, "principles"))
	for h, row := range map[string]injectedRow{"claude": claude, "grok": grok} {
		want, _ := sessionAssembly(env.Constitution, always, env.ConstPath, h, env.Cfg.ContextLimit(h), 0)
		if row.Bytes != len(want) {
			t.Errorf("driver[%s] = %d bytes, want the hook's assembly length %d", h, row.Bytes, len(want))
		}
	}
	if claude.Bytes == grok.Bytes {
		t.Errorf("claude and grok share a figure (%d); each harness has its own limit", claude.Bytes)
	}
	if !strings.Contains(claude.Note, "omitted") {
		t.Errorf("claude row must say what its limit omitted, got %q", claude.Note)
	}
	if strings.Contains(grok.Note, "omitted") {
		t.Errorf("grok's larger limit fits everything, got %q", grok.Note)
	}
}

func mustList(t *testing.T, env injectedEnv, kind string) []docindex.Doc {
	t.Helper()
	d, err := env.Docs.List(context.Background(), kind)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// AC3: each dispatched seat is labelled with its resolved adapter and interface,
// principles=none drops the resident set from a seat's figure, and a seat with
// no binding is an adapter-named unavailable, never a number.
func TestInjectedDispatchedSeatsAreAdapterLabelled(t *testing.T) {
	env := injectedFixture(t, injectedAgentsTOML)
	rows := injectedRows(context.Background(), env)

	reviewer := rowFor(t, rows, "reviewer[claude/")
	summary := rowFor(t, rows, "reviewer-summary[grok/")
	if reviewer.Bytes == 0 || summary.Bytes == 0 {
		t.Fatalf("seat figures must be computed: %+v %+v", reviewer, summary)
	}
	if summary.Bytes >= reviewer.Bytes {
		t.Errorf("principles=none must shrink a seat: summary %d vs reviewer %d", summary.Bytes, reviewer.Bytes)
	}

	// A named seat the route allocates but agents.toml does not declare.
	env.Agents = config.AgentsConfig{}
	unbound := rowFor(t, injectedRows(context.Background(), env), "reviewer-summary")
	if !strings.Contains(unbound.Unavailable, "no binding declared") || unbound.Bytes != 0 {
		t.Errorf("an unbound seat must be unavailable, not a number: %+v", unbound)
	}

	// A binding whose executable no adapter recognises is named, never assumed.
	unknown := injectedFixture(t, "[reviewer]\ncommand = \"mystery-agent --print {system}\"\ntools = \"Read\"\n")
	got := rowFor(t, injectedRows(context.Background(), unknown), "reviewer[unknown/")
	if !strings.Contains(got.Unavailable, "mystery-agent: unrecognised adapter") || got.Bytes != 0 {
		t.Errorf("an unrecognised adapter must be named unavailable: %+v", got)
	}

	// An agents.toml that will not load makes every dispatched seat say so.
	env.AgentsErr = os.ErrNotExist
	broken := rowFor(t, injectedRows(context.Background(), env), "reviewer")
	if !strings.Contains(broken.Unavailable, "agents.toml does not load") {
		t.Errorf("a broken agents.toml must name the cause, got %+v", broken)
	}
}

// AC3: a budget the repo sets warns when exceeded, never when absent, and
// printing the report has no error channel to fail validate through.
func TestInjectedBudgetWarnsOnlyWhenSet(t *testing.T) {
	env := injectedFixture(t, injectedAgentsTOML)
	rows := injectedRows(context.Background(), env)

	var none strings.Builder
	printInjectedReport(&none, rows, config.InjectedConfig{})
	if strings.Contains(none.String(), "WARN") {
		t.Errorf("no budget configured, yet the report warns:\n%s", none.String())
	}
	if !strings.Contains(none.String(), "no budget") {
		t.Errorf("rows must say there is no budget:\n%s", none.String())
	}
	if !strings.Contains(none.String(), "estimate: bytes / 4") {
		t.Errorf("header must print the ratio in use:\n%s", none.String())
	}

	var over strings.Builder
	printInjectedReport(&over, rows, config.InjectedConfig{Budget: map[string]int{"driver": 1, "principles": 1 << 20}})
	for _, want := range []string{"WARN  driver[claude]", "WARN  driver[grok]", "over budget 1 tokens"} {
		if !strings.Contains(over.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, over.String())
		}
	}
	if regexp.MustCompile(`WARN  principles`).MatchString(over.String()) {
		t.Errorf("a generous budget must not warn:\n%s", over.String())
	}
}

// The ratio is configuration: a different bytes_per_token changes the estimate.
func TestInjectedBytesPerTokenIsConfigurable(t *testing.T) {
	rows := []injectedRow{{Group: "kind", Label: "principles", Bytes: 1000}}
	var four, ten strings.Builder
	printInjectedReport(&four, rows, config.InjectedConfig{})
	printInjectedReport(&ten, rows, config.InjectedConfig{BytesPerToken: 10})
	if !strings.Contains(four.String(), "250 tokens") || !strings.Contains(ten.String(), "100 tokens") {
		t.Errorf("tokens must follow bytes_per_token:\n%s\n%s", four.String(), ten.String())
	}
}

// AC3: no budget number is hard-coded in Go. The one numeric constant that
// governs the report is the documented conversion ratio, in config.
func TestInjectedReportHardCodesNoBudget(t *testing.T) {
	src, err := os.ReadFile("validate_injected.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		code := line
		if c := strings.Index(code, "//"); c >= 0 {
			code = code[:c]
		}
		if strings.Contains(code, "budget") && regexp.MustCompile(`[=<>:]\s*[1-9]\d*\b`).MatchString(code) {
			t.Errorf("validate_injected.go:%d carries a numeric literal beside a budget: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// End to end: a budget below the real size warns and validate still exits 0; the
// same repo with no budget prints no WARN.
func TestValidateWarnsOnBudgetAndStillPasses(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	t.Chdir(repo)

	out, err := runRoot(t, "validate")
	if err != nil {
		t.Fatalf("validate on a fresh repo: %v\n%s", err, out)
	}
	if !strings.Contains(out, "# injected context") || !strings.Contains(out, "driver[claude]") || !strings.Contains(out, "driver[grok]") {
		t.Fatalf("validate must report injected context per seat:\n%s", out)
	}
	if strings.Contains(out, "WARN  ") {
		t.Errorf("no budget configured, yet validate warns:\n%s", out)
	}

	toml := filepath.Join(repo, ".satelle", "satelle.toml")
	body, rerr := os.ReadFile(toml)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if werr := os.WriteFile(toml, append(body, []byte("\n[validate.injected.budget]\ndriver = 1\n")...), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err = runRoot(t, "validate")
	if err != nil {
		t.Fatalf("an exceeded budget must never fail validate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "WARN  driver[claude]") {
		t.Errorf("budget of 1 token must warn on the driver:\n%s", out)
	}
}
