package config

import (
	"strings"
	"testing"
)

// The read-only context source declaration (sty_12499ecc AC1). The neutral name
// "src" stands for whatever a repo declares; no vendor appears in this file.

const goodContextToml = `
[context.src]
kind = "mcp"
url = "https://ctx.example.test/mcp"
read_only = true
tools = ["get_issue", "search_issues"]

[context.src.headers]
Authorization = "Bearer ${CTX_TOKEN}"

[coder]
command = "claude"
role = "agent"
`

func TestContextSource_LoadsAndIsNotABinding(t *testing.T) {
	ac, err := loadAgentsBody(goodContextToml)
	if err != nil {
		t.Fatal(err)
	}
	src, ok := ac.ContextSource("src")
	if !ok {
		t.Fatalf("context.src not loaded: %+v", ac.Context)
	}
	if src.ResolvedType() != "http" || len(src.Tools) != 2 || !src.ReadOnly {
		t.Errorf("src = %+v", src)
	}
	// Neither the reserved `context` section nor a [context.<name>] table is read as a binding.
	for _, n := range []string{"context", "src"} {
		if _, bound := ac.Agents[n]; bound {
			t.Errorf("%q was read as an agent binding", n)
		}
	}
	if _, bound := ac.Agents["coder"]; !bound {
		t.Error("the real binding was lost")
	}
}

func TestContextSource_RefusesEachBrokenRule(t *testing.T) {
	cases := []struct{ name, toml, want string }{
		{"not read-only", `[context.src]
kind = "mcp"
url = "https://x.test"
tools = ["a"]`, "read_only = true is required"},
		{"read_only false", `[context.src]
kind = "mcp"
url = "https://x.test"
read_only = false
tools = ["a"]`, "read_only = true is required"},
		{"no tools", `[context.src]
kind = "mcp"
url = "https://x.test"
read_only = true`, "tools is required"},
		{"empty tools", `[context.src]
kind = "mcp"
url = "https://x.test"
read_only = true
tools = []`, "tools is required"},
		{"blank tool", `[context.src]
kind = "mcp"
url = "https://x.test"
read_only = true
tools = ["a", " "]`, "blank name"},
		{"wrong kind", `[context.src]
kind = "rest"
url = "https://x.test"
read_only = true
tools = ["a"]`, "the only kind is"},
		{"no kind", `[context.src]
url = "https://x.test"
read_only = true
tools = ["a"]`, "the only kind is"},
		{"neither url nor command", `[context.src]
kind = "mcp"
read_only = true
tools = ["a"]`, "exactly one of url"},
		{"both url and command", `[context.src]
kind = "mcp"
url = "https://x.test"
command = "srv"
read_only = true
tools = ["a"]`, "exactly one of url"},
		{"http with args", `[context.src]
kind = "mcp"
url = "https://x.test"
args = ["--x"]
read_only = true
tools = ["a"]`, "type http takes url"},
		{"stdio with headers", `[context.src]
kind = "mcp"
command = "srv"
read_only = true
tools = ["a"]
[context.src.headers]
A = "b"`, "type stdio takes command"},
		{"unknown type", `[context.src]
kind = "mcp"
type = "sse"
url = "https://x.test"
read_only = true
tools = ["a"]`, `type = "sse"`},
		{"bad name", `[context."a b"]
kind = "mcp"
url = "https://x.test"
read_only = true
tools = ["a"]`, "the name must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadAgentsBody(tc.toml)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestContextSource_VarsResolveAndNeverAppearInErrors(t *testing.T) {
	ac, err := loadAgentsBody(goodContextToml)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveAgentEnvs(ac, map[string]string{"CTX_TOKEN": "tok-123"})
	if err != nil {
		t.Fatal(err)
	}
	if h := got.Context["src"].Headers["Authorization"]; h != "Bearer tok-123" {
		t.Errorf("header = %q", h)
	}
	if ac.Context["src"].Headers["Authorization"] != "Bearer ${CTX_TOKEN}" {
		t.Error("the unresolved layer was mutated")
	}
	_, err = ResolveAgentEnvs(ac, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "[context.src] header Authorization") || !strings.Contains(err.Error(), "CTX_TOKEN") {
		t.Errorf("missing var err = %v, want the source, header and var named", err)
	}
	srv := got.Context["src"].Server("src")
	if srv.Name != "src" || srv.Type != "http" || srv.Headers["Authorization"] != "Bearer tok-123" || len(srv.Tools) != 2 {
		t.Errorf("server = %+v", srv)
	}
}

func TestContextSource_LayersReplaceWholeByName(t *testing.T) {
	repo, err := loadAgentsBody(goodContextToml)
	if err != nil {
		t.Fatal(err)
	}
	local, err := loadAgentsBody(`[context.src]
kind = "mcp"
url = "https://local.example.test/mcp"
read_only = true
tools = ["only_this"]
`)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: repo, Local: local}, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	src := out.Context["src"]
	if src.URL != "https://local.example.test/mcp" || len(src.Tools) != 1 || len(src.Headers) != 0 {
		t.Errorf("a higher layer must replace the source whole, got %+v", src)
	}
	if got := (AgentLayers{Repo: repo, Local: local}).Authored().Context["src"]; got.URL != src.URL {
		t.Errorf("Authored() = %+v", got)
	}
}

func TestContextSource_EncodeRoundTrips(t *testing.T) {
	ac, err := loadAgentsBody(goodContextToml)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeAgents(ac)
	if err != nil {
		t.Fatal(err)
	}
	back, err := loadAgentsBody(string(b))
	if err != nil {
		t.Fatalf("re-load: %v\n%s", err, b)
	}
	got, want := back.Context["src"], ac.Context["src"]
	if got.URL != want.URL || got.Headers["Authorization"] != want.Headers["Authorization"] || len(got.Tools) != len(want.Tools) {
		t.Errorf("round trip lost the source: %+v vs %+v\n%s", got, want, b)
	}
}
