package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	var yes bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Re-materialise the embedded default substrate (OVERWRITES drifted on-disk copies)",
		Long: `restore re-installs the binary's embedded default substrate — the reviewer/
executor skills and the operating principles — OVERWRITING the on-disk copies
under .satelle. It is the recovery path when a repo's substrate is broken or has
drifted: the inverse of init's never-clobber seeding.

Because it overwrites files it asks for confirmation first (pass --yes to
confirm non-interactively). The embedded baseline WORKFLOW is deliberately NOT
written to disk (it stays the binary's fallback; a disk copy would compete with
the repo's own workflow), and authored files with no embedded counterpart —
your workflows, documents, tasks, configs, constitution — are never touched.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			return runRestore(cmd.OutOrStdout(), cmd.InOrStdin(), filepath.Dir(a.DBPath), yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the overwrite non-interactively")
	register(cmd)
}

// restorePlanEntry is one embedded default and what restore would do with it.
type restorePlanEntry struct {
	rel    string // path relative to the data dir, e.g. skills/commit.md
	path   string // absolute on-disk path
	body   string // the embedded canonical bytes
	action string // "create" | "overwrite" | "unchanged"
}

// runRestore re-materialises the embedded default skills + principles into
// dataDir, overwriting drifted copies after an explicit confirmation
// (sty_9e2426b3). It never touches the embedded baseline workflow (embedded-only
// by design — a disk copy would compete with the repo's authored workflow) or
// any file without an embedded counterpart.
func runRestore(out io.Writer, in io.Reader, dataDir string, yes bool) error {
	var plan []restorePlanEntry
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "workflows" {
			continue // baseline workflow stays embedded-only (init parity)
		}
		rel := filepath.Join(d.Kind, d.Name+".md")
		p := filepath.Join(dataDir, rel)
		action := "create"
		if cur, err := os.ReadFile(p); err == nil {
			if string(cur) == d.Body {
				action = "unchanged"
			} else {
				action = "overwrite"
			}
		}
		plan = append(plan, restorePlanEntry{rel: rel, path: p, body: d.Body, action: action})
	}

	changes := 0
	for _, e := range plan {
		if e.action != "unchanged" {
			changes++
		}
	}
	if changes == 0 {
		fmt.Fprintln(out, "restore: every embedded default is already in place — nothing to do")
		return nil
	}

	// Show the plan, then require an explicit yes — restore overwrites user files.
	for _, e := range plan {
		if e.action == "unchanged" {
			continue
		}
		fmt.Fprintf(out, "  %s %s\n", map[string]string{"create": "+", "overwrite": "~"}[e.action], e.rel)
	}
	if !yes {
		fmt.Fprintf(out, "restore will overwrite/create %d file(s) under %s. Type 'yes' to continue: ", changes, dataDir)
		reader := bufio.NewReader(in)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(line)) != "yes" {
			fmt.Fprintln(out, "restore: aborted — nothing written")
			return nil
		}
	}

	restored := 0
	for _, e := range plan {
		if e.action == "unchanged" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
			return fmt.Errorf("restore: %s: %w", e.rel, err)
		}
		if err := os.WriteFile(e.path, []byte(e.body), 0o644); err != nil {
			return fmt.Errorf("restore: %s: %w", e.rel, err)
		}
		restored++
	}
	fmt.Fprintf(out, "restore: %d file(s) re-materialised to the embedded defaults (run `satelle reindex` to sync the index)\n", restored)
	return nil
}
