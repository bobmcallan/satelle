package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/reviewer"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	var validate bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Sync authored markdown (documents, workflows, principles, skills) into the index",
		Long: `index runs the directory monitor once: it walks the configured authored
dirs, (re)indexes changed markdown files, and prunes entries whose files were
removed. It is a PASS-THROUGH — it never blocks indexing; instead, each changed
authored doc that fails its structure reviewer is filed as a type:system story
for implementation (deduped). The web server runs the same sync continuously
(without validation, to keep the poll loop cheap).`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			// Reconcile the portable story markdown mirror with the store: import
			// edited/copied-in files, export any DB story lacking a file.
			if imp, exp, serr := verb.SyncStories(cmd.Context()); serr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "index: story markdown sync — %v\n", serr)
			} else if imp+exp > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "stories: %d imported, %d exported to markdown\n", imp, exp)
			}
			// Sync through the verb seam (CLI and web refresh via the same path).
			body, _ := json.Marshal(map[string]any{"dirs": a.AuthoredDirs()})
			resp, err := verb.Dispatch(cmd.Context(), "doc-sync", body)
			if err != nil {
				return err
			}
			_ = printJSON(cmd, resp)
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

// validateChanged runs each changed reviewer-backed doc through its structure
// reviewer and files a type:system story for any failure. Fail-soft: if no agent
// CLI is configured (or a review errors), it notes that on stderr and indexing
// stands — index never blocks (pass-through).
func validateChanged(cmd *cobra.Command, a *app.App, changed []docindex.DocRef) {
	g, _, err := gaterForCmd(cmd)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "index: skipping structure validation — %v\n", err)
		return
	}
	ctx := context.Background()
	out := cmd.OutOrStdout()
	for _, ch := range changed {
		rev := reviewer.StructureReviewerFor(ch.Kind)
		if rev == "" {
			continue // no structure reviewer for this kind (e.g. documents)
		}
		doc, derr := a.Store.DocIndex.Get(ctx, ch.Kind, ch.Name)
		if derr != nil {
			continue
		}
		dec, rerr := g.ReviewStructure(ctx, rev, ch.Kind, ch.Name, doc.Body)
		if rerr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "index: validate %s/%s: %v\n", ch.Kind, ch.Name, rerr)
			continue
		}
		if !dec.Gated || dec.Accept {
			continue
		}
		id, filed, ferr := fileSystemStory(ctx, a, ch, dec.Notes)
		switch {
		case ferr != nil:
			fmt.Fprintf(cmd.ErrOrStderr(), "index: file story for %s/%s: %v\n", ch.Kind, ch.Name, ferr)
		case filed:
			fmt.Fprintf(out, "FAIL  %s/%s — filed %s (type:system): %s\n", ch.Kind, ch.Name, id, dec.Notes)
		default:
			fmt.Fprintf(out, "FAIL  %s/%s — open story %s already tracks it\n", ch.Kind, ch.Name, id)
		}
	}
}

// docTag is the dedup key tagging a system story to the doc it tracks.
func docTag(ch docindex.DocRef) string { return "doc:" + ch.Kind + "/" + ch.Name }

// fileSystemStory creates a type:system story for a non-conforming doc, unless an
// OPEN story already tracks it (same doc tag). Returns the story id, whether it
// was newly filed, and any error. Created directly via the store, so the
// auto-filed story does not itself re-enter the create gate.
func fileSystemStory(ctx context.Context, a *app.App, ch docindex.DocRef, notes string) (string, bool, error) {
	tag := docTag(ch)
	existing, err := a.Store.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Status: "open"})
	if err != nil {
		return "", false, err
	}
	for _, it := range existing {
		for _, t := range it.Tags {
			if t == tag {
				return it.ID, false, nil // already tracked — no duplicate
			}
		}
	}
	title := fmt.Sprintf("Fix %s structure: %s/%s", ch.Kind, ch.Kind, ch.Name)
	body := fmt.Sprintf("The authored %s `%s` was indexed but failed its %s structure reviewer. "+
		"Bring it into conformance, then re-index.\n\nReviewer notes:\n%s",
		strings.TrimSuffix(ch.Kind, "s"), ch.Name, reviewer.StructureReviewerFor(ch.Kind), notes)
	ac := fmt.Sprintf("1. %s/%s passes `satelle validate %s %s`.\n2. The reviewer notes above are resolved.",
		ch.Kind, ch.Name, ch.Kind, ch.Name)
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
