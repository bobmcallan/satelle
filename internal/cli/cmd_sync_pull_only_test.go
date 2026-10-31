package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// A machine marked pull-only (sty_07eefabe): every push to the bound project is
// refused naming [sync] pull_only, every pull still works, and the mark is read
// from satelle.local.toml alone.

// pullOnlyRig is a signed-in checkout opting skills, documents and stories in,
// with one of each to push, pointed at a combined fake that counts every write
// to the project. Registering the checkout's location publishes nothing of the
// project and is not counted.
type pullOnlyRig struct {
	t      *testing.T
	repo   string
	url    string
	fake   *fakeSyncServer
	writes atomic.Int64
}

func newPullOnlyRig(t *testing.T) *pullOnlyRig {
	t.Helper()
	base, f := newFakeSyncServer(t)
	r := &pullOnlyRig{t: t, fake: f}
	inner := base.Config.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead && strings.HasPrefix(req.URL.Path, "/api/v1/projects/") {
			r.writes.Add(1)
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(ts.Close)
	r.url = ts.URL
	seedCred(t, ts.URL)
	r.repo = syncConfigRepo(t, "[sync]\n"+boundProjectToml)
	writeRepoFile(t, r.repo, ".satelle/skills/my-skill.md", "---\ntype: skill\n---\nbody\n")
	writeRepoFile(t, r.repo, ".satelle/documents/local-doc.md", "# local\n")
	pointAt(t, r.repo)
	if out, err := runRoot(t, "story", "create", "--title", "Push probe", "--body", "x", "--acceptance", "1. ok"); err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	return r
}

// hostedWrites is every write the fake received: HTTP PUT/POST and work-state RPCs.
func (r *pullOnlyRig) hostedWrites() int64 {
	r.fake.mu.Lock()
	ws := r.fake.workstate
	r.fake.mu.Unlock()
	return r.writes.Load() + int64(ws)
}

func (r *pullOnlyRig) noWrites(what, out string) {
	r.t.Helper()
	if n := r.hostedWrites(); n != 0 {
		r.t.Errorf("%s on a pull-only machine made %d hosted write(s):\n%s", what, n, out)
	}
}

func (r *pullOnlyRig) pullOnly(mode string) {
	r.t.Helper()
	if out, err := runRoot(r.t, "project", "pull-only", mode); err != nil {
		r.t.Fatalf("project pull-only %s: %v\n%s", mode, err, out)
	}
}

// wantRefused fails unless err is the pull-only refusal, naming the setting.
func wantRefused(t *testing.T, what string, err error, out string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "pull_only") {
		t.Errorf("%s: err = %v, want the refusal naming [sync] pull_only\n%s", what, err, out)
	}
}

func TestPullOnlyRefusesSyncPush(t *testing.T) {
	r := newPullOnlyRig(t)
	r.pullOnly("on")

	out, err := runRoot(t, "sync", "push", "--server", r.url)
	wantRefused(t, "sync push", err, out)
	r.noWrites("sync push", out)

	// The composition itself refuses at its config leg, whoever calls it.
	cmd, buf := testCmd()
	err = runSyncPushLegs(cmd, r.url, false, nil)
	wantRefused(t, "runSyncPushLegs", err, buf.String())
	r.noWrites("runSyncPushLegs", buf.String())
}

func TestPullOnlyRefusesEachAreaPush(t *testing.T) {
	r := newPullOnlyRig(t)
	r.pullOnly("on")
	for _, area := range []string{"config", "documents", "workstate"} {
		out, err := runRoot(t, "sync", area, "push", "--server", r.url)
		wantRefused(t, "sync "+area+" push", err, out)
	}
	r.noWrites("the per-area pushes", "")
}

func TestPullOnlyRefusesDryRunPush(t *testing.T) {
	r := newPullOnlyRig(t)
	r.pullOnly("on")
	out, err := runRoot(t, "sync", "push", "--dry-run", "--server", r.url)
	wantRefused(t, "sync push --dry-run", err, out)
	if strings.Contains(out, "Would push") {
		t.Errorf("a refused dry run still previewed a push:\n%s", out)
	}
}

// Bare sync on a pull-only machine runs its pulls and none of its pushes.
func TestPullOnlyBareSyncPullsAndSkipsPush(t *testing.T) {
	r := newPullOnlyRig(t)
	r.fake.docs.put("probe", "documents/remote-only.md", []byte("# remote\n"))
	r.pullOnly("on")

	out, err := runRoot(t, "sync", "--server", r.url)
	if err != nil {
		t.Fatalf("bare sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "push skipped:") || !strings.Contains(out, "pull_only") {
		t.Errorf("bare sync did not report the skipped push naming pull_only:\n%s", out)
	}
	if got := readRepoFile(t, r.repo, ".satelle/documents/remote-only.md"); got != "# remote\n" {
		t.Errorf("the documents pull did not run: remote-only.md = %q\n%s", got, out)
	}
	r.noWrites("bare sync", out)
}

// sync pull on a pull-only machine brings in a behind area, and leaves a
// heads-only area unsettled rather than publish its first snapshot.
func TestPullOnlySyncPullWorks(t *testing.T) {
	t.Run("behind", func(t *testing.T) {
		_, b, f := behindFixture(t)
		if err := setPullOnly(io.Discard, b.repo, true); err != nil {
			t.Fatal(err)
		}
		puts := len(f.putLog())
		out, err := b.pull()
		if err != nil {
			t.Fatalf("pull: %v\n%s", err, out)
		}
		if got := b.read("skills/x.md"); got != "from A\n" {
			t.Errorf("skills/x.md = %q, want A's bytes\n%s", got, out)
		}
		if got := f.putLog()[puts:]; len(got) != 0 {
			t.Errorf("pull on a pull-only machine PUT %v", got)
		}
	})
	t.Run("heads-only", func(t *testing.T) {
		m, f := scaffoldOnlyMachine(t)
		f.cfg.put("probe", "skills/team.md", []byte("team skill\n"))
		if err := setPullOnly(io.Discard, m.repo, true); err != nil {
			t.Fatal(err)
		}
		puts := len(f.putLog())
		out, err := m.pull()
		if err != nil {
			t.Fatalf("pull: %v\n%s", err, out)
		}
		if !strings.Contains(out, "heads-only area skills not settled") || !strings.Contains(out, "pull_only") {
			t.Errorf("pull did not report the unsettled area naming pull_only:\n%s", out)
		}
		if got := f.putLog()[puts:]; len(got) != 0 {
			t.Errorf("pull on a pull-only machine PUT %v", got)
		}
	})
}

// Bind on a pull-only machine records the binding and seeds nothing, and
// bind --pull-only writes the mark before the seed is tried.
func TestPullOnlyBindDoesNotSeed(t *testing.T) {
	for _, viaFlag := range []bool{false, true} {
		name := "marked-before"
		if viaFlag {
			name = "bind-flag"
		}
		t.Run(name, func(t *testing.T) {
			ts, h := newFakeSeedServer(t, noneSubstrate(holderOf(holderID)))
			repo := bindSeedRepo(t, "[team]\npaths = [\"skills/**\"]\n")
			seedServerFor(t, ts, holderID)
			writeRepoFile(t, repo, ".satelle/skills/x.md", tierSkill("repo"))
			var out string
			var err error
			if viaFlag {
				out, err = runRoot(t, "project", "bind", seedProject, "--pull-only")
			} else {
				if err := setPullOnly(io.Discard, repo, true); err != nil {
					t.Fatal(err)
				}
				out, err = runBind(t)
			}
			if err != nil {
				t.Fatalf("bind: %v\n%s", err, out)
			}
			assertBound(t, repo)
			if got := h.putPaths(); len(got) != 0 {
				t.Errorf("a pull-only bind seeded %v\n%s", got, out)
			}
			if !strings.Contains(out, "substrate seed skipped") || !strings.Contains(out, "pull_only") {
				t.Errorf("bind did not report the skipped seed naming pull_only:\n%s", out)
			}
			if on, err := config.PullOnly(repo); err != nil || !on {
				t.Errorf("PullOnly after bind = %v, %v; want true", on, err)
			}
		})
	}
}

// A pull_only key that arrives in satelle.toml by deploy marks nothing: the
// deploying machine pushes as before.
func TestPullOnlyNotTakenFromRepoConfig(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	const toml = "[sync]\npull_only = true\n" + boundProjectToml
	newMachine := func() *snapMachine {
		return &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, toml), state: filepath.Join(t.TempDir(), "document-sync-state.json")}
	}
	a := newMachine()
	a.write("skills/x.md", "x\n")
	a.pushConfig()

	b := &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, "[sync]\n"+boundProjectToml), state: filepath.Join(t.TempDir(), "document-sync-state.json")}
	// A pinned deploy writes the hosted satelle.toml over this checkout's own.
	b.use()
	cmd, buf := testCmd()
	if err := runSyncConfigDeploy(cmd, ts.URL, "personal", 1); err != nil {
		t.Fatalf("deploy: %v\n%s", err, buf.String())
	}
	if !strings.Contains(b.read("satelle.toml"), "pull_only = true") {
		t.Fatalf("the deployed satelle.toml does not carry pull_only:\n%s", b.read("satelle.toml"))
	}
	if on, err := config.PullOnly(b.repo); err != nil || on {
		t.Fatalf("PullOnly = %v, %v after a deployed pull_only; want false", on, err)
	}
	b.write("skills/y.md", "y\n")
	puts := len(f.putLog())
	// --force: the pinned deploy records no snapshot base, which is not what is judged here.
	b.use()
	if out, err := runRoot(t, "sync", "config", "push", "--server", ts.URL, "--force"); err != nil {
		t.Fatalf("push from the deploying machine: %v\n%s", err, out)
	}
	if len(f.putLog()) == puts {
		t.Error("a push from the deploying machine reached no write endpoint")
	}
}

// Clearing the mark restores push.
func TestPullOnlyOffRestoresPush(t *testing.T) {
	r := newPullOnlyRig(t)
	r.pullOnly("on")
	out, err := runRoot(t, "sync", "push", "--server", r.url)
	wantRefused(t, "sync push while on", err, out)
	r.noWrites("sync push while on", out)

	r.pullOnly("off")
	out, err = runRoot(t, "sync", "push", "--server", r.url)
	if err != nil {
		t.Fatalf("sync push after off: %v\n%s", err, out)
	}
	if _, _, ok := r.fake.cfg.get("probe", "skills/my-skill.md", 0); !ok {
		t.Errorf("config push did not reach the server after off:\n%s", out)
	}
	if r.hostedWrites() == 0 {
		t.Errorf("no hosted write after off:\n%s", out)
	}
}

// The setter writes only satelle.local.toml.
func TestProjectPullOnlyWritesLocalFileOnly(t *testing.T) {
	repo := syncConfigRepo(t, "[sync]\n"+boundProjectToml)
	pointAt(t, repo)
	committed := readRepoFile(t, repo, ".satelle/satelle.toml")
	for _, c := range []struct{ mode, want, line string }{
		{"on", "pull_only = true", "push: disabled"},
		{"off", "pull_only = false", "push: enabled"},
	} {
		out, err := runRoot(t, "project", "pull-only", c.mode)
		if err != nil {
			t.Fatalf("pull-only %s: %v\n%s", c.mode, err, out)
		}
		if !strings.Contains(out, c.line) {
			t.Errorf("pull-only %s printed %q, want %q", c.mode, out, c.line)
		}
		if got := readRepoFile(t, repo, ".satelle/satelle.toml"); got != committed {
			t.Errorf("pull-only %s changed satelle.toml:\n%s", c.mode, got)
		}
		if got := readRepoFile(t, repo, ".satelle/"+config.LocalConfigName); !strings.Contains(got, c.want) {
			t.Errorf("pull-only %s: satelle.local.toml = %q, want %q", c.mode, got, c.want)
		}
	}
	if _, err := runRoot(t, "project", "pull-only", "maybe"); err == nil {
		t.Error("pull-only accepted a mode other than on|off")
	}
}

// project show prints the same push mode as sync status.
func TestProjectShowPrintsPushMode(t *testing.T) {
	repo := syncConfigRepo(t, boundProjectToml)
	pointAt(t, repo)
	for _, c := range []struct {
		on   bool
		want string
	}{{false, "push: enabled"}, {true, "push: disabled — pull-only"}} {
		if err := setPullOnly(io.Discard, repo, c.on); err != nil {
			t.Fatal(err)
		}
		cmd, buf := testCmd()
		if err := runProjectShow(cmd, "https://h.example"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("project show (pull-only %v) = %q, want %q", c.on, buf.String(), c.want)
		}
	}
}

// A hosted pre-mutation backup on a pull-only machine stays local, saying why.
func TestPullOnlyBackupStaysLocal(t *testing.T) {
	testutil.IsolateHome(t)
	repo := t.TempDir()
	if err := setPullOnly(io.Discard, repo, true); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	cfg.Backup.Hosted = true
	opts := ResolveBackupOpts(cfg, repo)
	opts.HostedServer, opts.HostedProject = "https://h.example", "probe"
	pushed := false
	opts.HostedPush = func(_ context.Context, _ string, _ []byte) (string, error) {
		pushed = true
		return "", nil
	}
	res, err := backupExistingFile(t.TempDir(), BackupKindPreMutation, "skills/x.md", []byte("x\n"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if pushed {
		t.Error("a pull-only machine pushed its backup")
	}
	if !strings.Contains(res.Notice, "pull_only") {
		t.Errorf("notice = %q, want it to name pull_only", res.Notice)
	}
	if _, err := os.Stat(res.LocalPath); err != nil {
		t.Errorf("the local copy did not land: %v", err)
	}
}
