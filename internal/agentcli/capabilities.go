package agentcli

import (
	"fmt"
	"strings"
)

// Per-adapter capability table (sty_52a8cb4a, epic:agent-parity). One row per
// adapter satelle drives, one column per thing a mechanism reads off agent
// output or session. A capability is either available or carries an
// adapter-named reason it is not — never a silent zero. `satelle help
// agent-dispatch` embeds the table rendered by CapabilityTableMarkdown, and
// capabilities_test.go checks every cell against the code that produces it, so
// the help, this table and the mappers cannot drift apart.

// Capability is one cell: available, or unavailable with the reason.
type Capability struct {
	Available bool
	// Reason is empty when Available and non-empty (and names the adapter's
	// behaviour) when not.
	Reason string
}

func yes() Capability { return Capability{Available: true} }

func no(reason string) Capability { return Capability{Reason: reason} }

// Cell is the help-table spelling: "yes" or "unavailable: <reason>".
func (c Capability) Cell() string {
	if c.Available {
		return "yes"
	}
	return "unavailable: " + c.Reason
}

// AdapterCapabilities is one adapter's row.
type AdapterCapabilities struct {
	// Adapter is "<provider> <transport>", e.g. "claude stream".
	Adapter string
	// Usage: token counts read off the adapter's output.
	Usage Capability
	// CacheSplit: fresh / cache-read / cache-write reported separately.
	CacheSplit Capability
	// ResolvedModel: the model id the run actually used.
	ResolvedModel Capability
	// ModelInheritance: a session model of this provider can be inherited into
	// a dispatch of this adapter (config.SelectModel's in-loop tier).
	ModelInheritance Capability
	// LiveSession: the adapter can be opened as a live session
	// (OpenerFromBinding).
	LiveSession Capability
}

// CapabilityTable returns the table in the order help prints it.
func CapabilityTable() []AdapterCapabilities {
	const notLive = "interface=command is one-shot only"
	const noGrokHookModel = "grok's hook payload carries no model, so the in-loop tier is unknown"
	return []AdapterCapabilities{
		{
			Adapter: "claude command", Usage: yes(), CacheSplit: yes(), ResolvedModel: yes(),
			ModelInheritance: yes(), LiveSession: no(notLive),
		},
		{
			Adapter: "claude stream", Usage: yes(), CacheSplit: yes(), ResolvedModel: yes(),
			ModelInheritance: yes(), LiveSession: yes(),
		},
		{
			Adapter: "grok command", Usage: yes(), CacheSplit: yes(), ResolvedModel: yes(),
			ModelInheritance: no(noGrokHookModel), LiveSession: no(notLive),
		},
		{
			Adapter: "grok acp", Usage: yes(), CacheSplit: yes(), ResolvedModel: yes(),
			ModelInheritance: no(noGrokHookModel), LiveSession: yes(),
		},
		{
			Adapter: "codex command", Usage: yes(), CacheSplit: yes(),
			ResolvedModel:    no("codex exec --json names no model"),
			ModelInheritance: yes(), LiveSession: no(notLive),
		},
		{
			Adapter:          "codex acp",
			Usage:            no("no captured usage report from the peer"),
			CacheSplit:       no("no captured usage report from the peer"),
			ResolvedModel:    no("codex acp reports no model"),
			ModelInheritance: no("the in-loop model is recorded under harness codex, but the default binding's executable is npx, so the cross-provider guard does not match"),
			LiveSession:      yes(),
		},
	}
}

// capabilityColumns are the table headings, in cell order.
var capabilityColumns = []string{"usage", "cache split", "resolved model", "model inheritance", "live session"}

// cells returns the row's cells in capabilityColumns order.
func (a AdapterCapabilities) cells() []Capability {
	return []Capability{a.Usage, a.CacheSplit, a.ResolvedModel, a.ModelInheritance, a.LiveSession}
}

// CapabilityTableMarkdown renders CapabilityTable as the markdown table
// `satelle help agent-dispatch` carries.
func CapabilityTableMarkdown() string {
	var b strings.Builder
	b.WriteString("| adapter | " + strings.Join(capabilityColumns, " | ") + " |\n")
	b.WriteString("| --- |" + strings.Repeat(" --- |", len(capabilityColumns)) + "\n")
	for _, a := range CapabilityTable() {
		fmt.Fprintf(&b, "| %s |", a.Adapter)
		for _, c := range a.cells() {
			fmt.Fprintf(&b, " %s |", c.Cell())
		}
		b.WriteString("\n")
	}
	return b.String()
}
