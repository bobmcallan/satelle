package agentcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Read-only context sources (sty_12499ecc). A dispatched agent may be handed MCP
// servers the repo declared for its step — a requirement tracker, a code mirror —
// so it judges or performs with the context a story came from. This file owns
// how each adapter and transport carries them and how the tools they expose are
// admitted: the MCP tool-name format is an adapter detail and lives here and
// nowhere else ([[satelle-agent-agnostic]] §1). The sources themselves, their
// URLs and their tool allow-lists are configuration; nothing here names one.

// The transports of a context-source row that are not a runner's interface.
const (
	// ContextTransportCloud is a provider-hosted session. Satelle cannot hand it a
	// local MCP server, and a resolved header secret must never leave the machine.
	ContextTransportCloud = "cloud"
)

// The MCP server types a context source may be.
const (
	ContextHTTP  = "http"
	ContextStdio = "stdio"
)

// ContextServer is one resolved, read-only context source offered to a dispatch:
// an MCP server and the allow-list of the tools the agent may call on it. Every
// value is already ${VAR}-resolved by the caller. Headers MAY carry a secret, so
// like Env it is never put in Command() evidence, logs or error strings.
type ContextServer struct {
	// Name is the declared source name; it is also the MCP server name.
	Name string
	// Type is ContextHTTP or ContextStdio.
	Type string
	// URL is the http endpoint (Type http).
	URL string
	// Command and Args spawn the server (Type stdio).
	Command string
	Args    []string
	// Headers are sent with every http request.
	Headers map[string]string
	// Tools is the allow-list: the server's tool names the agent may call, and no
	// other tool of that server is admitted.
	Tools []string
}

// contextTransports is the support table: the adapters and transports that can
// carry a context source. Every other row of the matrix fails the dispatch with
// ContextSupport's error before anything is spawned. ACP additionally needs the
// peer to advertise the matching MCP capability, which is judged on the wire.
var contextTransports = map[string]map[string]bool{
	HarnessClaude: {InterfaceCommand: true, InterfaceStream: true},
	HarnessGrok:   {InterfaceACP: true},
	HarnessCursor: {InterfaceACP: true},
	HarnessPi:     {InterfaceACP: true},
}

// ContextSupport reports whether an adapter's transport can carry context
// sources: nil when it can, else an error naming the adapter and the transport.
// transport is InterfaceCommand, InterfaceStream, InterfaceACP or
// ContextTransportCloud; an unrecognised harness is HarnessUnknown and is never
// supported ([[satelle-agent-agnostic]] §3).
func ContextSupport(adapter, transport string) error {
	if contextTransports[adapter][transport] {
		return nil
	}
	return fmt.Errorf("agentcli: context sources not supported by %s/%s", adapter, transport)
}

// requireContext is ContextSupport for a request: it only judges a request that
// carries sources.
func requireContext(adapter, transport string, req Request) error {
	if len(req.ContextServers) == 0 {
		return nil
	}
	return ContextSupport(adapter, transport)
}

// contextToolName is the harness-facing name of one tool of one context server,
// per adapter. claude names an MCP tool mcp__<server>__<tool>; the ACP peers
// satelle drives are admitted by that same form, and an ACP call that names its
// tool any other way is unmapped and stays denied. A peer that names them
// differently diverges here, in its own case, and nowhere else.
func contextToolName(adapter, server, tool string) string {
	_ = adapter
	return "mcp__" + server + "__" + tool
}

// ContextToolNames lists, per adapter, the tool names the declared sources admit,
// sorted and without duplicates.
func ContextToolNames(adapter string, servers []ContextServer) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range servers {
		for _, t := range s.Tools {
			n := contextToolName(adapter, s.Name, strings.TrimSpace(t))
			if strings.TrimSpace(t) != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ContextSourceNames lists the declared source names, in declared order.
func ContextSourceNames(servers []ContextServer) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Name)
	}
	return out
}

// withContext returns the admission set extended with the exact tools the
// declared sources allow. It admits those names alone: another tool of the same
// server, or of an undeclared server, is still the MCP class the grant denies.
func (a grantAdmits) withContext(adapter string, servers []ContextServer) grantAdmits {
	names := ContextToolNames(adapter, servers)
	if len(names) == 0 {
		return a
	}
	out := grantAdmits{classes: a.classes, names: a.names, declared: map[string]bool{}}
	for k := range a.declared {
		out.declared[k] = true
	}
	for _, n := range names {
		out.declared[strings.ToLower(n)] = true
	}
	return out
}

// ReviewerPermissionPolicyWithContext is ReviewerPermissionPolicy for a reviewer
// offered context sources: the grant's policy, plus the exact tools its sources
// allow-list. adapter names the harness whose tool-name form the live transport
// reports.
func ReviewerPermissionPolicyWithContext(grant, adapter string, servers []ContextServer) PermissionPolicy {
	return reviewerPolicyFrom(admitsFromGrant(grant, false).withContext(adapter, servers))
}

// claudeMCPConfig renders the sources as claude's --mcp-config JSON.
func claudeMCPConfig(servers []ContextServer) (string, error) {
	cfg := map[string]any{}
	for _, s := range servers {
		switch s.Type {
		case ContextHTTP:
			e := map[string]any{"type": ContextHTTP, "url": s.URL}
			if len(s.Headers) > 0 {
				e["headers"] = s.Headers
			}
			cfg[s.Name] = e
		case ContextStdio:
			e := map[string]any{"type": ContextStdio, "command": s.Command}
			if len(s.Args) > 0 {
				e["args"] = s.Args
			}
			cfg[s.Name] = e
		default:
			return "", fmt.Errorf("agentcli: context source %q: unknown type %q", s.Name, s.Type)
		}
	}
	b, err := json.Marshal(map[string]any{"mcpServers": cfg})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// contextRender prepares a claude spawn that carries context sources: the
// request whose AllowedTools also pre-approves the declared tools (used for the
// {tools} placeholder only — the --tools trim keeps the original grant, since it
// names built-in tools alone) and the argv flag that hands the server config
// over. req is returned unchanged when it carries no sources.
func contextRender(adapter string, req Request) (render Request, flag string, err error) {
	if len(req.ContextServers) == 0 {
		return req, "", nil
	}
	cfg, err := claudeMCPConfig(req.ContextServers)
	if err != nil {
		return req, "", err
	}
	render = req
	names := ContextToolNames(adapter, req.ContextServers)
	if g := strings.TrimSpace(req.AllowedTools); g != "" {
		names = append([]string{g}, names...)
	}
	render.AllowedTools = strings.Join(names, ",")
	// One token (--flag=value): the option is variadic in claude, so a following
	// positional would otherwise be read as another config.
	return render, "--mcp-config=" + cfg, nil
}

// acpMCPCaps is what an ACP peer advertised under agentCapabilities.mcpCapabilities
// on initialize. stdio servers are part of the base protocol and need no flag.
type acpMCPCaps struct {
	HTTP bool `json:"http"`
}

// acpMCPServers renders the sources as session/new's mcpServers. A source of a
// type the peer did not advertise fails with an error naming the adapter and
// transport; header values never appear in it.
func acpMCPServers(adapter string, servers []ContextServer, caps acpMCPCaps) ([]any, error) {
	out := make([]any, 0, len(servers))
	for _, s := range servers {
		switch s.Type {
		case ContextHTTP:
			if !caps.HTTP {
				return nil, fmt.Errorf("agentcli: context sources not supported by %s/%s: the peer does not advertise http MCP servers (source %q)", adapter, InterfaceACP, s.Name)
			}
			out = append(out, map[string]any{"type": ContextHTTP, "name": s.Name, "url": s.URL, "headers": acpNameValues(s.Headers)})
		case ContextStdio:
			args := s.Args
			if args == nil {
				args = []string{}
			}
			out = append(out, map[string]any{"name": s.Name, "command": s.Command, "args": args, "env": []any{}})
		default:
			return nil, fmt.Errorf("agentcli: context source %q: unknown type %q", s.Name, s.Type)
		}
	}
	return out, nil
}

// acpNameValues is a header map in ACP's [{name,value}] form, sorted by name.
func acpNameValues(m map[string]string) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{"name": k, "value": m[k]})
	}
	return out
}
