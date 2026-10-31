// runtime_liveness.go — refuse relocating a live runtime (sty_5308eb60).
//
// migrate / runtime migrate take a VACUUM INTO snapshot of the legacy DB and
// then remove it as residue. Without a liveness guard, every write the working
// session commits AFTER the snapshot is stranded (deleted inode / removed
// file) while new processes read the home-keyed copy. This package is pure
// mechanism: detect live holders, refuse (or warn under --allow-live).
package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	_ "modernc.org/sqlite"
)

// liveHolder is one reason migrate should refuse (or warn under --allow-live).
type liveHolder struct {
	Kind   string // "transition" | "serve"
	Detail string // one-line description naming the holder
}

// detectLiveRuntime reports transitions in flight in the legacy DB and a
// responding satelle serve on this repo's resolved web port. The legacy DB is
// opened read-only with the same DSN shape vacuumInto uses — never store.Open,
// which would create wal/shm and fight VACUUM INTO.
//
// A transition whose process died, or that outlived ledger.InFlightTTL, is not
// open, so a dead dispatch never wedges migrate.
func detectLiveRuntime(cfg config.Config, repoRoot, legacyDB string) ([]liveHolder, error) {
	var out []liveHolder
	if fileExists(legacyDB) {
		open, err := scanOpenTransitions(legacyDB)
		if err != nil {
			return nil, err
		}
		out = append(out, open...)
	}
	if h, ok := probeServe(cfg, repoRoot); ok {
		out = append(out, h)
	}
	return out, nil
}

// serveProbeEnabled gates the HTTP serve probe. Unit tests flip it off so a
// developer's live satelle serve on :8787 does not refuse every migrate fixture.
var serveProbeEnabled = true

// probeServe dials resolved ports and checks /healthz. Returns a holder on
// an ok body. A foreign process on the same port is a false positive — the
// refusal message names --allow-live for that case.
func probeServe(cfg config.Config, repoRoot string) (liveHolder, bool) {
	if !serveProbeEnabled {
		return liveHolder{}, false
	}
	for _, port := range livenessPorts(cfg, repoRoot) {
		if port <= 0 {
			continue
		}
		url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		if !healthzOK(url) {
			continue
		}
		return liveHolder{
			Kind:   "serve",
			Detail: fmt.Sprintf("satelled          http://127.0.0.1:%d/ responding", port),
		}, true
	}
	return liveHolder{}, false
}

// livenessPorts returns the one machine service port to probe.
func livenessPorts(cfg config.Config, repoRoot string) []int {
	_ = cfg
	_ = repoRoot
	if gc, err := config.LoadGlobal(); err == nil {
		return []int{gc.Service.ResolvePort()}
	}
	return []int{config.DefaultWebPort}
}

// healthzOK is the dial+GET used by the serve probe; tests swap it.
var healthzOK = defaultHealthzOK

func defaultHealthzOK(url string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	// Quick dial first so a closed port fails fast.
	host := strings.TrimPrefix(url, "http://")
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	conn, err := net.DialTimeout("tcp", host, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	return strings.Contains(strings.ToLower(string(buf[:n])), "ok")
}

// scanOpenTransitions opens legacyDB read-only and returns each item with a
// transition in flight there: a dispatch still writing to it. A database that
// predates the ledger yields none.
func scanOpenTransitions(legacyDB string) ([]liveHolder, error) {
	db, err := sql.Open("sqlite", store.ReadOnlyDSN(legacyDB))
	if err != nil {
		return nil, fmt.Errorf("live-runtime: open legacy db: %w", err)
	}
	defer db.Close()
	now := time.Now().UTC()
	open, err := ledger.New(db).ListOpenTransitions(context.Background(), "", now)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("live-runtime: list transitions: %w", err)
	}
	ids := make([]string, 0, len(open))
	for id := range open {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]liveHolder, 0, len(ids))
	for _, id := range ids {
		t := open[id]
		out = append(out, liveHolder{Kind: "transition", Detail: fmt.Sprintf("transition in flight  %s  %s → %s  started %s ago",
			id, t.From, t.To, formatAge(now.Sub(t.StartedAt)))})
	}
	return out, nil
}

func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// refuseLiveRuntimeError is the non-zero exit for a live runtime without --allow-live.
func refuseLiveRuntimeError(holders []liveHolder) error {
	var b strings.Builder
	b.WriteString("migrate: refusing — this repo runtime is LIVE. Relocating now would strand every write made after the snapshot.\n")
	for _, h := range holders {
		b.WriteString("  ")
		b.WriteString(h.Detail)
		b.WriteByte('\n')
	}
	b.WriteString("Wait for that transition to finish (inspect: satelle story get <id>), stop satelle serve, then re-run. If the holder is not really satelle, override with --allow-live (UNSAFE — see satelle migrate --help).")
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// warnAllowLive prints the AC3 risk statement when overriding a live runtime.
func warnAllowLive(out io.Writer, holders []liveHolder) {
	fmt.Fprintln(out, "WARNING: --allow-live — migrating with a LIVE runtime.")
	fmt.Fprintln(out, "  Every write the live session commits AFTER this snapshot is STRANDED: it")
	fmt.Fprintln(out, "  lands in the legacy database this migration then removes (or on a deleted")
	fmt.Fprintln(out, "  inode), while new processes read the home-keyed copy immediately. Status")
	fmt.Fprintln(out, "  transitions, ledger rows and attachments written in")
	fmt.Fprintln(out, "  that window will silently vanish. Holders:")
	for _, h := range holders {
		fmt.Fprintf(out, "    %s\n", h.Detail)
	}
}

// printLivePlanLine adds a plan summary line for dry-run / plan print.
func printLivePlanLine(out io.Writer, holders []liveHolder) {
	if len(holders) == 0 {
		fmt.Fprintln(out, "  runtime:          idle")
		return
	}
	fmt.Fprintf(out, "  runtime:          LIVE — %d holder(s)\n", len(holders))
	for _, h := range holders {
		fmt.Fprintf(out, "    - %s\n", h.Detail)
	}
}

// migrateSplitBrainHelp is the AC4 field guidance for a repo already split by a
// mid-story migration. Embedded in `satelle migrate` Long help so it travels
// with the binary (repo-agnostic mechanism guidance, not dogfood docs).
const migrateSplitBrainHelp = `
If a migration already raced a live session (split brain):

  1. satelle runtime path — confirm the home-keyed DB is now authoritative.
  2. satelle story get <id> and satelle ledger list --story <id> — compare against
     what the working session believes it did. Anything missing was written into
     the removed legacy DB and is gone.
  3. RE-DRIVE lost transitions through the workflow (satelle story set <id>
     --status <state>) so every gate runs again — never hand-patch status forward
     to catch up; the reviews that vanished did not happen on record.
  4. Re-attach documents written in the window (satelle story attach).
  5. If residue removal failed and the legacy DB survives, it is the ONLY record
     of the lost window — read it before removing anything.
`

// ensureLiveOK is the shared gate used by runMigrate and runRuntimeMigrate.
// dryRun=true only reports; apply paths refuse unless allowLive.
// Returns holders (for plan printing) and a non-nil error when refused.
func ensureLiveOK(out io.Writer, cfg config.Config, repoRoot, legacyDB string, allowLive, applying bool) ([]liveHolder, error) {
	if strings.TrimSpace(legacyDB) == "" || !fileExists(legacyDB) {
		return nil, nil
	}
	holders, err := detectLiveRuntime(cfg, repoRoot, legacyDB)
	if err != nil {
		if allowLive {
			fmt.Fprintf(out, "WARNING: --allow-live — could not inspect live runtime (%v); proceeding\n", err)
			return nil, nil
		}
		return nil, fmt.Errorf("migrate: cannot inspect live runtime: %w", err)
	}
	if len(holders) == 0 {
		return nil, nil
	}
	if !applying {
		return holders, nil
	}
	if allowLive {
		warnAllowLive(out, holders)
		return holders, nil
	}
	return holders, refuseLiveRuntimeError(holders)
}
