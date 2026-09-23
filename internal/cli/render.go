package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/compact"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/retrieve"
)

// renderResponse is dispatch's one output path (sty_75b76691): plain indented JSON,
// unless verbName is one of this repo's configured [output] compact_commands
// AND compact mode is requested (--compact, or [output].compact_for_agents
// with an agent-looking caller) AND --json has not forced plain. storyID
// (from the request, requestStoryID) links any offloaded content to the story
// it came from.
//
// A verb outside compact_commands, a store-less context (should not happen —
// every caller here carries needsStore), or a fold that does not actually pay
// for itself (compact.Fold* return ok=false) all fall back to the original
// indented rendering unchanged — the plain path this command always had.
func renderResponse(cmd *cobra.Command, verbName, storyID string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if !compactRequested(cmd, verbName) {
		return printJSON(cmd, raw)
	}
	out, folded := renderCompact(cmd, verbName, storyID, raw)
	if !folded {
		return printJSON(cmd, raw)
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
}

// compactRequested resolves the AC4 mode matrix.
func compactRequested(cmd *cobra.Command, verbName string) bool {
	if v, err := cmd.Flags().GetBool("json"); err == nil && v {
		return false
	}
	// --full (story diff only — absent elsewhere, GetBool errors and is
	// ignored) skips BOTH noise-stripping and ranking, printing the raw patch
	// (sty_918e2086 AC3).
	if v, err := cmd.Flags().GetBool("full"); err == nil && v {
		return false
	}
	a, err := appFrom(cmd)
	if err != nil || a == nil {
		return false
	}
	if !a.Config.Output.IsCompactCommand(verbName) {
		return false
	}
	if v, err := cmd.Flags().GetBool("compact"); err == nil && v {
		return true
	}
	return a.Config.Output.CompactForAgents && config.IsAgentCaller()
}

// renderCompact folds raw for verbName. story-diff carries free-text (the
// patch) alongside a file list, so it gets its own path (renderCompactDiff);
// every other compact command is a bare JSON array a table fold applies to
// directly (compact.FoldTable).
func renderCompact(cmd *cobra.Command, verbName, storyID string, raw json.RawMessage) (string, bool) {
	a, err := appFrom(cmd)
	if err != nil || a == nil || a.Store == nil || a.Store.Retrieve == nil {
		return "", false
	}
	off := retrieveAdapter{ctx: cmd.Context(), store: a.Store.Retrieve, storyID: storyID}
	if verbName == "story-diff" {
		return renderCompactDiff(a.Config.Output, raw, off)
	}
	cell := a.Config.Output.ResolveLongCellBytes()
	out, ok := compact.FoldTable(raw, cell, off, off)
	size := len(raw)
	if ok {
		size = len(out)
	}
	// Lossy last resort (sty_aa34491d): only for a marked command whose
	// lossless form is still over the configured budget.
	if crushCfg := a.Config.Output.Crush.Resolve(); crushCfg.Over(size) {
		if crushed, cok := compact.CrushArray(raw, crushCfg, off); cok {
			return renderCrushed(crushed, cell, off), true
		}
	}
	return out, ok
}

// renderCrushed renders a crushed array: the kept rows table-folded when they
// still fold, the trailing marker element appended as its own line (never
// part of the table, which needs uniform rows), else the crushed JSON indented.
func renderCrushed(crushed json.RawMessage, cell int, off retrieveAdapter) string {
	var elems []json.RawMessage
	if err := json.Unmarshal(crushed, &elems); err == nil && len(elems) > 1 {
		last := len(elems) - 1
		kept := append(append([]byte{'['}, bytes.Join(rawSlices(elems[:last]), []byte{','})...), ']')
		if tbl, ok := compact.FoldTable(kept, cell, off, off); ok {
			return tbl + "\n" + string(elems[last])
		}
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, crushed, "", "  "); err != nil {
		return string(crushed)
	}
	return buf.String()
}

func rawSlices(in []json.RawMessage) [][]byte {
	out := make([][]byte, len(in))
	for i, e := range in {
		out[i] = e
	}
	return out
}

// renderCompactDiff compacts a story-diff response's "patch" field in place
// (compact.CompactPatch, then compact.FoldRepeats through the Fold guard) and
// re-marshals the object indented — story-diff's "files"/"stat" fields are
// already small and are not a list of uniform objects, so no table fold
// applies to them (AC2 scopes this story to the patch's noise, not the whole
// response shape).
func renderCompactDiff(cfg config.OutputConfig, raw json.RawMessage, off retrieveAdapter) (string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", false
	}
	pv, ok := obj["patch"]
	if !ok {
		return "", false
	}
	var patch string
	if err := json.Unmarshal(pv, &patch); err != nil || patch == "" {
		return "", false
	}
	compacted := compact.CompactPatch(patch, cfg.NoisePatterns, off)
	if cfg.DiffRank.Enabled {
		compacted = compact.RankPatch(compacted, cfg.DiffRank.Resolve(), off)
	}
	compacted = compact.Fold(compacted, compact.FoldRepeats(compacted, cfg.ResolveRepeatMin()),
		func(s string) (string, error) { return compact.UnfoldRepeats(s), nil })

	// SetEscapeHTML(false): a compacted patch carries literal retrieve markers
	// (<<ccr:HASH,hunk,SIZE>>) the default encoder would turn into
	// <<ccr:... — still the same value once unmarshalled, but no
	// longer the plain-text-greppable marker Marker/MarkerRE promise.
	enc, err := marshalNoEscape(compacted)
	if err != nil {
		return "", false
	}
	obj["patch"] = enc
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(obj); err != nil {
		return "", false
	}
	out := buf.String()
	if len(out) >= len(raw) {
		return "", false
	}
	return out, true
}

// marshalNoEscape JSON-encodes v without HTML-escaping '<'/'>'/'&' — see the
// comment at its call site in renderCompactDiff.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// retrieveAdapter adapts internal/retrieve.Store to compact.Offloader and
// compact.Resolver — the CCR store every other offload/retrieve path in this
// codebase already shares (sty_b0577532).
type retrieveAdapter struct {
	ctx     context.Context
	store   *retrieve.Store
	storyID string
}

func (r retrieveAdapter) Put(content []byte) (string, error) {
	ref, err := r.store.Put(r.ctx, r.storyID, content, time.Now())
	if err != nil {
		return "", err
	}
	return ref.Hash, nil
}

func (r retrieveAdapter) Get(hash string) ([]byte, error) {
	return r.store.Get(r.ctx, hash)
}
