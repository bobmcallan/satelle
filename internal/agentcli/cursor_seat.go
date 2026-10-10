package agentcli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// cursor-agent as a dispatched performer or reviewer seat (sty_10c52ab3). Every
// cursor-specific fact a seat needs lives here, so the validator, the engine and
// the config layer ask agentcli and hold no cursor literal of their own
// ([[satelle-agent-agnostic]] §1). The evidence is testdata/cursor/README.md:
// fixtures 1a/1b/1c/1g/8a/8b (transport and prompt delivery), 15-* (a read-only
// seat opens out-of-tree material with its Read tool), 16-*/17/18 (plan and ask
// mode refuse a write and a mutating shell even under approvalMode "unrestricted").

// cursorReadOnlyMode is the cursor mode a reviewer is forced into. The mode, not
// the approval setting and not a tool allow-list (cursor has none), is what keeps
// a reviewer from writing. It is ask, not plan: a plan-mode reviewer answers with
// a plan instead of the verdict it was asked for, an ask-mode one answers.
const cursorReadOnlyMode = "ask"

// cursorACPLabel is the acpAdapterLabel of a cursor ACP peer.
const cursorACPLabel = "cursor acp"

// Where an adapter takes the step's instructions (the {system} body).
const (
	// SystemOnArgv: the adapter has an argv flag for it, so the binding's command
	// template must carry {system} as its own token.
	SystemOnArgv = "argv"
	// SystemOnStdin: the adapter has no system-prompt flag; satelle writes the
	// instructions ahead of the work item on stdin.
	SystemOnStdin = "stdin"
)

// SystemDelivery says where adapter takes a step's instructions. Cursor has no
// system-prompt flag (sty_383ff068), so its instructions ride stdin.
func SystemDelivery(adapter string) string {
	if adapter == HarnessCursor {
		return SystemOnStdin
	}
	return SystemOnArgv
}

// ReadsMaterialByPath reports whether adapter's seat opens satelle's scratch
// material (the absolute docs[].path / diff.patch_path of a payload) with its
// own read tool, so no binding grant has to name a context channel. Cursor does,
// even in plan and ask mode and outside its workspace (probes 15-*).
func ReadsMaterialByPath(adapter string) bool { return adapter == HarnessCursor }

// ToolsGrantNote is the validate note for an adapter whose tool grant is only
// advisory, or "" when the grant means what it says. Cursor has no allow-list.
func ToolsGrantNote(adapter string) string {
	if adapter != HarnessCursor {
		return ""
	}
	return "tools: advisory (cursor has no allow-list); read-only enforced by mode"
}

// ReadOnlyCeilingNote is the validate note for a reviewer whose read-only
// ceiling is a forced mode rather than a tools grant, or "" for any other
// adapter.
func ReadOnlyCeilingNote(adapter string) string {
	if adapter != HarnessCursor {
		return ""
	}
	return "ceiling: forced " + cursorReadOnlyMode + " mode (cursor has no allow-list)"
}

// cursorOfferedUnavailable is the OfferedSource a cursor reviewer records.
const cursorOfferedUnavailable = "unavailable: cursor has no --tools allow-list; read-only is enforced by --mode ask / session/set_mode ask"

// errCursorReadOnlyMode builds the refusal of a reviewer template that names a
// cursor mode which can write.
func errCursorReadOnlyMode(mode string) error {
	return fmt.Errorf("agentcli: cursor: a reviewer must run in --mode ask (read-only is enforced by the mode, probes 16-*, and a plan-mode reviewer returns a plan, not a verdict); this template asks --mode %s", mode)
}

// cursorReadOnlyArgs returns the argv a read-only cursor spawn runs with: no
// --mode gets `--mode ask` appended, ask is kept, and any other mode (plan
// included) is refused.
func cursorReadOnlyArgs(args []string) ([]string, error) {
	modes := flagValues(args, "--mode")
	if len(modes) == 0 {
		return append(append([]string(nil), args...), "--mode", cursorReadOnlyMode), nil
	}
	// Every occurrence counts: cursor honours the last one, so `--mode ask
	// --mode agent` would run in agent mode.
	for _, mode := range modes {
		if strings.ToLower(strings.TrimSpace(mode)) != cursorReadOnlyMode {
			return nil, errCursorReadOnlyMode(mode)
		}
	}
	return args, nil
}

// flagValues returns the value of every occurrence of --name in args, in both the
// "--name v" and "--name=v" forms; a flag with no value contributes "".
func flagValues(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == name:
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				out = append(out, args[i+1])
				i++
			} else {
				out = append(out, "")
			}
		case strings.HasPrefix(a, name+"="):
			out = append(out, strings.TrimPrefix(a, name+"="))
		}
	}
	return out
}

// ReadOnlyEnforced reports whether satelle itself holds a reviewer on adapter to
// read-only, independent of any tools grant: the ceiling a validator may rely on.
// For a cursor command template that is the mode (the error says why a template
// naming a writing mode is not); for cursor ACP it is the forced session mode.
// Every other adapter answers (false, nil) and keeps its grant-based judgement.
func ReadOnlyEnforced(adapter string, args []string, iface string) (bool, error) {
	if adapter != HarnessCursor {
		return false, nil
	}
	switch iface {
	case InterfaceACP:
		return true, nil
	case InterfaceCommand, "":
		if _, err := cursorReadOnlyArgs(args); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// cursorTemplateProblem refuses a cursor command template that cannot serve the
// command transport: a positional prompt suppresses stdin (fixture 8b), and only
// the json result envelope is read (stream-json would route its camelCase usage
// to another adapter's mapping; text has no envelope).
func cursorTemplateProblem(args []string) error {
	for _, tok := range args {
		if tok == "{system}" || tok == "{payload}" {
			return fmt.Errorf("agentcli: cursor: %s as an argv token is a positional prompt, which suppresses stdin (fixture 8b) — remove it; satelle delivers the instructions and the work item on stdin", tok)
		}
	}
	formats := flagValues(args, "--output-format")
	if len(formats) == 0 {
		return fmt.Errorf("agentcli: cursor command dispatch reads the json result envelope; use --output-format json")
	}
	for _, v := range formats {
		if v != "json" {
			return fmt.Errorf("agentcli: cursor command dispatch reads the json result envelope; use --output-format json")
		}
	}
	return nil
}

// cursorStdin is what a cursor run reads: the step's instructions, then the work
// item. Writing them as AGENTS.md or .cursor/rules would leave an untracked change
// in the tree under review; a positional prompt suppresses stdin and risks
// MAX_ARG_STRLEN on a large payload (fixtures 1c, 8a, 8b).
func cursorStdin(req Request) string {
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return req.Payload
	}
	return req.SystemPrompt + "\n\n---\n\n" + req.Payload
}

// cursorSpawn renders a cursor command spawn: a read-only request gets the ask
// mode (or is refused), and the request's stdin carries instructions and payload.
func cursorSpawn(args []string, req Request) ([]string, Request, error) {
	if req.ReadOnly {
		var err error
		if args, err = cursorReadOnlyArgs(args); err != nil {
			return nil, req, err
		}
	}
	req.Payload = cursorStdin(req)
	return args, req, nil
}

// cursorACPProblem refuses a cursor ACP spawn line that does not start the ACP
// server: only `cursor-agent acp` speaks it (fixture 5), so a one-shot line such as
// `cursor-agent -p …` is not an ACP spawn however it is declared.
func cursorACPProblem(args []string) error {
	for _, a := range args {
		if a == "acp" {
			return nil
		}
	}
	return fmt.Errorf("agentcli: cursor: interface=acp needs the acp subcommand (cursor-agent acp), got %q", strings.Join(args, " "))
}

// errCursorStream refuses interface=stream: cursor emits stream-json output but
// takes no stream-json input (fixture 1b), so the live stream transport does not
// fit.
func errCursorStream() error {
	return fmt.Errorf("agentcli: cursor: no stream-json input (fixture 1b); use interface=command or acp")
}

// cursorEnvelope is `cursor-agent -p --output-format json`: the decision text is
// `result` and usage is camelCase (fixture 1a). A claude envelope shares
// type=result and result, so cursor is told apart by request_id or camelCase usage.
type cursorEnvelope struct {
	Type      string         `json:"type"`
	Result    string         `json:"result"`
	RequestID string         `json:"request_id"`
	Usage     map[string]any `json:"usage"`
}

// cursorCostReason is why a cursor run records no dollar cost: no capture and no
// bundle schema carries a price, and satelle never prices from another provider's
// table (sty_a3258bb3).
const cursorCostReason = "cursor reports no per-token price (billed through the cursor account)"

// cursorEnvTokens maps the json envelope's four token counts to a UsageResult.
// The envelope reads EXCLUSIVELY: inputTokens is the uncached input and the cache
// counts sit beside it, so fresh = inputTokens and the total input is
// inputTokens + cacheReadTokens + cacheWriteTokens (measured: a warm resumed turn
// reported inputTokens 74 with cacheReadTokens 12664, so a cache count above
// inputTokens is the normal warm shape, never invalid). A missing input or output
// is a cursor-named unavailable, never a zero; a missing cache field leaves the
// split unreported and InputTokens at the uncached figure.
func cursorEnvTokens(input, output, read, write *int, label string) UsageResult {
	if input == nil || output == nil {
		return unavailableUsage(label, "usage carried no inputTokens/outputTokens")
	}
	u := UsageResult{
		Available:    true,
		InputTokens:  *input,
		OutputTokens: *output,
	}
	if read != nil && write != nil && *read >= 0 && *write >= 0 {
		u.CacheSplitAvailable = true
		u.FreshInputTokens = *input
		u.CacheReadInputTokens = *read
		u.CacheCreationInputTokens = *write
		u.InputTokens = *input + *read + *write
	}
	u.TotalTokens = u.InputTokens + u.OutputTokens
	return u
}

// cursorTokens maps the interactive stop hook payload's four token counts to a
// UsageResult. Unlike the json envelope, the stop payload reads INCLUSIVELY:
// input_tokens includes the cache reads (measured: every warm turn's input_tokens
// exceeds its cache_read_tokens by about 100, 7-stop 16563/25758 and 13057/13161),
// so fresh = input - read - write. A missing input or output is a cursor-named
// unavailable, never a zero; a missing cache field leaves the split unreported; a
// cache sum above input is not a count that can be subtracted, so the split is
// withheld rather than recorded negative.
func cursorTokens(input, output, read, write *int, label string) UsageResult {
	if input == nil || output == nil {
		return unavailableUsage(label, "usage carried no inputTokens/outputTokens")
	}
	u := UsageResult{
		Available:    true,
		InputTokens:  *input,
		OutputTokens: *output,
		TotalTokens:  *input + *output,
	}
	switch {
	case read == nil || write == nil:
		// cache fields not reported: the split is unreported, not zero.
	case *read < 0 || *write < 0 || *read+*write > *input:
		// Reported but inconsistent with the inclusive reading: keep the totals and
		// leave the split unavailable (CacheSplitAvailable false) rather than negative.
	default:
		u.CacheSplitAvailable = true
		u.CacheReadInputTokens = *read
		u.CacheCreationInputTokens = *write
		u.FreshInputTokens = *input - *read - *write
	}
	return u
}

// cursorEnvUsage reads the json envelope's camelCase usage object.
func cursorEnvUsage(m map[string]any) UsageResult {
	const label = "cursor command"
	if m == nil {
		return unavailableUsage(label, "json envelope carried no usage")
	}
	num := func(key string) *int {
		f, ok := m[key].(float64)
		if !ok {
			return nil
		}
		n := int(f)
		return &n
	}
	return cursorEnvTokens(num("inputTokens"), num("outputTokens"), num("cacheReadTokens"), num("cacheWriteTokens"), label)
}

// unwrapCursor reads a cursor json envelope. ok is false for any other shape.
// Usage is mapped from the camelCase usage object (cursorTokens); an envelope
// without it is a cursor-named unavailable, never a zero read through another
// adapter's field names. Cost is always a cursor-named unavailable, and the
// envelope names no model.
func unwrapCursor(trimmed []byte) (text []byte, u UsageResult, ok bool) {
	var env cursorEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil || env.Type != "result" || env.Result == "" {
		return nil, UsageResult{}, false
	}
	if _, camel := env.Usage["inputTokens"]; env.RequestID == "" && !camel {
		return nil, UsageResult{}, false
	}
	u = cursorEnvUsage(env.Usage)
	u.CostUSD = nil
	u.CostUnavailableReason = "cursor command: " + cursorCostReason
	u.ModelResolved = noModelReport("cursor command")
	return []byte(env.Result), u, true
}

// acpUnavailableUsage is the usage a cursor ACP prompt result records: the
// session/prompt response carries none (testdata/cursor/5-acp.json).
func acpUnavailableUsage(adapter string) UsageResult {
	if adapter == cursorACPLabel {
		return unavailableUsage(cursorACPLabel, "session/prompt response carries no usage (testdata/cursor/5-acp.json)")
	}
	return unavailableUsage("acp", "session/prompt response carried no usage token fields")
}

// cursorACPGrant widens the reviewer grant a cursor ACP session is judged against
// by the read tool: ask mode lets cursor read without asking (probe 18), so a
// read is not an out-of-grant tool, while a write is refused by the mode itself.
func cursorACPGrant(adapter, grant string) string {
	if adapter != cursorACPLabel {
		return grant
	}
	if strings.TrimSpace(grant) == "" {
		return "Read"
	}
	return grant + ",Read"
}

// cursorModelValue maps a binding's model to the value cursor's session/new
// advertises in its `model` config option. Option values are parameterised
// ("composer-2.5[fast=true]") and the peer rejects a bare name, so an exact value
// is kept, else the first value starting with "<model>[" is used. A reply that
// advertises no model option leaves the model as given; one that does and has no
// match refuses the run, listing what is available.
func cursorModelValue(sessRes json.RawMessage, model string) (string, error) {
	var reply struct {
		ConfigOptions []struct {
			ID      string `json:"id"`
			Options []struct {
				Value string `json:"value"`
			} `json:"options"`
		} `json:"configOptions"`
	}
	if json.Unmarshal(sessRes, &reply) != nil {
		return model, nil
	}
	var values []string
	for _, opt := range reply.ConfigOptions {
		if opt.ID != "model" {
			continue
		}
		for _, o := range opt.Options {
			values = append(values, o.Value)
		}
	}
	if len(values) == 0 {
		return model, nil
	}
	for _, v := range values {
		if v == model {
			return v, nil
		}
	}
	for _, v := range values {
		if strings.HasPrefix(v, model+"[") {
			return v, nil
		}
	}
	return "", fmt.Errorf("cursor: acp model %q is not an available model; available values: %s", model, strings.Join(values, ", "))
}

// cursorSetModeError is the refusal of a reviewer session whose peer would not
// enter ask mode: without it nothing keeps the reviewer from writing.
func cursorSetModeError(err error) error {
	return fmt.Errorf("cursor: session/set_mode %s failed: %v; refusing to run a reviewer without read-only", cursorReadOnlyMode, err)
}

// cursorOffered is DescribeReviewer's answer for a cursor spawn.
func cursorOffered(transport string) ReviewerIsolation {
	return ReviewerIsolation{
		Adapter:       HarnessCursor + " " + transport,
		OfferedSource: cursorOfferedUnavailable,
	}
}

// cursorPreflight reports the one way a cursor reviewer binding cannot be held
// read-only: a command template that asks for a mode which can write.
func cursorPreflight(iface string, args []string) []IsolationGap {
	if _, err := ReadOnlyEnforced(HarnessCursor, args, iface); err != nil {
		return []IsolationGap{{
			Adapter: HarnessCursor + "/" + iface,
			What:    strings.TrimPrefix(err.Error(), "agentcli: "),
			Fix:     "drop --mode (satelle forces ask) or use --mode ask",
		}}
	}
	return nil
}
