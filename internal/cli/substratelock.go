package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/spf13/cobra"
)

// substrateLaneCategory is the canonical story category whose route authors
// process markdown under the data dir and is judged by the workflow-change
// review. A story in it is the lane out of the substrate lock: without one,
// satelle could never ship a skill, a gate rubric or a workflow.
const substrateLaneCategory = "substrate"

// substrateLockDenyActor is the ledger actor for a lock refusal — the gate, not
// a seat's agent, made the decision.
const substrateLockDenyActor = "edit-gate"

// substrateLockFootprint is the set of prefixes the lock never applies to: the
// footprint satelle's own machinery writes mid-session (the managed .gitignore
// block and the harness scaffolds under .claude/, .grok/ and .pi/). It starts
// from the seeded managed list so there is one answer about the footprint, and
// adds .pi/ because the pi extension is deployed lazily by the binary. A lock
// list that names one of these cannot lock it — the product must not deadlock
// against its own writes.
func substrateLockFootprint() []string {
	return append(append([]string{}, managedEditExemptEntries...), ".pi/")
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

// seatStatus is the committed status a seat's story is performing at.
func seatStatus(s seatInfo) string {
	if s.StoryStatus != "" {
		return s.StoryStatus
	}
	return s.State
}

// substrateLockHolders is the set of stories the lock is held against: the
// session's own seat when it resolved one, else every live performing seat in
// the repo. It is never a function of the session's identity — a session that
// matches no seat is not thereby free of a lock some story's seat imposes.
func substrateLockHolders(info seatInfo, engaged bool, live []seatInfo) []seatInfo {
	if engaged && info.ItemID != "" && !info.Stale {
		return []seatInfo{info}
	}
	return live
}

// substrateLockReason is the deny text: it names the seat-holding stories, the
// locked path, and the lane out.
func substrateLockReason(holders []seatInfo, relPath string) string {
	var who, finish string
	if len(holders) == 1 {
		who = fmt.Sprintf("story %s holds a performing seat (status %q)", holders[0].ItemID, seatStatus(holders[0]))
		finish = "finish or park " + holders[0].ItemID
	} else {
		parts := make([]string, 0, len(holders))
		for _, h := range holders {
			parts = append(parts, fmt.Sprintf("%s (status %q)", h.ItemID, seatStatus(h)))
		}
		who = "stories " + strings.Join(parts, ", ") + " hold performing seats"
		finish = "finish or park them"
	}
	return fmt.Sprintf(
		"satelle: substrate lock — %s is locked while %s: the workflows, skills and agent bindings under it are what judge that work, so a story may not rewrite its own judge. "+
			"Lane out: make the change under a substrate-lane story (category %q, judged by satelle-workflow-change-review) — file one, or re-file this change as one — or %s first. "+
			"[gate] lock_substrate_paths sets which prefixes are locked; an empty list is the documented opt-out and `satelle doctor` reports it.",
		relPath, who, substrateLaneCategory, finish)
}

// substrateLockPayload is the ledger payload of a refusal. It carries nothing
// harness-specific, so the row reads the same whichever harness was refused.
type substrateLockPayload struct {
	Path   string `json:"path"`
	Status string `json:"status,omitempty"`
	Lane   string `json:"lane"`
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
	_, invoking, _, processPath, lerr := config.LoadInvokingProcess()
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
// The lock is a function of holders — the live performing seats
// (substrateLockHolders) — never of the session's identity, the harness or the
// model; the caller skips the gate when there are none, so with no seat held
// nothing is read and an exempt path stays writable. A holder's category is read
// from the store and every doubt about it refuses — a lock that fails open is
// not a lock. The committed config is read ONCE (loadSubstrateLockConfig).
func substrateLockGate(cmd *cobra.Command, raw []byte, target string, holders []seatInfo) error {
	return substrateLockGateWith(loadSubstrateLockConfig(), cmd, raw, target, holders)
}

// substrateLockGateWith is substrateLockGate over an already-loaded config, so a
// Bash command with several candidate targets reads the config once.
func substrateLockGateWith(cfg lockConfig, cmd *cobra.Command, raw []byte, target string, holders []seatInfo) error {
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
		return nil
	}
	rel := abs
	if r, rerr := filepath.Rel(lockBase, abs); rerr == nil {
		rel = filepath.ToSlash(r)
	}
	a, oerr := app.Open()
	if oerr != nil {
		// Cannot read the store to confirm the lane: refuse, and say why.
		return denyPreToolUse(cmd, raw, substrateLockReason(holders, rel)+" (the substrate lane could not be confirmed: "+oerr.Error()+")")
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	var outside []seatInfo
	var cerr error
	for _, h := range holders {
		it, gerr := a.Store.Stories.Get(ctx, h.ItemID)
		if gerr != nil {
			cerr = gerr
		}
		if gerr != nil || !strings.EqualFold(strings.TrimSpace(it.Category), substrateLaneCategory) {
			outside = append(outside, h)
		}
	}
	if len(outside) == 0 {
		return nil // every holder is in the lane out
	}
	reason := substrateLockReason(outside, rel)
	if cerr != nil {
		reason += " (a holder's category could not be read, so the substrate lane could not be confirmed: " + cerr.Error() + ")"
	}
	for _, h := range outside {
		recordSubstrateLockDeny(ctx, a, h, rel)
	}
	return denyPreToolUse(cmd, raw, reason)
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
	inHome, foreign := bashMutationTargets(command, anchor)
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
// what let a shell command rewrite the substrate that judges the story. holders is
// called only once the command has a candidate, so a command that touches nothing
// lockable opens no store; the config is read once.
//
// What the command text cannot show is not guessed at. Two cases are invisible and
// therefore allowed: (a) an interpreter running a script file whose body and target
// are not in the command text (`python3 tool.py`), and (b) a path assembled from
// fragments that never spell the lock root (`'.sat'+'elle'`). The rule is
// conservative the other way: an interpreter command whose text, or a NAME=value
// assignment in the same command, names a locked path or the bare lock root is a
// candidate even if it only reads it — reads have the
// read tools and `satelle doc get`. A substrate-lane holder is never refused. The
// backstop for the invisible cases is unchanged: substrate diffs are seen at the
// commit and at integration review.
func bashSubstrateLockGate(cmd *cobra.Command, raw []byte, command string, holders func() ([]seatInfo, error)) error {
	cfg := loadSubstrateLockConfig()
	cands := bashLockCandidates(command, cfg)
	if len(cands) == 0 {
		return nil
	}
	hs, err := holders()
	if err != nil {
		return denyPreToolUse(cmd, raw, "satelle: "+err.Error())
	}
	if len(hs) == 0 {
		return nil
	}
	for _, c := range cands {
		if err := substrateLockGateWith(cfg, cmd, raw, c, hs); err != nil {
			return err
		}
	}
	return nil
}

// recordSubstrateLockDeny appends the refusal to the holder's ledger so it is
// auditable after the fact. A failure to record never lifts the refusal: the
// caller denies either way.
func recordSubstrateLockDeny(ctx context.Context, a *app.App, holder seatInfo, rel string) {
	payload, err := json.Marshal(substrateLockPayload{Path: rel, Status: seatStatus(holder), Lane: substrateLaneCategory})
	if err != nil {
		return
	}
	_, _ = a.Store.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: holder.ItemID, Kind: ledger.KindSubstrateLockDeny, Actor: substrateLockDenyActor,
		Body:    fmt.Sprintf("substrate lock refused an edit to %s while %s held a performing seat (status %s)", rel, holder.ItemID, seatStatus(holder)),
		Payload: payload,
	}, time.Now().UTC())
}
