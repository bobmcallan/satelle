// Package remotetest runs a remote definition's own workflow locally, on a
// sample story, before the definition is published.
//
// Each edge of each segment runs the way a real transition runs it: the
// performer through the agent step engine's DispatchExecutor, then the edge's
// reviewers through Gate. The work happens in a scratch worktree the run cuts
// from a named base on a throwaway branch and removes afterwards. The
// definition's host commands are rendered into the report as text and never
// executed, the story and its documents are never written, and a cloud launch
// is always refused — a test run stays on the developer's machine.
package remotetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
	"github.com/bobmcallan/satelle/internal/worktree"
)

// Verdicts an edge can end with.
const (
	VerdictAccept    = "accept"    // every reviewer accepted
	VerdictReject    = "reject"    // a reviewer rejected, or the edge could not run
	VerdictPerformed = "performed" // a performer ran and no reviewer judged it
	VerdictNoOp      = "no-op"     // neither a performer nor a reviewer
)

// Actor is the ledger actor of a remote_test row.
const Actor = "remote-test"

// Options carries everything the run needs from the repository it runs in.
type Options struct {
	// Base is the ref the scratch worktree is cut from. It is required and never
	// defaulted, as `satelle story worktree --base` is.
	Base string
	// MainRoot is the main working tree the scratch worktree is added to.
	MainRoot string
	// Include is the repo's [worktree] include list, carried into the scratch
	// tree so a performer there has the gate wiring the main tree declares.
	Include []string
	// TempDir is where the scratch worktree is created; "" is os.TempDir().
	TempDir string
	// NewEngine builds the engine the walk runs on, over docs and rooted at the
	// scratch tree, wired from the remote's own agents layer the way a normal
	// dispatch is: the CLI supplies it.
	NewEngine func(docs agentstep.DocGetter, root string, agents config.AgentsConfig) (*agentstep.Engine, error)
	// NewRunner resolves a binding's agent CLI, for the pre-flight and for the
	// dispatch itself. Nil is agentstep.LookupRunner.
	NewRunner func(iface, command string) (agentcli.Runner, error)
	// ReviewerRunner, when set, replaces the engine's reviewer runner.
	ReviewerRunner agentcli.Runner
	// StoryDocs lists the sample story's documents; they ride a reviewer's
	// payload beside whatever the performer attaches. Nil lists none.
	StoryDocs func(ctx context.Context, itemID string) []agentstep.DocState
	// Ledger appends one remote_test row. Nil appends none.
	Ledger func(ctx context.Context, in ledger.AppendInput) error
}

// Step is one edge of the walk.
type Step struct {
	Segment int    `json:"segment"`
	From    string `json:"from"`
	To      string `json:"to"`
	// Skill is the performer's skill, or the reviewers' when nothing was performed.
	Skill string `json:"skill,omitempty"`
	// Binding is the agent binding dispatched: the performer's, or the reviewers'
	// when nothing was performed. Reviewer names the reviewing binding when both ran.
	Binding  string `json:"binding,omitempty"`
	Reviewer string `json:"reviewer,omitempty"`
	// Identity is the host identity label the definition runs under.
	Identity string `json:"identity"`
	// OnHost marks a step whose work would run on the host under that identity;
	// HostCommand is the host.run line it would be given, rendered and not run.
	OnHost      bool   `json:"on_host,omitempty"`
	HostCommand string `json:"host_command,omitempty"`
	Verdict     string `json:"verdict"`
	Notes       string `json:"notes,omitempty"`
}

// Report is what a run did.
type Report struct {
	Remote   string   `json:"remote"`
	Story    string   `json:"story"`
	Identity string   `json:"identity,omitempty"`
	Steps    []Step   `json:"steps,omitempty"`
	Refusals []string `json:"refusals,omitempty"`
	// CloudRefused counts cloud launches the run refused at dispatch; the
	// pre-flight makes it zero for any definition that was allowed to start.
	CloudRefused int `json:"cloud_refused,omitempty"`
}

// RefusedError is a definition that cannot run, found before any dispatch. It
// unwraps to the causes behind its reasons, so an agent CLI this machine lacks
// is still an *agentstep.ExecutableNotFoundError to errors.As.
type RefusedError struct {
	Remote  string
	Reasons []string
	causes  []error
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("remote %s cannot be test-run: %s", e.Remote, strings.Join(e.Reasons, "; "))
}

func (e *RefusedError) Unwrap() []error { return e.causes }

// problems collects every reason a definition cannot run.
type problems struct{ errs []error }

func (p *problems) addf(format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf(format, args...))
}

func (p *problems) refusal(remote string) *RefusedError {
	r := &RefusedError{Remote: remote, causes: p.errs}
	for _, e := range p.errs {
		r.Reasons = append(r.Reasons, e.Error())
	}
	return r
}

// RejectedError is a walk stopped by a rejected edge.
type RejectedError struct {
	Remote   string
	From, To string
	Notes    string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("remote %s: %s→%s rejected: %s", e.Remote, e.From, e.To, e.Notes)
}

// errBaseRequired mirrors `satelle story worktree --base`.
var errBaseRequired = errors.New("--base <ref> is required — name the ref the scratch worktree is cut from; satelle never defaults to HEAD")

// edge is one transition of a segment, with the bindings it needs.
type edge struct {
	segment  int
	from, to string
	tr       wfdot.Transition
	state    wfdot.State
	// perf is the dispatched performer's binding; zero when the step has none.
	perf config.AgentBinding
	// reviewers are the skills judging the edge; reviewer is the binding the LLM
	// ones run on ("" when every one is a functional check).
	reviewers []string
	reviewer  string
}

// Run walks every segment of the remote in dir on item, in order, and reports
// each edge. name is the remote's name. A definition that cannot run is a
// *RefusedError before anything is dispatched; a rejected edge stops the walk
// with a *RejectedError, the report still carrying every edge that ran.
func Run(ctx context.Context, name, dir string, item workitem.Item, opts Options) (rep Report, err error) {
	rep = Report{Remote: name, Story: item.ID}
	if strings.TrimSpace(opts.Base) == "" {
		return rep, errBaseRequired
	}
	if strings.TrimSpace(opts.MainRoot) == "" || opts.NewEngine == nil {
		return rep, errors.New("remotetest: MainRoot and NewEngine are required")
	}
	newRunner := opts.NewRunner
	if newRunner == nil {
		newRunner = agentstep.LookupRunner
	}

	var probs problems
	refuse := func() (Report, error) {
		r := probs.refusal(name)
		rep.Refusals = r.Reasons
		return rep, r
	}
	if findings := remotedef.Validate(dir); len(findings) > 0 {
		for _, f := range findings {
			probs.addf("%s", f.Detail)
		}
		return refuse()
	}
	def, err := remotedef.Load(dir)
	if err != nil {
		probs.addf("%w", err)
		return refuse()
	}
	agents, err := config.LoadAgents(dir)
	if err != nil {
		probs.addf("%w", err)
		return refuse()
	}
	rep.Identity = def.Identity.Label

	docs := loadDocs(dir)
	spec, _, _, err := wfgovern.SpecFor(docs.kinds["workflows"], item)
	if err != nil {
		probs.addf("the remote's workflow does not govern a %q story: %w", item.Category, err)
		return refuse()
	}
	edges := planEdges(&probs, spec, def, item, docs, agents, newRunner)
	if len(probs.errs) > 0 {
		return refuse()
	}

	scratch, cleanup, err := openScratch(ctx, name, opts)
	if err != nil {
		return rep, err
	}
	defer func() {
		if cerr := cleanup(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()

	eng, err := opts.NewEngine(docs, scratch, agents)
	if err != nil {
		return rep, err
	}
	w := &wiring{scratch: scratch, opts: opts, newRunner: newRunner}
	defer func() { rep.CloudRefused = w.cloudRefused }()
	w.apply(eng)

	cur := item
	for _, e := range edges {
		cur.Status = e.from
		step := runEdge(ctx, eng, cur, e, def)
		rep.Steps = append(rep.Steps, step)
		if opts.Ledger != nil {
			if lerr := opts.Ledger(ctx, ledgerRow(name, item.ID, step)); lerr != nil {
				return rep, fmt.Errorf("remote_test ledger row for %s→%s: %w", e.from, e.to, lerr)
			}
		}
		if step.Verdict == VerdictReject {
			return rep, &RejectedError{Remote: name, From: e.from, To: e.to, Notes: step.Notes}
		}
	}
	return rep, nil
}

// wiring is what a test run installs on the engine it walks, beyond the grants
// the CLI factory applies: where the performers run, what a reviewer is shown of
// their work, and the refusal that stands in for a cloud launch.
type wiring struct {
	scratch   string
	opts      Options
	newRunner func(iface, command string) (agentcli.Runner, error)
	// attached is what performers attached; it lives in memory, never the store.
	attached []agentstep.DocState
	// cloudRefused counts the cloud launches the run refused. The pre-flight keeps
	// it at zero; it is the refusal behind that, not the check.
	cloudRefused int
}

func (w *wiring) apply(eng *agentstep.Engine) {
	eng.SetRunnerFactory(w.newRunner)
	if w.opts.ReviewerRunner != nil {
		eng.SetRunner(w.opts.ReviewerRunner)
	}
	// A test run never launches a cloud session, even for a binding the
	// pre-flight did not foresee.
	eng.SetCloudDispatch(w.refuseCloud)
	eng.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, docName, docType, body string) (string, string, error) {
		w.attached = append(w.attached, agentstep.DocState{Name: docName, Type: docType, Body: body})
		return docName, docType, nil
	})
	eng.SetDocsResolver(func(ctx context.Context, id string) []agentstep.DocState {
		var out []agentstep.DocState
		if w.opts.StoryDocs != nil {
			out = append(out, w.opts.StoryDocs(ctx, id)...)
		}
		return append(out, w.attached...)
	})
	eng.SetDiffResolver(func(ctx context.Context, _ string) *agentstep.DiffState {
		return scratchDiff(ctx, w.scratch, w.opts.Base)
	})
}

func (w *wiring) refuseCloud(_ context.Context, _ workitem.Item, toStatus, agent string) (verb.DispatchResult, error) {
	w.cloudRefused++
	return verb.DispatchResult{}, fmt.Errorf("step %q on agent %q resolves to a cloud binding; a remote test run never launches a cloud session", toStatus, agent)
}

// runEdge performs one edge: the performer, then the reviewers.
func runEdge(ctx context.Context, eng *agentstep.Engine, cur workitem.Item, e edge, def remotedef.Definition) Step {
	s := Step{Segment: e.segment, From: e.from, To: e.to, Identity: def.Identity.Label}
	reject := func(notes string) Step {
		s.Verdict, s.Notes = VerdictReject, notes
		return s
	}

	perf, err := eng.StepPerformer(ctx, cur, e.to)
	if err != nil {
		return reject(err.Error())
	}
	performed := false
	if perf.Dispatched {
		s.Binding, s.OnHost = perf.Agent, true
		s.HostCommand = hostLine(def, e.perf.CommandTemplate())
		res, derr := eng.DispatchExecutor(ctx, cur, e.to)
		s.Skill = res.Skill
		if derr != nil {
			return reject(derr.Error())
		}
		performed = res.Dispatched
	}

	dec, gerr := eng.Gate(ctx, cur, e.to)
	if gerr != nil {
		return reject(gerr.Error())
	}
	switch {
	case dec.Gated:
		if performed {
			s.Reviewer = e.reviewer
		} else {
			s.Binding = e.reviewer
		}
		if s.Skill == "" {
			s.Skill = strings.Join(e.reviewers, ",")
		}
		if dec.Accept {
			s.Verdict = VerdictAccept
		} else {
			s.Verdict = VerdictReject
		}
		s.Notes = dec.Notes
	case performed:
		s.Verdict = VerdictPerformed
	default:
		s.Verdict = VerdictNoOp
	}
	return s
}

// hostLine renders host.run for command. It is text only: nothing here runs.
func hostLine(def remotedef.Definition, command string) string {
	argv := make([]string, len(def.Host.Run))
	for i, a := range def.Host.Run {
		a = strings.ReplaceAll(a, "{label}", def.Identity.Label)
		argv[i] = strings.ReplaceAll(a, remotedef.CommandPlaceholder, command)
	}
	return strings.Join(argv, " ")
}

func ledgerRow(remote, storyID string, s Step) ledger.AppendInput {
	payload, _ := json.Marshal(map[string]any{
		"remote": remote, "from": s.From, "to": s.To, "verdict": s.Verdict,
		"notes": s.Notes, "binding": s.Binding, "skill": s.Skill,
	})
	return ledger.AppendInput{
		StoryID: storyID, Kind: ledger.KindRemoteTest, Actor: Actor,
		Body:    fmt.Sprintf("remote %s %s→%s: %s", remote, s.From, s.To, s.Verdict),
		Payload: payload,
	}
}

// planEdges lists the edges of every segment in order and checks, before
// anything is dispatched, that each can run: its bindings resolve to an agent CLI
// on this machine and none is a cloud binding. Every reason found goes to p.
func planEdges(p *problems, spec wfdot.Spec, def remotedef.Definition, item workitem.Item, docs *memDocs,
	agents config.AgentsConfig, newRunner func(string, string) (agentcli.Runner, error)) []edge {
	var edges []edge
	for i, seg := range def.Segments {
		pairs, why := spinePath(spec, seg.Entry, seg.Exit)
		if why != "" {
			p.addf("segment[%d]: %s", i, why)
			continue
		}
		for _, pair := range pairs {
			e := edge{segment: i, from: pair[0], to: pair[1]}
			for _, tr := range spec.Transitions {
				if tr.From == e.from && tr.To == e.to {
					e.tr = tr
					break
				}
			}
			e.state, _ = spec.StateNamed(e.to)
			label := fmt.Sprintf("step %s→%s", e.from, e.to)
			planPerformer(p, &e, agents, newRunner, label)
			planReviewers(p, &e, spec, item, docs, agents, newRunner, label)
			edges = append(edges, e)
		}
	}
	return edges
}

// spinePath is the forward steps from entry to exit along the route's spine.
func spinePath(spec wfdot.Spec, entry, exit string) ([][2]string, string) {
	ei, xi := spec.SpineIndex(entry), spec.SpineIndex(exit)
	switch {
	case ei < 0:
		return nil, fmt.Sprintf("entry %q is not a step of this story's route", entry)
	case xi < 0:
		return nil, fmt.Sprintf("exit %q is not a step of this story's route", exit)
	case xi <= ei:
		return nil, fmt.Sprintf("exit %q does not follow entry %q on this story's route", exit, entry)
	}
	var out [][2]string
	for cur := entry; cur != exit; {
		next := ""
		for _, s := range spec.Successors(cur) {
			if spec.SpineIndex(s) == spec.SpineIndex(cur)+1 {
				next = s
				break
			}
		}
		if next == "" {
			return nil, fmt.Sprintf("the route declares no step after %q on the way to %q", cur, exit)
		}
		out = append(out, [2]string{cur, next})
		cur = next
	}
	return out, ""
}

// planPerformer resolves the performer a step dispatches, and refuses what could
// not run: no binding, a cloud binding, a step placed on a cloud agent, or an
// agent CLI this machine lacks.
func planPerformer(p *problems, e *edge, agents config.AgentsConfig, newRunner func(string, string) (agentcli.Runner, error), label string) {
	if ra := e.state.RemoteAgent; ra != "" {
		if b, ok := agents.NamedBinding(ra); !ok {
			p.addf("%s: remote_agent %q has no binding in the remote's agents.toml", label, ra)
		} else if b.ResolvedInterface() == config.InterfaceCloud {
			p.addf("%s: remote_agent %q is a cloud binding, which a test run never launches", label, ra)
		}
	}
	switch e.state.Agent {
	case "", "executor", "reviewer":
		return
	}
	b, ok := agents.NamedBinding(e.state.Agent)
	if !ok {
		p.addf("%s: agent %q has no binding in the remote's agents.toml", label, e.state.Agent)
		return
	}
	if config.ResolvedRole(e.state.Agent, b) == config.RoleReviewer || config.IsInLoopCommand(b.CommandTemplate()) {
		return // a judge, or the driving session: nothing is dispatched
	}
	if checkBinding(p, label, e.state.Agent, b, newRunner) {
		e.perf = b
	}
}

// planReviewers resolves the reviewers of an edge. A functional-check skill runs
// a command and needs no agent; every other reviewer runs on a binding.
func planReviewers(p *problems, e *edge, spec wfdot.Spec, item workitem.Item, docs *memDocs, agents config.AgentsConfig,
	newRunner func(string, string) (agentcli.Runner, error), label string) {
	type ref struct{ skill, agent string }
	var refs []ref
	for _, sk := range e.tr.Skills {
		refs = append(refs, ref{sk, e.tr.Agent})
	}
	for _, sc := range spec.ScopedReviewers(e.to, item.Tags) {
		refs = append(refs, ref{sc.Skill, sc.Agent})
	}
	seen := map[string]bool{}
	for _, r := range refs {
		if r.skill == "" {
			continue
		}
		// The route overlays the binary's baseline gates on the remote's own, and
		// a gate whose skill the remote does not carry is skipped by the engine
		// as advisory — so it judges nothing and needs no binding here.
		d, err := docs.Get(context.Background(), "skills", r.skill)
		if err != nil {
			continue
		}
		e.reviewers = append(e.reviewers, r.skill)
		if structure.CheckCommand(d.Body) != "" {
			continue
		}
		section := r.agent
		if section == "" {
			section = "reviewer"
		}
		if e.reviewer == "" {
			e.reviewer = section
		}
		if seen[section] {
			continue
		}
		seen[section] = true
		b := agents.ReviewerBinding()
		if section != "reviewer" {
			var ok bool
			if b, ok = agents.NamedBinding(section); !ok {
				p.addf("%s: reviewer binding %q is not in the remote's agents.toml", label, section)
				continue
			}
		}
		if config.IsInLoopCommand(b.CommandTemplate()) {
			continue
		}
		checkBinding(p, label, section, b, newRunner)
	}
}

// checkBinding reports whether binding b can run here, adding why not to p.
func checkBinding(p *problems, label, section string, b config.AgentBinding, newRunner func(string, string) (agentcli.Runner, error)) bool {
	if b.ResolvedInterface() == config.InterfaceCloud {
		p.addf("%s: binding %s is a cloud binding, which a test run never launches", label, section)
		return false
	}
	if _, err := newRunner(b.ResolvedInterface(), b.CommandTemplate()); err != nil {
		p.addf("%s: binding %s: %w", label, section, err)
		return false
	}
	return true
}

// openScratch cuts the scratch worktree from opts.Base on a throwaway branch and
// carries [worktree] include into it. The returned cleanup removes the tree and
// the branch; it is safe to call after a failure part way.
func openScratch(ctx context.Context, name string, opts Options) (string, func() error, error) {
	tmp := opts.TempDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	var tag [4]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return "", nil, err
	}
	id := name + "-" + hex.EncodeToString(tag[:])
	holder, err := os.MkdirTemp(tmp, "remote-test-"+id+"-")
	if err != nil {
		return "", nil, err
	}
	if resolved, rerr := filepath.EvalSymlinks(holder); rerr == nil {
		holder = resolved
	}
	path, branch := filepath.Join(holder, "tree"), "remote-test/"+id
	cleanup := func() error {
		var errs []error
		if _, serr := os.Stat(path); serr == nil {
			if out, gerr := gitOut(ctx, opts.MainRoot, "worktree", "remove", "--force", path); gerr != nil {
				errs = append(errs, fmt.Errorf("remove scratch worktree: %v: %s", gerr, out))
			}
		}
		_, _ = gitOut(ctx, opts.MainRoot, "worktree", "prune")
		if _, serr := gitOut(ctx, opts.MainRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); serr == nil {
			if out, gerr := gitOut(ctx, opts.MainRoot, "branch", "-D", branch); gerr != nil {
				errs = append(errs, fmt.Errorf("delete scratch branch %s: %v: %s", branch, gerr, out))
			}
		}
		if rerr := os.RemoveAll(holder); rerr != nil {
			errs = append(errs, rerr)
		}
		return errors.Join(errs...)
	}
	if err := worktree.Open(ctx, opts.MainRoot, path, branch, opts.Base); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	if _, err := worktree.Carry(ctx, opts.MainRoot, path, opts.Include); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return path, cleanup, nil
}

// scratchDiff enumerates what the performers changed in the scratch tree
// against its base, for a reviewer's payload. A failure is a marker, never an
// error: enumeration must not fail a verdict.
func scratchDiff(ctx context.Context, root, base string) *agentstep.DiffState {
	// Intent-to-add makes files a performer created show in the diff; the tree is
	// throwaway, so its index is the run's to use.
	_, _ = gitOut(ctx, root, "add", "-A", "-N", ".")
	names, err := gitOut(ctx, root, "diff", "--name-only", base)
	if err != nil {
		return &agentstep.DiffState{NoBaseline: true, Note: "scratch tree diff unavailable: " + err.Error()}
	}
	stat, _ := gitOut(ctx, root, "diff", "--stat", base)
	patch, _ := gitOut(ctx, root, "diff", base)
	var files []string
	for _, f := range strings.Split(names, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return &agentstep.DiffState{
		Baseline: base, Files: files, Stat: stat, Patch: patch,
		Note: "the remote test run's scratch tree against its base", Source: "remote-test",
	}
}

// gitOut runs git in dir without any inherited GIT_* variable.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}
