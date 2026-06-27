// `satelle skill|workflow|principle create` is satelle's gated upsert path for
// authored objects: it writes the markdown file THROUGH the object's
// satelle-<object>-review structure gate, refusing to persist a non-conforming
// artifact — the same discipline `satelle story create` applies to work items.
// This is configuration-over-code: a repo authors substrate, the binary gates
// its structure (sty_a792cff3).
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/reviewer"
)

func init() {
	// skill/principle get their own command groups; workflow create is attached
	// to the existing `satelle workflow` group (see cmd_workflow.go).
	register(authoredGroup("skill", "skills"))
	register(authoredGroup("principle", "principles"))
}

// authoredGroup builds a `satelle <singular>` group with a gated `create`.
func authoredGroup(singular, kind string) *cobra.Command {
	g := &cobra.Command{Use: singular, Short: "Manage authored " + kind + " (markdown substrate)"}
	g.AddCommand(authoredCreateCmd(kind))
	return g
}

// authoredCreateCmd builds the gated `create` for an authored doc kind.
func authoredCreateCmd(kind string) *cobra.Command {
	var name, from string
	var force bool
	c := &cobra.Command{
		Use:   "create --name <name> [--from <file>]",
		Short: "Create an authored " + kind + " doc through its structure gate",
		Long: "create writes a " + kind + " markdown file under its substrate dir, but only\n" +
			"after the " + reviewer.StructureReviewerFor(kind) + " structure reviewer accepts it.\n" +
			"The markdown is read from --from <file>, or from stdin. A reject writes nothing.",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validArtifactName(name); err != nil {
				return err
			}
			body, err := readDraft(cmd, from)
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return fmt.Errorf("empty draft — provide markdown via --from <file> or stdin")
			}

			g, a, err := gaterForCmd(cmd)
			if err != nil {
				return err
			}
			rev := reviewer.StructureReviewerFor(kind)
			dec, err := g.ReviewStructure(context.Background(), rev, kind, name, body)
			if err != nil {
				return err
			}
			if dec.Gated && !dec.Accept {
				return fmt.Errorf("%s rejected by %s: %s", kind, rev, dec.Notes)
			}

			dir := a.Config.ResolveAuthoredDirs(a.RepoRoot)[kind]
			if dir == "" {
				return fmt.Errorf("no substrate dir configured for kind %q", kind)
			}
			path := filepath.Join(dir, name+".md")
			if !force {
				if _, statErr := os.Stat(path); statErr == nil {
					return fmt.Errorf("%s already exists (use --force to overwrite)", path)
				}
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return err
			}
			// Sync so the new doc is immediately queryable.
			_, _ = a.Store.DocIndex.Sync(context.Background(), a.AuthoredDirs(), time.Now())

			out := cmd.OutOrStdout()
			if !dec.Gated {
				fmt.Fprintf(out, "wrote %s (advisory — %s rubric not installed)\n", path, rev)
			} else {
				fmt.Fprintf(out, "wrote %s (accepted by %s)\n", path, rev)
			}
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "artifact name (required; bare, no .md)")
	c.Flags().StringVar(&from, "from", "", "read markdown from this file (default: stdin)")
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	_ = c.MarkFlagRequired("name")
	return c
}

// validArtifactName rejects a name that is not a bare slug (no path separators,
// no .md suffix), so create always writes <dir>/<name>.md.
func validArtifactName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.ContainsAny(name, "/\\") || strings.HasSuffix(name, ".md") {
		return fmt.Errorf("--name must be a bare artifact name (no path, no .md)")
	}
	return nil
}

// readDraft reads the draft markdown from a file (when from != "") or stdin.
func readDraft(cmd *cobra.Command, from string) (string, error) {
	if from != "" {
		b, err := os.ReadFile(from)
		if err != nil {
			return "", fmt.Errorf("read --from %s: %w", from, err)
		}
		return string(b), nil
	}
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	return string(b), nil
}
