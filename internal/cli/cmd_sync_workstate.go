package cli

// `satelle sync workstate` — one-way local→server work-state replication
// (epic:scoped-sync, order:7). Always targets this repo's bound hosted
// PROJECT's personal collection; a team active-workspace binding never
// redirects work-state. The [sync] scope still gates whether each work-state
// area (stories, ledger, executions) is pushed at all (local → skip;
// personal|shared → push to personal+project).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// WorkstateAreas are the [sync] areas that form the work-state kind. A shared
// scope on any of these only means "eligible to leave the machine" — destination
// remains the personal workspace.
var WorkstateAreas = []string{"stories", "executions", "ledger"}

// newSyncWorkstateCmd builds the `satelle sync workstate` group (push only).
func newSyncWorkstateCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "workstate",
		Short: "Push work state to this repo's bound hosted project personal collection (one-way; local default skips)",
	}

	var pushServer string
	var dryRun bool
	push := &cobra.Command{
		Use:         "push",
		Short:       "Replicate opted-in work-state areas to the personal workspace",
		Annotations: needsStore(),
		Long: `push collects local stories, task-executions, and ledger entries for work-state
areas whose resolved [sync] scope is personal or shared, and upserts them into
this repo's bound hosted PROJECT's personal collection on the hosted server
(origin=cli-sync). Local-scoped areas are skipped. A team active-workspace
binding does NOT redirect work-state — destination is always the bound project's
personal partition. Requires "satelle project bind <slug>". There is no pull
(one-way by design).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstatePush(cmd, pushServer, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List which areas would be pushed without contacting the server.")
	// --workspace is intentionally absent: work-state never team-shares.
	group.AddCommand(push)
	return group
}

func runSyncWorkstatePush(cmd *cobra.Command, serverArg string, dryRun bool) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	out := cmd.OutOrStdout()

	// Resolve which areas opt in. Shared still means "push to personal".
	optIn := map[string]bool{}
	for _, area := range WorkstateAreas {
		scope, serr := config.ScopeFor(a.Config, area)
		if serr != nil {
			return fmt.Errorf("sync workstate area %q: %w", area, serr)
		}
		if scope != config.LocalScope {
			optIn[area] = true
		}
	}
	if len(optIn) == 0 {
		fmt.Fprintln(out, "No work-state to push — every work-state area is local. Set [sync] stories|ledger|executions = personal|shared to opt in.")
		return nil
	}
	if dryRun {
		fmt.Fprintf(out, "Would push work-state areas to bound project personal collection on %s:\n", server)
		for _, area := range WorkstateAreas {
			if optIn[area] {
				fmt.Fprintf(out, "  %s -> personal\n", area)
			}
		}
		return nil
	}

	// Bound project before any network (AC5).
	project, err := resolveBoundProject(a.Config)
	if err != nil {
		return err
	}

	batch, err := collectWorkstate(cmd.Context(), a, optIn)
	if err != nil {
		return err
	}
	if len(batch.Items) == 0 && len(batch.Ledger) == 0 {
		fmt.Fprintln(out, "No work-state rows to push (opted-in areas are empty).")
		return nil
	}

	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	// ALWAYS personal — ignore active team workspace binding (AC1, AC3).
	personalID, err := client.ActiveWorkspaceID(cmd.Context(), config.PersonalWorkspace)
	if err != nil {
		return fmt.Errorf("resolve personal workspace: %w", err)
	}
	res, err := client.PushWorkstate(cmd.Context(), personalID, project, batch)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("push workstate: %w", err)
	}
	fmt.Fprintf(out, "Pushed work-state to project %q personal collection on %s: %d item(s), %d ledger entr(y/ies).\n",
		project, server, res.Items, res.Ledger)
	return nil
}

// collectWorkstate builds the ingest batch from the local store for the
// opted-in areas. Stories and executions become items; ledger entries are
// collected per known story id (the ledger store refuses unfiltered scans).
func collectWorkstate(ctx context.Context, a *app.App, optIn map[string]bool) (hosted.WorkstateIngest, error) {
	var batch hosted.WorkstateIngest
	// Stories and executions both live in the workitem store, distinguished by kind.
	if optIn["stories"] {
		items, err := a.Store.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000, IncludeArchived: true})
		if err != nil {
			return batch, fmt.Errorf("list stories: %w", err)
		}
		for _, it := range items {
			raw, err := marshalWorkstateItem(it)
			if err != nil {
				return batch, err
			}
			batch.Items = append(batch.Items, raw)
		}
	}
	if optIn["executions"] {
		items, err := a.Store.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindExecution, Limit: 2000, IncludeArchived: true})
		if err != nil {
			return batch, fmt.Errorf("list executions: %w", err)
		}
		for _, it := range items {
			raw, err := marshalWorkstateItem(it)
			if err != nil {
				return batch, err
			}
			batch.Items = append(batch.Items, raw)
		}
	}
	if optIn["ledger"] {
		// Need story ids to list ledger (filter required). Use all stories
		// regardless of whether the stories area itself is opted in.
		stories, err := a.Store.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000, IncludeArchived: true})
		if err != nil {
			return batch, fmt.Errorf("list stories for ledger: %w", err)
		}
		for _, st := range stories {
			entries, lerr := a.Store.Ledger.ListByStory(ctx, st.ID, "")
			if lerr != nil {
				return batch, fmt.Errorf("list ledger for %s: %w", st.ID, lerr)
			}
			for _, e := range entries {
				raw, merr := marshalWorkstateLedger(e)
				if merr != nil {
					return batch, merr
				}
				batch.Ledger = append(batch.Ledger, raw)
			}
		}
	}
	if batch.Items == nil {
		batch.Items = []json.RawMessage{}
	}
	if batch.Ledger == nil {
		batch.Ledger = []json.RawMessage{}
	}
	return batch, nil
}

// marshalWorkstateItem encodes a work item with the promoted fields the server
// extracts (id, kind, status, title, updated_at) plus the full record.
func marshalWorkstateItem(it workitem.Item) (json.RawMessage, error) {
	// Re-marshal the item JSON — the struct already carries the promoted fields.
	// Ensure updated_at is RFC3339 so the server's parseTimePtr accepts it.
	type wire struct {
		ID        string    `json:"id"`
		Kind      string    `json:"kind"`
		Status    string    `json:"status"`
		Title     string    `json:"title"`
		Body      string    `json:"body,omitempty"`
		Priority  string    `json:"priority,omitempty"`
		Category  string    `json:"category,omitempty"`
		ParentID  string    `json:"parent_id,omitempty"`
		Tags      []string  `json:"tags,omitempty"`
		UpdatedAt time.Time `json:"updated_at"`
		CreatedAt time.Time `json:"created_at"`
		Archived  bool      `json:"archived,omitempty"`
	}
	w := wire{
		ID: it.ID, Kind: string(it.Kind), Status: it.Status, Title: it.Title,
		Body: it.Body, Priority: it.Priority, Category: it.Category, ParentID: it.ParentID,
		Tags: it.Tags, UpdatedAt: it.UpdatedAt, CreatedAt: it.CreatedAt, Archived: it.Archived,
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("encode workstate item %s: %w", it.ID, err)
	}
	return b, nil
}

// marshalWorkstateLedger encodes a ledger entry for ingest.
func marshalWorkstateLedger(e ledger.Entry) (json.RawMessage, error) {
	type wire struct {
		ID        string          `json:"id"`
		StoryID   string          `json:"story_id,omitempty"`
		Kind      string          `json:"kind"`
		Actor     string          `json:"actor,omitempty"`
		Body      string          `json:"body,omitempty"`
		Payload   json.RawMessage `json:"payload,omitempty"`
		CreatedAt time.Time       `json:"created_at"`
	}
	w := wire{
		ID: e.ID, StoryID: e.StoryID, Kind: e.Kind, Actor: e.Actor,
		Body: e.Body, Payload: e.Payload, CreatedAt: e.CreatedAt,
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("encode workstate ledger %s: %w", e.ID, err)
	}
	return b, nil
}
