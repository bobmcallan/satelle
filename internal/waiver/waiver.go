// Package waiver is the developer-attested gate waiver (sty_bef97244): the
// developer waives ONE named check of ONE gate on ONE story, recorded as the
// developer rather than as an agent, so a gate that cannot be satisfied after
// the fact is closed by a decision on record.
//
// Three rules make it an attestation rather than a note any session could write:
//
//  1. Record is the ONLY writer of a gate_waiver row. The generic `ledger append`
//     verb refuses the kind (ledger.IsReservedKind), so no agent can append one.
//  2. Record refuses an agent session (config.IsAgentCaller), and requires the
//     developer to retype the story id on the controlling terminal. A dispatched
//     agent has no controlling terminal, so the confirmation is the attestation.
//  3. There is no fallback to stdin: a terminal that cannot be opened is a
//     refusal, never a prompt on a pipe an agent can feed.
//
// Honest posture: an agent with a pty-spawning tool, or with direct write access
// to the ledger store, could still fake it — like the containment principle's
// hooks, this is a fence that raises the cost and leaves a trail, not a sandbox.
//
// This package only records and enumerates. Whether a gate honours a waiver is
// that gate's rubric, never decided here.
package waiver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// ActorDeveloper is the actor on every gate_waiver row.
const ActorDeveloper = "developer"

// DefaultTTY is the controlling terminal the default confirmer reads.
const DefaultTTY = "/dev/tty"

// Input is one waiver as the developer states it.
type Input struct {
	StoryID string
	Gate    string // the gate's skill
	Check   string // the named check of that gate's rubric
	Reason  string
}

// Payload is the payload of a gate_waiver row.
type Payload struct {
	Gate   string `json:"gate"`
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

// Waiver is a recorded waiver read back from the ledger.
type Waiver struct {
	ID        string
	StoryID   string
	Actor     string
	CreatedAt time.Time
	Payload
}

// Confirmer obtains the developer's attestation. It returns nil only when the
// developer typed want back on the controlling terminal.
type Confirmer interface {
	Confirm(prompt, want string) error
}

// TTYConfirmer reads the confirmation from the controlling terminal. Path
// defaults to DefaultTTY and a test points it at a file; the prompt goes to Out
// (default os.Stderr) so it never lands in the file being read.
type TTYConfirmer struct {
	Path string
	Out  io.Writer
}

// Confirm prints prompt, reads one line from the terminal and requires it to
// equal want. A terminal that cannot be opened is an error: there is no stdin
// fallback.
func (c TTYConfirmer) Confirm(prompt, want string) error {
	path := c.Path
	if path == "" {
		path = DefaultTTY
	}
	tty, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("waiver: no controlling terminal (%v): a waiver is a developer decision; run it from your own terminal", err)
	}
	defer tty.Close()
	out := c.Out
	if out == nil {
		out = os.Stderr
	}
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("waiver: read confirmation: %w", err)
	}
	if strings.TrimSpace(line) != want {
		return fmt.Errorf("waiver: confirmation does not match %s; nothing recorded", want)
	}
	return nil
}

// Record appends one gate_waiver row (actor developer) after the developer
// retypes the story id. Every refusal — an agent caller, a missing field, no
// terminal, a mismatched id — returns an error and writes no row.
func Record(ctx context.Context, ls *ledger.Store, in Input, confirm Confirmer, now time.Time) (Waiver, error) {
	if config.IsAgentCaller() {
		return Waiver{}, errors.New("waiver: a waiver is a developer decision; run it from your own terminal, not an agent session")
	}
	in = Input{
		StoryID: strings.TrimSpace(in.StoryID), Gate: strings.TrimSpace(in.Gate),
		Check: strings.TrimSpace(in.Check), Reason: strings.TrimSpace(in.Reason),
	}
	for _, f := range []struct{ name, v string }{{"story id", in.StoryID}, {"--gate", in.Gate}, {"--check", in.Check}, {"--reason", in.Reason}} {
		if f.v == "" {
			return Waiver{}, fmt.Errorf("waiver: %s required", f.name)
		}
	}
	if confirm == nil {
		confirm = TTYConfirmer{}
	}
	prompt := fmt.Sprintf("Waive check %q of gate %q on %s.\nReason: %s\nRetype the story id to confirm: ", in.Check, in.Gate, in.StoryID, in.Reason)
	if err := confirm.Confirm(prompt, in.StoryID); err != nil {
		return Waiver{}, err
	}
	p := Payload{Gate: in.Gate, Check: in.Check, Reason: in.Reason}
	raw, err := json.Marshal(p)
	if err != nil {
		return Waiver{}, err
	}
	body := fmt.Sprintf("developer waived check %s of gate %s: %s", in.Check, in.Gate, in.Reason)
	e, err := ls.Append(ctx, ledger.AppendInput{
		StoryID: in.StoryID, Kind: ledger.KindGateWaiver, Actor: ActorDeveloper, Body: body, Payload: raw,
	}, now)
	if err != nil {
		return Waiver{}, fmt.Errorf("waiver: record: %w", err)
	}
	return Waiver{ID: e.ID, StoryID: e.StoryID, Actor: e.Actor, CreatedAt: e.CreatedAt, Payload: p}, nil
}

// List returns the story's recorded waivers, oldest first. A row whose actor is
// not the developer, or whose payload does not decode, is not a waiver.
func List(ctx context.Context, ls *ledger.Store, storyID string) ([]Waiver, error) {
	if ls == nil || strings.TrimSpace(storyID) == "" {
		return nil, nil
	}
	rows, err := ls.ListByStory(ctx, storyID, ledger.KindGateWaiver)
	if err != nil {
		return nil, err
	}
	var out []Waiver
	for _, e := range rows {
		var p Payload
		if e.Actor != ActorDeveloper || json.Unmarshal(e.Payload, &p) != nil || p.Gate == "" || p.Check == "" {
			continue
		}
		out = append(out, Waiver{ID: e.ID, StoryID: e.StoryID, Actor: e.Actor, CreatedAt: e.CreatedAt, Payload: p})
	}
	return out, nil
}
