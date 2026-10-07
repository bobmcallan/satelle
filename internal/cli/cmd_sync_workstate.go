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
	"io"
	"maps"
	"reflect"
	"slices"
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
		Long: `push upserts local stories, task-executions, and ledger entries, for areas whose
[sync] scope is personal or shared, into the bound hosted project's personal
collection (origin=cli-sync). Only records changed since the last successful
push are sent; --full re-sends everything. A team workspace binding does NOT
redirect work-state. Requires "satelle project bind <slug>". Pull is the recover
path: "satelle sync workstate pull".

A story in progress is held, with a "held <id>" line, until its engagement tree
is clean and its HEAD is on a remote-tracking ref; the next push sends it.
[sync] hold_unpushed = false turns this off.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstatePush(cmd, pushServer, dryRun, full)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List which areas would be pushed, and which stories would be held, without contacting the server.")
	push.Flags().BoolVar(&full, "full", false, "Ignore the stored cursor and push the complete set (then advance the cursor).")
	group.AddCommand(push)

	var pullServer string
	var pullDryRun, force, pullVerbose bool
	pull := &cobra.Command{
		Use:         "pull",
		Short:       "Restore opted-in work-state from the personal workspace into the local store",
		Annotations: needsStore(),
		Long: `pull fetches the bound hosted PROJECT's personal stories, executions and
ledger entries for areas whose [sync] scope is personal or shared, and merges
them into the local DB by id. Story attachment files are not mirrored.

Local-only rows are kept. The pull refuses, naming each id, only where it would
lose local information: a story changed locally since hosted, or a ledger row
whose payload differs. --force lets hosted win (local-only rows stay; an
existing ledger row is never rewritten).

Afterwards it names each pulled story whose status claims code the git remote
lacks, as of the last fetch; --verbose lists stories that left work with no
recorded head. It changes nothing.

Local-scoped areas are skipped. Requires "satelle project bind <slug>".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstatePull(cmd, pullServer, pullDryRun, force, pullVerbose)
		},
	}
	pull.Flags().StringVar(&pullServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	pull.Flags().BoolVar(&pullDryRun, "dry-run", false, "List which areas would be pulled without contacting the server.")
	pull.Flags().BoolVar(&force, "force", false, "Materialize even where a same-id local row differs from hosted (hosted wins; local-only rows kept).")
	pull.Flags().BoolVar(&pullVerbose, "verbose", false, "List every story that left work with no recorded head, not just a count.")
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
	client := newHostedClient(cmd.Context(), server, a.RepoRoot)
	items, ledgerRows, err := client.Snapshot(cmd.Context(), project, "")
	if err != nil {
		return err
	}
	nItems, nLedger, nKept, merr := materializeWorkstate(cmd.Context(), a, optIn, items, ledgerRows, force)
	if merr != nil {
		return merr
	}
	if err := pushMirrorAfterWorkstate(cmd.Context(), a, nItems+nLedger); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "workstate: mirror push warning: %v\n", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Snapshot work-state from project %q: %d item(s), %d ledger.\n",
		project, nItems, nLedger)
	if nKept > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%d item(s) kept — local copy is newer than hosted\n", nKept)
	}
	return nil
}

// pushMirrorAfterWorkstate posts a light drain snapshot to the local serve
// mirror after workstate materialise so newly pulled stories appear on the
// project page without a fake story mutation (sty_e4e1a008). No-op when nothing
// was upserted or no serve endpoint resolves. Failures are returned for the
// caller to warn on; they must not fail the sync.
func pushMirrorAfterWorkstate(ctx context.Context, a *app.App, upserted int) error {
	if upserted <= 0 || a == nil {
		return nil
	}
	var ep string
	if gc, err := config.LoadGlobal(); err == nil {
		ep = gc.Service.ResolveEndpoint()
	}
	if ep == "" {
		return nil
	}
	pushCtx, cancel := context.WithTimeout(ctx, uiDrainBudget)
	defer cancel()
	snap, err := buildUIDrainSnapshot(pushCtx, a)
	if err != nil {
		return err
	}
	if snap == nil {
		return nil
	}
	return postUISnapshotContext(pushCtx, ep, snap)
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
		return dryRunHolds(cmd, a, optIn, out, server)
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
	hadCursor := !cursor.ItemsUpdatedAt.IsZero() || !cursor.LedgerCreatedAt.IsZero() || cursor.LedgerSeq > 0
	if full {
		cursor = hosted.WorkstateCursor{}
	}
	if optIn["ledger"] {
		var reset bool
		cursor, reset, err = validateLedgerCursor(cmd.Context(), a, cursor)
		if err != nil {
			return err
		}
		if reset {
			fmt.Fprintln(cmd.ErrOrStderr(), "ledger cursor reset: store changed; sending full ledger")
		}
	}

	batch, maxItems, lmark, ledgerRows, err := collectWorkstateRows(cmd.Context(), a, optIn, cursor)
	if err != nil {
		return err
	}
	startMark := ledgerMark{
		createdAt: cursor.LedgerCreatedAt,
		seq:       cursor.LedgerSeq,
		anchorID:  cursor.LedgerAnchorID,
		storeID:   lmark.storeID,
	}
	// SQL uses a whole-second lower bound so boundary-second rows reappear;
	// drop anything not strictly after the cursor so a true no-op sends nothing
	// (AC2/AC7) while same-second new nanos still After() and ride (sty_88e83180).
	// The ledger needs no such filter: it is paged strictly after an insertion
	// position, not a timestamp (sty_4a31e1ed).
	if !full && !cursor.ItemsUpdatedAt.IsZero() {
		batch.Items = filterItemsAfter(batch.Items, cursor.ItemsUpdatedAt)
	}

	if len(batch.Items) == 0 && len(batch.Ledger) == 0 {
		settlePendingClaims(cmd.Context(), out, cmd.ErrOrStderr(), a, server, project)
		if hadCursor && !full {
			fmt.Fprintf(out, "Work-state up to date on %s — no records changed since the last push.\n", server)
			recordWorkstatePush(a.RepoRoot, true, "")
			return nil
		}
		fmt.Fprintln(out, "No work-state rows to push (opted-in areas are empty).")
		return nil
	}

	client := newHostedClient(cmd.Context(), server, a.RepoRoot)
	site := holdSite{client: client, server: server, project: project, repoRoot: repoRoot}
	errw := cmd.ErrOrStderr()

	// Hold a story whose code is not on the git remote (sty_7361569a). Held rows
	// stay behind the cursors, so the next push re-checks and sends them.
	holdOn, err := config.HoldUnpushed(a.Config)
	if err != nil {
		return err
	}
	var held map[string]string
	var earliestHeld time.Time
	if holdOn {
		res, herr := classifyHolds(cmd.Context(), a, batch.Items, ledgerRows)
		if herr != nil {
			return herr
		}
		for _, l := range res.lines() {
			fmt.Fprintln(out, l)
		}
		if len(res.held) > 0 {
			held = res.held
			// The story's rows stay local, but its hosted marker must not go stale.
			refreshHeldBack(cmd.Context(), out, errw, site, held)
			batch.Items, batch.Ledger, earliestHeld = withoutHeld(held, batch.Items, batch.Ledger, ledgerRows)
			lmark = ledgerMarkBeforeHold(startMark, held, ledgerRows)
			if !earliestHeld.IsZero() {
				maxItems = itemsCursorBefore(batch.Items, earliestHeld, cursor.ItemsUpdatedAt)
			}
			if len(batch.Items) == 0 && len(batch.Ledger) == 0 {
				fmt.Fprintln(out, "No work-state rows to push after holding unpushed stories.")
				reconcilePendingClaims(cmd.Context(), out, errw, site)
				recordWorkstatePush(a.RepoRoot, true, "")
				return nil
			}
		}
	}

	// Chunked push; cursor advances only after every chunk confirms (AC3).
	// Prefer a single POST when both sides fit in one chunk (preserves the
	// small-batch shape tests and production already rely on).
	skipped, keep, skipErr := partitionByHold(cmd.Context(), client, server, project, repoRoot, batch.Items)
	if skipErr != nil {
		return skipErr
	}
	for _, s := range skipped {
		fmt.Fprintf(out, "skip %s (held elsewhere): %s\n", s.ItemID, s.Error())
	}
	batch.Items = keep
	if len(skipped) > 0 {
		// Do not advance the items cursor past a skipped foreign-held row
		// or it is silently dropped until --full (sty_f6cff549).
		maxItems = cursor.ItemsUpdatedAt
	} else if len(keep) > 0 && earliestHeld.IsZero() {
		maxItems = maxItemUpdatedAt(keep)
	}
	var totalItems, totalLedger int
	var publishedItems, publishedLedger []json.RawMessage
	apply := func(items, ledgerRaw []json.RawMessage) error {
		if items == nil {
			items = []json.RawMessage{}
		}
		if ledgerRaw == nil {
			ledgerRaw = []json.RawMessage{}
		}
		res, perr := client.Apply(cmd.Context(), project, hosted.WorkstateIngest{Items: items, Ledger: ledgerRaw})
		if perr != nil {
			recordWorkstatePush(a.RepoRoot, false, perr.Error())
			if errors.Is(perr, hosted.ErrLoginRequired) || errors.Is(perr, hosted.ErrHeldElsewhere) {
				return perr
			}
			return fmt.Errorf("apply workstate: %w", perr)
		}
		totalItems += res.Items
		totalLedger += res.Ledger
		publishedItems = append(publishedItems, items...)
		publishedLedger = append(publishedLedger, ledgerRaw...)
		return nil
	}

	// A story engaged here before the server could be asked has no hosted hold
	// yet, and a story the server has never seen cannot be checked out until its
	// item is there. So those items go first, alone; then every pending claim is
	// placed, and a story another location got to loses its remaining rows
	// (sty_52eb8c2f). They stay behind the cursors, so a takeover later sends them.
	if pending, perr := hosted.PendingClaims(server, project, repoRoot); perr == nil && len(pending) > 0 {
		var first, rest []json.RawMessage
		for _, raw := range batch.Items {
			if _, ok := pending[rawItemID(raw)]; ok {
				first = append(first, raw)
			} else {
				rest = append(rest, raw)
			}
		}
		if len(first) > 0 {
			if err := apply(first, nil); err != nil {
				return err
			}
			batch.Items = rest
		}
	}
	if collided := reconcilePendingClaims(cmd.Context(), out, errw, site); len(collided) > 0 {
		batch.Ledger = withoutStories(batch.Ledger, collided)
		stopped := maps.Clone(held)
		if stopped == nil {
			stopped = map[string]string{}
		}
		for id := range collided {
			stopped[id] = "held elsewhere"
		}
		lmark = ledgerMarkBeforeHold(startMark, stopped, ledgerRows)
		maxItems = cursor.ItemsUpdatedAt
	}
	if len(batch.Items) == 0 && len(batch.Ledger) == 0 && len(publishedItems) == 0 {
		fmt.Fprintln(out, "No work-state rows to push after hold partition.")
		return nil
	}
	type partial struct {
		items  []json.RawMessage
		ledger []json.RawMessage
	}
	var parts []partial
	switch {
	case len(batch.Items) == 0 && len(batch.Ledger) == 0:
		// Everything went ahead of the claims.
	case len(batch.Items) <= workstateItemChunk && len(batch.Ledger) <= workstateLedgerChunk:
		parts = []partial{{items: batch.Items, ledger: batch.Ledger}}
	default:
		for _, c := range chunkRaw(batch.Items, workstateItemChunk) {
			parts = append(parts, partial{items: c})
		}
		for _, c := range chunkRaw(batch.Ledger, workstateLedgerChunk) {
			parts = append(parts, partial{ledger: c})
		}
	}
	for _, p := range parts {
		if err := apply(p.items, p.ledger); err != nil {
			return err
		}
	}
	// Advance cursor only after full success.
	next := hosted.WorkstateCursor{
		ItemsUpdatedAt:  maxItems,
		LedgerCreatedAt: lmark.createdAt,
		LedgerSeq:       lmark.seq,
		LedgerAnchorID:  lmark.anchorID,
		LedgerStoreID:   lmark.storeID,
	}
	if err := hosted.SaveWorkstateCursor(server, project, repoRoot, next); err != nil {
		return fmt.Errorf("save workstate cursor: %w", err)
	}
	fmt.Fprintf(out, "Pushed work-state to project %q personal collection on %s: %d item(s), %d ledger entr(y/ies).\n",
		project, server, totalItems, totalLedger)
	if len(held) > 0 {
		fmt.Fprintf(out, "%d stor(y/ies) held until their code is on the remote.\n", len(held))
	}
	// A published story at rest no longer needs its in-flight marker.
	if released := releaseAtRest(cmd.Context(), errw, a, site, publishedStoryIDs(publishedItems, publishedLedger)); len(released) > 0 {
		fmt.Fprintf(out, "released the hosted hold on %s.\n", strings.Join(released, ", "))
	}
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

func runSyncWorkstatePull(cmd *cobra.Command, serverArg string, dryRun, force, verbose bool) error {
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

	client := newHostedClient(ctx, server, a.RepoRoot)

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

	if !force {
		if cerr := checkWorkstatePullConflicts(ctx, a, optIn, items, ledgerRows); cerr != nil {
			return cerr
		}
	}

	nItems, nLedger, nKept, merr := materializeWorkstate(ctx, a, optIn, items, ledgerRows, force)
	if merr != nil {
		return merr
	}
	// Stories in flight elsewhere are worth knowing even when nothing was pulled.
	defer reportInFlight(ctx, out, cmd.ErrOrStderr(),
		holdSite{client: client, server: server, project: project, repoRoot: a.RepoRoot}, items)
	if nItems == 0 && nLedger == 0 && nKept == 0 {
		fmt.Fprintln(out, "No work-state rows to pull (hosted opted-in areas are empty).")
		return nil
	}
	if err := pushMirrorAfterWorkstate(ctx, a, nItems+nLedger); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "workstate: mirror push warning: %v\n", err)
	}
	fmt.Fprintf(out, "Pulled work-state from project %q personal collection on %s: %d item(s), %d ledger entr(y/ies).\n",
		project, server, nItems, nLedger)
	if nKept > 0 {
		fmt.Fprintf(out, "%d item(s) kept — local copy is newer than hosted\n", nKept)
	}
	if optIn["stories"] {
		var ids []string
		for _, hi := range items {
			if hi.Kind == string(workitem.KindStory) {
				ids = append(ids, hi.ID)
			}
		}
		for _, line := range reconcilePulledStories(ctx, a, ids, verbose) {
			fmt.Fprintln(out, line)
		}
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

// maxConflictIDs caps how many conflicting ids per area the refusal names.
const maxConflictIDs = 10

// checkWorkstatePullConflicts refuses a pull only where it would lose local
// information. Merge is by id, so a local row the hosted copy has never seen, or
// a same-id row that is identical to or older than hosted, is safe. What is not:
// a same-id story the local side has changed since hosted (newer and differing),
// and a same-id ledger row whose kind or payload differs (a ledger row is never
// rewritten, so the hosted version would be dropped).
func checkWorkstatePullConflicts(ctx context.Context, a *app.App, optIn map[string]bool, items []hosted.WorkstateItem, ledgerRows []hosted.WorkstateLedgerRow) error {
	found := map[string][]string{}
	for _, hi := range items {
		area := workstateAreaForKind(hi.Kind)
		if area == "" || !optIn[area] {
			continue
		}
		incoming, err := parseWorkstateItem(hi)
		if err != nil {
			continue // materialize names the undecodable row
		}
		local, err := a.Store.Stories.Get(ctx, incoming.ID)
		if err != nil {
			if errors.Is(err, workitem.ErrNotFound) {
				continue
			}
			return err
		}
		if local.UpdatedAt.After(incoming.UpdatedAt) && workstateItemsDiffer(local, incoming) {
			found[area] = append(found[area], incoming.ID)
		}
	}
	if optIn["ledger"] {
		for _, hr := range ledgerRows {
			incoming, err := parseWorkstateLedger(hr)
			if err != nil {
				continue
			}
			local, ok, err := a.Store.Ledger.GetByID(ctx, incoming.ID)
			if err != nil {
				return err
			}
			if ok && workstateLedgerDiffers(local, incoming) {
				found["ledger"] = append(found["ledger"], incoming.ID)
			}
		}
	}
	var conflicts []string
	for _, area := range WorkstateAreas {
		ids := found[area]
		if len(ids) == 0 {
			continue
		}
		slices.Sort(ids)
		extra := ""
		if len(ids) > maxConflictIDs {
			extra = fmt.Sprintf(" (+%d more)", len(ids)-maxConflictIDs)
			ids = ids[:maxConflictIDs]
		}
		conflicts = append(conflicts, fmt.Sprintf("%s %s%s", area, strings.Join(ids, ", "), extra))
	}
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s — re-run with --force to let hosted win (local-only rows kept; an existing ledger row is never rewritten)",
		ErrWorkstatePullConflict, strings.Join(conflicts, "; "))
}

// workstateItemsDiffer compares the fields a work-state record carries, leaving
// the timestamps out: they say which side is newer, not whether the content differs.
func workstateItemsDiffer(a, b workitem.Item) bool {
	return a.Kind != b.Kind || a.Title != b.Title || a.Body != b.Body || a.Status != b.Status ||
		a.Priority != b.Priority || a.Category != b.Category || a.ParentID != b.ParentID ||
		a.AcceptanceCriteria != b.AcceptanceCriteria || a.Archived != b.Archived ||
		a.ParkOrigin != b.ParkOrigin || !slices.Equal(a.Tags, b.Tags)
}

// workstateLedgerDiffers reports whether two same-id ledger rows disagree on
// kind or payload. Payloads are compared as JSON values, so key order and
// whitespace do not count, and an empty payload equals {}.
func workstateLedgerDiffers(a, b ledger.Entry) bool {
	if a.Kind != b.Kind {
		return true
	}
	return !jsonPayloadEqual(a.Payload, b.Payload)
}

func jsonPayloadEqual(a, b json.RawMessage) bool {
	var av, bv any
	if len(a) == 0 {
		a = json.RawMessage("{}")
	}
	if len(b) == 0 {
		b = json.RawMessage("{}")
	}
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(av, bv)
}

// materializeWorkstate upserts hosted rows into the local store for opted-in
// areas, then regenerates the backlog view. Returns counts of rows applied
// and of local rows kept because they (or their ledger) are newer than hosted.
// On mid-batch error, reports how many landed and that re-run is safe.
func materializeWorkstate(ctx context.Context, a *app.App, optIn map[string]bool, items []hosted.WorkstateItem, ledgerRows []hosted.WorkstateLedgerRow, force bool) (nItems, nLedger, nKept int, err error) {
	now := time.Now().UTC()
	var applied []workitem.Item
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
		// local row has already moved past — UpsertForce. The default path uses
		// Upsert, which preserves stored status (sty_38915987); status is
		// applied below after the hosted ledger lands.
		upsert := a.Store.Stories.Upsert
		if force {
			upsert = a.Store.Stories.UpsertForce
		}
		if _, uerr := upsert(ctx, it, now); uerr != nil {
			return nItems, nLedger, nKept, fmt.Errorf("upsert item %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", hi.ID, nItems, nLedger, uerr)
		}
		applied = append(applied, it)
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
	// Default Upsert no longer writes status. Forward it when the (now-hosted)
	// ledger's latest TO matches the incoming row, or on first import with no
	// local status_transition yet (sty_38915987). Force already wrote status.
	if !force {
		for _, incoming := range applied {
			if serr := applyHostedWorkstateStatus(ctx, a, incoming, now); serr != nil {
				return nItems, nLedger, nKept, fmt.Errorf("apply status for %s after %d item(s), %d ledger: %w — re-run is safe (upsert-by-id)", incoming.ID, nItems, nLedger, serr)
			}
		}
	}
	if _, _, verr := verb.SyncStoryBacklog(ctx, a.Store.Stories, now); verr != nil {
		return nItems, nLedger, nKept, fmt.Errorf("regenerate story views after %d item(s), %d ledger: %w — store rows landed; re-run is safe", nItems, nLedger, verr)
	}
	return nItems, nLedger, nKept, nil
}

// applyHostedWorkstateStatus moves stored status to match a hosted row when the
// ledger authorises it (latest status_transition TO == incoming), or when there
// is no local transition history yet (first import / newer-stamp apply with no
// ledger). A hosted FROM that disagrees with a ledgered TO is left alone —
// Upsert already kept stored status.
func applyHostedWorkstateStatus(ctx context.Context, a *app.App, incoming workitem.Item, now time.Time) error {
	local, err := a.Store.Stories.Get(ctx, incoming.ID)
	if err != nil {
		return err
	}
	if local.Status == incoming.Status {
		return nil
	}
	e, ok, lerr := verb.LatestStatusTransition(ctx, a.Store.Ledger, incoming.ID)
	if lerr != nil {
		return lerr
	}
	if ok {
		if verb.TransitionTo(e) != incoming.Status {
			return nil
		}
	}
	_, err = a.Store.Stories.SetStatus(ctx, incoming.ID, incoming.Status, now)
	return err
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

// ledgerMark is where a collection left the ledger: the insertion position and
// id of the last row sent, the database it was read from, and the newest
// created_at seen.
type ledgerMark struct {
	createdAt time.Time
	seq       int64
	anchorID  string
	storeID   string
}

// validateLedgerCursor drops a ledger position that no longer belongs to the
// local store, so the next collection sends the whole ledger once (idempotent:
// the server ingests by id). A position belongs when the store's instance id
// matches the one saved AND the row at that position is still the anchor row;
// the id alone survives a VACUUM that renumbers rowids, the anchor alone could
// coincide in a rehydrated database. A cursor with no position (never pushed,
// or saved by a created_at-only binary) already starts from the top. Reports
// whether a saved position was dropped.
func validateLedgerCursor(ctx context.Context, a *app.App, cursor hosted.WorkstateCursor) (hosted.WorkstateCursor, bool, error) {
	if cursor.LedgerSeq <= 0 {
		return cursor, false, nil
	}
	storeID, err := a.Store.Ledger.InstanceID(ctx)
	if err != nil {
		return cursor, false, err
	}
	valid := cursor.LedgerStoreID == storeID
	if valid {
		id, ok, ierr := a.Store.Ledger.IDAtSeq(ctx, cursor.LedgerSeq)
		if ierr != nil {
			return cursor, false, ierr
		}
		valid = ok && id == cursor.LedgerAnchorID
	}
	if valid {
		return cursor, false, nil
	}
	cursor.LedgerCreatedAt = time.Time{}
	cursor.LedgerSeq = 0
	cursor.LedgerAnchorID = ""
	cursor.LedgerStoreID = ""
	return cursor, true, nil
}

// collectWorkstateSince builds the ingest batch for records past the cursor,
// paging to exhaustion (sty_88e83180). Work items page on updated_at; the ledger
// pages on insertion order, because a row can commit with a created_at behind a
// row already pushed (sty_4a31e1ed). Returns the batch plus the marks observed
// (seeded from the cursor so empty areas never rewind it).
func collectWorkstateSince(ctx context.Context, a *app.App, optIn map[string]bool, cursor hosted.WorkstateCursor) (hosted.WorkstateIngest, time.Time, ledgerMark, error) {
	batch, maxItems, mark, _, err := collectWorkstateRows(ctx, a, optIn, cursor)
	return batch, maxItems, mark, err
}

// collectWorkstateRows is collectWorkstateSince plus, for each collected ledger
// row in batch order, who owns it and where it sits — what the unpushed-code
// hold needs to cut the ledger cursor ahead of a held row (sty_7361569a).
func collectWorkstateRows(ctx context.Context, a *app.App, optIn map[string]bool, cursor hosted.WorkstateCursor) (hosted.WorkstateIngest, time.Time, ledgerMark, []ledgerRowMeta, error) {
	var rows []ledgerRowMeta
	var batch hosted.WorkstateIngest
	maxItems := cursor.ItemsUpdatedAt
	maxLedger := ledgerMark{
		createdAt: cursor.LedgerCreatedAt,
		seq:       cursor.LedgerSeq,
		anchorID:  cursor.LedgerAnchorID,
		storeID:   cursor.LedgerStoreID,
	}

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
			return batch, maxItems, maxLedger, rows, fmt.Errorf("list stories: %w", err)
		}
	}
	if optIn["executions"] {
		if err := pageItems(workitem.KindExecution); err != nil {
			return batch, maxItems, maxLedger, rows, fmt.Errorf("list executions: %w", err)
		}
	}
	if optIn["ledger"] {
		storeID, err := a.Store.Ledger.InstanceID(ctx)
		if err != nil {
			return batch, maxItems, maxLedger, rows, fmt.Errorf("list ledger: %w", err)
		}
		maxLedger.storeID = storeID
		for {
			entries, err := a.Store.Ledger.ListInsertedAfter(ctx, maxLedger.seq, workstateLedgerChunk)
			if err != nil {
				return batch, maxItems, maxLedger, rows, fmt.Errorf("list ledger: %w", err)
			}
			if len(entries) == 0 {
				break
			}
			for _, e := range entries {
				raw, merr := marshalWorkstateLedger(e.Entry)
				if merr != nil {
					return batch, maxItems, maxLedger, rows, merr
				}
				batch.Ledger = append(batch.Ledger, raw)
				rows = append(rows, ledgerRowMeta{storyID: e.StoryID, id: e.ID, seq: e.Seq, createdAt: e.CreatedAt})
				if e.CreatedAt.After(maxLedger.createdAt) {
					maxLedger.createdAt = e.CreatedAt
				}
				maxLedger.seq = e.Seq
				maxLedger.anchorID = e.ID
			}
			if len(entries) < workstateLedgerChunk {
				break
			}
		}
	}
	if batch.Items == nil {
		batch.Items = []json.RawMessage{}
	}
	if batch.Ledger == nil {
		batch.Ledger = []json.RawMessage{}
	}
	return batch, maxItems, maxLedger, rows, nil
}

// dryRunHolds lists the stories a real push would hold, from the same live git
// check. The cursor is read when the repo is bound so the list is what a push
// would actually consider; otherwise every story is checked.
func dryRunHolds(cmd *cobra.Command, a *app.App, optIn map[string]bool, out io.Writer, server string) error {
	on, err := config.HoldUnpushed(a.Config)
	if err != nil {
		return err
	}
	if !on {
		fmt.Fprintln(out, "Hold is off ([sync] hold_unpushed = false) — no story would be held.")
		return nil
	}
	var cursor hosted.WorkstateCursor
	if project, perr := resolveBoundProject(a.Config, a.RepoRoot); perr == nil {
		if c, lerr := hosted.LoadWorkstateCursor(server, project, a.RepoRoot); lerr == nil {
			cursor = c
		}
	}
	if optIn["ledger"] {
		if cursor, _, err = validateLedgerCursor(cmd.Context(), a, cursor); err != nil {
			return err
		}
	}
	batch, _, _, rows, err := collectWorkstateRows(cmd.Context(), a, optIn, cursor)
	if err != nil {
		return err
	}
	batch.Items = filterItemsAfter(batch.Items, cursor.ItemsUpdatedAt)
	res, err := classifyHolds(cmd.Context(), a, batch.Items, rows)
	if err != nil {
		return err
	}
	if len(res.held) == 0 && len(res.unavailable) == 0 {
		fmt.Fprintln(out, "No story would be held.")
		return nil
	}
	for _, l := range res.lines() {
		fmt.Fprintln(out, l)
	}
	return nil
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

func rawItemID(raw json.RawMessage) string {
	var w struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &w)
	return w.ID
}

func maxItemUpdatedAt(items []json.RawMessage) time.Time {
	var max time.Time
	for _, raw := range items {
		var w struct {
			UpdatedAt time.Time `json:"updated_at"`
		}
		_ = json.Unmarshal(raw, &w)
		if w.UpdatedAt.After(max) {
			max = w.UpdatedAt
		}
	}
	return max
}

// partitionByHold drops items held by another location so push cannot fork
// a second live copy (sty_f6cff549). Local registry is a cache; ItemHold
// probes unknown ids. Probe errors fall through to Apply (server authority).
func partitionByHold(ctx context.Context, client *hosted.Client, server, project, repoRoot string, items []json.RawMessage) (skipped []*hosted.HeldError, keep []json.RawMessage, err error) {
	if len(items) == 0 {
		return nil, items, nil
	}
	reg, err := hosted.LoadHolds(server, project, repoRoot)
	if err != nil {
		return nil, nil, err
	}
	self := client.Location()
	keep = make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		id := rawItemID(raw)
		if id == "" {
			keep = append(keep, raw)
			continue
		}
		if loc, ok := reg[id]; ok && loc != "" && self != "" && loc != self {
			skipped = append(skipped, &hosted.HeldError{ItemID: id, Hold: hosted.HoldState{LocationID: loc}})
			continue
		}
		if self != "" && (reg[id] == "" || reg[id] != self) {
			st, herr := client.ItemHold(ctx, project, id)
			if herr == nil && st.LocationID != "" && st.LocationID != self {
				_ = hosted.RecordHold(server, project, repoRoot, id, st.LocationID)
				skipped = append(skipped, &hosted.HeldError{ItemID: id, Hold: st})
				continue
			}
		}
		keep = append(keep, raw)
	}
	return skipped, keep, nil
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
