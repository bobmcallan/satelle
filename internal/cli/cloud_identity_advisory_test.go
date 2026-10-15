package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// cloudAdvisoryRepo is a session-context repo whose machine-local agents layer
// selects the ops profile (when named) and whose catalog carries its identity.
func cloudAdvisoryRepo(t *testing.T, named bool) string {
	t.Helper()
	repo := sessionContextRepo(t)
	wf := filepath.Join(repo, config.DefaultDataDir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, config.AgentsConfigName), []byte("[executor]\ncommand = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if named {
		if err := os.WriteFile(filepath.Join(wf, config.LocalAgentsName), []byte(localOps), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCatalog(t, launchCatalog)
	return repo
}

// hookContextText runs the session-context hook for harness and returns every
// string the harness's own output shape carries (claude: additionalContext and
// systemMessage; grok: additionalContext; cursor: additional_context).
func hookContextText(t *testing.T, harness, session string) string {
	t.Helper()
	var out, errb bytes.Buffer
	raw := []byte(`{"session_id":"` + session + `"}`)
	if err := runHookContext(&out, &errb, harness, raw); err != nil {
		t.Fatalf("the hook must exit 0, got %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		return ""
	}
	var doc any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			b.WriteString(x + "\n")
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
	return b.String()
}

func hookContextDoc(t *testing.T, harness, session string) sessionDoc {
	t.Helper()
	var out, errb bytes.Buffer
	raw := []byte(`{"session_id":"` + session + `"}`)
	if err := runHookContext(&out, &errb, harness, raw); err != nil {
		t.Fatalf("the hook must exit 0, got %v", err)
	}
	var doc sessionDoc
	if strings.TrimSpace(out.String()) == "" {
		return doc
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	return doc
}

// AC4: a repo whose configuration names an identity warns a claude or grok
// session that skipped the launcher — in model context and in the user notice —
// and says nothing to one that went through it. Nothing runs and nothing is
// fetched: PATH holds no provider CLI and the proxy is unroutable.
func TestHookContext_LaunchStepsWarning(t *testing.T) {
	for _, harness := range []string{"claude", "grok"} {
		t.Run(harness+"/not launched", func(t *testing.T) {
			cloudAdvisoryRepo(t, true)
			t.Setenv(config.LauncherEnv, "")
			t.Setenv("PATH", t.TempDir())
			t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
			t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

			text := hookContextText(t, harness, "cloud-warn-"+harness)
			want := "was not started through `satelle-" + harness + "`"
			if !strings.Contains(text, want) || !strings.Contains(text, `profile "ops"`) {
				t.Errorf("missing the not-launched warning (%q):\n%s", want, text)
			}
			if strings.Contains(text, sentToken) {
				t.Errorf("the warning carries the secret")
			}
			if harness == "claude" {
				// Claude delivers the line to the model AND, as systemMessage, to the user.
				doc := hookContextDoc(t, harness, "cloud-warn-claude-2")
				for where, got := range map[string]string{"additionalContext": doc.HookSpecificOutput.AdditionalContext, "systemMessage": doc.SystemMessage} {
					if !strings.Contains(got, want) {
						t.Errorf("%s lacks the warning:\n%s", where, got)
					}
				}
			}
		})
		t.Run(harness+"/launched", func(t *testing.T) {
			cloudAdvisoryRepo(t, true)
			t.Setenv(config.LauncherEnv, harness)
			if text := hookContextText(t, harness, "cloud-ok-"+harness); strings.Contains(text, "launch steps") {
				t.Errorf("a launched session was warned:\n%s", text)
			}
		})
	}

	t.Run("no steps declared", func(t *testing.T) {
		cloudAdvisoryRepo(t, false)
		t.Setenv(config.LauncherEnv, "")
		if text := hookContextText(t, "claude", "cloud-none"); strings.Contains(text, "launch steps") {
			t.Errorf("a repo with no launch steps was warned:\n%s", text)
		}
	})

	// AC5: pi and cursor have no launcher, so they read "unavailable" rather than
	// a warning about a launcher that does not exist.
	for _, harness := range []string{"pi", "cursor"} {
		t.Run(harness+"/unavailable", func(t *testing.T) {
			cloudAdvisoryRepo(t, true)
			t.Setenv(config.LauncherEnv, "")
			text := hookContextText(t, harness, "cloud-unavail-"+harness)
			want := "launch steps: unavailable (" + harness + " has no satelle launcher)"
			if !strings.Contains(text, want) {
				t.Errorf("missing %q:\n%s", want, text)
			}
			if strings.Contains(text, "was not started through") {
				t.Error("pi/cursor were told to use a launcher that does not exist")
			}
		})
	}
}

// The decision is a pure function of the profile, the harness and the
// environment it is handed.
func TestLaunchLine(t *testing.T) {
	cases := []struct {
		name, harness string
		env           []string
		want          string // substring; "" means no line
	}{
		{"claude, no marker", "claude", nil, "not started through `satelle-claude`"},
		{"grok, no marker", "grok", []string{"PATH=/bin"}, "not started through `satelle-grok`"},
		{"claude, empty marker", "claude", []string{config.LauncherEnv + "="}, "not started through"},
		{"claude, launched", "claude", []string{config.LauncherEnv + "=claude"}, ""},
		{"grok, launched", "grok", []string{config.LauncherEnv + "=grok"}, ""},
		{"pi", "pi", nil, "launch steps: unavailable (pi has no satelle launcher)"},
		{"cursor", "cursor", []string{config.LauncherEnv + "=cursor"}, "launch steps: unavailable (cursor has no satelle launcher)"},
		{"harness from env", "", []string{"CLAUDECODE=1"}, "not started through `satelle-claude`"},
		{"unknown harness", "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := launchLine("ops", c.harness, c.env)
			if c.want == "" {
				if got != "" {
					t.Fatalf("line = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, c.want) || !strings.Contains(got, `"ops"`) {
				t.Errorf("line = %q, want it to contain %q and the profile", got, c.want)
			}
		})
	}
}
