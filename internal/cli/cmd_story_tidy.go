package cli

import (
	"github.com/spf13/cobra"
)

// storyTidyCommands is `satelle story tidy <id> <path>...` and
// `satelle story untidy <id> [<path>...|--all]` (sty_d74e9b1b).
func storyTidyCommands() []*cobra.Command {
	tidy := &cobra.Command{
		Use:   "tidy <id> <path>...",
		Short: "Move untracked files this story created out of the tree (reversible; never deletes)",
		Long: `Move files the story itself created out of the working tree into a story-level
tidy area under the satelle scratch directory, recording one ledger row per file.
Nothing is deleted; "satelle story untidy" moves it back.

Reach for it when a gate rejects for stray untracked files (debris a dispatched
coder could create but not delete). It works at any performing step, from the
driver or a dispatched session, and is not blocked by the edit gate because it
can only remove the story's own creations.

A path is refused — with a stated reason, and NOTHING is moved — when it is
tracked, exists in HEAD or at the engagement baseline, predates the engagement
baseline, does not exist, or lies outside the worktree.`,
		Args:        cobra.MinimumNArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-tidy", tidyRequest(cmd, args[0], args[1:], false))
		},
	}
	var all bool
	untidy := &cobra.Command{
		Use:   "untidy <id> [<path>...]",
		Short: "Restore files previously moved by satelle story tidy",
		Long: `Move tidied files back to where they were. Name the original path(s), or pass
--all to restore everything tidied for the story that has not been restored.
Refuses (moving nothing) when the original location now exists — it never
overwrites.`,
		Args:        cobra.MinimumNArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-untidy", tidyRequest(cmd, args[0], args[1:], all))
		},
	}
	untidy.Flags().BoolVar(&all, "all", false, "restore every unrestored tidy for the story")
	return []*cobra.Command{tidy, untidy}
}

func tidyRequest(cmd *cobra.Command, id string, paths []string, all bool) map[string]any {
	req := map[string]any{"id": id, "paths": paths}
	if all {
		req["all"] = true
	}
	// The scratch parent is keyed by the configured repo root, exactly as the
	// dispatch layout is, so tidy and dispatch scratch share one story dir.
	if a, err := appFrom(cmd); err == nil && a != nil {
		req["repo_root"] = a.RepoRoot
	}
	return req
}
