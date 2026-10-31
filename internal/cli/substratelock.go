package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/spf13/cobra"
)

// substrateLaneCategory is the canonical story category whose route authors
// process markdown under the data dir and is judged by the workflow-change
// review. A story in it is the lane out of the substrate lock: without one,
// satelle could never ship a skill, a gate rubric or a workflow.
const substrateLaneCategory = "substrate"

// substrateLockDenyActor is the ledger actor for a lock refusal — the gate, not
// a performer, made the decision.
const substrateLockDenyActor = "edit-gate"

// substrateLockFootprint is the set of prefixes the lock never applies to: the
// footprint satelle's own machinery writes mid-session (the managed .gitignore
// block and the harness scaffolds under .claude/, .grok/, .pi/ and .cursor/). It
// starts from the seeded managed list so there is one answer about the
// footprint, and adds .pi/ and .cursor/ because those scaffolds are written by
// `satelle agents install` and healed by the binary. A lock list that names one
// of these cannot lock it — the product must not deadlock against its own
// writes.
func substrateLockFootprint() []string {
	return append(append([]string{}, managedEditExemptEntries...), ".pi/", ".cursor/")
}

// substrateLocked is the pure, harness-neutral predicate the edit gate applies:
// abs (an already-absolute target, see resolveAbsTarget) is locked when it sits
// under a lock root and under none of the carve-outs. It consumes only the
// resolved path — never a tool name, envelope or harness — so a claude-shaped
// and a grok-shaped edit of the same file classify identically.
//
// Carve-outs are evaluated first and win over the lock list: the process temp
// dir and /tmp, the [gate] edit_exempt_globs filename patterns (story-reference
// dumps), and the deployed footprint. lockRoots and footprint are absolute.
func substrateLocked(lockRoots, footprint, exemptGlobs []string, root, abs string) bool {
	if len(lockRoots) == 0 {
		return false
	}
	if tempDraftTarget(root, abs) || editExemptPattern(exemptGlobs, root, abs) {
		return false
	}
	if editExempt(footprint, root, abs) {
		return false
	}
	return editExempt(lockRoots, root, abs)
}

// engagementStatus is the committed status an engaged story is performing at.
func engagementStatus(s engagement) string {
	if s.StoryStatus != "" {
		return s.StoryStatus
	}
	return s.State
}

// lockEngagements is what one hook call knows about the engagements: the answer
// resolveEngagementsFor gave (info, engaged — the story engaged from the tree
// this edit lands in, when there is one) and every performing item in the repo.
type lockEngagements struct {
	info    engagement
	engaged bool
	live    []engagement
}

// Why a refused edit was refused — the condition the deny text names.
const (
	laneOwnNotSubstrate = "own-not-substrate" // the edit is attributed to a story that is not substrate
	laneUnresolved      = "unresolved"        // no story is engaged from the edited tree
)

// lockLane is the partition of the performing items for one edit of locked
// substrate. Lane, when set, is the substrate-lane story making the change: the
// edit passes and Noted (the other performing items) get a ledger note instead
// of a refusal. Otherwise Holders are the items the lock is held against and Why
// says which condition applied. Substrate is a performing substrate-lane story
// the edit could have been made under instead, when one exists.
type lockLane struct {
	Lane      *engagement
	Noted     []engagement
	Holders   []engagement
	Why       string
	Substrate *engagement
}

// substrateLane decides which story is making a change to locked substrate. The
// decision is whether the substrate story is the one making it — never what a
// sibling happens to be doing. isSub answers whether a story is in the substrate
// lane; a doubt about it is "no".
//
//  1. The edit is attributed to the story engaged from the tree it lands in:
//     that story decides. A substrate story engaged from another tree never
//     overrides it.
//  2. Otherwise every performing item holds the lock.
func substrateLane(s *lockEngagements, isSub func(id string) bool) lockLane {
	var lane lockLane
	for i := range s.live {
		if isSub(s.live[i].ItemID) {
			lane.Substrate = &s.live[i]
			break
		}
	}
	info := s.info
	if s.engaged && info.ItemID != "" {
		if isSub(info.ItemID) {
			var noted []engagement
			for _, l := range s.live {
				if l.ItemID != info.ItemID {
					noted = append(noted, l)
				}
			}
			lane.Lane, lane.Noted = &info, noted
			return lane
		}
		lane.Holders, lane.Why = []engagement{info}, laneOwnNotSubstrate
		return lane
	}
	lane.Holders, lane.Why = s.live, laneUnresolved
	return lane
}

// laneHolderDesc is one outside holder as the deny text names it.
func laneHolderDesc(h engagement, cats map[string]string) string {
	cat := cats[h.ItemID]
	if cat == "" {
		cat = "unknown"
	}
	return fmt.Sprintf("%s (status %q, category %q)", h.ItemID, engagementStatus(h), cat)
}

// substrateLockReason is the deny text: it names each outside holder with its
// status and category, the locked path, the condition that applied and the fix
// for that condition. cats maps a story id to its category as the gate read it;
// tree is the tree the lock list was resolved against. A lane with no Why (the
// store could not be opened) names the holders only.
func substrateLockReason(outside []engagement, relPath string, lane lockLane, cats map[string]string, tree string) string {
	if len(outside) == 0 {
		return fmt.Sprintf(
			"satelle: substrate lock — %s is locked substrate and no story in the substrate lane is engaged from this tree: the workflows, skills and agent bindings under it are what judge the work, so it is changed only under a substrate-lane story, whether or not anything else is performing. "+
				"Lane out: file a story with --category %s (judged by satelle-workflow-change-review) and engage it from this tree. "+
				"If the substrate itself prevents engagement (for example an invalid .satelle/workflows/step.toml), ask the developer to repair it outside the agent session. "+
				"[gate] lock_substrate_paths sets which prefixes are locked; an empty list is the documented opt-out and `satelle doctor` reports it.",
			relPath, substrateLaneCategory)
	}
	var who, finish string
	if len(outside) == 1 {
		who = "story " + laneHolderDesc(outside[0], cats) + " is performing"
		finish = "finish or park " + outside[0].ItemID
	} else {
		parts := make([]string, 0, len(outside))
		for _, h := range outside {
			parts = append(parts, laneHolderDesc(h, cats))
		}
		who = "stories " + strings.Join(parts, ", ") + " are performing"
		finish = "finish or park them"
	}
	var cond string
	switch lane.Why {
	case laneOwnNotSubstrate:
		cond = fmt.Sprintf("This edit is attributed to story %s, engaged from %s, which is not in the substrate lane. ", outside[0].ItemID, tree)
		if lane.Substrate != nil {
			cond += fmt.Sprintf("Make it from the worktree of substrate-lane story %s instead. ", lane.Substrate.ItemID)
		}
	case laneUnresolved:
		cond = fmt.Sprintf("No performing story is engaged from %s, so none counted. Engage the substrate story from this tree; if it is engaged, edit from its worktree. ", tree)
	}
	laneOut := "Lane out: "
	if lane.Substrate == nil {
		laneOut += fmt.Sprintf("make the change under a substrate-lane story (category %q, judged by satelle-workflow-change-review) — file one, or re-file this change as one — or ", substrateLaneCategory)
	}
	laneOut += finish + " first. "
	return fmt.Sprintf(
		"satelle: substrate lock — %s is locked while %s: the workflows, skills and agent bindings under it are what judge that work, so a story may not rewrite its own judge. %s%s"+
			"[gate] lock_substrate_paths sets which prefixes are locked; an empty list is the documented opt-out and `satelle doctor` reports it.",
		relPath, who, cond, laneOut)
}

// substrateLockPayload is the ledger payload of a refusal. It carries nothing
// harness-specific, so the row reads the same whichever harness was refused.
type substrateLockPayload struct {
	Path   string `json:"path"`
	Status string `json:"status,omitempty"`
	Lane   string `json:"lane"`
}

// substrateChangePayload is the ledger payload of a change note: the path, the
// substrate story that changed it, and the noted story's status at the time.
type substrateChangePayload struct {
	Path           string `json:"path"`
	SubstrateStory string `json:"substrate_story"`
	Status         string `json:"status,omitempty"`
}

// readLockConfig reads the committed config for the lock gate; a var so a test
// can count the reads a single edit performs.
var readLockConfig = os.ReadFile

// lockConfig is the committed configuration the substrate lock reads, loaded once
// per hook call so the candidate scan and the gate cannot disagree about it.
type lockConfig struct {
	root      string   // the invoking tree the lock list is resolved against
	globs     []string // [gate] edit_exempt_globs, the carve-out for story dumps
	lockPaths []string // [gate] lock_substrate_paths, as written
}

// loadSubstrateLockConfig reads the committed config for the lock. A missing
// process file, or no path because the invoking tree is the main tree and that
// tree has no config file, is the default lock; any other read or parse failure
// also fails closed to that default, with the seeded dump globs so the
// story-reference dumps stay writable.
func loadSubstrateLockConfig() lockConfig {
	return lockConfigFrom(loadGateProcess())
}

// lockConfigFrom is loadSubstrateLockConfig over an already-loaded process
// config, so a hook call that has loaded it does not load it again.
func lockConfigFrom(gp gateProcess) lockConfig {
	invoking, processPath, lerr := gp.invoking, gp.path, gp.err
	var content []byte
	var rerr error
	if lerr != nil || strings.TrimSpace(processPath) == "" {
		rerr = os.ErrNotExist
	} else {
		content, rerr = readLockConfig(processPath)
	}
	root := invoking
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	globs, ok := config.ParseEditExemptGlobs(string(content))
	if rerr != nil || !ok {
		globs = managedEditExemptGlobs
	}
	lockPaths, _ := config.ParseLockSubstratePaths(string(content))
	return lockConfig{root: root, globs: globs, lockPaths: lockPaths}
}

// substrateLockGate applies the lock to a path edit. It returns nil to let the
// ordinary gate carry on and a deny error to refuse. It runs BEFORE the
// exempt-path early return, because that exemption is exactly what let a
// performing story rewrite the substrate that judges it.
//
// The lock is a function of who is making the change (substrateLane), never of
// the harness or the model: a substrate-lane story's change passes whatever
// siblings are performing (they get a ledger note, recordSubstrateChangeNotes, once the change is allowed),
// and any other change to locked substrate is refused — whether a story outside
// the lane is performing or nothing is engaged at all. The caller runs the gate on
// every edit; a path that is not locked (an exempt path outside the lock list)
// is passed without reading the store. A holder's category is read from the store
// and every doubt about it refuses — a lock that fails open is not a lock. The
// committed config is read ONCE (loadSubstrateLockConfig).
func substrateLockGate(cmd *cobra.Command, raw []byte, target string, engs *lockEngagements) error {
	return substrateLockGateWith(loadSubstrateLockConfig(), cmd, raw, target, engs)
}

// substrateLockGateWith is substrateLockGate over an already-loaded config, so a
// Bash command with several candidate targets reads the config once.
func substrateLockGateWith(cfg lockConfig, cmd *cobra.Command, raw []byte, target string, engs *lockEngagements) error {
	al, err := substrateLockJudge(cfg, cmd, raw, target, engs)
	if err != nil {
		return err
	}
	return recordLockAllowance(cmd, raw, al)
}

// pendingChangeNote is a substrate_change_note an allowed change owes a sibling.
// It is written only once the whole call has passed: a Bash command refused on
// any candidate never ran, so it changed nothing to note.
type pendingChangeNote struct {
	noted, substrate engagement
	rel              string
}

// pendingWrite is the substrate_written row an allowed change owes the
// substrate-lane story making it, written on the same terms as a change note.
type pendingWrite struct {
	story, root, rel string
}

// lockAllowance is what an allowed call owes the ledger once it has passed.
type lockAllowance struct {
	notes  []pendingChangeNote
	writes []pendingWrite
}

// recordSubstrateWritesFn records an allowed call's writes; a var so a test can
// make the append fail.
var recordSubstrateWritesFn = recordSubstrateWrites

// recordLockAllowance writes what an allowed call owes. A write that cannot be
// recorded refuses the call: another story's change set leaves the file out on
// the strength of that row, so the write must not run without it.
func recordLockAllowance(cmd *cobra.Command, raw []byte, al lockAllowance) error {
	if err := recordSubstrateWritesFn(al.writes); err != nil {
		return denyPreToolUse(cmd, raw, "satelle: substrate lock — the substrate-lane write could not be recorded, so it is refused: "+err.Error())
	}
	recordSubstrateChangeNotes(al.notes)
	return nil
}

// substrateLockJudge is the decision behind substrateLockGateWith: a deny error to
// refuse, or nil with what the caller records once the call as a whole is
// allowed. A refusal writes its own substrate_lock_deny rows.
func substrateLockJudge(cfg lockConfig, cmd *cobra.Command, raw []byte, target string, engs *lockEngagements) (lockAllowance, error) {
	root, globs, lockPaths := cfg.root, cfg.globs, cfg.lockPaths
	abs := resolveAbsTarget(root, target)
	// The lock list is repo-relative, so it must be resolved against the tree the
	// target lives in. A linked worktree of the session repository passes the
	// foreign-tree fence (sty_bcf837ff); resolving against the invoking tree alone
	// would leave the sibling tree's substrate (its .satelle/) unlocked, and a
	// performing story could rewrite the substrate that judges it.
	lockBase := root
	if tree := gitRootOf(abs); tree != "" && linkedTreeTarget(sessionAnchor(), abs) {
		lockBase = tree
	}
	lockRoots := make([]string, 0, len(lockPaths))
	for _, p := range lockPaths {
		lockRoots = append(lockRoots, resolveAbsTarget(lockBase, p))
	}
	footprint := substrateLockFootprint()
	for i, f := range footprint {
		footprint[i] = resolveAbsTarget(lockBase, f)
	}
	if !substrateLocked(lockRoots, footprint, globs, lockBase, abs) {
		return lockAllowance{}, nil
	}
	rel := abs
	if r, rerr := filepath.Rel(lockBase, abs); rerr == nil {
		rel = filepath.ToSlash(r)
	}
	a, oerr := app.Open()
	if errors.Is(oerr, app.ErrNotInitialised) {
		return lockAllowance{}, nil // a repo satelle does not govern has no substrate to lock
	}
	if oerr != nil {
		// Cannot read the store to confirm the lane: refuse, and say why.
		return lockAllowance{}, denyPreToolUse(cmd, raw, substrateLockReason(engs.live, rel, lockLane{}, nil, lockBase)+" (the substrate lane could not be confirmed: "+oerr.Error()+")")
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	cats := map[string]string{}
	var cerr error
	isSub := func(id string) bool {
		if c, ok := cats[id]; ok {
			return c == substrateLaneCategory
		}
		it, gerr := a.Store.Stories.Get(ctx, id)
		if gerr != nil {
			cerr = gerr
			return false
		}
		cats[id] = strings.ToLower(strings.TrimSpace(it.Category))
		return cats[id] == substrateLaneCategory
	}
	lane := substrateLane(engs, isSub)
	if lane.Lane != nil {
		// A substrate-lane story is making the change: siblings performing now do
		// not block it; their ledger records that their judge changed, once the
		// caller knows the change is allowed.
		al := lockAllowance{writes: []pendingWrite{{story: lane.Lane.ItemID, root: lockBase, rel: rel}}}
		for _, n := range lane.Noted {
			if !isSub(n.ItemID) {
				al.notes = append(al.notes, pendingChangeNote{noted: n, substrate: *lane.Lane, rel: rel})
			}
		}
		return al, nil
	}
	var outside []engagement
	for _, h := range lane.Holders {
		if !isSub(h.ItemID) {
			outside = append(outside, h)
		}
	}
	if len(outside) == 0 {
		// Nothing outside the lane holds the lock, but this session is not in the
		// lane either (nothing engaged from the edited tree, or a substrate story
		// engaged from another one): locked substrate needs an engaged
		// substrate-lane story whether or not anything else is performing. No
		// holder is at fault, so no ledger row is written.
		return lockAllowance{}, denyPreToolUse(cmd, raw, substrateLockReason(nil, rel, lane, cats, lockBase))
	}
	reason := substrateLockReason(outside, rel, lane, cats, lockBase)
	if cerr != nil {
		reason += " (a holder's category could not be read, so the substrate lane could not be confirmed: " + cerr.Error() + ")"
	}
	for _, h := range outside {
		recordSubstrateLockDeny(ctx, a, h, rel)
	}
	return lockAllowance{}, denyPreToolUse(cmd, raw, reason)
}

// bashLockCandidates is every absolute path a Bash command could change that the
// substrate lock must judge, from the command text alone: the classified mutation
// targets (redirects, tee, rm/mv/cp/sed -i and the other mutationVerbs) in the
// session tree, those in a linked worktree of the session repository (a `-C` dir
// included — the foreign-tree fence lets them through, and that tree's own lock
// root applies), and what an interpreter command's text references under a lock
// path (interpreterPathRefs); a NAME=value assignment naming a lock path counts
// when the command runs an interpreter, or a classified target still holds an
// unexpanded `$`. With no lock paths configured there is nothing to
// lock, so there are no candidates.
func bashLockCandidates(command string, cfg lockConfig) []string {
	if len(cfg.lockPaths) == 0 {
		return nil
	}
	anchor := sessionAnchor()
	if strings.TrimSpace(anchor) == "" {
		anchor = cfg.root
	}
	inHome, foreign := bashTargets(command, anchor, true)
	out := append([]string{}, inHome...)
	for _, t := range foreign {
		if linkedTreeTarget(filepath.Clean(anchor), t) {
			out = append(out, t)
		}
	}
	seen := make(map[string]bool, len(out))
	unexpanded := false
	for _, c := range out {
		seen[c] = true
		unexpanded = unexpanded || strings.Contains(c, "$")
	}
	refs, assigned := interpreterPathRefs(command, anchor, cfg.lockPaths)
	if unexpanded {
		refs = append(refs, assigned...)
	}
	for _, c := range refs {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// bashSubstrateLockGate applies the substrate lock to a Bash command, with the
// same predicate, deny text and ledger row as an Edit of the path
// (substrateLockGateWith). It runs before the exempt-path filter and in both Bash
// handlers (`hook gate`, `hook commitgate`), because the exemption of .satelle/ is
// what let a shell command rewrite the substrate that judges the story. engs is
// called only once the command has a candidate, with the git root of that
// candidate ("" when it has none), so a command that touches nothing lockable
// opens no store; the config is read once.
//
// What the command text cannot show is not guessed at. Two cases are invisible and
// therefore allowed: (a) an interpreter running a script file whose body and target
// are not in the command text (`python3 tool.py`), and (b) a path assembled from
// fragments that never spell the lock root (`'.sat'+'elle'`). The rule is
// conservative the other way: an interpreter command whose text, or a NAME=value
// assignment in the same command, names a locked path or the bare lock root is a
// candidate even if it only reads it — reads have the
// read tools and `satelle doc get`. A shell -c program is classified as shell,
// by what it writes, as the same command run bare is; only non-shell interpreter
// text, and a shell program shellProgram cannot read, is pattern-scanned. A substrate-lane story's change is never refused; any other
// change to locked substrate is, with a story performing or not (sty_66abc0e3). The
// backstop for the invisible cases is unchanged: substrate diffs are seen at the
// commit and at integration review.
func bashSubstrateLockGate(cmd *cobra.Command, raw []byte, command string, gp gateProcess, engs func(tree string) (*lockEngagements, error)) error {
	cfg := lockConfigFrom(gp)
	cands := bashLockCandidates(command, cfg)
	if len(cands) == 0 {
		return nil
	}
	// Each candidate is attributed by its own tree, exactly as the Edit path does
	// with treeOf(target): a change to one worktree.s substrate is judged by the story engaged from that
	// worktree, never as if it were unattributed.
	// The notes of an allowed candidate wait for the others: a command refused on
	// any candidate does not run, so it leaves no note claiming a change.
	var all lockAllowance
	byTree := map[string]*lockEngagements{}
	for _, c := range cands {
		tree := treeOf(c)
		ss, ok := byTree[tree]
		if !ok {
			var err error
			if ss, err = engs(tree); err != nil {
				return denyPreToolUse(cmd, raw, "satelle: "+err.Error())
			}
			byTree[tree] = ss
		}
		al, err := substrateLockJudge(cfg, cmd, raw, c, ss)
		if err != nil {
			return err
		}
		all.notes = append(all.notes, al.notes...)
		all.writes = append(all.writes, al.writes...)
	}
	return recordLockAllowance(cmd, raw, all)
}

// recordSubstrateWrites appends one substrate_written row per (story, path) of
// an allowed call, hashing each file before the write runs.
func recordSubstrateWrites(writes []pendingWrite) error {
	if len(writes) == 0 {
		return nil
	}
	a, err := app.Open()
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	done := map[pendingWrite]bool{}
	for _, w := range writes {
		if done[w] {
			continue
		}
		done[w] = true
		if err := verb.RecordSubstrateWritten(ctx, a.Store.Ledger, w.story, w.root, w.rel); err != nil {
			return err
		}
	}
	return nil
}

// recordSubstrateChangeNotes appends, to each story that was performing
// while a substrate-lane story changed locked substrate, a note naming that story
// and the path: the lane does not wait for its siblings, so each sibling's ledger
// records that its judge changed. One note per (noted story, substrate story,
// path) per hook call. A failure to record never lifts the allowance.
func recordSubstrateChangeNotes(notes []pendingChangeNote) {
	if len(notes) == 0 {
		return
	}
	a, err := app.Open()
	if err != nil {
		return
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	done := map[string]bool{}
	for _, n := range notes {
		key := n.noted.ItemID + "|" + n.substrate.ItemID + "|" + n.rel
		if done[key] {
			continue
		}
		done[key] = true
		payload, err := json.Marshal(substrateChangePayload{Path: n.rel, SubstrateStory: n.substrate.ItemID, Status: engagementStatus(n.noted)})
		if err != nil {
			continue
		}
		_, _ = a.Store.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: n.noted.ItemID, Kind: ledger.KindSubstrateChangeNote, Actor: substrateLockDenyActor,
			Body:    fmt.Sprintf("substrate story %s changed %s while %s was performing (status %s); the judge of this story's work may have changed", n.substrate.ItemID, n.rel, n.noted.ItemID, engagementStatus(n.noted)),
			Payload: payload,
		}, time.Now().UTC())
	}
}

// recordSubstrateLockDeny appends the refusal to the holder's ledger so it is
// auditable after the fact. A failure to record never lifts the refusal: the
// caller denies either way.
func recordSubstrateLockDeny(ctx context.Context, a *app.App, holder engagement, rel string) {
	payload, err := json.Marshal(substrateLockPayload{Path: rel, Status: engagementStatus(holder), Lane: substrateLaneCategory})
	if err != nil {
		return
	}
	_, _ = a.Store.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: holder.ItemID, Kind: ledger.KindSubstrateLockDeny, Actor: substrateLockDenyActor,
		Body:    fmt.Sprintf("substrate lock refused an edit to %s while %s was performing (status %s)", rel, holder.ItemID, engagementStatus(holder)),
		Payload: payload,
	}, time.Now().UTC())
}
