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
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// WorkstateAreas are the [sync] areas that form the work-state kind. A shared
// scope on any of these only means "eligible to leave the machine" — destination
// remains the personal workspace.
var WorkstateAreas = config.WorkstateAreas

// ErrWorkstatePullConflict is returned when local and hosted both have data for
// an opted-in area and --force was not set. Nothing is written before this error.
var ErrWorkstatePullConflict = errors.New("workstate pull conflict")

// newSyncWorkstateCmd builds the `satelle sync workstate` group (push + pull).
func newSyncWorkstateCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "workstate",
		Short: "Push/pull work state for this repo's bound hosted project personal collection (local default skips)",
		Long: `Move work state — stories, tasks, ledger — between this repo and its bound
hosted project's personal collection.

This is the continuity path for the same work across machines, not a team
channel: the personal collection is yours. Skipped entirely unless the workstate
area is opted in, and a pull merges into local rows rather than replacing them.`,
	}

	var pushServer string
	var dryRun, full bool
	push := &cobra.Command{
		Use:         "push",
		Short:       "Replicate opted-in work-state areas to the personal workspace",
		Annotations: needsStore(),
		Long: `push collects local stories, task-executions, and ledger entries for work-state
areas whose resolved [sync] scope is personal or shared, and upserts them into
this repo's bound hosted PROJECT's personal collection on the hosted server
(origin=cli-sync). Only records changed since the last successful push are sent
(cursor outside the repo; sty_88e83180). Local-scoped areas are skipped. A team
active-workspace binding does NOT redirect work-state — destination is always
the bound project's personal partition. Requires "satelle project bind <slug>".
--full ignores the stored cursor and re-sends everything. Pull is the recover
path: "satelle sync workstate pull".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstatePush(cmd, pushServer, dryRun, full)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List which areas would be pushed without contacting the server.")
	push.Flags().BoolVar(&full, "full", false, "Ignore the stored cursor and push the complete set (then advance the cursor).")
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
primary restore target; story attachment files under the home-keyed runtime
stories dir are not part of the workstate mirror and are not restored.

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
	pull.Flags().StringVar(&pullServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	pull.Flags().BoolVar(&pullDryRun, "dry-run", false, "List which areas would be pulled without contacting the server.")
	pull.Flags().BoolVar(&force, "force", false, "Materialize even when local and hosted both have data for an area (upsert by id; no wipe).")
	group.AddCommand(pull)

	var snapServer string
	var snapForce bool
	snap := &cobra.Command{
		Use:         "snapshot",
		Short:       "Pull current hosted work-state into the local store (lazy Snapshot)",
		Annotations: needsStore(),
		Long: `snapshot fetches the bound project's current work-state via the checkout-sync
Snapshot adapter (gRPC Sync/Snapshot) and materializes opted-in
areas into the local store. Local-only areas are a no-op. satelled may
exec this verb; it does not talk to the hosted server itself.

A hosted story whose updated_at is older than the local row — or older than
the local ledger's last status_transition — is skipped so a stale hosted
copy cannot rewind status. --force upserts hosted over those rows.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstateSnapshot(cmd, snapServer, snapForce)
		},
	}
	snap.Flags().StringVar(&snapServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	snap.Flags().BoolVar(&snapForce, "force", false, "Upsert hosted rows even when the local copy or ledger is newer.")
	group.AddCommand(snap)

	return group
}

func runSyncWorkstateSnapshot(cmd *cobra.Command, serverArg string, force bool) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	optIn, err := workstateOptIn(a.Config)
	if err != nil {
		return err
	}
	if len(optIn) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "workstate snapshot: all areas local, nothing to pull")
		return nil
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	project, err := resolveBoundProject(a.Config, a.RepoRoot)
	if err != nil {
		return err
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	items, ledgerRows, err := client.Snapshot(cmd.Context(), project, "")
	if err != nil {
		return err
	}
	nItems, nLedger, nKept, merr := materializeWorkstate(cmd.Context(), a, optIn, items, ledgerRows, force)
	if merr != nil {
		return merr
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Snapshot work-state from project %q: %d item(s), %d ledger.\n",
		project, nItems, nLedger)
	if nKept > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%d item(s) kept — local copy is newer than hosted\n", nKept)
	}
	return nil
}

// Chunk sizes for work-state push (package vars so tests can shrink them).
var (
	workstateItemChunk   = 500
	workstateLedgerChunk = 1000
)

func runSyncWorkstatePush(cmd *cobra.Command, serverArg string, dryRun, full bool) error {
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

	project, err := resolveBoundProject(a.Config, a.RepoRoot)
	if err != nil {
		return err
	}

	repoRoot := a.RepoRoot
	cursor, err := hosted.LoadWorkstateCursor(server, project, repoRoot)
	if err != nil {
		return fmt.Errorf("load workstate cursor: %w", err)
	}
	hadCursor := !cursor.ItemsUpdatedAt.IsZero() || !cursor.LedgerCreatedAt.IsZero()
	if full {
		cursor = hosted.WorkstateCursor{}
	}

	batch, maxItems, maxLedger, err := collectWorkstateSince(cmd.Context(), a, optIn, cursor)
	if err != nil {
		return err
	}
	// SQL uses a whole-second lower bound so boundary-second rows reappear;
	// drop anything not strictly after the cursor so a true no-op sends nothing
	// (AC2/AC7) while same-second new nanos still After() and ride (sty_88e83180).
	if !full && !cursor.ItemsUpdatedAt.IsZero() {
		batch.Items = filterItemsAfter(batch.Items, cursor.ItemsUpdatedAt)
	}
	if !full && !cursor.LedgerCreatedAt.IsZero() {
		batch.Ledger = filterLedgerAfter(batch.Ledger, cursor.LedgerCreatedAt)
	}
	if len(batch.Items) == 0 && len(batch.Ledger) == 0 {
		if hadCursor && !full {
			fmt.Fprintf(out, "Work-state up to date on %s — no records changed since the last push.\n", server)
			recordWorkstatePush(a.RepoRoot, true, "")
			return nil
		}
		fmt.Fprintln(out, "No work-state rows to push (opted-in areas are empty).")
		return nil
	}

	// Chunked push; cursor advances only after every chunk confirms (AC3).
	// Prefer a single POST when both sides fit in one chunk (preserves the
	// small-batch shape tests and production already rely on).
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	var totalItems, totalLedger int
	type partial struct {
		items  []json.RawMessage
		ledger []json.RawMessage
	}
	var parts []partial
	if len(batch.Items) <= workstateItemChunk && len(batch.Ledger) <= workstateLedgerChunk {
		parts = []partial{{items: batch.Items, ledger: batch.Ledger}}
	} else {
		for _, c := range chunkRaw(batch.Items, workstateItemChunk) {
			parts = append(parts, partial{items: c})
		}
		for _, c := range chunkRaw(batch.Ledger, workstateLedgerChunk) {
			parts = append(parts, partial{ledger: c})
		}
	}
	if len(parts) == 0 {
		parts = []partial{{}}
	}
	for _, p := range parts {
		chunk := hosted.WorkstateIngest{Items: p.items, Ledger: p.ledger}
		if chunk.Items == nil {
			chunk.Items = []json.RawMessage{}
		}
		if chunk.Ledger == nil {
			chunk.Ledger = []json.RawMessage{}
		}
		res, perr := client.Apply(cmd.Context(), project, chunk)
		if perr != nil {
			if errors.Is(perr, hosted.ErrLoginRequired) {
				recordWorkstatePush(a.RepoRoot, false, perr.Error())
				return perr
			}
			recordWorkstatePush(a.RepoRoot, false, perr.Error())
			return fmt.Errorf("apply workstate: %w", perr)
		}
		totalItems += res.Items
		totalLedger += res.Ledger
	}
	// Advance cursor only after full success.
	next := hosted.WorkstateCursor{
		ItemsUpdatedAt:  maxItems,
		LedgerCreatedAt: maxLedger,
	}
	if err := hosted.SaveWorkstateCursor(server, project, repoRoot, next); err != nil {
		return fmt.Errorf("save workstate cursor: %w", err)
	}
	fmt.Fprintf(out, "Pushed work-state to project %q personal collection on %s: %d item(s), %d ledger entr(y/ies).\n",
		project, server, totalItems, totalLedger)
	recordWorkstatePush(a.RepoRoot, true, "")
	return nil
}

func recordWorkstatePush(repoPath string, success bool, reason string) {
	_ = syncstate.RecordPush(config.GlobalDir(), repoPath, success, reason, "", time.Now())
}

func chunkRaw(in []json.RawMessage, size int) [][]json.RawMessage {
	if size <= 0 {
		size = 1
	}
	if len(in) == 0 {
		return nil
	}
	var out [][]json.RawMessage
	for i := 0; i < len(in); i += size {
		end := i + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
}

// filterItemsAfter keeps only marshaled workstate items whose updated_at is
// strictly after since. Records at the cursor high-water mark (re-selected by
// the second-truncated SQL bound) drop out so a no-op push issues no request.
func filterItemsAfter(items []json.RawMessage, since time.Time) []json.RawMessage {
	if since.IsZero() || len(items) == 0 {
		return items
	}
	var out []json.RawMessage
	for _, raw := range items {
		var w struct {
			UpdatedAt time.Time `json:"updated_at"`
		}
		if err := json.Unmarshal(raw, &w); err != nil || !w.UpdatedAt.After(since) {
			continue
		}
		out = append(out, raw)
	}
	if out == nil {
		return []json.RawMessage{}
	}
	return out
}

// filterLedgerAfter keeps only ledger rows strictly after since (see filterItemsAfter).
func filterLedgerAfter(entries []json.RawMessage, since time.Time) []json.RawMessage {
	if since.IsZero() || len(entries) == 0 {
		return entries
	}
	var out []json.RawMessage
	for _, raw := range entries {
		var w struct {
			CreatedAt time.Time `json:"created_at"`
		}
		if err := json.Unmarshal(raw, &w); err != nil || !w.CreatedAt.After(since) {
			continue
		}
		out = append(out, raw)
	}
	if out == nil {
		return []json.RawMessage{}
	}
	return out
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

	project, err := resolveBoundProject(a.Config, a.RepoRoot)
	if err != nil {
		return err
	}

	client := hosted.NewClient(server, hosted.FileStore{}, nil)

	items, ledgerRows, err := client.Snapshot(ctx, project, "")
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("snapshot workstate: %w", err)
	}
	if !(optIn["stories"] || optIn["executions"]) {
		items = nil
	}
	if !optIn["ledger"] {
		ledgerRows = nil
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

	nItems, nLedger, nKept, merr := materializeWorkstate(ctx, a, optIn, items, ledgerRows, force)
	if merr != nil {
		return merr
	}
	if nItems == 0 && nLedger == 0 && nKept == 0 {
		fmt.Fprintln(out, "No work-state rows to pull (hosted opted-in areas are empty).")
		return nil
	}
	fmt.Fprintf(out, "Pulled work-state from project %q personal collection on %s: %d item(s), %d ledger entr(y/ies).\n",
		project, server, nItems, nLedger)
	if nKept > 0 {
		fmt.Fprintf(out, "%d item(s) kept — local copy is newer than hosted\n", nKept)
	}
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
// areas, then regenerates the backlog view. Returns counts of rows applied
// and of local rows kept because they (or their ledger) are newer than hosted.
// On mid-batch error, reports how many landed and that re-run is safe.
func materializeWorkstate(ctx context.Context, a *app.App, optIn map[string]bool, items []hosted.WorkstateItem, ledgerRows []hosted.WorkstateLedgerRow, force bool) (nItems, nLedger, nKept int, err error) {
	now := time.Now().UTC()
	for _, hi := range items {
		area := workstateAreaForKind(hi.Kind)
		if area == "" || !optIn[area] {
			continue
		}
		it, perr := parseWorkstateItem(hi)
		if perr != nil {
			return nItems, nLedger, nKept, fmt.Errorf("materialize item %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hi.ID, nItems, nLedger, perr)
		}
		if !force {
			keep, kerr := keepLocalWorkstateItem(ctx, a, it)
			if kerr != nil {
				return nItems, nLedger, nKept, fmt.Errorf("compare item %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hi.ID, nItems, nLedger, kerr)
			}
			if keep {
				nKept++
				continue
			}
		}
		// --force means the hosted copy wins outright, including a status the
		// local row has already moved past — so it takes the store's explicit
		// opt-out from the stale-status guard (sty_2c71eff6). The default path
		// keeps the guard, on top of the newer-local check above.
		upsert := a.Store.Stories.Upsert
		if force {
			upsert = a.Store.Stories.UpsertForce
		}
		if _, uerr := upsert(ctx, it, now); uerr != nil {
			return nItems, nLedger, nKept, fmt.Errorf("upsert item %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hi.ID, nItems, nLedger, uerr)
		}
		nItems++
	}
	if optIn["ledger"] {
		for _, hr := range ledgerRows {
			e, perr := parseWorkstateLedger(hr)
			if perr != nil {
				return nItems, nLedger, nKept, fmt.Errorf("materialize ledger %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hr.ID, nItems, nLedger, perr)
			}
			if _, uerr := a.Store.Ledger.Upsert(ctx, e, now); uerr != nil {
				return nItems, nLedger, nKept, fmt.Errorf("upsert ledger %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hr.ID, nItems, nLedger, uerr)
			}
			nLedger++
		}
	}
	if _, _, verr := verb.SyncStoryBacklog(ctx, a.Store.Stories, now); verr != nil {
		return nItems, nLedger, nKept, fmt.Errorf("regenerate story views after %d item(s), %d ledger: %w — store rows landed; re-run is safe", nItems, nLedger, verr)
	}
	return nItems, nLedger, nKept, nil
}

// keepLocalWorkstateItem reports whether the local row (or its ledger) is
// newer than the incoming hosted item, so upserting would rewind status.
func keepLocalWorkstateItem(ctx context.Context, a *app.App, incoming workitem.Item) (bool, error) {
	local, err := a.Store.Stories.Get(ctx, incoming.ID)
	if err != nil {
		if errors.Is(err, workitem.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if local.UpdatedAt.After(incoming.UpdatedAt) {
		return true, nil
	}
	e, ok, lerr := verb.LatestStatusTransition(ctx, a.Store.Ledger, incoming.ID)
	if lerr != nil {
		return false, lerr
	}
	if !ok {
		return false, nil
	}
	to := verb.TransitionTo(e)
	if to != "" && to != incoming.Status && e.CreatedAt.After(incoming.UpdatedAt) {
		return true, nil
	}
	return false, nil
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
	ParkOrigin         string    `json:"park_origin,omitempty"`
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
		ParkOrigin: w.ParkOrigin,
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

// collectWorkstate builds the full ingest batch (no cursor). Kept for pull
// conflict checks and tests that want an unfiltered snapshot.
func collectWorkstate(ctx context.Context, a *app.App, optIn map[string]bool) (hosted.WorkstateIngest, error) {
	batch, _, _, err := collectWorkstateSince(ctx, a, optIn, hosted.WorkstateCursor{})
	return batch, err
}

// collectWorkstateSince builds the ingest batch for records at or after the
// cursor high-water marks, paging to exhaustion (sty_88e83180). Returns the
// batch plus the max timestamps observed (seeded from the cursor so empty areas
// never rewind it).
func collectWorkstateSince(ctx context.Context, a *app.App, optIn map[string]bool, cursor hosted.WorkstateCursor) (hosted.WorkstateIngest, time.Time, time.Time, error) {
	var batch hosted.WorkstateIngest
	maxItems := cursor.ItemsUpdatedAt
	maxLedger := cursor.LedgerCreatedAt

	pageItems := func(kind workitem.Kind) error {
		offset := 0
		for {
			items, err := a.Store.Stories.ListChangedSince(ctx, kind, cursor.ItemsUpdatedAt, workstateItemChunk, offset)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				break
			}
			for _, it := range items {
				raw, merr := marshalWorkstateItem(it)
				if merr != nil {
					return merr
				}
				batch.Items = append(batch.Items, raw)
				if it.UpdatedAt.After(maxItems) {
					maxItems = it.UpdatedAt
				}
			}
			if len(items) < workstateItemChunk {
				break
			}
			offset += len(items)
		}
		return nil
	}

	if optIn["stories"] {
		if err := pageItems(workitem.KindStory); err != nil {
			return batch, maxItems, maxLedger, fmt.Errorf("list stories: %w", err)
		}
	}
	if optIn["executions"] {
		if err := pageItems(workitem.KindExecution); err != nil {
			return batch, maxItems, maxLedger, fmt.Errorf("list executions: %w", err)
		}
	}
	if optIn["ledger"] {
		offset := 0
		for {
			entries, err := a.Store.Ledger.ListChangedSince(ctx, cursor.LedgerCreatedAt, workstateLedgerChunk, offset)
			if err != nil {
				return batch, maxItems, maxLedger, fmt.Errorf("list ledger: %w", err)
			}
			if len(entries) == 0 {
				break
			}
			for _, e := range entries {
				raw, merr := marshalWorkstateLedger(e)
				if merr != nil {
					return batch, maxItems, maxLedger, merr
				}
				batch.Ledger = append(batch.Ledger, raw)
				if e.CreatedAt.After(maxLedger) {
					maxLedger = e.CreatedAt
				}
			}
			if len(entries) < workstateLedgerChunk {
				break
			}
			offset += len(entries)
		}
	}
	if batch.Items == nil {
		batch.Items = []json.RawMessage{}
	}
	if batch.Ledger == nil {
		batch.Ledger = []json.RawMessage{}
	}
	return batch, maxItems, maxLedger, nil
}

// marshalWorkstateItem encodes a work item with the promoted fields the server
// extracts (id, kind, status, title, updated_at) plus the full record.
func marshalWorkstateItem(it workitem.Item) (json.RawMessage, error) {
	w := workstateItemWire{
		ID: it.ID, Kind: string(it.Kind), Status: it.Status, Title: it.Title,
		Body: it.Body, Priority: it.Priority, Category: it.Category, ParentID: it.ParentID,
		AcceptanceCriteria: it.AcceptanceCriteria, Tags: it.Tags,
		UpdatedAt: it.UpdatedAt, CreatedAt: it.CreatedAt, Archived: it.Archived,
		ParkOrigin: it.ParkOrigin,
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
