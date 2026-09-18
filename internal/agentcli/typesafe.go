package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

// TypeSafeAPIKeyEnv is the env var that carries the System One Bearer token.
// Binding authors reference it as env = { TYPESAFE_API_KEY = "${TYPESAFE_API_KEY}" };
// the value is never logged or included in Command() evidence.
const TypeSafeAPIKeyEnv = "TYPESAFE_API_KEY"

// DefaultTypeSafeEndpoint is the System One evaluation URL.
const DefaultTypeSafeEndpoint = "https://api.typesafe.ai/v1/systemone"

// typeSafeRunner is a one-shot HTTP transport for interface=typesafe. It POSTs
// the satelle-built payload plus skill-authored questions to System One and
// re-emits {"decision","notes","reasoning"} bytes so parseDecision / enact stay
// unchanged. Rubric questions and low-confidence policy live in a ```typesafe
// fence on the skill body (Request.SystemPrompt) — never as Go string literals.
type typeSafeRunner struct {
	endpoint string
	client   *http.Client // nil → http.DefaultClient; tests inject httptest clients
}

// newTypeSafeRunner builds a typesafe runner from a single https:// endpoint URL.
// Rejects empty/in-loop, multi-token commands, and any {…} placeholder (those are
// meaningless on an HTTP transport — reject loudly, same posture as stream).
func newTypeSafeRunner(command string) (Runner, error) {
	cmd := strings.TrimSpace(command)
	if cmd == "" || strings.EqualFold(cmd, "in-loop") {
		return nil, fmt.Errorf("agentcli: interface=typesafe requires a single https:// endpoint URL (e.g. %s), not in-loop/empty", DefaultTypeSafeEndpoint)
	}
	fields := strings.Fields(cmd)
	if len(fields) != 1 {
		return nil, fmt.Errorf("agentcli: interface=typesafe command must be a single URL token, got %d", len(fields))
	}
	raw := fields[0]
	if strings.Contains(raw, "{") && strings.Contains(raw, "}") {
		return nil, fmt.Errorf("agentcli: interface=typesafe command must not contain placeholders — the endpoint is a literal URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("agentcli: interface=typesafe command %q: want an https:// URL", raw)
	}
	return typeSafeRunner{endpoint: raw}, nil
}

func (t typeSafeRunner) Name() string {
	if u, err := url.Parse(t.endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return "typesafe"
}

// Command returns the endpoint URL only — never the Bearer key or Env.
func (t typeSafeRunner) Command() string { return t.endpoint }

func (t typeSafeRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, fmt.Errorf("agentcli: typesafe: model is required (pin a versioned id such as jev-1.13.0 on the binding)")
	}
	key := typeSafeAPIKey(req.Env)
	if key == "" {
		return nil, fmt.Errorf("agentcli: typesafe: %s is not set — export it or resolve it via the binding env", TypeSafeAPIKeyEnv)
	}
	policy, err := parseTypeSafePolicy(req.SystemPrompt)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(typeSafeRequest{
		Model:     model,
		State:     typeSafeState(req),
		Questions: policy.Questions,
	})
	if err != nil {
		return nil, fmt.Errorf("agentcli: typesafe: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agentcli: typesafe: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+key)
	httpReq.Header.Set("Content-Type", "application/json")

	client := t.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		// Honour ctx cancellation / deadline; never embed the key in the error.
		return nil, fmt.Errorf("agentcli: typesafe: POST %s: %w", t.endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("agentcli: typesafe: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		tail := strings.TrimSpace(string(raw))
		if len(tail) > 300 {
			tail = "…" + tail[len(tail)-300:]
		}
		return nil, fmt.Errorf("agentcli: typesafe: POST %s: HTTP %d: %s", t.endpoint, resp.StatusCode, tail)
	}
	return emitTypeSafeDecision(raw, policy)
}

func typeSafeAPIKey(env map[string]string) string {
	if env != nil {
		if v := strings.TrimSpace(env[TypeSafeAPIKeyEnv]); v != "" && !isUnresolvedEnvRef(v) {
			return v
		}
	}
	return strings.TrimSpace(os.Getenv(TypeSafeAPIKeyEnv))
}

func isUnresolvedEnvRef(v string) bool {
	return strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")
}

// typeSafeState builds the System One `state` object from the satelle request.
// The transition payload is the primary evidence; the skill prose (without the
// typesafe fence) rides alongside so question instructions can refer to it.
func typeSafeState(req Request) any {
	state := map[string]any{}
	if strings.TrimSpace(req.Payload) != "" {
		var payload any
		if err := json.Unmarshal([]byte(req.Payload), &payload); err == nil {
			state["payload"] = payload
		} else {
			state["payload"] = req.Payload
		}
	}
	if prose := stripTypeSafeFence(req.SystemPrompt); prose != "" {
		state["system"] = prose
	}
	if len(state) == 0 {
		return map[string]any{}
	}
	return state
}

type typeSafeRequest struct {
	Model     string         `json:"model"`
	State     any            `json:"state"`
	Questions map[string]any `json:"questions"`
}

// typeSafePolicy is the skill-authored ```typesafe fence. Questions and the
// optional confidence floor are configuration; Go carries no default threshold
// and never composes Nouls into pass/fail.
type typeSafePolicy struct {
	Questions       map[string]any
	VerdictQuestion string
	MinConfidence   *float64
	OnLowConfidence string
}

type typeSafePolicyRaw struct {
	Questions       json.RawMessage `json:"questions"`
	Verdict         string          `json:"verdict"`
	VerdictQuestion string          `json:"verdict_question"`
	MinConfidence   *float64        `json:"min_confidence"`
	OnLowConfidence string          `json:"on_low_confidence"`
}

func parseTypeSafePolicy(systemPrompt string) (typeSafePolicy, error) {
	block := typeSafeFence(systemPrompt)
	if block == "" {
		return typeSafePolicy{}, fmt.Errorf("agentcli: typesafe: skill body has no ```typesafe fence — author the typed questions and optional min_confidence policy in the gate skill")
	}
	var raw typeSafePolicyRaw
	if err := json.Unmarshal([]byte(block), &raw); err != nil {
		return typeSafePolicy{}, fmt.Errorf("agentcli: typesafe: ```typesafe fence is not valid JSON: %w", err)
	}
	questions, err := decodeTypeSafeQuestions(raw.Questions)
	if err != nil {
		return typeSafePolicy{}, err
	}
	if len(questions) == 0 {
		return typeSafePolicy{}, fmt.Errorf("agentcli: typesafe: ```typesafe fence must declare a non-empty questions map")
	}
	verdict := strings.TrimSpace(raw.VerdictQuestion)
	if verdict == "" {
		verdict = strings.TrimSpace(raw.Verdict)
	}
	if verdict == "" {
		verdict = "verdict"
	}
	onLow := strings.ToLower(strings.TrimSpace(raw.OnLowConfidence))
	switch onLow {
	case "", "reject":
		// Fail-closed only: never allow on_low_confidence=accept (that would
		// flip a vendor Choice reject into accept under a confidence floor).
	default:
		return typeSafePolicy{}, fmt.Errorf("agentcli: typesafe: on_low_confidence %q: want \"reject\" or omit (fail-open \"accept\" is refused)", raw.OnLowConfidence)
	}
	return typeSafePolicy{
		Questions:       questions,
		VerdictQuestion: verdict,
		MinConfidence:   raw.MinConfidence,
		OnLowConfidence: onLow,
	}, nil
}

func decodeTypeSafeQuestions(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("agentcli: typesafe: ```typesafe fence missing questions")
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err == nil {
		return asMap, nil
	}
	var asList []map[string]any
	if err := json.Unmarshal(raw, &asList); err != nil {
		return nil, fmt.Errorf("agentcli: typesafe: questions must be a map or an array of {id,…} objects: %w", err)
	}
	out := make(map[string]any, len(asList))
	for i, q := range asList {
		id, _ := q["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("agentcli: typesafe: questions[%d] missing id", i)
		}
		cp := make(map[string]any, len(q))
		for k, v := range q {
			if k == "id" {
				continue
			}
			cp[k] = v
		}
		out[id] = cp
	}
	return out, nil
}

// typeSafeFence extracts the first ```typesafe fenced block from a skill body.
func typeSafeFence(body string) string {
	lines := strings.Split(body, "\n")
	in := false
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(t, "```") {
				info := strings.TrimSpace(strings.TrimPrefix(t, "```"))
				if info == "typesafe" || strings.HasPrefix(info, "typesafe ") {
					in = true
				}
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			break
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// stripTypeSafeFence returns the skill body with the ```typesafe fence removed
// so the System One state does not re-send the machine policy block as prose.
func stripTypeSafeFence(body string) string {
	lines := strings.Split(body, "\n")
	var out []string
	in := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(t, "```") {
				info := strings.TrimSpace(strings.TrimPrefix(t, "```"))
				if info == "typesafe" || strings.HasPrefix(info, "typesafe ") {
					in = true
					continue
				}
			}
			out = append(out, ln)
			continue
		}
		if strings.HasPrefix(t, "```") {
			in = false
			continue
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

type typeSafeAPIResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]typeSafeAnswer `json:"answers"`
	Usage   json.RawMessage           `json:"usage"`
}

type typeSafeAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
	Noul          float64            `json:"noul"`
	Score         float64            `json:"score"`
}

type emittedDecision struct {
	Decision  string `json:"decision"`
	Notes     string `json:"notes"`
	Reasoning string `json:"reasoning"`
}

// emitTypeSafeDecision maps System One answers onto decision/notes bytes and
// appends a {"typesafe_raw":…} wrapper (no "decision" key) so parseDecision's
// last-object rule still picks the emitted verdict.
func emitTypeSafeDecision(raw []byte, policy typeSafePolicy) ([]byte, error) {
	var resp typeSafeAPIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("agentcli: typesafe: decode response: %w", err)
	}
	ans, ok := resp.Answers[policy.VerdictQuestion]
	if !ok {
		return nil, fmt.Errorf("agentcli: typesafe: response missing verdict answer %q", policy.VerdictQuestion)
	}
	if !strings.EqualFold(ans.Type, "choice") && ans.Choice == "" {
		return nil, fmt.Errorf("agentcli: typesafe: verdict answer %q is not a choice", policy.VerdictQuestion)
	}
	choice := strings.ToLower(strings.TrimSpace(ans.Choice))
	decision := ""
	switch choice {
	case "accept", "reject":
		decision = choice
	default:
		return nil, fmt.Errorf("agentcli: typesafe: verdict choice %q: want accept or reject", ans.Choice)
	}
	// Low-confidence policy is skill-authored and fail-closed only. Absent
	// min_confidence → no floor. on_low_confidence=accept is refused at parse
	// time so a vendor Choice reject can never become accept here.
	if policy.MinConfidence != nil && ans.Confidence < *policy.MinConfidence {
		decision = "reject"
	}
	notes := synthesiseTypeSafeNotes(decision, policy.VerdictQuestion, ans, resp.Answers, policy)
	decObj, err := json.Marshal(emittedDecision{
		Decision:  decision,
		Notes:     notes,
		Reasoning: notes,
	})
	if err != nil {
		return nil, fmt.Errorf("agentcli: typesafe: marshal decision: %w", err)
	}
	var rawObj any
	if err := json.Unmarshal(raw, &rawObj); err != nil {
		rawObj = json.RawMessage(raw)
	}
	wrap, err := json.Marshal(map[string]any{"typesafe_raw": rawObj})
	if err != nil {
		return nil, fmt.Errorf("agentcli: typesafe: marshal typesafe_raw: %w", err)
	}
	out := append(append(decObj, '\n'), wrap...)
	return out, nil
}

func synthesiseTypeSafeNotes(decision, verdictID string, verdict typeSafeAnswer, answers map[string]typeSafeAnswer, policy typeSafePolicy) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("typesafe verdict=%s choice=%s confidence=%.3f", decision, verdict.Choice, verdict.Confidence))
	if policy.MinConfidence != nil {
		parts = append(parts, fmt.Sprintf("min_confidence=%.3f on_low_confidence=%s", *policy.MinConfidence, policy.OnLowConfidence))
	}
	if len(verdict.Probabilities) > 0 {
		keys := make([]string, 0, len(verdict.Probabilities))
		for k := range verdict.Probabilities {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var probs []string
		for _, k := range keys {
			probs = append(probs, fmt.Sprintf("%s=%.3f", k, verdict.Probabilities[k]))
		}
		parts = append(parts, "probabilities{"+strings.Join(probs, ", ")+"}")
	}
	ids := make([]string, 0, len(answers))
	for id := range answers {
		if id == verdictID {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := answers[id]
		switch strings.ToLower(a.Type) {
		case "noul":
			parts = append(parts, fmt.Sprintf("noul %s=%.3f", id, a.Noul))
		case "choice":
			parts = append(parts, fmt.Sprintf("choice %s=%s conf=%.3f", id, a.Choice, a.Confidence))
		case "score":
			parts = append(parts, fmt.Sprintf("score %s=%.3f conf=%.3f", id, a.Score, a.Confidence))
		default:
			parts = append(parts, fmt.Sprintf("%s type=%s", id, a.Type))
		}
	}
	return strings.Join(parts, "; ")
}
