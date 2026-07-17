package cli

// `satelle sync workstate` — personal work-state push (backup) and pull
// (rehydrate). epic:scoped-sync order:7 delivered push; epic:workspace-rehydrate
// order:3 adds pull. Always targets this repo's bound hosted PROJECT's personal
// collection; a team active-workspace binding never redirects work-state. The
// [sync] scope gates whether each work-state area (stories, ledger, executions)
// is transferred (local → skip; personal|shared → personal+project).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// WorkstateAreas are the [sync] areas that form the work-state kind. A shared
// scope on any of these only means "eligible to leave the machine" — destination
// remains the personal workspace.
var WorkstateAreas = []string{"stories", "executions", "ledger"}

// ErrWorkstatePullConflict is returned when local and hosted both have data for
// an opted-in area and --force was not set. Nothing is written before this error.
var ErrWorkstatePullConflict = errors.New("workstate pull conflict")

// newSyncWorkstateCmd builds the `satelle sync workstate` group (push + pull).
func newSyncWorkstateCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "workstate",
		Short: "Push/pull work state for this repo's bound hosted project personal collection (local default skips)",
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
personal partition. Requires "satelle project bind <slug>". Pull is the recover
path: "satelle sync workstate pull".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstatePush(cmd, pushServer, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List which areas would be pushed without contacting the server.")
	group.AddCommand(push)

	var pullServer string
	var pullDryRun, force bool
	pull := &cobra.Command{
		Use:         "pull",
		Short:       "Restore opted-in work-state from the personal workspace into the local store",
		Annotations: needsStore(),
		Long: `pull fetches stories, task-executions, and ledger entries for work-state areas
whose resolved [sync] scope is personal or shared from this repo's bound hosted
PROJECT's personal collection, and materializes them into the local DB (store
upsert + derived backlog view regeneration). On-disk story markdown is never the
primary restore target; story attachment files under .satelle/stories/<id>/ are
not part of the workstate mirror and are not restored.

Conflict policy (per opted-in area):
  - local empty + hosted non-empty → materialize (prefer hosted)
  - local non-empty AND hosted non-empty → fail with a named error (no silent clobber)
  - --force overrides the conflict check; hosted rows upsert over same-id local
    rows; local-only rows are left alone (not a wipe-and-replace)

Local-scoped areas are skipped. Source is always the personal workspace (team
binding ignored). Requires "satelle project bind <slug>".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstatePull(cmd, pullServer, pullDryRun, force)
		},
	}
	pull.Flags().StringVar(&pullServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	pull.Flags().BoolVar(&pullDryRun, "dry-run", false, "List which areas would be pulled without contacting the server.")
	pull.Flags().BoolVar(&force, "force", false, "Materialize even when local and hosted both have data for an area (upsert by id; no wipe).")
	group.AddCommand(pull)

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

	optIn, err := workstateOptIn(a.Config)
	if err != nil {
		return err
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
	res, err := client.PushWorkstate(cmd.Context(), project, batch)
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

func runSyncWorkstatePull(cmd *cobra.Command, serverArg string, dryRun, force bool) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	out := cmd.OutOrStdout()
	ctx := cmd.Context()

	optIn, err := workstateOptIn(a.Config)
	if err != nil {
		return err
	}
	if len(optIn) == 0 {
		fmt.Fprintln(out, "No work-state to pull — every work-state area is local. Set [sync] stories|ledger|executions = personal|shared to opt in.")
		return nil
	}
	if dryRun {
		fmt.Fprintf(out, "Would pull work-state areas from bound project personal collection on %s:\n", server)
		for _, area := range WorkstateAreas {
			if optIn[area] {
				fmt.Fprintf(out, "  personal -> %s\n", area)
			}
		}
		return nil
	}

	project, err := resolveBoundProject(a.Config)
	if err != nil {
		return err
	}

	client := hosted.NewClient(server, hosted.FileStore{}, nil)

	var items []hosted.WorkstateItem
	if optIn["stories"] || optIn["executions"] {
		items, err = client.ListWorkstateItems(ctx, project, "")
		if err != nil {
			if errors.Is(err, hosted.ErrLoginRequired) {
				return err
			}
			return fmt.Errorf("list workstate items: %w", err)
		}
	}
	var ledgerRows []hosted.WorkstateLedgerRow
	if optIn["ledger"] {
		ledgerRows, err = client.ListWorkstateLedger(ctx, project, "")
		if err != nil {
			if errors.Is(err, hosted.ErrLoginRequired) {
				return err
			}
			return fmt.Errorf("list workstate ledger: %w", err)
		}
	}

	// Partition hosted rows by area for conflict checks and materialize.
	hostedStories, hostedExecs := 0, 0
	for _, it := range items {
		switch it.Kind {
		case string(workitem.KindStory):
			hostedStories++
		case string(workitem.KindExecution):
			hostedExecs++
		}
	}
	hostedByArea := map[string]int{
		"stories":    hostedStories,
		"executions": hostedExecs,
		"ledger":     len(ledgerRows),
	}

	if !force {
		if cerr := checkWorkstatePullConflicts(ctx, a, optIn, hostedByArea); cerr != nil {
			return cerr
		}
	}

	nItems, nLedger, merr := materializeWorkstate(ctx, a, optIn, items, ledgerRows)
	if merr != nil {
		return merr
	}
	if nItems == 0 && nLedger == 0 {
		fmt.Fprintln(out, "No work-state rows to pull (hosted opted-in areas are empty).")
		return nil
	}
	fmt.Fprintf(out, "Pulled work-state from project %q personal collection on %s: %d item(s), %d ledger entr(y/ies).\n",
		project, server, nItems, nLedger)
	return nil
}

func workstateOptIn(cfg config.Config) (map[string]bool, error) {
	optIn := map[string]bool{}
	for _, area := range WorkstateAreas {
		scope, serr := config.ScopeFor(cfg, area)
		if serr != nil {
			return nil, fmt.Errorf("sync workstate area %q: %w", area, serr)
		}
		if scope != config.LocalScope {
			optIn[area] = true
		}
	}
	return optIn, nil
}

func checkWorkstatePullConflicts(ctx context.Context, a *app.App, optIn map[string]bool, hostedByArea map[string]int) error {
	var conflicts []string
	for _, area := range WorkstateAreas {
		if !optIn[area] {
			continue
		}
		localN, err := localWorkstateCount(ctx, a, area)
		if err != nil {
			return err
		}
		hostedN := hostedByArea[area]
		if localN > 0 && hostedN > 0 {
			conflicts = append(conflicts, fmt.Sprintf("%s (local=%d hosted=%d)", area, localN, hostedN))
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s — re-run with --force to upsert hosted over same-id local rows (local-only rows kept)",
		ErrWorkstatePullConflict, strings.Join(conflicts, "; "))
}

func localWorkstateCount(ctx context.Context, a *app.App, area string) (int, error) {
	switch area {
	case "stories":
		return a.Store.Stories.Count(ctx, workitem.KindStory)
	case "executions":
		return a.Store.Stories.Count(ctx, workitem.KindExecution)
	case "ledger":
		return a.Store.Ledger.Count(ctx)
	default:
		return 0, nil
	}
}

// materializeWorkstate upserts hosted rows into the local store for opted-in
// areas, then regenerates the backlog view. Returns counts of rows applied.
// On mid-batch error, reports how many landed and that re-run is safe.
func materializeWorkstate(ctx context.Context, a *app.App, optIn map[string]bool, items []hosted.WorkstateItem, ledgerRows []hosted.WorkstateLedgerRow) (nItems, nLedger int, err error) {
	now := time.Now().UTC()
	for _, hi := range items {
		area := workstateAreaForKind(hi.Kind)
		if area == "" || !optIn[area] {
			continue
		}
		it, perr := parseWorkstateItem(hi)
		if perr != nil {
			return nItems, nLedger, fmt.Errorf("materialize item %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hi.ID, nItems, nLedger, perr)
		}
		if _, uerr := a.Store.Stories.Upsert(ctx, it, now); uerr != nil {
			return nItems, nLedger, fmt.Errorf("upsert item %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hi.ID, nItems, nLedger, uerr)
		}
		nItems++
	}
	if optIn["ledger"] {
		for _, hr := range ledgerRows {
			e, perr := parseWorkstateLedger(hr)
			if perr != nil {
				return nItems, nLedger, fmt.Errorf("materialize ledger %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hr.ID, nItems, nLedger, perr)
			}
			if _, uerr := a.Store.Ledger.Upsert(ctx, e, now); uerr != nil {
				return nItems, nLedger, fmt.Errorf("upsert ledger %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hr.ID, nItems, nLedger, uerr)
			}
			nLedger++
		}
	}
	if _, _, verr := verb.SyncStoryBacklog(ctx, a.Store.Stories, now); verr != nil {
		return nItems, nLedger, fmt.Errorf("regenerate story views after %d item(s), %d ledger: %w — store rows landed; re-run is safe", nItems, nLedger, verr)
	}
	return nItems, nLedger, nil
}

func workstateAreaForKind(kind string) string {
	switch kind {
	case string(workitem.KindStory):
		return "stories"
	case string(workitem.KindExecution):
		return "executions"
	default:
		return ""
	}
}

// workstateItemWire is the CLI record shape pushed as payload and restored from
// hosted.record on pull.
type workstateItemWire struct {
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"`
	Status             string    `json:"status"`
	Title              string    `json:"title"`
	Body               string    `json:"body,omitempty"`
	Priority           string    `json:"priority,omitempty"`
	Category           string    `json:"category,omitempty"`
	ParentID           string    `json:"parent_id,omitempty"`
	AcceptanceCriteria string    `json:"acceptance_criteria,omitempty"`
	Tags               []string  `json:"tags,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
	CreatedAt          time.Time `json:"created_at"`
	Archived           bool      `json:"archived,omitempty"`
}

func parseWorkstateItem(hi hosted.WorkstateItem) (workitem.Item, error) {
	raw := hi.Record
	if len(raw) == 0 {
		// Fall back to promoted fields when record is empty.
		raw = mustJSON(map[string]any{
			"id": hi.ID, "kind": hi.Kind, "status": hi.Status, "title": hi.Title,
		})
	}
	var w workstateItemWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return workitem.Item{}, fmt.Errorf("decode record: %w", err)
	}
	if w.ID == "" {
		w.ID = hi.ID
	}
	if w.Kind == "" {
		w.Kind = hi.Kind
	}
	if w.Title == "" {
		w.Title = hi.Title
	}
	if w.Status == "" {
		w.Status = hi.Status
	}
	k := workitem.Kind(w.Kind)
	switch k {
	case workitem.KindStory, workitem.KindTask, workitem.KindExecution:
	default:
		return workitem.Item{}, fmt.Errorf("invalid kind %q", w.Kind)
	}
	if strings.TrimSpace(w.Title) == "" {
		return workitem.Item{}, fmt.Errorf("title required")
	}
	return workitem.Item{
		ID: w.ID, Kind: k, Title: w.Title, Body: w.Body, Status: w.Status,
		Priority: w.Priority, Category: w.Category, ParentID: w.ParentID,
		AcceptanceCriteria: w.AcceptanceCriteria, Tags: w.Tags,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, Archived: w.Archived,
	}, nil
}

func parseWorkstateLedger(hr hosted.WorkstateLedgerRow) (ledger.Entry, error) {
	raw := hr.Record
	if len(raw) == 0 {
		raw = mustJSON(map[string]any{
			"id": hr.ID, "story_id": hr.StoryID, "kind": hr.Kind,
		})
	}
	var w struct {
		ID        string          `json:"id"`
		StoryID   string          `json:"story_id,omitempty"`
		ProjectID string          `json:"project_id,omitempty"`
		Kind      string          `json:"kind"`
		Actor     string          `json:"actor,omitempty"`
		Body      string          `json:"body,omitempty"`
		Payload   json.RawMessage `json:"payload,omitempty"`
		Refs      json.RawMessage `json:"refs,omitempty"`
		CreatedAt time.Time       `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return ledger.Entry{}, fmt.Errorf("decode record: %w", err)
	}
	if w.ID == "" {
		w.ID = hr.ID
	}
	if w.Kind == "" {
		w.Kind = hr.Kind
	}
	if w.StoryID == "" {
		w.StoryID = hr.StoryID
	}
	if strings.TrimSpace(w.ID) == "" || strings.TrimSpace(w.Kind) == "" {
		return ledger.Entry{}, fmt.Errorf("ledger id and kind required")
	}
	return ledger.Entry{
		ID: w.ID, StoryID: w.StoryID, ProjectID: w.ProjectID, Kind: w.Kind,
		Actor: w.Actor, Body: w.Body, Payload: w.Payload, Refs: w.Refs,
		CreatedAt: w.CreatedAt,
	}, nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// collectWorkstate builds the ingest batch from the local store for the
// opted-in areas. Stories and executions become items; ledger entries are
// collected per known story id (the ledger store refuses unfiltered scans).
func collectWorkstate(ctx context.Context, a *app.App, optIn map[string]bool) (hosted.WorkstateIngest, error) {
	var batch hosted.WorkstateIngest
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
	w := workstateItemWire{
		ID: it.ID, Kind: string(it.Kind), Status: it.Status, Title: it.Title,
		Body: it.Body, Priority: it.Priority, Category: it.Category, ParentID: it.ParentID,
		AcceptanceCriteria: it.AcceptanceCriteria, Tags: it.Tags,
		UpdatedAt: it.UpdatedAt, CreatedAt: it.CreatedAt, Archived: it.Archived,
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
		ProjectID string          `json:"project_id,omitempty"`
		Kind      string          `json:"kind"`
		Actor     string          `json:"actor,omitempty"`
		Body      string          `json:"body,omitempty"`
		Payload   json.RawMessage `json:"payload,omitempty"`
		Refs      json.RawMessage `json:"refs,omitempty"`
		CreatedAt time.Time       `json:"created_at"`
	}
	w := wire{
		ID: e.ID, StoryID: e.StoryID, ProjectID: e.ProjectID, Kind: e.Kind, Actor: e.Actor,
		Body: e.Body, Payload: e.Payload, Refs: e.Refs, CreatedAt: e.CreatedAt,
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("encode workstate ledger %s: %w", e.ID, err)
	}
	return b, nil
}
