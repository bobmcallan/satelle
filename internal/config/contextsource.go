package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// ContextSourceKindMCP is the one kind of read-only context source: a Model
// Context Protocol server. It names a protocol, not a vendor — which server a
// repo declares is its configuration.
const ContextSourceKindMCP = "mcp"

// ContextSource is one [context.<name>] table of agents.toml (sty_12499ecc): a
// read-only MCP server the workflow's steps may offer the agents satelle
// dispatches, with the allow-list of the tools those agents may call on it. A
// step opts in by name (step.toml `context = ["<name>"]`); nothing here says
// which step, and nothing in the binary names a source.
//
// Every string value is ${VAR}-resolved at wiring time (ResolveAgentEnvs), so a
// header secret lives in [vars] and never in the repo.
type ContextSource struct {
	// Kind is ContextSourceKindMCP.
	Kind string `toml:"kind"`
	// Type is "http" (url) or "stdio" (command, args). Absent, it follows from
	// which of url/command is set.
	Type    string            `toml:"type"`
	URL     string            `toml:"url"`
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Headers map[string]string `toml:"headers"`
	// Tools is the allow-list of the server's tool names an agent may call. It is
	// required and non-empty: a source that admits no tool offers nothing.
	Tools []string `toml:"tools"`
	// ReadOnly must be true. It is the operator's attestation that every tool in
	// Tools only reads; satelle holds the agent to the allow-list but cannot see
	// into the server.
	ReadOnly bool `toml:"read_only"`
}

// contextSourceName is a name that is safe as an MCP server name and as the
// middle segment of a harness tool name.
var contextSourceName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ResolvedType is the source's transport: its declared type, else http when it
// has a url and stdio when it has a command.
func (c ContextSource) ResolvedType() string {
	if t := strings.ToLower(strings.TrimSpace(c.Type)); t != "" {
		return t
	}
	switch {
	case strings.TrimSpace(c.URL) != "" && strings.TrimSpace(c.Command) == "":
		return agentcli.ContextHTTP
	case strings.TrimSpace(c.Command) != "" && strings.TrimSpace(c.URL) == "":
		return agentcli.ContextStdio
	}
	return ""
}

// Validate refuses a source an agent could not be held to: wrong kind, not
// attested read-only, no allow-list, or a transport that is not exactly one of
// url/command.
func (c ContextSource) Validate(name string) error {
	at := fmt.Sprintf("[context.%s]", name)
	if !contextSourceName.MatchString(name) {
		return fmt.Errorf("%s: the name must be letters, digits, '_' or '-' (it is the MCP server name)", at)
	}
	if k := strings.ToLower(strings.TrimSpace(c.Kind)); k != ContextSourceKindMCP {
		return fmt.Errorf("%s: kind = %q — the only kind is %q", at, c.Kind, ContextSourceKindMCP)
	}
	if !c.ReadOnly {
		return fmt.Errorf("%s: read_only = true is required — it attests that every allow-listed tool only reads", at)
	}
	if len(c.Tools) == 0 {
		return fmt.Errorf("%s: tools is required and must list at least one tool — a source that admits no tool offers nothing", at)
	}
	for _, t := range c.Tools {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("%s: tools carries a blank name", at)
		}
	}
	hasURL, hasCmd := strings.TrimSpace(c.URL) != "", strings.TrimSpace(c.Command) != ""
	if hasURL == hasCmd {
		return fmt.Errorf("%s: declare exactly one of url (http) or command (stdio)", at)
	}
	switch c.ResolvedType() {
	case agentcli.ContextHTTP:
		if !hasURL || hasCmd || len(c.Args) > 0 {
			return fmt.Errorf("%s: type http takes url and headers only", at)
		}
	case agentcli.ContextStdio:
		if !hasCmd || hasURL || len(c.Headers) > 0 {
			return fmt.Errorf("%s: type stdio takes command and args only", at)
		}
	default:
		return fmt.Errorf("%s: type = %q — use %q or %q", at, c.Type, agentcli.ContextHTTP, agentcli.ContextStdio)
	}
	return nil
}

// Server is the source as the adapter layer receives it, named name. The caller
// resolves ${VAR} first (ResolveAgentEnvs).
func (c ContextSource) Server(name string) agentcli.ContextServer {
	return agentcli.ContextServer{
		Name: name, Type: c.ResolvedType(), URL: strings.TrimSpace(c.URL), Command: strings.TrimSpace(c.Command),
		Args: append([]string(nil), c.Args...), Headers: copyStrings(c.Headers),
		Tools: append([]string(nil), c.Tools...),
	}
}

func copyStrings(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// validateContext validates every declared source.
func (a AgentsConfig) validateContext() error {
	for _, n := range sortedContextNames(a.Context) {
		if err := a.Context[n].Validate(n); err != nil {
			return err
		}
	}
	return nil
}

func sortedContextNames(m map[string]ContextSource) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ContextSource returns the declared source named name.
func (a AgentsConfig) ContextSource(name string) (ContextSource, bool) {
	c, ok := a.Context[name]
	return c, ok
}

// layerContext folds the [context.<name>] layers, lowest first: a source named in
// a higher layer replaces the lower one whole, so no field of a read-only
// declaration is inherited piecemeal from another layer.
func layerContext(layers ...map[string]ContextSource) map[string]ContextSource {
	var out map[string]ContextSource
	for _, l := range layers {
		for n, c := range l {
			if out == nil {
				out = map[string]ContextSource{}
			}
			out[n] = c
		}
	}
	return out
}

// resolveContext returns the sources with every string value ${NAME}-expanded
// against vars. The error names the source and field, never the value.
func resolveContext(src map[string]ContextSource, vars map[string]string) (map[string]ContextSource, error) {
	if len(src) == 0 {
		return src, nil
	}
	out := make(map[string]ContextSource, len(src))
	for _, n := range sortedContextNames(src) {
		c := src[n]
		var err error
		expand := func(field, v string) string {
			if err != nil {
				return v
			}
			var r string
			if r, err = ExpandVars(v, vars); err != nil {
				err = fmt.Errorf("agents.toml [context.%s] %s: %w", n, field, err)
			}
			return r
		}
		c.URL = expand("url", c.URL)
		c.Command = expand("command", c.Command)
		if len(c.Args) > 0 {
			args := make([]string, len(c.Args))
			for i, a := range c.Args {
				args[i] = expand("args", a)
			}
			c.Args = args
		}
		if len(c.Headers) > 0 {
			hs := make(map[string]string, len(c.Headers))
			for _, k := range sortedStringKeys(c.Headers) {
				hs[k] = expand("header "+k, c.Headers[k])
			}
			c.Headers = hs
		}
		if err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, nil
}

// contextTable renders the sources for EncodeAgents.
func contextTable(src map[string]ContextSource) map[string]any {
	out := map[string]any{}
	for n, c := range src {
		t := map[string]any{"kind": c.Kind, "read_only": c.ReadOnly, "tools": c.Tools}
		if s := strings.TrimSpace(c.Type); s != "" {
			t["type"] = s
		}
		if s := strings.TrimSpace(c.URL); s != "" {
			t["url"] = s
		}
		if s := strings.TrimSpace(c.Command); s != "" {
			t["command"] = s
		}
		if len(c.Args) > 0 {
			t["args"] = c.Args
		}
		if len(c.Headers) > 0 {
			t["headers"] = c.Headers
		}
		out[n] = t
	}
	return out
}
