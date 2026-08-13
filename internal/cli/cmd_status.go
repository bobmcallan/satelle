package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	var line bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Show the local repo's config, database, and store counts",
		Long: `Show what satelle resolved for THIS repo: the config it loaded, where the
database and runtime plane live, and how many stories, tasks and docs it holds.

The orientation command in an unfamiliar checkout, and the first check when
something writes to a place you did not expect. For "can satelle govern here at
all", use satelle doctor, which judges rather than reports.`,
		Annotations: needsStore(),
		// --line is the Claude statusline renderer (sty_4e6f0788). It must never
		// fail loudly, so it does NOT go through the store bootstrap: openAppForCmd
		// refuses on breaking drift or a missing agents layer, and a refusal there
		// would put an error where the operator's status area should be. Defining
		// PersistentPreRunE here overrides the root's for this command only —
		// every other status invocation still opens the store exactly as before.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if line {
				return nil
			}
			return openAppForCmd(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if line {
				return runStatusLine(os.Stdout)
			}
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			stories, err := a.Store.Stories.Count(ctx, workitem.KindStory)
			if err != nil {
				return err
			}
			tasks, err := a.Store.Stories.Count(ctx, workitem.KindTask)
			if err != nil {
				return err
			}
			events, err := a.Store.Ledger.Count(ctx)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "repo root\t%s\n", a.RepoRoot)
			fmt.Fprintf(w, "data dir\t%s\n", a.DataDir)
			fmt.Fprintf(w, "runtime dir\t%s\n", a.RuntimeDir)
			fmt.Fprintf(w, "database\t%s\n", a.DBPath)
			if note := a.Config.LegacyRuntimeNote(a.RepoRoot); note != "" {
				fmt.Fprintf(w, "runtime layout\tlegacy — run satelle runtime migrate\n")
			} else {
				fmt.Fprintf(w, "runtime layout\thome-keyed\n")
				if line := runtimeResidueLine(a); line != "" {
					fmt.Fprint(w, line)
				}
			}
			// Real availability, not a config echo (sty_fb5e6d96): the URL a
			// user opens plus whether anything is answering on it.
			fmt.Fprintf(w, "web service\t%s\n", probeWebAvailability().statusValue())
			for _, s := range config.MachineScopeStrays(a.DataDir) {
				fmt.Fprintf(w, "config stray\t%s\n", s.Warning())
			}
			fmt.Fprintf(w, "log level\t%s\n", a.Config.ResolveLogLevel())
			fmt.Fprintf(w, "stories\t%d\n", stories)
			fmt.Fprintf(w, "tasks\t%d\n", tasks)
			fmt.Fprintf(w, "ledger entries\t%d\n", events)

			dirs := a.AuthoredDirs()
			for _, kind := range config.AuthoredKinds {
				n, err := a.Store.DocIndex.Count(ctx, kind)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "indexed %s\t%d  (%s)\n", kind, n, dirs[kind])
			}
			// Scaffold drift (sty_ac25b787): status is exempt from fail-closed refuse
			// so it can report here. Clean when no harness scaffolding is deployed
			// or all wrappers match this binary's canonical bodies.
			if findings := DetectScaffoldDrift(a.RepoRoot); len(findings) == 0 {
				fmt.Fprintf(w, "scaffold\tclean\n")
			} else {
				fmt.Fprintf(w, "scaffold\tDRIFT (%d) — run satelle init\n", len(findings))
				for _, f := range findings {
					fmt.Fprintf(w, "  scaffold.%s\t[%s] %s\n", f.Path, f.Kind, f.Detail)
				}
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&line, "line", false,
		"render one line for a terminal statusline: local web service link plus <story_id>::<stage> (read-only; always exits 0). "+
			"Opt in by setting it as statusLine.command in your own "+claudeUserSettingsRel+" — satelle never installs it")
	register(c)
}

// runtimeResidueLine reports in-repo .satelle/stories when the runtime plane is
// already home-keyed (sty_58fa970e AC4). Empty when legacy layout or no residue.
func runtimeResidueLine(a *app.App) string {
	if a == nil || a.Config.LegacyRuntimeNote(a.RepoRoot) != "" {
		return ""
	}
	residue := filepath.Join(a.DataDir, "stories")
	if !dirExists(residue) {
		return ""
	}
	return "runtime residue\t" + residue + " — pre-relocation attachment dir; rm -rf " + residue + " after verifying home-keyed copy\n"
}
