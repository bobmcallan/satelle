package agentcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DriverSnapshot is the driving (in-loop) session's CUMULATIVE usage so far,
// read directly from that harness's own on-disk session record — the whole
// transcript/rollout/usage file a live CLI session writes for itself, not a
// dispatched one-shot invocation's captured stdout (usage_adapters.go covers
// that channel). The verb layer diffs two snapshots for the same session id
// to produce one driver_usage ledger row (sty_81caa41b).
type DriverSnapshot struct {
	SessionID  string
	Executable string
	Model      string

	FreshInputTokens         int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	OutputTokens             int

	CostUSD               *float64
	CostUnavailableReason string

	// Available distinguishes a record that yielded a real cumulative figure
	// from one that could not be read/parsed, or carries no usage yet.
	Available         bool
	UnavailableReason string

	// Turns is the number of usage-bearing entries folded into this snapshot —
	// zero alongside Available=false says "the record exists but reports
	// nothing yet", distinct from "the record itself could not be opened".
	Turns int

	// MayUndercountInFlightTurn is true for a harness whose session record
	// only flushes a turn's usage once that turn fully completes, so a
	// snapshot read DURING a tool call that is itself part of the in-flight
	// turn (e.g. the `satelle story set` call that closes/parks a story) can
	// undercount that very turn (sty_81caa41b AC6). True for grok and codex —
	// confirmed against real captures: grokDriverSnapshot's cumulative only
	// updates via the "session" object's summary, and codexDriverSnapshot's
	// cumulative is the LAST completed turn's token_count event, both written
	// only once a turn finishes. False for claude, whose transcript already
	// carries the calling message's own usage by the time a tool call within
	// it executes (also confirmed against a real capture — see
	// testdata/driver/README.md) — a verb-layer catch-up keyed on this field
	// must never apply to a harness that does not actually have the problem,
	// or it will misattribute claude's genuinely-later, unrelated usage onto
	// whatever story happened to close first.
	MayUndercountInFlightTurn bool

	// TurnBreakdown is each individual turn's OWN usage delta (not a running
	// cumulative), in completion order, for a harness whose session record
	// can be decomposed per turn (grok's usage.json turns[], codex's sequence
	// of token_count events) — nil for claude and any harness this reader
	// cannot decompose. len(TurnBreakdown) == Turns whenever populated. This
	// is what lets a verb-layer Pending catch-up (sty_81caa41b AC6) credit
	// EXACTLY the one turn that was still in flight at a close/park read —
	// TurnBreakdown[N] where N is that read's own Turns count — without
	// guessing at whatever LATER, unrelated turns a delayed confirming read
	// also picked up.
	TurnBreakdown []DriverTurn
}

// DriverTurn is one turn's own usage delta and completion time, as broken out
// by a harness whose session record carries a per-turn history.
type DriverTurn struct {
	EndedAt    time.Time
	FreshInput int
	CacheRead  int
	CacheWrite int
	Output     int
	CostUSD    *float64
}

// SessionUsageSnapshot reads harness's own session record for sessionID and
// returns its cumulative usage. repoRoot scopes the per-repo session
// directory each in-loop harness keys its record by. An unrecognised harness,
// or a record that cannot be found, opened, or parsed, yields Available=false
// with an adapter-named UnavailableReason — never zeros that look like a
// measurement (satelle-agent-agnostic §2/§3).
func SessionUsageSnapshot(harness, sessionID, repoRoot string) DriverSnapshot {
	sessionID = strings.TrimSpace(sessionID)
	adapter := harness
	if strings.TrimSpace(adapter) == "" {
		adapter = HarnessUnknown
	}
	if sessionID == "" {
		return DriverSnapshot{Executable: adapter, UnavailableReason: fmt.Sprintf("%s: no session id resolved", adapter)}
	}
	switch harness {
	case HarnessClaude:
		return claudeDriverSnapshot(sessionID, repoRoot)
	case HarnessGrok:
		return grokDriverSnapshot(sessionID, repoRoot)
	case HarnessCodex:
		return codexDriverSnapshot(sessionID)
	default:
		return DriverSnapshot{SessionID: sessionID, Executable: adapter,
			UnavailableReason: fmt.Sprintf("%s: no driver-usage reader for this harness", adapter)}
	}
}

// claudeConfigDir/grokHomeDir/codexHomeDir resolve each harness's home
// directory, honouring an env override so tests (and an unusual install) can
// point at testdata instead of the real user home.
func claudeConfigDir() string {
	if v := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".claude")
	}
	return ".claude"
}

func grokHomeDir() string {
	if v := strings.TrimSpace(os.Getenv("GROK_HOME")); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".grok")
	}
	return ".grok"
}

func codexHomeDir() string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".codex")
	}
	return ".codex"
}

// claudeProjectSlug mirrors Claude Code's own project directory naming: the
// absolute repo path with every separator turned into a dash (observed
// directly off this session's own memory path,
// ~/.claude/projects/-home-*-Development-satelle/ — sty_81caa41b).
func claudeProjectSlug(repoRoot string) string {
	return strings.ReplaceAll(filepath.Clean(repoRoot), string(filepath.Separator), "-")
}

// claudeDriverSnapshot reads a Claude Code session transcript JSONL: one JSON
// object per line, an assistant message carrying message.id/model/usage in
// Anthropic's disjoint shape (claudeUsageFromMap). Usage is summed across every
// assistant message, deduplicated by message id — a streamed session can repeat
// the same message id with a growing usage object, so only the LAST occurrence
// of a given id is kept, not summed with its own earlier partials.
func claudeDriverSnapshot(sessionID, repoRoot string) DriverSnapshot {
	snap := DriverSnapshot{SessionID: sessionID, Executable: HarnessClaude}
	path := filepath.Join(claudeConfigDir(), "projects", claudeProjectSlug(repoRoot), sessionID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		snap.UnavailableReason = fmt.Sprintf("claude: session transcript unreadable: %v", err)
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	defer f.Close()

	type perMessage struct {
		fresh, cacheCreate, cacheRead, out int
		model                              string
	}
	byID := map[string]perMessage{}
	var order []string
	var lastModel string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			Type    string `json:"type"`
			Message struct {
				ID    string         `json:"id"`
				Model string         `json:"model"`
				Usage map[string]any `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &row) != nil || row.Type != "assistant" || row.Message.Usage == nil {
			continue
		}
		u := claudeUsageFromMap(row.Message.Usage, "claude")
		if !u.Available {
			continue
		}
		id := row.Message.ID
		if id == "" {
			// No message id to dedupe by: treat as its own entry so usage is
			// never silently dropped.
			id = fmt.Sprintf("__noid_%d", len(order))
		}
		if _, seen := byID[id]; !seen {
			order = append(order, id)
		}
		byID[id] = perMessage{fresh: u.FreshInputTokens, cacheCreate: u.CacheCreationInputTokens, cacheRead: u.CacheReadInputTokens, out: u.OutputTokens, model: row.Message.Model}
		if row.Message.Model != "" {
			lastModel = row.Message.Model
		}
	}
	if err := scanner.Err(); err != nil {
		snap.UnavailableReason = fmt.Sprintf("claude: session transcript unreadable: %v", err)
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	if len(order) == 0 {
		snap.UnavailableReason = "claude: session transcript carries no assistant usage yet"
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	for _, id := range order {
		m := byID[id]
		snap.FreshInputTokens += m.fresh
		snap.CacheCreationInputTokens += m.cacheCreate
		snap.CacheReadInputTokens += m.cacheRead
		snap.OutputTokens += m.out
	}
	snap.Available = true
	snap.Model = lastModel
	snap.Turns = len(order)
	// A claude transcript reports no dollar figure at all (sty_c4df7376: never
	// derive cost from a price table) — cost stays named-unavailable here.
	snap.CostUnavailableReason = "claude: session transcript reports no cost field"
	return snap
}

// grokRepoDirName is grok's per-repo session directory name under
// ~/.grok/sessions/<repo>/<session-id>/usage.json: the absolute cwd,
// URL-encoded (confirmed against a real capture — sty_81caa41b coder round 2 —
// e.g. /home/bobmcallan/Development/satelle becomes
// %2Fhome%2Fbobmcallan%2FDevelopment%2Fsatelle, NOT the dash-slug claude uses).
func grokRepoDirName(repoRoot string) string {
	return url.QueryEscape(filepath.Clean(repoRoot))
}

// grokDriverSnapshot reads grok's cumulative per-session usage.json. The
// session-level cumulative lives under the top-level "session" object (its
// per-turn breakdown lives under "turns", which this reader does not need);
// it reuses grokUsageFromMap, the same camelCase mapping the ACP per-turn
// transport uses, against that "session" object.
func grokDriverSnapshot(sessionID, repoRoot string) DriverSnapshot {
	snap := DriverSnapshot{SessionID: sessionID, Executable: HarnessGrok}
	path := filepath.Join(grokHomeDir(), "sessions", grokRepoDirName(repoRoot), sessionID, "usage.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if info, derr := os.Stat(filepath.Dir(path)); derr == nil && info.IsDir() {
			// grok writes usage.json when a turn ends, so a session directory
			// without it is a session still inside its first turn: nothing
			// flushed yet, and that turn may be the one making this call.
			snap.Available = true
			snap.MayUndercountInFlightTurn = true
			snap.CostUnavailableReason = "grok: first turn not yet flushed to usage.json"
			return snap
		}
	}
	if err != nil {
		snap.UnavailableReason = fmt.Sprintf("grok: session usage.json unreadable: %v", err)
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil {
		snap.UnavailableReason = "grok: session usage.json is not valid JSON"
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	session, ok := raw["session"].(map[string]any)
	if !ok {
		snap.UnavailableReason = "grok: session usage.json carries no session summary"
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	u := grokUsageFromMap(session)
	if !u.Available {
		snap.UnavailableReason = u.UnavailableReason
		snap.CostUnavailableReason = u.CostUnavailableReason
		return snap
	}
	snap.Available = true
	snap.MayUndercountInFlightTurn = true
	snap.FreshInputTokens = freshOf(u)
	snap.CacheCreationInputTokens = u.CacheCreationInputTokens
	snap.CacheReadInputTokens = u.CacheReadInputTokens
	snap.OutputTokens = u.OutputTokens
	snap.CostUSD = u.CostUSD
	snap.CostUnavailableReason = u.CostUnavailableReason
	snap.Model = grokModelFromRaw(session)
	if turnCount, ok := session["turnCount"]; ok {
		snap.Turns = intValue(turnCount)
	} else {
		snap.Turns = 1
	}
	snap.TurnBreakdown = grokTurnBreakdown(raw)
	return snap
}

// grokTurnBreakdown decomposes usage.json's "turns" array — each entry
// carries the same field shape as the top-level "session" summary
// (grokUsageFromMap already maps it) plus its own "endedAt" (sty_81caa41b
// AC6). A turn this reader cannot map is skipped rather than aborting the
// whole breakdown — a partial breakdown still lets the verb layer index the
// turns it CAN see; it never fabricates one it can't.
func grokTurnBreakdown(raw map[string]any) []DriverTurn {
	turnsRaw, ok := raw["turns"].([]any)
	if !ok {
		return nil
	}
	out := make([]DriverTurn, 0, len(turnsRaw))
	for _, tr := range turnsRaw {
		tm, ok := tr.(map[string]any)
		if !ok {
			continue
		}
		u := grokUsageFromMap(tm)
		if !u.Available {
			continue
		}
		dt := DriverTurn{
			FreshInput: freshOf(u), CacheRead: u.CacheReadInputTokens,
			CacheWrite: u.CacheCreationInputTokens, Output: u.OutputTokens, CostUSD: u.CostUSD,
		}
		if ea, ok := tm["endedAt"].(string); ok {
			if t, err := time.Parse(time.RFC3339, ea); err == nil {
				dt.EndedAt = t
			}
		}
		out = append(out, dt)
	}
	return out
}

// grokModelFromRaw prefers the session summary's "primaryModelId" (the real
// field grok's usage.json carries), falling back to an explicit "model" field
// or the single key of a one-entry modelUsage map (usage_adapters.go's
// documented shape: "modelUsage is keyed by resolved model id").
func grokModelFromRaw(raw map[string]any) string {
	if m, ok := raw["primaryModelId"].(string); ok && m != "" {
		return m
	}
	if m, ok := raw["model"].(string); ok && m != "" {
		return m
	}
	if mu, ok := raw["modelUsage"].(map[string]any); ok && len(mu) == 1 {
		for k := range mu {
			return k
		}
	}
	return ""
}

// freshOf returns the fresh-input share of a mapped UsageResult, falling back
// to the undecomposed InputTokens when the provider named no cache split at
// all — so a session with zero cache activity still attributes its input
// tokens to something, rather than losing them to an unset split field.
func freshOf(u *UsageResult) int {
	if u.CacheSplitAvailable {
		return u.FreshInputTokens
	}
	return u.InputTokens
}

// codexDriverSnapshot reads a codex session rollout JSONL under
// ~/.codex/sessions/**/rollout-*<session-id>*.jsonl. The model comes from a
// turn_context event's payload.model; the cumulative usage comes from the
// LAST event_msg event whose payload.type is "token_count", at
// payload.info.total_token_usage — confirmed against a real rollout capture
// (sty_81caa41b coder round 2). total_token_usage is already the harness's
// own cumulative figure for the session (unlike claude/grok, no summing
// needed).
func codexDriverSnapshot(sessionID string) DriverSnapshot {
	snap := DriverSnapshot{SessionID: sessionID, Executable: HarnessCodex}
	root := filepath.Join(codexHomeDir(), "sessions")
	path, err := findCodexRollout(root, sessionID)
	if err != nil {
		snap.UnavailableReason = fmt.Sprintf("codex: %v", err)
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	f, err := os.Open(path)
	if err != nil {
		snap.UnavailableReason = fmt.Sprintf("codex: session rollout unreadable: %v", err)
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	defer f.Close()

	var last map[string]any
	var lastUsage *UsageResult
	var model string
	turns := 0
	var breakdown []DriverTurn
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev map[string]any
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		typ, _ := ev["type"].(string)
		payload, _ := ev["payload"].(map[string]any)
		switch typ {
		case "turn_context":
			if m, ok := payload["model"].(string); ok && m != "" {
				model = m
			}
		case "event_msg":
			if msgTyp, _ := payload["type"].(string); msgTyp != "token_count" {
				continue
			}
			info, _ := payload["info"].(map[string]any)
			total, _ := info["total_token_usage"].(map[string]any)
			if total == nil {
				continue
			}
			// last, turns and breakdown advance TOGETHER, only for an event
			// whose usage map actually parses (sty_81caa41b Revision 4): a
			// skipped/unavailable token_count event must never count as a turn
			// with no breakdown entry to match it (len(TurnBreakdown)==Turns is
			// an invariant the verb layer's turn-index credit indexes into), and
			// must never overwrite `last` with a map codexUsageFromMap itself
			// would reject, which would otherwise flip the WHOLE snapshot
			// Available=false even though an earlier event already read fine.
			u := codexUsageFromMap(total)
			if !u.Available {
				continue
			}
			last = total
			turns++
			// total_token_usage is the harness's own SESSION-cumulative figure
			// (see the doc comment above) — this event's own turn delta is the
			// difference from the previous token_count event's cumulative, not
			// the map itself (sty_81caa41b AC6 needs per-turn deltas, not
			// running totals, to isolate one closing turn from later ones).
			dt := DriverTurn{FreshInput: freshOf(u), CacheRead: u.CacheReadInputTokens, Output: u.OutputTokens}
			if lastUsage != nil {
				dt.FreshInput -= freshOf(lastUsage)
				dt.CacheRead -= lastUsage.CacheReadInputTokens
				dt.Output -= lastUsage.OutputTokens
			}
			if ts, ok := ev["timestamp"].(string); ok {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					dt.EndedAt = t
				}
			}
			breakdown = append(breakdown, dt)
			lastUsage = u
		}
	}
	if err := scanner.Err(); err != nil {
		snap.UnavailableReason = fmt.Sprintf("codex: session rollout unreadable: %v", err)
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	if last == nil {
		snap.UnavailableReason = "codex: no token_count usage in session rollout"
		snap.CostUnavailableReason = snap.UnavailableReason
		return snap
	}
	u := codexUsageFromMap(last)
	if !u.Available {
		snap.UnavailableReason = u.UnavailableReason
		snap.CostUnavailableReason = u.CostUnavailableReason
		return snap
	}
	snap.Available = true
	snap.MayUndercountInFlightTurn = true
	snap.Model = model
	snap.FreshInputTokens = freshOf(u)
	snap.CacheReadInputTokens = u.CacheReadInputTokens
	snap.OutputTokens = u.OutputTokens
	snap.Turns = turns
	snap.TurnBreakdown = breakdown
	// codex has no cache-write concept (OpenAI prompt caching bills no write
	// share) and its rollout carries no dollar figure.
	snap.CostUnavailableReason = "codex: session rollout reports no cost field"
	return snap
}

// findCodexRollout walks root for the one file matching rollout-*<sessionID>*.jsonl.
func findCodexRollout(root, sessionID string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort walk: an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") && strings.Contains(name, sessionID) {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("session rollout directory unreadable: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("no session rollout found for %s under %s", sessionID, root)
	}
	return found, nil
}
