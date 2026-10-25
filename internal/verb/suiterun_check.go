package verb

// Tree-keyed suite evidence for functional-check gates (sty_dc554488).
//
// A check skill that declares `check_reuse: tree` has each run recorded as a
// KindSuiteRun row naming the tree it proved. The next presentation of the same
// check on the same story, command and tree finds that green row and the gate
// accepts without running the command. The row is the ledger's own suite_run
// kind (the payload gains optional fields), so `ledger-citation` still reads it.
// Enumeration only: the engine decides what a hit means.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// SuiteEvidence is one recorded run of a functional check on a tree.
type SuiteEvidence struct {
	ID         string
	Check      string // the check skill's name
	Tree       string // GitContentTree of the directory the command ran in
	SHA        string // HEAD when the command ran
	Command    string
	CommandSHA string // CommandDigest(Command)
	Outcome    string // SuiteGreen or SuiteRed
	DurationMS int64
	RecordedAt time.Time
}

// SuiteGreen and SuiteRed are the outcome vocabulary of a recorded run.
const (
	SuiteGreen = suiteOutcomeGreen
	SuiteRed   = suiteOutcomeRed
)

// CommandDigest is the identity of a check command: editing the skill's script
// invalidates the proof a previous run left.
func CommandDigest(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:])
}

// ShortTree abbreviates a tree oid for notes and bodies.
func ShortTree(tree string) string {
	if len(tree) > 12 {
		return tree[:12]
	}
	return tree
}

// GitContentTree returns the git tree oid of HEAD plus every working-tree
// change — tracked edits and untracked, non-ignored files — together with HEAD's
// commit sha. It builds the tree in a throwaway index (GIT_INDEX_FILE), so the
// repository's own index is never touched. A commit that adds nothing to the
// tree keeps the same oid.
func GitContentTree(dir string) (tree, head string, err error) {
	tmp, err := os.MkdirTemp("", "satelle-tree-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
	git := func(args ...string) (string, error) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = env
		out, cerr := c.Output()
		if cerr != nil {
			return "", fmt.Errorf("git %s: %w", args[0], cerr)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if head, err = git("rev-parse", "HEAD"); err != nil {
		return "", "", err
	}
	if _, err = git("read-tree", "HEAD"); err != nil {
		return "", "", err
	}
	if _, err = git("add", "-A"); err != nil {
		return "", "", err
	}
	if tree, err = git("write-tree"); err != nil {
		return "", "", err
	}
	return tree, head, nil
}

// RecordCheckRun appends one check run as a suite_run row and returns its id.
// With no ledger wired it records nothing and returns "".
func RecordCheckRun(ctx context.Context, storyID string, ev SuiteEvidence) (string, error) {
	if ledgerStore == nil {
		return "", nil
	}
	now := time.Now()
	started := now.Add(-time.Duration(ev.DurationMS) * time.Millisecond).UTC().Format(time.RFC3339)
	payload, err := json.Marshal(suiteRunPayload{
		SHA:        ev.SHA,
		Command:    ev.Command,
		Outcome:    ev.Outcome,
		StartedAt:  started,
		FinishedAt: now.UTC().Format(time.RFC3339),
		Check:      ev.Check,
		Tree:       ev.Tree,
		CommandSHA: ev.CommandSHA,
		DurationMS: ev.DurationMS,
	})
	if err != nil {
		return "", err
	}
	e, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    ledger.KindSuiteRun,
		Actor:   "gate",
		Body:    fmt.Sprintf("suite run %s %s tree %s", ev.Outcome, ev.Check, ShortTree(ev.Tree)),
		Payload: payload,
	}, now)
	if err != nil {
		return "", err
	}
	return e.ID, nil
}

// FindCheckRun returns the newest green run of check on this story whose
// command and tree match. A red run is never returned.
func FindCheckRun(ctx context.Context, storyID, check, commandSHA, tree string) (SuiteEvidence, bool) {
	if ledgerStore == nil || tree == "" {
		return SuiteEvidence{}, false
	}
	rows, err := ledgerStore.ListByStory(ctx, storyID, ledger.KindSuiteRun)
	if err != nil {
		return SuiteEvidence{}, false
	}
	for i := len(rows) - 1; i >= 0; i-- {
		var p suiteRunPayload
		if json.Unmarshal(rows[i].Payload, &p) != nil {
			continue
		}
		if p.Outcome != suiteOutcomeGreen || p.Check != check || p.CommandSHA != commandSHA || p.Tree != tree {
			continue
		}
		return SuiteEvidence{
			ID: rows[i].ID, Check: p.Check, Tree: p.Tree, SHA: p.SHA, Command: p.Command,
			CommandSHA: p.CommandSHA, Outcome: p.Outcome, DurationMS: p.DurationMS,
			RecordedAt: rows[i].CreatedAt,
		}, true
	}
	return SuiteEvidence{}, false
}
