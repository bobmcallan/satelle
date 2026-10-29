package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

const seedProject = "p"

// fakeSeedServer is the combined sync fake whose GET /api/v1/projects/{p}
// reports substrate (omitted when nil), or answers status when set. Every
// config PUT is recorded by path.
type fakeSeedServer struct {
	*fakeSyncServer
	mu        sync.Mutex
	substrate map[string]any
	status    int
	puts      []string
}

func newFakeSeedServer(t *testing.T, substrate map[string]any) (*httptest.Server, *fakeSeedServer) {
	t.Helper()
	t.Setenv(hosted.SessionTokenEnv, "")
	base, f := newFakeSyncServer(t)
	h := &fakeSeedServer{fakeSyncServer: f, substrate: substrate}
	inner := base.Config.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segs := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/", 3)
		h.mu.Lock()
		sub, status := h.substrate, h.status
		h.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && len(segs) == 1 && r.Method == http.MethodGet:
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			p := map[string]any{"id": "p1", "slug": segs[0], "name": "Seed", "team_id": "t1"}
			if sub != nil {
				p["substrate"] = sub
			}
			_ = json.NewEncoder(w).Encode(p)
			return
		case len(segs) == 3 && segs[1] == "config" && r.Method == http.MethodPut:
			h.mu.Lock()
			h.puts = append(h.puts, segs[2])
			h.mu.Unlock()
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, h
}

func (h *fakeSeedServer) putPaths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), h.puts...)
	sort.Strings(out)
	return out
}

func noneSubstrate(holder hosted.SubstrateHolder) map[string]any {
	return map[string]any{"source": "none", "holder": holder}
}

// seedServerFor makes ts the machine's hosted server and signs in as caller.
func seedServerFor(t *testing.T, ts *httptest.Server, caller string) {
	t.Helper()
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	seedCredAs(t, ts.URL, caller)
}

// bindSeedRepo is a repo whose satelle.toml is satelleToml (no [sync]
// entries: every area local) and whose shared agents layer is the plain
// executor, pointed at. Bind records the project.
func bindSeedRepo(t *testing.T, satelleToml string) string {
	t.Helper()
	return agentsPushRepo(t, satelleToml, executorOnlyAgents)
}

func runBind(t *testing.T) (string, error) {
	t.Helper()
	cmd, buf := testCmd()
	cmd.SetErr(buf)
	err := runProjectBind(cmd, seedProject)
	return buf.String(), err
}

func assertBound(t *testing.T, repo string) {
	t.Helper()
	if !strings.Contains(readRepoFile(t, repo, ".satelle/satelle.toml"), `project = "`+seedProject+`"`) {
		t.Errorf("the binding was not recorded:\n%s", readRepoFile(t, repo, ".satelle/satelle.toml"))
	}
}

// AC1: the holder binding a `none` project pushes exactly the repo's team-owned
// files — a [team] path outside the config areas included — with every area
// local, and says the project was seeded from the repo.
func TestBindSeedsRepoTeamSubstrate(t *testing.T) {
	ts, h := newFakeSeedServer(t, noneSubstrate(holderOf(holderID)))
	repo := bindSeedRepo(t, "[team]\npaths = [\"workflows/agents.toml\", \"workflows/**\", \"skills/**\", \"scripts/*.sh\"]\n")
	seedServerFor(t, ts, holderID)
	writeRepoFile(t, repo, ".satelle/skills/x.md", tierSkill("repo"))
	writeRepoFile(t, repo, ".satelle/workflows/y.toml", "[y]\nstatus = \"backlog\"\n")
	writeRepoFile(t, repo, ".satelle/scripts/x.sh", "#!/bin/sh\necho x\n")
	writeRepoFile(t, repo, ".satelle/principles/not-team.md", "---\ntype: principle\nname: not-team\n---\n# not team\n")
	writeRepoFile(t, repo, ".satelle/"+memberTask, memberTaskBody("v1"))

	out, err := runBind(t)
	if err != nil {
		t.Fatalf("bind: %v\n%s", err, out)
	}
	want := []string{"scripts/x.sh", "skills/x.md", config.AgentsRel, "workflows/y.toml"}
	sort.Strings(want)
	if got := h.putPaths(); !reflect.DeepEqual(got, want) {
		t.Errorf("PUT paths = %v, want %v\n%s", got, want, out)
	}
	for _, rel := range want {
		if b, _, ok := h.cfg.get(seedProject, rel, 0); !ok || string(b) != readRepoFile(t, repo, ".satelle/"+rel) {
			t.Errorf("hosted %s = %q (ok=%v), want the repo's bytes", rel, b, ok)
		}
	}
	if !strings.Contains(out, "substrate: seeded project p from repo: 4 file(s)") {
		t.Errorf("bind output must say the project was seeded from the repo:\n%s", out)
	}
	assertBound(t, repo)
}

// AC1: a 200 with no substrate is a project with none, but with no holder
// reported the caller is not proven the holder: nothing is seeded.
func TestBindNilSubstrateNoHolderSeedsNothing(t *testing.T) {
	ts, h := newFakeSeedServer(t, nil)
	repo := bindSeedRepo(t, "")
	seedServerFor(t, ts, holderID)
	writeRepoFile(t, repo, ".satelle/skills/x.md", tierSkill("repo"))

	out, err := runBind(t)
	if err != nil {
		t.Fatalf("bind: %v\n%s", err, out)
	}
	if got := h.putPaths(); len(got) != 0 {
		t.Errorf("PUTs = %v, want none", got)
	}
	if !strings.Contains(out, "substrate: not seeded — only the project's substrate holder unknown can seed it") {
		t.Errorf("bind output must say not seeded, holder unknown:\n%s", out)
	}
	assertBound(t, repo)
}

// AC1: the seed runs the push's deployed-system validation before any PUT; an
// invalid configuration fails the bind and nothing is sent.
func TestBindSeedRefusedByPreflight(t *testing.T) {
	useRealPushValidator(t)
	ts, h := newFakeSeedServer(t, noneSubstrate(holderOf(holderID)))
	repo := routedPushRepo(t, "", executorOnlyAgents)
	seedServerFor(t, ts, holderID)

	out, err := runBind(t)
	if err == nil || !strings.Contains(err.Error(), "push refused: the configuration fails validation") {
		t.Fatalf("bind err = %v, want the preflight refusal\n%s", err, out)
	}
	if got := h.putPaths(); len(got) != 0 {
		t.Errorf("PUTs = %v, want none", got)
	}
	assertBound(t, repo)
}

// AC2: a holder binding a `none` project from a repo with no team-owned
// substrate pushes exactly the embedded defaults the team-owned set owns.
func TestBindSeedsEmbeddedDefaultsWhenRepoHasNone(t *testing.T) {
	ts, h := newFakeSeedServer(t, noneSubstrate(holderOf(holderID)))
	if strings.TrimSpace(os.Getenv("SATELLE_HOME")) == "" {
		t.Setenv("SATELLE_HOME", t.TempDir())
	}
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("SATELLE_CONFIG", "")
	_ = os.Unsetenv("SATELLE_CONFIG")
	seedServerFor(t, ts, holderID)

	team := config.NewTeamSet(nil)
	var want []string
	for _, d := range config.EmbeddedDefaults() {
		if team.Owns(d.RelPath()) {
			want = append(want, d.RelPath())
		}
	}
	sort.Strings(want)
	if len(want) == 0 {
		t.Fatal("premise: the team-owned set owns no embedded default")
	}

	out, err := runBind(t)
	if err != nil {
		t.Fatalf("bind: %v\n%s", err, out)
	}
	got := h.putPaths()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PUT paths = %v, want the team-owned embedded defaults %v\n%s", got, want, out)
	}
	for _, p := range got {
		if p == config.ConfigName || strings.HasPrefix(p, "tasks/") {
			t.Errorf("seed PUT %s, which the project does not hold", p)
		}
	}
	if !strings.Contains(out, "from embedded defaults") {
		t.Errorf("bind output must say the embedded defaults were seeded:\n%s", out)
	}
	assertBound(t, repo)
}

// AC3: a project that already has substrate is not reseeded; the repo takes its
// project tier — the tree's team-owned copy becomes the project's bytes.
func TestBindExistingSubstrateTakesProjectTier(t *testing.T) {
	for _, source := range []string{hosted.SubstrateHosted, hosted.SubstrateRepo} {
		t.Run(source, func(t *testing.T) {
			ts, h := newFakeSeedServer(t, map[string]any{"source": source, "holder": holderOf(holderID)})
			repo := bindSeedRepo(t, "")
			seedServerFor(t, ts, holderID)
			writeRepoFile(t, repo, ".satelle/skills/x.md", tierSkill("local-A"))
			h.cfg.put(seedProject, "skills/x.md", []byte(tierSkill("project-B")))

			out, err := runBind(t)
			if err != nil {
				t.Fatalf("bind: %v\n%s", err, out)
			}
			if got := h.putPaths(); len(got) != 0 {
				t.Errorf("PUTs = %v, want none\n%s", got, out)
			}
			if !strings.Contains(out, "substrate: project already has substrate (source "+source+"); not seeded") {
				t.Errorf("bind output must say it already has substrate:\n%s", out)
			}
			if got := readRepoFile(t, repo, ".satelle/skills/x.md"); got != tierSkill("project-B") {
				t.Errorf("tree skills/x.md = %q, want the project's bytes\n%s", got, out)
			}
			assertBound(t, repo)
		})
	}
}

// AC4: a caller who is not the holder of a `none` project seeds nothing, and
// the output names the holder — by email, else by user id. An older server
// (404 on the project) and a caller with no credential seed nothing either and
// say why; the binding stands in every case.
func TestBindNonHolderSeedsNothing(t *testing.T) {
	cases := []struct {
		name      string
		substrate map[string]any
		status    int
		signIn    bool
		want      string
	}{
		{"holder by email", noneSubstrate(hosted.SubstrateHolder{UserID: "V", Email: "holder@x"}), 0, true,
			"only the project's substrate holder holder@x can seed it"},
		{"holder by user id", noneSubstrate(hosted.SubstrateHolder{UserID: "V"}), 0, true,
			"only the project's substrate holder V can seed it"},
		{"older server", nil, http.StatusNotFound, true,
			"substrate: not seeded — the server does not report project substrate (404)"},
		{"not signed in", noneSubstrate(holderOf(memberID)), 0, false,
			"substrate: not seeded — not signed in to"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts, h := newFakeSeedServer(t, c.substrate)
			h.status = c.status
			repo := bindSeedRepo(t, "")
			if c.signIn {
				seedServerFor(t, ts, memberID)
			} else if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
				t.Fatal(err)
			}
			writeRepoFile(t, repo, ".satelle/skills/x.md", tierSkill("repo"))

			out, err := runBind(t)
			if err != nil {
				t.Fatalf("bind must succeed: %v\n%s", err, out)
			}
			if got := h.putPaths(); len(got) != 0 {
				t.Errorf("PUTs = %v, want none", got)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("bind output must contain %q:\n%s", c.want, out)
			}
			assertBound(t, repo)
		})
	}
}

func TestIsSubstrateHolder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(hosted.SessionTokenEnv, "")
	const server = "http://holder.example.invalid"
	seedCredAs(t, server, holderID)
	mine, theirs := holderOf(holderID), holderOf(memberID)
	for _, c := range []struct {
		name   string
		holder *hosted.SubstrateHolder
		want   bool
	}{
		{"nil holder", nil, false},
		{"no user id", &hosted.SubstrateHolder{Email: mine.Email}, false},
		{"match", &mine, true},
		{"mismatch", &theirs, false},
	} {
		if got := isSubstrateHolder(server, t.TempDir(), c.holder); got != c.want {
			t.Errorf("%s: isSubstrateHolder = %v, want %v", c.name, got, c.want)
		}
	}
}
