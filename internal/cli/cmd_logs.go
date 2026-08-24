package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/logsread"
)

func init() {
	var role, story string
	var tail int
	var showPath bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Read this repo's runtime logs (via .satelle/logs pointer or home-keyed dir)",
		Long: `logs reads satelle's runtime logs for this repo. Storage is home-keyed
(~/.satelle/<repo-key>/logs); .satelle/logs is a symlink pointer to that
directory. --path prints the resolved runtime directory. --story and --role
select the newest matching dispatch log.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, cfgPath, err := config.Load("")
			if err != nil {
				return err
			}
			root := config.RepoRootFromConfigPath(cfgPath)
			abs := cfg.ResolveLogsDir(root)
			if showPath {
				fmt.Fprintln(cmd.OutOrStdout(), abs)
				return nil
			}
			logsDir := abs
			if st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(logsPointerRel))); err == nil && st.Mode()&os.ModeSymlink != 0 {
				logsDir = filepath.Join(root, filepath.FromSlash(logsPointerRel))
			}
			f, ok, err := logsread.Select(logsDir, story, role)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("logs: no matching dispatch log")
			}
			raw, err := os.ReadFile(f.Path)
			if err != nil {
				return err
			}
			lines := logsread.LastNLines(string(raw), tail)
			fmt.Fprintf(cmd.OutOrStdout(), "# %s\n", f.Path)
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n"))
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "dispatch agent/role (executor, planner, reviewer, …)")
	cmd.Flags().StringVar(&story, "story", "", "story id")
	cmd.Flags().IntVar(&tail, "tail", 50, "last N lines")
	cmd.Flags().BoolVar(&showPath, "path", false, "print the resolved runtime logs directory")
	register(cmd)
}
