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
// not a lock. The committed config is read ONCE, and a config that cannot be
// read or parsed fails closed: the default lock, with the seeded dump globs so
// the story-reference dumps stay writable.
func substrateLockGate(cmd *cobra.Command, raw []byte, target string, holders []seatInfo) error {
	_, invoking, _, processPath, lerr := config.LoadInvokingProcess()
	var content []byte
	var rerr error
	// A missing process file, or no path because the invoking tree is the
	// main tree and that tree has no config file, is the default lock.
	// Any other read or parse failure also fails closed to that default.
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
	lockPaths, _ := config.ParseLockSubstratePaths(string(content))
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
