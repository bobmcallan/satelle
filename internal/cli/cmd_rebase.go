// `satelle rebase` — the "start clean" recovery: back up the repo's authored
// process substrate (workflows, skills, principles), WIPE it, and redeploy the
// complete embedded default solution. One step beyond `satelle restore`, which
// only overwrites files that have embedded counterparts and never touches
// extras. Destructive by design, so the backup is mandatory (no backup written →
// abort) and the wipe needs an explicit confirmation (or --yes).

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
)

// rebaseKinds are the substrate dirs rebase backs up, wipes, and redeploys — the
// kinds the embedded default solution owns. Documents, tasks, the constitution,
// configs, and the database are the repo's own content and are never touched.
var rebaseKinds = []string{"workflows", "skills", "principles"}

func init() {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rebase",
		Short: "Back up the process substrate, then reset it to the complete default solution (DESTRUCTIVE)",
		Long: `rebase resets a repo's process substrate to the embedded default solution:

  1. BACKS UP .satelle/{workflows,skills,principles} to a timestamped directory
     under .satelle/backups/ — mandatory: if the backup cannot be written the
     rebase aborts with nothing changed,
  2. WIPES those three dirs (the backup is the undo),
  3. REDEPLOYS the complete default solution: the generic project, parent, and
     task-execution workflows plus every gate skill they reference, and the
     embedded operating principles.

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
			return runRebase(cmd.OutOrStdout(), cmd.InOrStdin(), filepath.Dir(a.DBPath), yes, time.Now())
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the wipe non-interactively")
	register(cmd)
}

// runRebase performs the backup → wipe → redeploy sequence for dataDir. The
// backup is the wipe: each kind dir is RENAMED into the timestamped backup dir,
// so no bytes are lost before the defaults land — and a rename failure aborts
// before anything else moves (the backup is mandatory).
func runRebase(out io.Writer, in io.Reader, dataDir string, yes bool, now time.Time) error {
	backupDir := filepath.Join(dataDir, "backups", now.Format("20060102-150405"))

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
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("rebase: create backup dir %s: %w (aborted — nothing changed)", backupDir, err)
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

	// 2+3. Recreate each dir (with its README keep-file) and redeploy the complete
	//      default solution — the same materialisers init uses on a fresh repo.
	for _, kind := range rebaseKinds {
		dir := filepath.Join(dataDir, kind)
		if _, err := ensureDir(dir); err != nil {
			return fmt.Errorf("rebase: %w", err)
		}
		if _, err := ensureReadme(dir, kind); err != nil {
			return fmt.Errorf("rebase: %w", err)
		}
	}
	deployed := 0
	for _, line := range materializeDefaultSolution(dataDir) {
		fmt.Fprintln(out, line)
		deployed++
	}
	for _, line := range materializePrinciples(dataDir) {
		fmt.Fprintln(out, line)
		deployed++
	}

	fmt.Fprintf(out, "rebase: backed up %d dir(s) to %s; deployed %d default file(s) (run `satelle reindex` to sync the index)\n",
		backedUp, backupDir, deployed)
	return nil
}
