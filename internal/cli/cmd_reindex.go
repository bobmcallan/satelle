package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	var validate bool
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Normalize authored markdown to OKF and sync it into the index; regenerate the read-only views",
		Long: `reindex runs the directory monitor once: it walks the configured authored
dirs, (re)indexes changed markdown files, prunes entries whose files were
removed, ingests tasks, and regenerates the read-only OKF views (the stories
backlog, the summary sub-bundle). It is a PASS-THROUGH — it never blocks
indexing; instead, each changed authored doc that fails its structure reviewer is
filed as a type:system story for implementation (deduped). The web server runs
the same sync continuously (without validation, to keep the poll loop cheap).`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			// Sync through the verb seam (CLI and web refresh via the same path).
			body, _ := json.Marshal(map[string]any{"dirs": a.AuthoredDirs()})
			resp, err := verb.Dispatch(cmd.Context(), "doc-sync", body)
			if err != nil {
				return err
			}
			_ = printJSON(cmd, resp)
			// Tasks are authored substrate (sty_c1f9e74c): ingest every .satelle/tasks
			// work-definition file into the store (the file is the source of truth) and
			// adopt any legacy DB-only task by writing its file.
			if idx, mig, terr := verb.SyncTasks(cmd.Context(), a.Store.Stories, time.Now()); terr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "reindex: task sync: %v\n", terr)
			} else if idx > 0 || mig > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "tasks: indexed %d, migrated %d\n", idx, mig)
			}
			// Regenerate the read-only OKF backlog reference under .satelle/stories/
			// from the store (the DB stays the sole story store; this is a disposable
			// view). Best-effort — a render failure must not fail indexing.
			if n, serr := verb.SyncStoryBacklog(cmd.Context(), a.Store.Stories, time.Now()); serr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "reindex: story backlog: %v\n", serr)
			} else if n > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "stories: backlog reference +%d\n", n)
			}
			// Repo-health warning (sty_7db2ed7d): the legacy actors.toml filename is no
			// longer loaded, so a repo still carrying it is silently on defaults. Warn
			// on reindex (a pass-through — it does not fail).
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(a.DBPath), config.ActorsConfigName)); statErr == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "reindex: WARN deprecated %s/%s — rename it to %s (the legacy filename is no longer loaded)\n",
					config.DefaultDataDir, config.ActorsConfigName, config.AgentsConfigName)
			}
			// Story-conformance warning (sty_af239840): a non-empty category is a
			// DETERMINISTIC conformance rule (it selects the governing workflow), and
			// reindex is where structural conformance is reported for the DB-only
			// story store. A pass-through warning — bare stubs stay legal at create;
			// the operator fixes with `satelle story set <id> --category <c>`.
			warnUncategorisedStories(cmd, a)
			if !validate {
				return nil
			}
			var res docindex.SyncResult
			if err := json.Unmarshal(resp, &res); err != nil || len(res.Changed) == 0 {
				return nil
			}
			validateChanged(cmd, a, res.Changed)
			return nil
		},
	}
	cmd.Flags().BoolVar(&validate, "validate", true, "file a type:system story for each changed doc that fails its structure reviewer")
	register(cmd)
}

// validateChanged runs each changed authored doc through its DETERMINISTIC
// structure check (internal/structure) and files a type:system story for any
// failure. Deterministic and fast — no agent CLI, no flakiness; index stays a
// pass-through (it never blocks indexing).
func validateChanged(cmd *cobra.Command, a *app.App, changed []docindex.DocRef) {
	ctx := context.Background()
	out := cmd.OutOrStdout()
	resolve := skillResolver(a)
	for _, ch := range changed {
		if !structure.Checked(ch.Kind) {
			continue // no structure check for this kind (e.g. documents)
		}
		doc, derr := a.Store.DocIndex.Get(ctx, ch.Kind, ch.Name)
		if derr != nil {
			continue
		}
		problems := structure.Doc(ch.Kind, ch.Name, doc.Body, resolve)
		if len(problems) == 0 {
			continue
		}
		notes := strings.Join(problems, "; ")
		id, filed, ferr := fileSystemStory(ctx, a, ch, notes)
		switch {
		case ferr != nil:
			fmt.Fprintf(cmd.ErrOrStderr(), "reindex: file story for %s/%s: %v\n", ch.Kind, ch.Name, ferr)
		case filed:
			fmt.Fprintf(out, "FAIL  %s/%s — filed %s (type:system): %s\n", ch.Kind, ch.Name, id, notes)
		default:
			fmt.Fprintf(out, "FAIL  %s/%s — open story %s already tracks it\n", ch.Kind, ch.Name, id)
		}
	}
}

// warnUncategorisedStories reports every OPEN (non-terminal) story with an
// empty category as a reindex warning (sty_af239840) — deterministic code at
// the index path, no LLM, independent of gate_create. The message names the
// exact fix (--category). Best-effort: a list error never fails the reindex.
func warnUncategorisedStories(cmd *cobra.Command, a *app.App) {
	items, err := a.Store.Stories.List(context.Background(), workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil {
		return
	}
	var ids []string
	for _, it := range items {
		if it.Status == workitem.StatusDone || it.Status == "cancelled" {
			continue
		}
		if strings.TrimSpace(it.Category) == "" {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	const show = 5
	listed := strings.Join(ids[:min(len(ids), show)], ", ")
	if len(ids) > show {
		listed += fmt.Sprintf(", … (+%d more)", len(ids)-show)
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"reindex: WARN %d open stor%s without a category (%s) — set one with `satelle story set <id> --category <c>` (or pass --category at create); the category selects the governing workflow\n",
		len(ids), plural(len(ids), "y", "ies"), listed)
}

// plural returns sing when n==1, else plur.
func plural(n int, sing, plur string) string {
	if n == 1 {
		return sing
	}
	return plur
}

// docTag is the dedup key tagging a system story to the doc it tracks.
func docTag(ch docindex.DocRef) string { return "doc:" + ch.Kind + "/" + ch.Name }

// fileSystemStory creates a type:system story for a non-conforming doc, unless a
// still-open (non-terminal) story already tracks it (same doc tag). Returns the
// story id, whether it was newly filed, and any error. Created directly via the
// store, so the auto-filed story does not itself re-enter the create gate.
func fileSystemStory(ctx context.Context, a *app.App, ch docindex.DocRef, notes string) (string, bool, error) {
	tag := docTag(ch)
	existing, err := a.Store.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil {
		return "", false, err
	}
	for _, it := range existing {
		// Dedup against any NON-TERMINAL tracking story (it now rests at backlog,
		// or has moved further along) — a done/cancelled one should not suppress a
		// fresh story for a doc that is still non-conforming.
		if it.Status == workitem.StatusDone || it.Status == "cancelled" {
			continue
		}
		for _, t := range it.Tags {
			if t == tag {
				return it.ID, false, nil // already tracked — no duplicate
			}
		}
	}
	title := fmt.Sprintf("Fix %s structure: %s/%s", ch.Kind, ch.Kind, ch.Name)
	body := fmt.Sprintf("The authored %s `%s` was indexed but failed its deterministic structure check. "+
		"Bring it into conformance, then re-index.\n\nProblems:\n%s",
		strings.TrimSuffix(ch.Kind, "s"), ch.Name, notes)
	ac := fmt.Sprintf("1. %s/%s passes `satelle %s validate %s`.\n2. The reviewer notes above are resolved.",
		ch.Kind, ch.Name, strings.TrimSuffix(ch.Kind, "s"), ch.Name)
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: title, Body: body, AcceptanceCriteria: ac,
		Category: "system", Priority: "high",
		Tags: []string{"type:system", tag},
	}, time.Now())
	if err != nil {
		return "", false, err
	}
	return it.ID, true, nil
}
