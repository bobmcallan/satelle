// `satelle substrate` — effective process view and virtual-defaults tooling
// (sty_29e5a9a5 / epic:substrate-planes). Defaults resolve from the binary;
// on-disk files are overrides. list --effective shows provenance; edit
// materializes a default for day-one editing; prune removes unedited seeds.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/substrate"
)

func init() {
	root := &cobra.Command{
		Use:   "substrate",
		Short: "Effective process view and virtual-defaults tooling",
		Long: `Substrate is the authored process (workflows, skills, principles) layered
over the binary's sparse embedded defaults.

  satelle substrate list --effective   provenance default|edited|authored
  satelle substrate edit <kind> <name> materialize a default for editing
  satelle substrate prune              remove unedited seed copies (dry-run default)

See decision-substrate-planes-local-first.`,
	}

	var asJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List effective substrate with provenance",
		Long: `List the substrate this repo actually runs, each item tagged with where it came
from: default (embedded), edited (a materialised copy you changed), or authored
(yours alone).

The provenance is the reason to run it. Overriding is per FILE, so an embedded
default stays live until a repo-authored file of the same name exists — this is
what shows you which half of the tree is really yours.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			// Always effective (the only meaningful list under virtual defaults).
			return runSubstrateList(cmd.OutOrStdout(), a.Store.DocIndex, asJSON)
		},
	}
	listCmd.Flags().Bool("effective", true, "show effective process (default; always on)")
	listCmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")

	editCmd := &cobra.Command{
		Use:   "edit [kind] [name]",
		Short: "Materialize an embedded default onto disk for editing",
		Long: `Copy an embedded default onto disk under .satelle/ so you can edit it.

The copy takes over from that moment: the file OVERRIDES the embedded default by
name, and it no longer tracks improvements shipped in later binaries. Materialise
what you actually intend to diverge on — satelle substrate prune removes copies
that were never edited.`,
		Args:        cobra.ExactArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			return runSubstrateEdit(cmd.OutOrStdout(), a.DataDir, a.Store.DocIndex, args[0], args[1])
		},
	}

	var yes bool
	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove unedited seed copies of embedded defaults (dry-run unless --yes)",
		Long: `Remove on-disk copies of embedded defaults that were never edited, so the repo
tracks the shipped version again.

Dry-run unless --yes: it reports what it would remove first. Only byte-identical
copies go — anything you changed is authored substrate and is left alone, which
is why this cannot quietly discard your work.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			opts := ResolveBackupOpts(a.Config)
			opts.BackupsDir = a.RuntimeDir
			return runSubstratePrune(cmd.OutOrStdout(), cmd.InOrStdin(), a.DataDir, opts, yes)
		},
	}
	pruneCmd.Flags().BoolVar(&yes, "yes", false, "actually remove files (default is dry-run)")

	root.AddCommand(listCmd, editCmd, pruneCmd)
	register(root)
}

func runSubstrateList(out io.Writer, idx *docindex.Store, asJSON bool) error {
	rows, err := substrate.List(context.Background(), idx)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	fmt.Fprintf(out, "%-12s %-40s %-10s %s\n", "KIND", "NAME", "PROV", "SOURCE")
	for _, r := range rows {
		fmt.Fprintf(out, "%-12s %-40s %-10s %s\n", r.Kind, r.Name, r.Provenance, r.Source)
	}
	fmt.Fprintf(out, "\n%d effective item(s)\n", len(rows))
	return nil
}

// isUneditedSeed delegates to substrate (shared with prune and provenance).
func isUneditedSeed(diskBody, embeddedBody string) bool {
	return substrate.IsUneditedSeed(diskBody, embeddedBody)
}

func runSubstrateEdit(out io.Writer, dataDir string, idx *docindex.Store, kind, name string) error {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	if kind == "" || name == "" {
		return fmt.Errorf("substrate edit: kind and name required")
	}
	rel := kind + "/" + name + config.EmbeddedExt(kind, name)
	path := filepath.Join(dataDir, filepath.FromSlash(rel))
	if fileExists(path) {
		fmt.Fprintln(out, path)
		return nil
	}
	// Resolve embedded body.
	var body string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == kind && d.Name == name {
			body = d.Body
			break
		}
	}
	if body == "" {
		// Try docindex Get (may be already virtual).
		if doc, err := idx.Get(context.Background(), kind, name); err == nil {
			body = doc.Body
		}
	}
	if body == "" {
		return fmt.Errorf("substrate edit: no embedded default %s/%s", kind, name)
	}
	stamped := applyEmbeddedStamp(body, substrate.SHA(body))
	if err := writeEmbedded(path, stamped); err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

func runSubstratePrune(out io.Writer, in io.Reader, dataDir string, opts BackupOpts, yes bool) error {
	var plan []string // rel paths to remove
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "tasks" {
			continue
		}
		rel := d.RelPath()
		path := filepath.Join(dataDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if isUneditedSeed(string(body), d.Body) {
			plan = append(plan, rel)
		}
	}
	sort.Strings(plan)
	if len(plan) == 0 {
		fmt.Fprintln(out, "prune: no unedited seed copies found")
		return nil
	}
	fmt.Fprintf(out, "prune would remove %d unedited seed(s):\n", len(plan))
	for _, rel := range plan {
		fmt.Fprintf(out, "  - %s\n", rel)
	}
	if !yes {
		fmt.Fprintln(out, "dry-run only — re-run with --yes to remove (edited/authored files are never touched)")
		return nil
	}
	for _, rel := range plan {
		path := filepath.Join(dataDir, filepath.FromSlash(rel))
		body, _ := os.ReadFile(path)
		if _, err := backupExistingFile(dataDir, BackupKindPreMutation, rel, body, opts); err != nil {
			return fmt.Errorf("prune: backup %s: %w", rel, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("prune: remove %s: %w", rel, err)
		}
		fmt.Fprintf(out, "  removed %s\n", rel)
	}
	fmt.Fprintf(out, "prune: removed %d seed(s); effective process is unchanged (virtual defaults)\n", len(plan))
	return nil
}
