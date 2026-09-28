// Package verb is satelle's single execution path. Every data operation is a
// named function taking JSON in and returning JSON out, registered here. Both
// the CLI and (later) the local web server dispatch to the same Invoke
// functions — there is exactly one implementation per verb, so the two
// surfaces cannot drift.
//
// This is the architecture's one seam: CLI command / web handler →
// verb.Dispatch → domain store → sqlite. The OSS tier is always local, so
// dispatch is always in-process. Stores are wired in as package globals
// (SetWorkItemStore, SetLedgerStore, SetDocIndexStore) at bootstrap.
//
// Ported from satellites' internal/verb, stripped of the MCP role tier and
// auth wiring (no auth on the local surface).
package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Verb is a named entry in the dispatch registry.
type Verb struct {
	Name        string
	Description string
	Invoke      func(ctx context.Context, req json.RawMessage) (json.RawMessage, error)
	// DispatchesReviewer, when set, reports whether THIS request will run a
	// reviewer or another isolated agent (a gated transition, a gated create, an
	// amend, a re-summarise, a retrospective). It is the one declaration a caller
	// that must not sit through a long gate reads: an agent-facing CLI hands such
	// a request to a detached run instead of blocking (sty_c4b92c9e). Nil means
	// the verb never dispatches. The predicate reads only the request and the
	// wiring — it never dispatches, and a wrong "true" costs one process start.
	DispatchesReviewer func(req json.RawMessage) bool
}

var (
	mu    sync.RWMutex
	verbs = map[string]*Verb{}
)

// Register adds a verb to the global registry. Panics on duplicate names —
// verb names are a typed namespace and collisions are bugs.
func Register(v *Verb) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := verbs[v.Name]; exists {
		panic(fmt.Sprintf("verb: %q already registered", v.Name))
	}
	verbs[v.Name] = v
}

// Get returns the verb registered under name, or nil.
func Get(name string) *Verb {
	mu.RLock()
	defer mu.RUnlock()
	return verbs[name]
}

// Catalog returns the names of every registered verb, sorted.
func Catalog() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(verbs))
	for n := range verbs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Dispatch invokes a verb by name with raw JSON. Both CLI and web transports
// call here — same code path, same response shape.
func Dispatch(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
	v := Get(name)
	if v == nil {
		return nil, fmt.Errorf("verb: unknown %q", name)
	}
	return v.Invoke(ctx, req)
}
