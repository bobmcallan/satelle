package agentstep

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Cloud performer dispatch (sty_82cffd60): a step whose binding is
// interface=cloud launches a cloud session, waits for its pushed branch to carry
// the nonce trailer, and collects the branch into the story worktree. The fake
// launcher stands in for the provider: it "runs the session" by pushing to a
// temporary bare remote.

var cloudWF = spineWF("", "", "",
	"plan|cloudy|cloud-step",
	"in_progress|executor",
	"done")

const (
	cloudStepSkill      = "STEP-RUBRIC-BODY: implement the slice."
	cloudPerformerBody  = "PERFORMER-SKILL-BODY: work on the named branch."
	cloudPlanBody       = "PLAN-BODY-XYZ: touch a.txt only."
	cloudDocName        = "ac-evidence"
	cloudEvidenceBody   = "## Per-AC evidence\n\n| AC | proof |\n|----|-------|\n| 1  | TestX |"
	cloudSessionURL     = "https://claude.ai/code/session_TEST"
	cloudSessionID      = "session_TEST"
	cloudContractSkill  = "---\nname: cloud-step\ntype: skill\ndescription: x\noutput_name: design\noutput_type: design-note\noutput_required: true\noutput_schema: body\n---\nreturn an artifact"
	cloudStoryID        = "sty_cloud01"
	cloudDefaultPoll    = 10 * time.Millisecond
	cloudTestShortLimit = "400ms" // time-subject: the binding timeout of the tests whose subject is the timeout itself
	cloudTestLongLimit  = "60s"   // every other test: the session pushes before the wait starts, which returns at its first poll, so a slow `git` cannot expire it mid-poll
)

// cloudFix is a story worktree (a clone, upstream set) beside its bare remote.
type cloudFix struct {
	work, remote string
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newCloudFix(t *testing.T) cloudFix {
	t.Helper()
	testutil.IsolateHome(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	f := cloudFix{work: filepath.Join(root, "work"), remote: filepath.Join(root, "remote.git")}
	gitT(t, root, "init", "-q", "--bare", "-b", "main", f.remote)
	gitT(t, root, "clone", "-q", f.remote, f.work)
	if err := os.WriteFile(filepath.Join(f.work, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.work, "add", ".")
	gitT(t, f.work, "commit", "-q", "-m", "init")
	gitT(t, f.work, "branch", "-M", "main")
	gitT(t, f.work, "push", "-q", "-u", "origin", "main")
	return f
}

// session pushes message (as the tip commit) on branch from a fresh clone of the
// remote, changing files — what a cloud session leaves behind.
func (f cloudFix) session(t *testing.T, branch, message string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "session")
	gitT(t, filepath.Dir(dir), "clone", "-q", f.remote, dir)
	gitT(t, dir, "checkout", "-q", "-b", branch)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", message)
	gitT(t, dir, "push", "-q", "origin", branch)
}

var (
	branchLine = regexp.MustCompile(`(?m)^branch: (.+)$`)
	nonceLine  = regexp.MustCompile(`(?m)^nonce: (.+)$`)
)

// fakeCloud is a CloudLauncher that records its calls and runs act, which is
// handed the branch and nonce parsed from the prompt.
type fakeCloud struct {
	calls  int
	dir    string
	prompt string
	branch string
	nonce  string
	err    error
	act    func(branch, nonce string)
}

func (c *fakeCloud) launch(_ context.Context, dir, prompt string) (agentcli.CloudSession, error) {
	c.calls++
	c.dir, c.prompt = dir, prompt
	if m := branchLine.FindStringSubmatch(prompt); m != nil {
		c.branch = m[1]
	}
	if m := nonceLine.FindStringSubmatch(prompt); m != nil {
		c.nonce = m[1]
	}
	if c.err != nil {
		return agentcli.CloudSession{}, c.err
	}
	if c.act != nil {
		c.act(c.branch, c.nonce)
	}
	return agentcli.CloudSession{ID: cloudSessionID, URL: cloudSessionURL, Title: "t"}, nil
}

func cloudDocs(stepSkill string) fakeDocs {
	return fakeDocs{workflow: cloudWF, extraSkills: []docindex.Doc{
		{Kind: "skills", Name: "cloud-step", Body: stepSkill},
		{Kind: "skills", Name: cloudPerformerSkill, Body: cloudPerformerBody},
	}}
}

func cloudBinding(cmd, collect, timeout string) config.AgentBinding {
	return config.AgentBinding{Role: "agent", Interface: "cloud", Command: cmd, CollectDoc: collect, Timeout: timeout}
}

// cloudEngine is an engine over f whose binding "cloudy" is b, with a recorded
// attach path and telemetry.
type cloudEngine struct {
	*Engine
	attached map[string]string
	attachTy map[string]string
	tele     *[]telemetryRec
}

func newCloudEngine(t *testing.T, f cloudFix, docs fakeDocs, b config.AgentBinding) cloudEngine {
	t.Helper()
	g := New(&fakeRunner{}, docs, f.work, "")
	g.cloudPoll = cloudDefaultPoll
	ce := cloudEngine{Engine: g, attached: map[string]string{}, attachTy: map[string]string{}}
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) { return b, true })
	g.SetDocsResolver(func(context.Context, string) []DocState {
		return []DocState{{Name: "plan", Type: "plan", Body: cloudPlanBody}}
	})
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, body string) (string, string, error) {
		ce.attached[name], ce.attachTy[name] = body, typ
		return name, typ, nil
	})
	ce.tele = captureTelemetry(g)
	return ce
}

func cloudItem() workitem.Item {
	return workitem.Item{ID: cloudStoryID, Status: "backlog", Title: "t", AcceptanceCriteria: "1. first criterion\n2. second criterion"}
}

func (ce cloudEngine) dispatch(t *testing.T) (verb.DispatchResult, error) {
	t.Helper()
	return ce.DispatchExecutor(context.Background(), cloudItem(), "plan")
}

func installCloud(t *testing.T, harness string, c *fakeCloud) {
	t.Helper()
	t.Cleanup(agentcli.SetCloudLauncher(harness, c.launch))
}

func tipMessage(body, nonce string) string {
	return "feat: cloud work\n\n" + body + "\n\nSatelle-Nonce: " + nonce
}

// AC2: the prompt is rubric, then the cloud-performer skill, then the mechanism
// contract; it carries the story, its criteria, the plan, the branch and the
// nonce; the launch runs from the story worktree; and it names neither the
// hosted base URL nor the session-token variable.
func TestCloudDispatchPrompt(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		f.session(t, branch, tipMessage(cloudEvidenceBody, nonce), map[string]string{"b.txt": "b\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", cloudTestLongLimit))
	if _, err := ce.dispatch(t); err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 {
		t.Fatalf("launcher called %d times, want 1", c.calls)
	}
	if c.dir != f.work {
		t.Errorf("launched from %q, want the story worktree %q", c.dir, f.work)
	}
	p := c.prompt
	iRubric, iSkill, iMech := strings.Index(p, "STEP-RUBRIC-BODY"), strings.Index(p, "PERFORMER-SKILL-BODY"), strings.Index(p, "## Mechanism contract")
	if iRubric < 0 || iSkill < 0 || iMech < 0 || !(iRubric < iSkill && iSkill < iMech) {
		t.Fatalf("prompt order wrong (rubric %d, skill %d, mechanism %d):\n%s", iRubric, iSkill, iMech, p)
	}
	for _, want := range []string{cloudStoryID, "first criterion", "second criterion", cloudPlanBody, "branch: " + c.branch, "nonce: " + c.nonce, "Satelle-Nonce: " + c.nonce} {
		if !strings.Contains(p[iMech:], want) {
			t.Errorf("mechanism block missing %q:\n%s", want, p[iMech:])
		}
	}
	for _, banned := range []string{hosted.SessionTokenEnv, config.DefaultHostedServer, config.ResolveHostedServer(config.Config{})} {
		if strings.Contains(p, banned) {
			t.Errorf("prompt contains %q", banned)
		}
	}
}

// The harness takes the prompt as one argv token: a leading '-' is parsed as an
// option, and skills carry YAML frontmatter (which starts with `---`). The
// composed prompt starts with plain text and carries no frontmatter block, and
// the dispatch does not need the binding to grant a context channel.
func TestCloudDispatchPromptHasNoFrontmatterAndNoLeadingDash(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		f.session(t, branch, tipMessage(cloudEvidenceBody, nonce), map[string]string{"b.txt": "b\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	docs := fakeDocs{workflow: cloudWF, extraSkills: []docindex.Doc{
		{Kind: "skills", Name: "cloud-step", Body: "---\nname: cloud-step\ntype: skill\ndescription: FM-STEP-DESC\n---\n# Rubric\n" + cloudStepSkill},
		{Kind: "skills", Name: cloudPerformerSkill, Body: "---\nname: " + cloudPerformerSkill + "\ntype: skill\ndescription: FM-PERF-DESC\n---\n" + cloudPerformerBody},
	}}
	b := cloudBinding("claude -p {system}", "", cloudTestLongLimit)
	if b.Tools != "" {
		t.Fatal("fixture binding must grant no tools")
	}
	ce := newCloudEngine(t, f, docs, b)
	if _, err := ce.dispatch(t); err != nil {
		t.Fatalf("a cloud binding with no tools grant must dispatch: %v", err)
	}
	p := c.prompt
	if strings.HasPrefix(p, "-") {
		t.Fatalf("prompt starts with '-':\n%.80s", p)
	}
	if !strings.HasPrefix(p, "Satelle cloud step ") || !strings.Contains(strings.SplitN(p, "\n", 2)[0], cloudStoryID) {
		t.Errorf("prompt must open with a plain heading naming step and story:\n%.120s", p)
	}
	for _, banned := range []string{"FM-STEP-DESC", "FM-PERF-DESC", "type: skill", "name: cloud-step"} {
		if strings.Contains(p, banned) {
			t.Errorf("prompt carries frontmatter text %q", banned)
		}
	}
	for _, want := range []string{"STEP-RUBRIC-BODY", "PERFORMER-SKILL-BODY"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lost the skill body %q", want)
		}
	}
}

// A story whose text would carry the session-token variable into the prompt is
// refused before anything launches.
func TestCloudDispatchRefusesAPromptThatNamesTheToken(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", ""))
	item := cloudItem()
	item.Body = "export " + hosted.SessionTokenEnv + " first"
	_, err := ce.DispatchExecutor(context.Background(), item, "plan")
	if err == nil || !strings.Contains(err.Error(), hosted.SessionTokenEnv) {
		t.Fatalf("err = %v, want a refusal naming the variable", err)
	}
	if c.calls != 0 {
		t.Fatalf("launcher called %d times, want 0", c.calls)
	}
}

// AC3: a harness with no cloud runner fails the dispatch, adapter-named, and
// launches nothing; the claude runner names the branch, the engine never does.
func TestCloudDispatchUnavailableHarness(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("grok agent stdio", "", ""))
	_, err := ce.dispatch(t)
	var un agentcli.ErrCloudUnavailable
	if !errors.As(err, &un) || un.Harness != agentcli.HarnessGrok {
		t.Fatalf("err = %v, want ErrCloudUnavailable for grok", err)
	}
	if !strings.Contains(err.Error(), "grok") {
		t.Errorf("error %q does not name the adapter", err)
	}
	if c.calls != 0 {
		t.Fatalf("claude launcher ran for a grok binding")
	}
}

// AC4: a worktree whose branch is not pushed is refused, with nothing launched.
func TestCloudDispatchRefusesUnpushedBranch(t *testing.T) {
	f := newCloudFix(t)
	if err := os.WriteFile(filepath.Join(f.work, "a.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.work, "commit", "-q", "-am", "unpushed")
	c := &fakeCloud{}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", ""))
	_, err := ce.dispatch(t)
	if err == nil || !strings.Contains(err.Error(), "not pushed") || !strings.Contains(err.Error(), "git") {
		t.Fatalf("err = %v, want a not-pushed refusal naming git", err)
	}
	if c.calls != 0 {
		t.Fatal("a session was launched from an unpushed branch")
	}
}

// AC5: a skill that declares an output contract cannot be served by a cloud
// session; the refusal names the skill and the binding, before launch.
func TestCloudDispatchRefusesContractedSkill(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudContractSkill), cloudBinding("claude -p {system}", "", ""))
	_, err := ce.dispatch(t)
	if err == nil || !strings.Contains(err.Error(), `"cloud-step"`) || !strings.Contains(err.Error(), "[cloudy]") {
		t.Fatalf("err = %v, want a refusal naming skill cloud-step and binding [cloudy]", err)
	}
	if c.calls != 0 {
		t.Fatal("launched despite the contract refusal")
	}
}

// AC6: the trailer-marked branch is collected into the worktree; with
// collect_doc set the commit body (without the trailer) is attached under that
// name; the session, branch and commit are recorded.
func TestCloudDispatchCollectsAndAttaches(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		f.session(t, branch, tipMessage(cloudEvidenceBody, nonce), map[string]string{"b.txt": "from cloud\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", cloudDocName, cloudTestLongLimit))
	res, err := ce.dispatch(t)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(f.work, "b.txt")); err != nil || string(b) != "from cloud\n" {
		t.Fatalf("worktree did not collect the session's file: %q %v", b, err)
	}
	if got := ce.attached[cloudDocName]; got != cloudEvidenceBody {
		t.Errorf("attached %q = %q, want the commit body without the trailer %q", cloudDocName, got, cloudEvidenceBody)
	}
	if strings.Contains(ce.attached[cloudDocName], "Satelle-Nonce") {
		t.Error("the trailer leaked into the attached document")
	}
	if ce.attachTy[cloudDocName] != cloudDocType {
		t.Errorf("attached as type %q, want %q", ce.attachTy[cloudDocName], cloudDocType)
	}
	head := gitT(t, f.work, "rev-parse", "HEAD")
	if res.Cloud == nil || res.Cloud.Commit != head || res.Cloud.Branch != c.branch || res.Cloud.URL != cloudSessionURL ||
		res.Cloud.SessionID != cloudSessionID || res.Cloud.Doc != cloudDocName {
		t.Errorf("result.Cloud = %+v, want commit %s branch %s", res.Cloud, head, c.branch)
	}
	if !res.Dispatched || res.Agent != "cloudy" || res.Command != cloudSessionURL || res.UsageAvailable {
		t.Errorf("result = %+v", res)
	}
	if want := "claude cloud: usage unavailable"; res.UsageUnavailableReason != want {
		t.Errorf("usage note = %q, want %q", res.UsageUnavailableReason, want)
	}
	var ev *telemetryRec
	for i, r := range *ce.tele {
		if r.kind == "cloud_dispatch" {
			ev = &(*ce.tele)[i]
		}
	}
	if ev == nil || ev.data["session_id"] != cloudSessionID || ev.data["url"] != cloudSessionURL ||
		ev.data["branch"] != c.branch || ev.data["commit"] != head || ev.data["outcome"] != "collected" {
		t.Fatalf("cloud_dispatch ledger event = %+v", ev)
	}
}

// With no collect_doc nothing is attached and the dispatch still completes.
func TestCloudDispatchWithoutCollectDocAttachesNothing(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		f.session(t, branch, tipMessage(cloudEvidenceBody, nonce), map[string]string{"b.txt": "x\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", cloudTestLongLimit))
	res, err := ce.dispatch(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(ce.attached) != 0 {
		t.Errorf("attached %v, want nothing", ce.attached)
	}
	if res.Cloud == nil || res.Cloud.Doc != "" || res.Cloud.Commit == "" {
		t.Errorf("result.Cloud = %+v", res.Cloud)
	}
}

// A tip commit that carries no trailer is not a completion: the wait times out,
// the error carries the session URL, and the worktree is unchanged.
//
// time-subject: the 400ms binding timeout is the subject — a tip without the
// trailer must never complete, so the wait has to end at the timeout.
func TestCloudDispatchTimesOutWithoutTrailer(t *testing.T) {
	f := newCloudFix(t)
	before := gitT(t, f.work, "rev-parse", "HEAD")
	c := &fakeCloud{}
	c.act = func(branch, _ string) {
		f.session(t, branch, "wip: pushed without the marker\n\nbody", map[string]string{"b.txt": "x\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", cloudDocName, cloudTestShortLimit))
	_, err := ce.dispatch(t)
	if err == nil || !strings.Contains(err.Error(), cloudSessionURL) || !strings.Contains(err.Error(), "Satelle-Nonce") {
		t.Fatalf("err = %v, want a timeout naming the trailer and the session URL", err)
	}
	if got := gitT(t, f.work, "rev-parse", "HEAD"); got != before {
		t.Errorf("worktree moved from %s to %s", before, got)
	}
	if _, serr := os.Stat(filepath.Join(f.work, "b.txt")); serr == nil {
		t.Error("an uncollected file appeared in the worktree")
	}
	if len(ce.attached) != 0 {
		t.Errorf("attached %v after a timeout", ce.attached)
	}
}

// A session that never pushes times out at the binding's timeout; with no
// binding timeout the dispatch's own default deadline applies.
//
// time-subject: the 400ms binding timeout and the 300ms default deadline are the
// subject. The 10s ceiling below only checks the wait was bounded by them.
func TestCloudDispatchTimesOutWhenNothingIsPushed(t *testing.T) {
	f := newCloudFix(t)
	installCloud(t, agentcli.HarnessClaude, &fakeCloud{})

	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", cloudTestShortLimit))
	start := time.Now()
	if _, err := ce.dispatch(t); err == nil || !strings.Contains(err.Error(), cloudSessionURL) || !strings.Contains(err.Error(), cloudTestShortLimit) {
		t.Fatalf("binding timeout: err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the binding timeout did not bound the wait")
	}

	ce = newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", ""))
	ce.cloudDefaultDeadline = 300 * time.Millisecond
	if _, err := ce.dispatch(t); err == nil || !strings.Contains(err.Error(), "300ms") {
		t.Fatalf("default deadline: err = %v, want a timeout at the default deadline", err)
	}
}

// The shipped default deadline is 60 minutes.
func TestCloudDefaultDeadlineIsSixtyMinutes(t *testing.T) {
	g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
	if g.cloudDefaultDeadline != 60*time.Minute {
		t.Fatalf("default cloud deadline = %s, want 60m", g.cloudDefaultDeadline)
	}
}

// A collected branch that conflicts with the worktree is refused, the worktree
// is left exactly as it was, and the error carries the session URL.
func TestCloudDispatchRefusesConflictingBranch(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		// The worktree moves on locally while the session works from the old tip.
		if err := os.WriteFile(filepath.Join(f.work, "a.txt"), []byte("one\nLOCAL\nthree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, f.work, "commit", "-q", "-am", "local change")
		f.session(t, branch, tipMessage("body", nonce), map[string]string{"a.txt": "one\nCLOUD\nthree\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", cloudDocName, cloudTestLongLimit))
	_, err := ce.dispatch(t)
	if err == nil || !strings.Contains(err.Error(), "does not merge cleanly") || !strings.Contains(err.Error(), cloudSessionURL) {
		t.Fatalf("err = %v, want a merge refusal naming the session URL", err)
	}
	if st := gitT(t, f.work, "status", "--porcelain"); st != "" {
		t.Errorf("worktree dirty after a refused merge:\n%s", st)
	}
	if b, _ := os.ReadFile(filepath.Join(f.work, "a.txt")); string(b) != "one\nLOCAL\nthree\n" {
		t.Errorf("a.txt = %q, want the local content untouched", b)
	}
	if len(ce.attached) != 0 {
		t.Errorf("attached %v although the branch was refused", ce.attached)
	}
}

// A divergence that merges cleanly is collected with a merge commit.
func TestCloudDispatchMergesCleanDivergence(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		if err := os.WriteFile(filepath.Join(f.work, "local.txt"), []byte("l\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, f.work, "add", ".")
		gitT(t, f.work, "commit", "-q", "-m", "local")
		f.session(t, branch, tipMessage("body", nonce), map[string]string{"cloud.txt": "c\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", cloudTestLongLimit))
	if _, err := ce.dispatch(t); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"local.txt", "cloud.txt"} {
		if _, err := os.Stat(filepath.Join(f.work, name)); err != nil {
			t.Errorf("%s missing after a clean merge: %v", name, err)
		}
	}
}

// The usage-unavailable note and the branch are built from the resolved
// adapter, not from claude: a second harness gets its own name.
func TestCloudDispatchNamesTheResolvedAdapter(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		f.session(t, branch, tipMessage("body", nonce), map[string]string{"p.txt": "p\n"})
	}
	installCloud(t, agentcli.HarnessPi, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("pi --mode x", "", cloudTestLongLimit))
	res, err := ce.dispatch(t)
	if err != nil {
		t.Fatal(err)
	}
	if want := "pi cloud: usage unavailable"; res.UsageUnavailableReason != want {
		t.Errorf("usage note = %q, want %q", res.UsageUnavailableReason, want)
	}
	if !strings.HasPrefix(c.branch, "pi/satelle-") {
		t.Errorf("branch %q is not the adapter's", c.branch)
	}
}

// A launcher failure fails the dispatch and collects nothing.
func TestCloudDispatchLaunchFailure(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{err: errors.New("boom")}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", ""))
	if _, err := ce.dispatch(t); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the launcher's error", err)
	}
}

// The pre-launch line names no session; a failed launch adds nothing after it.
func TestCloudDispatchLaunchFailurePrintsNoSessionLine(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{err: errors.New("boom")}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", ""))
	var lines []string
	ce.SetProgress(func(msg string) { lines = append(lines, msg) })
	if _, err := ce.dispatch(t); err == nil {
		t.Fatal("dispatch succeeded despite the launcher's error")
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "dispatching step ") {
		t.Fatalf("progress lines = %q, want only the pre-launch dispatching line", lines)
	}
	if strings.Contains(lines[0], "cloud session started") || strings.Contains(lines[0], cloudSessionURL) {
		t.Errorf("pre-launch line %q names a started session", lines[0])
	}
}

// After a successful launch exactly one more progress line names the session
// URL, the branch and the deadline, and it is sent before the branch wait: the
// session only pushes when that line arrives, so a line sent after the wait
// would time the dispatch out.
func TestCloudDispatchPrintsSessionURLOnLaunch(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", "", cloudTestLongLimit))
	var lines []string
	ce.SetProgress(func(msg string) {
		lines = append(lines, msg)
		if strings.Contains(msg, cloudSessionURL) {
			f.session(t, c.branch, tipMessage(cloudEvidenceBody, c.nonce), map[string]string{"b.txt": "x\n"})
		}
	})
	res, err := ce.dispatch(t)
	if err != nil {
		t.Fatalf("dispatch: %v (progress %q)", err, lines)
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "dispatching step ") {
		t.Fatalf("progress lines = %q, want the dispatching line then one session line", lines)
	}
	if strings.Contains(lines[0], cloudSessionURL) {
		t.Errorf("pre-launch line %q already names the session URL", lines[0])
	}
	got := lines[1]
	// The line prints the parsed deadline, so "60s" reads as "1m0s".
	for _, want := range []string{cloudSessionURL, c.branch, time.Minute.String()} {
		if !strings.Contains(got, want) {
			t.Errorf("session line %q does not name %q", got, want)
		}
	}
	if res.Command != cloudSessionURL {
		t.Errorf("result.Command = %q, want %q", res.Command, cloudSessionURL)
	}
}

// AC8: a live session (rework relay, consult) is never opened on a cloud binding.
func TestOpenSessionRefusesCloudBinding(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Role: "agent", Interface: "cloud", Command: "claude -p {system}", Tools: "Read"}, true
	})
	opened := false
	g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
		opened = true
		return nil, errors.New("must not be reached")
	}
	for _, role := range []SessionRole{SessionRoleDriving, SessionRoleConsult} {
		_, err := g.OpenSessionAsWithModel(context.Background(), "coder", role,
			workitem.Item{ID: "sty_live", Status: "in_progress"}, nil, nil, "")
		if err == nil || !strings.Contains(err.Error(), "[coder]") || !strings.Contains(err.Error(), "interface=cloud") {
			t.Errorf("role %v: err = %v, want a refusal naming [coder] and interface=cloud", role, err)
		}
	}
	if opened {
		t.Error("an opener was built for a cloud binding")
	}
}
