package agentcli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const sampleTypeSafeSkill = `# Plan typesafe review

The typesafe runner emits {"decision":"accept"|"reject","notes":"…"}.

` + "```typesafe\n" + `{
  "questions": {
    "verdict": {
      "type": "choice",
      "instructions": "Accept only when the attached plan covers every numbered AC.",
      "criteria": {
        "accept": "Every AC is planned with concrete files/evidence.",
        "reject": "An AC is missing, hand-waved, or contradicted."
      }
    },
    "ac_coverage": {
      "type": "noul",
      "instructions": "Does the plan cover every numbered acceptance criterion?"
    }
  },
  "verdict": "verdict",
  "min_confidence": 0.7,
  "on_low_confidence": "reject"
}
` + "```\n"

func TestNewTypeSafeRunner_RejectsBadCommand(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"", "in-loop", "http://insecure.example/v1", "not-a-url", "https://api.typesafe.ai/v1/systemone {payload}", "https://a.example https://b.example"} {
		if _, err := newTypeSafeRunner(cmd); err == nil {
			t.Errorf("newTypeSafeRunner(%q) should error", cmd)
		}
	}
	r, err := newTypeSafeRunner(DefaultTypeSafeEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if r.Command() != DefaultTypeSafeEndpoint {
		t.Errorf("Command() = %q", r.Command())
	}
	if r.Name() != "api.typesafe.ai" {
		t.Errorf("Name() = %q, want api.typesafe.ai", r.Name())
	}
}

func TestRunnerFromBinding_TypeSafe(t *testing.T) {
	t.Parallel()
	r, err := RunnerFromBinding(InterfaceTypeSafe, DefaultTypeSafeEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if r.Command() != DefaultTypeSafeEndpoint {
		t.Errorf("Command() = %q", r.Command())
	}
	// Omitting the typesafe binding / empty interface stays the command path.
	cmdR, err := RunnerFromBinding("", DefaultGrokCommand)
	if err != nil {
		t.Fatal(err)
	}
	if cmdR.Name() != CLIGrok {
		t.Errorf("empty interface should remain command path, got Name=%q", cmdR.Name())
	}
	_, err = OpenerFromBinding(InterfaceTypeSafe, DefaultTypeSafeEndpoint)
	if err != ErrNotLiveCapable {
		t.Fatalf("OpenerFromBinding(typesafe) = %v, want ErrNotLiveCapable", err)
	}
}

func TestTypeSafeRunner_HTTPShapeAndRedaction(t *testing.T) {
	const secret = "ts_test_secret_do_not_leak"
	var gotAuth, gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "jev-1.13.0",
			"answers": map[string]any{
				"verdict": map[string]any{
					"type":          "choice",
					"choice":        "accept",
					"probabilities": map[string]any{"accept": 0.9, "reject": 0.1},
					"confidence":    0.85,
				},
				"ac_coverage": map[string]any{"type": "noul", "noul": 0.91},
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 2},
		})
	}))
	defer srv.Close()

	// Rewrite endpoint to httptest while keeping https check out of newTypeSafeRunner
	// by constructing the runner directly for the test server (http scheme).
	r := typeSafeRunner{endpoint: srv.URL + "/v1/systemone", client: srv.Client()}
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: sampleTypeSafeSkill,
		Payload:      `{"story":{"id":"sty_x"},"docs":[{"name":"plan"}]}`,
		Model:        "jev-1.13.0",
		Env:          map[string]string{TypeSafeAPIKeyEnv: secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/v1/systemone") {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer "+secret {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotBody["model"] != "jev-1.13.0" {
		t.Errorf("body model = %v", gotBody["model"])
	}
	qs, _ := gotBody["questions"].(map[string]any)
	if _, ok := qs["verdict"]; !ok {
		t.Errorf("questions missing verdict: %v", gotBody["questions"])
	}
	cmd := r.Command()
	if strings.Contains(cmd, secret) || strings.Contains(cmd, TypeSafeAPIKeyEnv+"=") {
		t.Errorf("Command() leaked secret material: %q", cmd)
	}
	if strings.Contains(string(out), secret) {
		t.Errorf("stdout leaked API key")
	}

	// parseDecision-compatible: decision object first, typesafe_raw second (no decision key).
	parts := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(parts) < 2 {
		t.Fatalf("want decision\\ntypesafe_raw, got %s", out)
	}
	var first emittedDecision
	if err := json.Unmarshal([]byte(parts[0]), &first); err != nil || first.Decision != "accept" {
		t.Fatalf("first object = %s err=%v", parts[0], err)
	}
	var wrap map[string]any
	if err := json.Unmarshal([]byte(parts[1]), &wrap); err != nil {
		t.Fatal(err)
	}
	if _, ok := wrap["decision"]; ok {
		t.Fatal("typesafe_raw wrapper must not carry a decision key (parseDecision last-object rule)")
	}
	if _, ok := wrap["typesafe_raw"]; !ok {
		t.Fatalf("want typesafe_raw wrapper, got %v", wrap)
	}
}

func TestTypeSafeRunner_Non2xxAndMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	r := typeSafeRunner{endpoint: srv.URL, client: srv.Client()}
	_, err := r.Run(context.Background(), Request{
		SystemPrompt: sampleTypeSafeSkill,
		Model:        "jev-1.13.0",
		Env:          map[string]string{TypeSafeAPIKeyEnv: "k"},
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want HTTP 401 error, got %v", err)
	}
	if strings.Contains(err.Error(), "k") && strings.Count(err.Error(), "k") > 1 {
		// single-letter key may appear in "401"; ensure Bearer token form is absent
	}
	if strings.Contains(err.Error(), "Bearer") {
		t.Errorf("error leaked Bearer material: %v", err)
	}

	_, err = r.Run(context.Background(), Request{
		SystemPrompt: sampleTypeSafeSkill,
		Model:        "jev-1.13.0",
	})
	if err == nil || !strings.Contains(err.Error(), TypeSafeAPIKeyEnv) {
		t.Fatalf("missing key must name %s, got %v", TypeSafeAPIKeyEnv, err)
	}
	_, err = r.Run(context.Background(), Request{
		SystemPrompt: sampleTypeSafeSkill,
		Env:          map[string]string{TypeSafeAPIKeyEnv: "k"},
	})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("empty model must error, got %v", err)
	}
	_, err = r.Run(context.Background(), Request{
		SystemPrompt: "# no fence\n",
		Model:        "jev-1.13.0",
		Env:          map[string]string{TypeSafeAPIKeyEnv: "k"},
	})
	if err == nil || !strings.Contains(err.Error(), "typesafe") {
		t.Fatalf("absent fence must error naming typesafe, got %v", err)
	}
}

func TestEmitTypeSafeDecision_LowConfidencePolicy(t *testing.T) {
	rawAccept := []byte(`{
	  "model":"jev-1.13.0",
	  "answers":{
	    "verdict":{"type":"choice","choice":"accept","probabilities":{"accept":0.55,"reject":0.45},"confidence":0.4},
	    "ac_coverage":{"type":"noul","noul":0.5}
	  }
	}`)
	min := 0.7
	withPolicy := typeSafePolicy{
		Questions:       map[string]any{"verdict": map[string]any{"type": "choice"}},
		VerdictQuestion: "verdict",
		MinConfidence:   &min,
		OnLowConfidence: "reject",
	}
	out, err := emitTypeSafeDecision(rawAccept, withPolicy)
	if err != nil {
		t.Fatal(err)
	}
	var dec emittedDecision
	if err := json.Unmarshal([]byte(strings.Split(string(out), "\n")[0]), &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Decision != "reject" {
		t.Fatalf("low confidence with on_low_confidence=reject must flip to reject, got %q notes=%q", dec.Decision, dec.Notes)
	}

	noPolicy := typeSafePolicy{
		Questions:       map[string]any{"verdict": map[string]any{"type": "choice"}},
		VerdictQuestion: "verdict",
	}
	out2, err := emitTypeSafeDecision(rawAccept, noPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.Split(string(out2), "\n")[0]), &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Decision != "accept" {
		t.Fatalf("absent min_confidence must keep choice accept, got %q", dec.Decision)
	}
	if !strings.Contains(dec.Notes, "noul ac_coverage=") {
		t.Errorf("notes should synthesise noul atoms, got %q", dec.Notes)
	}

	// Vendor Choice reject must stay reject even if a caller hand-builds the
	// old fail-open on_low_confidence=accept policy (parse refuses it).
	rawReject := []byte(`{
	  "model":"jev-1.13.0",
	  "answers":{
	    "verdict":{"type":"choice","choice":"reject","probabilities":{"accept":0.2,"reject":0.8},"confidence":0.3}
	  }
	}`)
	failOpen := typeSafePolicy{
		Questions:       map[string]any{"verdict": map[string]any{"type": "choice"}},
		VerdictQuestion: "verdict",
		MinConfidence:   &min,
		OnLowConfidence: "accept",
	}
	out3, err := emitTypeSafeDecision(rawReject, failOpen)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.Split(string(out3), "\n")[0]), &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Decision != "reject" {
		t.Fatalf("vendor reject must not become accept under fail-open policy, got %q", dec.Decision)
	}
}

func TestParseTypeSafePolicy_RefusesFailOpenAccept(t *testing.T) {
	body := "```typesafe\n" + `{
  "questions": {"verdict": {"type": "choice", "instructions": "x", "criteria": {"accept": null, "reject": null}}},
  "min_confidence": 0.7,
  "on_low_confidence": "accept"
}` + "\n```\n"
	_, err := parseTypeSafePolicy(body)
	if err == nil || !strings.Contains(err.Error(), "on_low_confidence") {
		t.Fatalf("want on_low_confidence=accept refused, got %v", err)
	}
}

func TestTypeSafeAPIKey_EnvFallbackAndUnresolved(t *testing.T) {
	t.Setenv(TypeSafeAPIKeyEnv, "from-process")
	if got := typeSafeAPIKey(nil); got != "from-process" {
		t.Errorf("process fallback = %q", got)
	}
	if got := typeSafeAPIKey(map[string]string{TypeSafeAPIKeyEnv: "from-binding"}); got != "from-binding" {
		t.Errorf("binding wins = %q", got)
	}
	if got := typeSafeAPIKey(map[string]string{TypeSafeAPIKeyEnv: "${TYPESAFE_API_KEY}"}); got != "from-process" {
		t.Errorf("unresolved ref should fall through to process, got %q", got)
	}
	_ = os.Unsetenv(TypeSafeAPIKeyEnv)
	if got := typeSafeAPIKey(map[string]string{TypeSafeAPIKeyEnv: "${TYPESAFE_API_KEY}"}); got != "" {
		t.Errorf("unresolved with empty process = %q", got)
	}
}

func TestDecodeTypeSafeQuestions_ArrayForm(t *testing.T) {
	raw := json.RawMessage(`[{"id":"verdict","type":"choice","instructions":"x","criteria":{"accept":null,"reject":null}}]`)
	qs, err := decodeTypeSafeQuestions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := qs["verdict"]; !ok {
		t.Fatalf("want verdict key, got %v", qs)
	}
	v := qs["verdict"].(map[string]any)
	if _, ok := v["id"]; ok {
		t.Error("id must be lifted out of the question object")
	}
}
