// `satelle rebase` — the "start clean" recovery: back up the repo's authored
// process substrate (workflows, skills, principles) and WIPE it, leaving the
// authored dirs empty so the embedded defaults govern through the read-time
// overlay. One step beyond `satelle restore`, which only overwrites files that
// have embedded counterparts and never touches extras. Destructive by design, so
// the backup is mandatory (no backup written → abort) and the wipe needs an
// explicit confirmation (or --yes).
//
// It does NOT re-seed the defaults onto disk (sty_cc550a88). Under virtual sparse
// defaults the known-good default solution IS the empty authored dir; seeding
// after the wipe would land stamped copies that shadow the shipped defaults and
// freeze them against the next binary upgrade — the opposite of a reset.

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
)

// rebaseKinds are the substrate dirs rebase backs up, wipes, and redeploys — the
// kinds the embedded default solution owns. Documents, tasks, the constitution,
// configs, and the database are the repo's own content and are never touched.
var rebaseKinds = []string{"workflows", "skills", "principles"}

// rebasePreserve are data-dir-relative paths that LIVE inside a rebaseKind but
// are repo CONFIGURATION, not part of the embedded default solution. They are
// restored from the backup after the wipe.
//
// The distinction that matters: every other file under those dirs has an
// embedded counterpart, so the read-time overlay heals its absence — wiping it
// returns it to its default. The agents layer has NO embedded counterpart, and
// an initialized repo with no loadable agents layer refuses to run
// (requireAgents, sty_d0d6bb67). Wiping it therefore bricks the repo rather than
// resetting it, which is exactly what shipped when the agents layer moved into
// workflows/ (sty_10f732ed) and nothing taught rebase about it (sty_72ccafaa).
//
// Spelled via config.AgentsRel so the location has ONE spelling and a future
// relocation moves both ends together.
var rebasePreserve = []string{config.AgentsRel}

func init() {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rebase",
		Short: "Back up the process substrate, then reset it to the embedded defaults (DESTRUCTIVE)",
		Long: `rebase resets a repo's process substrate to the embedded default solution:

  1. BACKS UP .satelle/{workflows,skills,principles} to a timestamped directory
     under .satelle/backups/ — mandatory: if the backup cannot be written the
     rebase aborts with nothing changed. Same backups/ root as init/restore
     pre-mutation backups (sty_873a5380); online/personal channel is best-effort
     (see [backup] local_only and [hosted]).
  2. WIPES those three dirs (the backup is the undo),
  3. RECREATES them empty, with only their README keep-file. It does NOT copy the
     defaults back — the shipped route, gate skills and operating principles
     govern from inside the binary through the read-time overlay, so an empty
     authored dir IS the default solution. Copying them back would shadow the
     shipped versions and freeze this repo against the next upgrade. Use
     'satelle substrate edit <kind> <name>' to materialize one for editing.

The embedded default TASK re-seeds, because a coded gate checks for an on-disk
task header — tasks cannot live virtually, and authored tasks are never wiped.

Repo CONFIGURATION that happens to live inside those dirs is preserved across
the wipe and restored from the backup — today that is workflows/agents.toml, the
agents layer. It has no embedded counterpart, so unlike everything else there the
overlay cannot heal its absence: wiping it would brick the repo rather than
reset it.

Documents, tasks, story attachments, the constitution, satelle.toml/agents.toml,
and the database are never touched. This is the "start clean" recovery, one step
beyond 'satelle restore' (which only overwrites files that have embedded
counterparts and leaves extras in place).

Because it wipes authored files it asks for confirmation first (pass --yes to
confirm non-interactively).`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			// Authored kinds live under DataDir; the mandatory backup lands under RuntimeDir.
			opts := ResolveBackupOpts(a.Config, a.RepoRoot)
			opts.BackupsDir = a.RuntimeDir
			return runRebase(cmd.OutOrStdout(), cmd.InOrStdin(), a.DataDir, a.RuntimeDir, yes, time.Now(), opts)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the wipe non-interactively")
	register(cmd)
}

// runRebase performs the backup → wipe → redeploy sequence for dataDir. The
// backup is the wipe: each kind dir is RENAMED into the timestamped backup dir
// under runtimeDir (sty_4660bbe1), so no bytes are lost before the defaults land
// — and a rename failure aborts before anything else moves (the backup is mandatory).
func runRebase(out io.Writer, in io.Reader, dataDir, runtimeDir string, yes bool, now time.Time, backupOpts ...BackupOpts) error {
	var opts BackupOpts
	if len(backupOpts) > 0 {
		opts = backupOpts[0]
	}
	if runtimeDir == "" {
		runtimeDir = dataDir // tests / legacy callers
	}
	backupDir := filepath.Join(runtimeDir, "backups", now.Format("20060102-150405"))

	// Show the plan, then require an explicit yes — rebase wipes authored files.
	fmt.Fprintf(out, "rebase will back up %s to %s, then reset each to the embedded default solution.\n",
		strings.Join(rebaseKinds, ", "), backupDir)
	if !yes {
		fmt.Fprint(out, "This wipes any customized/authored files in those dirs (the backup is the undo). Type 'yes' to continue: ")
		reader := bufio.NewReader(in)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(line)) != "yes" {
			fmt.Fprintln(out, "rebase: aborted — nothing changed")
			return nil
		}
	}

	// 1. Backup (mandatory): move each existing kind dir under the backup dir. A
	//    failure here aborts — a wipe must never proceed without its backup.
	//    Directory-level rename is the pre-mutation copy for wipe (same backups/
	//    root as the file-level helper; sty_873a5380).
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("rebase: create backup dir %s: %w (aborted — nothing changed)", backupDir, err)
	}
	// Advisory / online policy via the shared helper surface (empty body path
	// records the policy notice without a file copy — dirs are moved below).
	if note := backupPolicyNotice(opts, backupDir); note != "" {
		fmt.Fprintln(out, note)
	}
	backedUp := 0
	for _, kind := range rebaseKinds {
		src := filepath.Join(dataDir, kind)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue // nothing to back up for this kind
		}
		dst := filepath.Join(backupDir, kind)
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rebase: backup %s → %s: %w (aborted — already-moved kinds remain in the backup)", kind, dst, err)
		}
		backedUp++
		fmt.Fprintf(out, "  ^ %s/ → %s\n", kind, dst)
	}
	// Best-effort hosted push of the pre-wipe tree (sty_873a5380 AC2).
	if n, msg := pushBackupTreeHosted(backupDir, opts); msg != "" {
		fmt.Fprintln(out, msg)
		_ = n
	}

	// 2+3. Recreate each dir with its README keep-file, and STOP. Under virtual
	//      sparse defaults (sty_29e5a9a5) the known-good default solution IS the
	//      empty authored dir: List/Get overlay the embedded bytes at read time,
	//      so the wipe alone completes the reset.
	//
	//      Re-seeding here used to land 25+ stamped copies, which is worse than a
	//      no-op: a materialised copy SHADOWS the shipped default, so the next
	//      binary upgrade leaves the repo running a frozen fork of a gate it never
	//      chose to pin. That is the state sty_5604e741 had to delete by hand, and
	//      rebase was the thing putting it back (sty_cc550a88).
	for _, kind := range rebaseKinds {
		dir := filepath.Join(dataDir, kind)
		if _, err := ensureDir(dir); err != nil {
			return fmt.Errorf("rebase: %w", err)
		}
		if _, err := ensureReadme(dir, kind); err != nil {
			return fmt.Errorf("rebase: %w", err)
		}
	}
	// Restore the preserved configuration the wipe swept up. The wipe is a
	// directory RENAME, so the bytes are already in the backup; copy (not move)
	// them back so the backup stays a complete undo of the pre-rebase tree.
	for _, rel := range rebasePreserve {
		src := filepath.Join(backupDir, filepath.FromSlash(rel))
		if _, serr := os.Stat(src); serr != nil {
			continue // the repo never had one; nothing to preserve
		}
		dst := filepath.Join(dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("rebase: preserve %s: %w", rel, err)
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("rebase: preserve %s: %w", rel, err)
		}
		fmt.Fprintf(out, "  = %s (preserved — repo configuration, not default substrate)\n", rel)
	}

	// Tasks are the one carve-out, and it is pre-existing and principled: coded
	// gates check for an on-disk task HEADER, so a task cannot live virtually.
	// "tasks" is NOT a rebaseKind — authored tasks are repo content and are never
	// wiped — making this purely additive healing (sty_d4360e90).
	restored := 0
	for _, line := range materializeTasks(dataDir, opts) {
		fmt.Fprintln(out, line)
		restored++
	}

	fmt.Fprintf(out, "rebase: backed up %d dir(s) to %s; restored %d default task(s); workflows/skills/principles now resolve from the embedded defaults (run `satelle reindex` to sync the index)\n",
		backedUp, backupDir, restored)
	if _, err := writeDeployedVersion(dataDir); err != nil {
		return fmt.Errorf("rebase: write deployed.version: %w", err)
	}
	return nil
}
