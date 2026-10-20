package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/filebundle"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/sessionconfig"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// storySessionCommand builds `satelle story session <id>`: a story's session
// read from this machine's store alone, so a store filled by a pull shows the
// same as the machine that ran it.
func storySessionCommand() *cobra.Command {
	var configDigest, restoreDir string
	cmd := &cobra.Command{
		Use:   "session <id>",
		Short: "Show a story's session — configuration, inputs, logs and outputs — from the local store",
		Long: `Show a story's session from this machine's store only; nothing is fetched, so
pull it first ('satelle sync workstate pull <id>') on a machine that did not run it.

Sections: Configuration (each digest pair its runs used, and whether its bundle
is held), Inputs (story, messages, dispatch payload sizes), Logs (invocations and
verdicts), Outputs (documents and status).

--config <digest> prints one configuration's files with the digests recomputed
from them; <digest> is a prefix of either digest. --restore <dir> writes those
files into <dir>, each path checked first.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			if configDigest != "" {
				return runSessionConfig(cmd, args[0], configDigest, restoreDir, asJSON)
			}
			if restoreDir != "" {
				return fmt.Errorf("session: --restore needs --config <digest>")
			}
			return runSessionView(cmd, args[0], asJSON)
		},
	}
	cmd.Flags().StringVar(&configDigest, "config", "", "show one configuration by digest (prefix of either digest, or <substrate12>-<bindings12>)")
	cmd.Flags().StringVar(&restoreDir, "restore", "", "with --config, write the configuration's files into this empty directory")
	return cmd
}

type sessionConfigRef struct {
	SubstrateDigest string `json:"substrate_digest"`
	BindingsDigest  string `json:"bindings_digest"`
	Bundle          string `json:"bundle"`
	BundleHeld      bool   `json:"bundle_held"`
	Runs            int    `json:"runs"`
}

type sessionPayloadRef struct {
	Row          string `json:"row"`
	Step         string `json:"step"`
	Agent        string `json:"agent,omitempty"`
	Skill        string `json:"skill,omitempty"`
	SystemBytes  int    `json:"system_prompt_bytes,omitempty"`
	PayloadBytes int    `json:"payload_bytes,omitempty"`
}

type sessionRow struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Actor   string `json:"actor,omitempty"`
	Body    string `json:"body,omitempty"`
	Created string `json:"created_at"`
}

type storyOutputDoc struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type sessionView struct {
	ID            string             `json:"id"`
	Configuration []sessionConfigRef `json:"configuration"`
	Inputs        struct {
		Story            workitem.Item       `json:"story"`
		Messages         []sessionRow        `json:"messages"`
		DispatchPayloads []sessionPayloadRef `json:"dispatch_payloads"`
	} `json:"inputs"`
	Logs struct {
		Invocations []sessionRow `json:"invocations"`
		Verdicts    []sessionRow `json:"verdicts"`
	} `json:"logs"`
	Outputs struct {
		Documents []storyOutputDoc `json:"documents"`
		Status    string           `json:"status"`
	} `json:"outputs"`
}

func toSessionRow(e ledger.Entry) sessionRow {
	return sessionRow{ID: e.ID, Kind: e.Kind, Actor: e.Actor, Body: e.Body, Created: e.CreatedAt.Format("2006-01-02T15:04:05Z07:00")}
}

// sessionPairs are the story's distinct digest pairs, oldest first, from its
// stamped agent_invocation rows.
func sessionPairs(rows []ledger.Entry) []sessionConfigRef {
	var out []sessionConfigRef
	for _, e := range rows {
		var p struct {
			S string `json:"substrate_digest"`
			B string `json:"bindings_digest"`
		}
		if json.Unmarshal(e.Payload, &p) != nil || p.S == "" || p.B == "" {
			continue
		}
		i := slices.IndexFunc(out, func(r sessionConfigRef) bool { return r.SubstrateDigest == p.S && r.BindingsDigest == p.B })
		if i < 0 {
			out = append(out, sessionConfigRef{SubstrateDigest: p.S, BindingsDigest: p.B, Bundle: sessionconfig.BundleName(p.S, p.B)})
			i = len(out) - 1
		}
		out[i].Runs++
	}
	return out
}

func runSessionView(cmd *cobra.Command, id string, asJSON bool) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	item, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("session: %s: %w", id, err)
	}
	led := a.Store.Ledger
	list := func(kind string) ([]ledger.Entry, error) { return led.ListByStory(ctx, id, kind) }

	v := sessionView{ID: id}
	v.Inputs.Story = item
	invocations, err := list(ledger.KindAgentInvocation)
	if err != nil {
		return err
	}
	for _, e := range invocations {
		if ledger.IsToolPermissionRow(e) {
			continue
		}
		v.Logs.Invocations = append(v.Logs.Invocations, toSessionRow(e))
		var p struct {
			From   string `json:"from"`
			To     string `json:"to"`
			Agent  string `json:"agent"`
			Skill  string `json:"skill"`
			System int    `json:"system_prompt_bytes"`
			Bytes  int    `json:"payload_bytes"`
		}
		if json.Unmarshal(e.Payload, &p) == nil && (p.System > 0 || p.Bytes > 0) {
			v.Inputs.DispatchPayloads = append(v.Inputs.DispatchPayloads, sessionPayloadRef{
				Row: e.ID, Step: p.From + "→" + p.To, Agent: p.Agent, Skill: p.Skill, SystemBytes: p.System, PayloadBytes: p.Bytes})
		}
	}
	for _, kind := range []string{ledger.KindReviewAccept, ledger.KindReviewReject, ledger.KindPerformerReject} {
		rows, err := list(kind)
		if err != nil {
			return err
		}
		for _, e := range rows {
			v.Logs.Verdicts = append(v.Logs.Verdicts, toSessionRow(e))
		}
	}
	slices.SortStableFunc(v.Logs.Verdicts, func(a, b sessionRow) int { return strings.Compare(a.Created, b.Created) })
	msgs, err := list(ledger.KindAgentMessage)
	if err != nil {
		return err
	}
	for _, e := range msgs {
		v.Inputs.Messages = append(v.Inputs.Messages, toSessionRow(e))
	}
	v.Configuration = sessionPairs(invocations)
	for i := range v.Configuration {
		v.Configuration[i].BundleHeld = verb.HasStoryDoc(ctx, id, v.Configuration[i].Bundle)
	}
	docs, err := sessionDocs(cmd, id)
	if err != nil {
		return err
	}
	v.Outputs.Documents, v.Outputs.Status = docs, item.Status

	out := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	renderSession(out, v)
	return nil
}

func sessionDocs(cmd *cobra.Command, id string) ([]storyOutputDoc, error) {
	raw, err := verb.Dispatch(cmd.Context(), "story-doc-list", mustJSON(map[string]string{"story_id": id}))
	if err != nil {
		return nil, err
	}
	var docs []storyOutputDoc
	if err := json.Unmarshal(raw, &docs); err != nil {
		return nil, fmt.Errorf("session: documents: %w", err)
	}
	return docs, nil
}

func renderSession(w io.Writer, v sessionView) {
	fmt.Fprintf(w, "Session — %s: %s\n", v.ID, v.Inputs.Story.Title)
	fmt.Fprintln(w, "\nConfiguration")
	if len(v.Configuration) == 0 {
		fmt.Fprintln(w, "  (no stamped runs)")
	}
	for _, c := range v.Configuration {
		held := "bundle held"
		if !c.BundleHeld {
			held = "bundle not held here"
		}
		fmt.Fprintf(w, "  %s  substrate %s  bindings %s  %d run(s)  %s\n", c.Bundle, c.SubstrateDigest, c.BindingsDigest, c.Runs, held)
	}
	fmt.Fprintf(w, "\nInputs\n  story %s [%s] %s\n  %d message(s)\n", v.ID, v.Inputs.Story.Status, v.Inputs.Story.Title, len(v.Inputs.Messages))
	for _, p := range v.Inputs.DispatchPayloads {
		fmt.Fprintf(w, "  payload %s %s %s %s — system %dB, payload %dB\n", p.Row, p.Step, p.Agent, p.Skill, p.SystemBytes, p.PayloadBytes)
	}
	fmt.Fprintf(w, "\nLogs\n  %d agent invocation(s), %d verdict(s)\n", len(v.Logs.Invocations), len(v.Logs.Verdicts))
	for _, r := range v.Logs.Invocations {
		fmt.Fprintf(w, "  %s %s %s\n", r.Created, r.Kind, r.Body)
	}
	for _, r := range v.Logs.Verdicts {
		fmt.Fprintf(w, "  %s %s %s\n", r.Created, r.Kind, r.Body)
	}
	fmt.Fprintf(w, "\nOutputs\n  status %s\n", v.Outputs.Status)
	for _, d := range v.Outputs.Documents {
		fmt.Fprintf(w, "  %s (%s)\n", d.Name, d.Type)
	}
}

// matchSessionConfig picks the story's one configuration a --config argument
// names: a prefix of either digest, or the <substrate12>-<bindings12> pair.
func matchSessionConfig(pairs []sessionConfigRef, arg string) (sessionConfigRef, error) {
	arg = strings.TrimPrefix(strings.TrimSpace(arg), "config-")
	var hit []sessionConfigRef
	for _, p := range pairs {
		pair := strings.TrimPrefix(p.Bundle, "config-")
		if strings.HasPrefix(p.SubstrateDigest, arg) || strings.HasPrefix(p.BindingsDigest, arg) || strings.HasPrefix(pair, arg) {
			hit = append(hit, p)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], nil
	case 0:
		return sessionConfigRef{}, fmt.Errorf("session: no configuration matching %q on this story (run `satelle story session <id>` to list them)", arg)
	}
	names := make([]string, len(hit))
	for i, h := range hit {
		names[i] = h.Bundle
	}
	return sessionConfigRef{}, fmt.Errorf("session: %q matches %d configurations (%s) — give more of the digest", arg, len(hit), strings.Join(names, ", "))
}

type sessionConfigView struct {
	Bundle              string            `json:"bundle"`
	SubstrateDigest     string            `json:"substrate_digest"`
	BindingsDigest      string            `json:"bindings_digest"`
	RecomputedSubstrate string            `json:"recomputed_substrate_digest"`
	RecomputedBindings  string            `json:"recomputed_bindings_digest"`
	Verified            bool              `json:"verified"`
	Files               []sessionConfFile `json:"files"`
	Restored            string            `json:"restored_to,omitempty"`
}

type sessionConfFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func runSessionConfig(cmd *cobra.Command, id, arg, restoreDir string, asJSON bool) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if _, err := a.Store.Stories.Get(ctx, id); err != nil {
		return fmt.Errorf("session: %s: %w", id, err)
	}
	rows, err := a.Store.Ledger.ListByStory(ctx, id, ledger.KindAgentInvocation)
	if err != nil {
		return err
	}
	ref, err := matchSessionConfig(sessionPairs(rows), arg)
	if err != nil {
		return err
	}
	file, err := verb.ConfigBundleFile(ctx, id, ref.Bundle)
	if err != nil {
		return fmt.Errorf("session: bundle %s is not held on this machine (run `satelle sync workstate pull %s`): %w", ref.Bundle, id, err)
	}
	b, err := sessionconfig.Parse(file)
	if err != nil {
		return fmt.Errorf("session: bundle %s: %w", ref.Bundle, err)
	}
	if restoreDir != "" {
		if err := filebundle.Write(restoreDir, b.Entries, false, nil); err != nil {
			return fmt.Errorf("session: restore: %w", err)
		}
	}
	view := sessionConfigView{
		Bundle: ref.Bundle, SubstrateDigest: b.Meta.SubstrateDigest, BindingsDigest: b.Meta.BindingsDigest,
		RecomputedSubstrate: b.Substrate, RecomputedBindings: b.BindingsSH, Verified: b.Verified(), Restored: restoreDir,
	}
	for _, e := range b.Entries {
		view.Files = append(view.Files, sessionConfFile{Path: e.Path, Content: e.Content})
	}
	out := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	}
	fmt.Fprintf(out, "Configuration %s\n  substrate %s (recomputed %s)\n  bindings  %s (recomputed %s)\n  verified: %t\n",
		view.Bundle, view.SubstrateDigest, view.RecomputedSubstrate, view.BindingsDigest, view.RecomputedBindings, view.Verified)
	if restoreDir != "" {
		fmt.Fprintf(out, "  restored %d file(s) to %s\n", len(view.Files), restoreDir)
	}
	for _, f := range view.Files {
		fmt.Fprintf(out, "\n=== %s\n%s\n", f.Path, strings.TrimRight(f.Content, "\n"))
	}
	if !b.Verified() {
		return fmt.Errorf("session: bundle %s does not match its recorded digests", ref.Bundle)
	}
	return nil
}
