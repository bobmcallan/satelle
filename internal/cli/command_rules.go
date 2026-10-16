package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// Repo-authored agent command rules (sty_a74551d6). satelle ships no rule and no
// default list: [[gate.command_rule]] is authored by the repo, and with none
// configured commitgate refuses and warns nothing it did not already.
//
// This file is pure in-process matching on config that is already loaded — it
// spawns no process and makes no network call (command_rules_test.go parses this
// file's imports to keep it so). It judges only the command line the hook sees,
// through bashscan's tokenizer: wrappers, eval, aliases and scripts are not seen,
// and satelle knows nothing of what a command does, which account runs it, or
// what that account may do.

// commandRuleDecision is what evalCommandRules concluded about one command.
type commandRuleDecision struct {
	Matched bool
	Index   int
	Rule    config.CommandRule
	// Decision is commandRuleAllowed, commandRuleWarned or commandRuleDenied.
	Decision string
	// Reason is the deny text (Decision denied) or the warn text (warned).
	Reason string
	// Engaged is whether a story was engaged when the rule consulted the seat;
	// ItemID, Status and Step describe it. All empty when the seat was not
	// consulted (a deny rule with no allow list never reads it).
	Engaged bool
	ItemID  string
	Status  string
	Step    string
}

const (
	commandRuleAllowed = "allowed"
	commandRuleWarned  = "warned"
	commandRuleDenied  = "denied"
)

// commandRuleMaxRecorded bounds the command text written to the ledger.
const commandRuleMaxRecorded = 1000

// commandRulesFrom returns the loaded process config's command rules. refuse is
// non-empty when the config holds command rules that failed to load (or cannot
// be read at all because it does not parse): the hook then refuses every shell
// command with that text, because dropping the rules would silently turn every
// authored deny off. Edit and Write stay open, so the file can be fixed. Any
// other load error is not about command rules and behaves as it always did.
func commandRulesFrom(gp gateProcess) (rules []config.CommandRule, refuse string) {
	err, path := gp.err, gp.path
	if err == nil {
		return gp.proc.Gate.CommandRules, ""
	}
	var cre *config.CommandRuleError
	if errors.As(err, &cre) {
		if cre.Index < 0 {
			return nil, fmt.Sprintf("satelle: command rules in %s failed to load: key %s: %s — every shell command is refused until the file loads; fix it and retry", cre.Path, cre.Field, cre.Reason)
		}
		return nil, fmt.Sprintf("satelle: command rules in %s failed to load: rule %d field %s: %s — every shell command is refused until the file loads; fix it and retry", cre.Path, cre.Index, cre.Field, cre.Reason)
	}
	var pe *config.ParseError
	if errors.As(err, &pe) {
		p := pe.Path
		if p == "" {
			p = path
		}
		return nil, fmt.Sprintf("satelle: config %s failed to parse, so its command rules cannot be read: %v — every shell command is refused until the file parses; fix it and retry", p, pe.Err)
	}
	return nil, ""
}

// unquotedPlaceholder stands in for an argv token that is not a plain word (a
// heredoc, or a span the tokenizer could not unquote): only a * matches it.
const unquotedPlaceholder = "\x00"

// segmentArgv is a command segment's argv: leading NAME=value assignments are
// stripped, and a quoted span is unquoted in place so `tool "apply"` and
// `tool apply` agree.
func segmentArgv(toks []bashTok) []string {
	argv := make([]string, 0, len(toks))
	for _, t := range toks {
		switch {
		case t.Kind == "word" && t.Value != "":
			argv = append(argv, t.Value)
		case t.Kind == "string" && t.Raw == "":
			argv = append(argv, unquoteTok(t.Value))
		default:
			argv = append(argv, unquotedPlaceholder)
		}
	}
	i := 0
	for i < len(argv) {
		eq := strings.IndexByte(argv[i], '=')
		if eq <= 0 || !isShellIdent(argv[i][:eq]) {
			break
		}
		i++
	}
	return argv[i:]
}

// patternMatchesArgv reports whether pattern's tokens are an argv prefix, where
// "*" matches any one token.
func patternMatchesArgv(pattern []string, argv []string) bool {
	if len(pattern) == 0 || len(argv) < len(pattern) {
		return false
	}
	for i, p := range pattern {
		if p == "*" {
			continue
		}
		if argv[i] != p {
			return false
		}
	}
	return true
}

// matchCommandRule returns the index of the first rule, in file order, whose
// pattern matches any simple command in command. The tokenizer runs once.
func matchCommandRule(rules []config.CommandRule, command string) (int, bool) {
	if len(rules) == 0 || strings.TrimSpace(command) == "" {
		return 0, false
	}
	var argvs [][]string
	for _, seg := range splitSegments(tokenizeBash(command)) {
		if a := segmentArgv(seg.Toks); len(a) > 0 {
			argvs = append(argvs, a)
		}
	}
	for i, r := range rules {
		pat := config.CommandRuleTokens(r.Pattern)
		for _, a := range argvs {
			if patternMatchesArgv(pat, a) {
				return i, true
			}
		}
	}
	return 0, false
}

// commandRuleAllows reports whether the engaged story's status or step name is
// in the rule's allow list. The hook loads no workflow: the seat already carries
// both names.
func commandRuleAllows(allow []string, info seatInfo) bool {
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if a == strings.ToLower(strings.TrimSpace(info.StoryStatus)) || a == strings.ToLower(strings.TrimSpace(info.State)) {
			return true
		}
	}
	return false
}

// evalCommandRules judges command against rules. seat resolves the engaged story
// and is called only when a rule matched and its verdict depends on the story,
// so with no rules, or no match, no seat is read. A deny rule with an empty
// allow list refuses without reading the seat, in every status.
func evalCommandRules(rules []config.CommandRule, command string, seat func() (seatInfo, bool, error)) commandRuleDecision {
	idx, ok := matchCommandRule(rules, command)
	if !ok {
		return commandRuleDecision{}
	}
	rule := rules[idx]
	d := commandRuleDecision{Matched: true, Index: idx, Rule: rule}
	if len(rule.Allow) == 0 && rule.Action == config.CommandRuleDeny {
		d.Decision = commandRuleDenied
		d.Reason = commandRuleDenyText(rule, nil, "")
		return d
	}
	info, engaged, err := seat()
	if err != nil {
		if rule.Action == config.CommandRuleDeny {
			d.Decision = commandRuleDenied
			d.Reason = "satelle: " + err.Error()
			return d
		}
		engaged = false
	}
	if engaged {
		d.Engaged, d.ItemID, d.Status, d.Step = true, info.ItemID, seatStatus(info), info.State
		if commandRuleAllows(rule.Allow, info) {
			d.Decision = commandRuleAllowed
			return d
		}
	}
	if rule.Action == config.CommandRuleWarn {
		d.Decision = commandRuleWarned
		d.Reason = "satelle: " + strings.TrimSpace(rule.Message)
		if len(rule.Allow) > 0 {
			d.Reason += " — this command passes without a warning only while a story is engaged at one of: " + strings.Join(rule.Allow, ", ")
		}
		return d
	}
	d.Decision = commandRuleDenied
	engagedAt := ""
	if engaged {
		engagedAt = seatStatus(info)
	}
	d.Reason = commandRuleDenyText(rule, rule.Allow, engagedAt)
	return d
}

// commandRuleDenyText is the refusal: the rule's message, and what to engage.
func commandRuleDenyText(rule config.CommandRule, allow []string, engagedAt string) string {
	msg := "satelle: " + strings.TrimSpace(rule.Message)
	if len(allow) == 0 {
		return msg + " — refused in every status; no engaged story lifts this command rule"
	}
	where := "no story is engaged"
	if engagedAt != "" {
		where = fmt.Sprintf("the engaged story is at %q", engagedAt)
	}
	return fmt.Sprintf("%s — refused: %s. Engage a story at one of: %s", msg, where, strings.Join(allow, ", "))
}

// commandRulePayload is the ledger row for a matched command rule.
type commandRulePayload struct {
	Command   string `json:"command"`
	RuleIndex int    `json:"rule_index"`
	Pattern   string `json:"pattern"`
	Action    string `json:"action"`
	Decision  string `json:"decision"`
	Delivered bool   `json:"delivered"`
	Status    string `json:"status"`
	Step      string `json:"step"`
}

// recordCommandRule appends a matched rule to the engaged story's ledger through
// the call's shared store (gateStore), which the caller closes. With no engaged
// story there is no ledger to write to. A failure to record never
// changes the decision.
func recordCommandRule(gs *gateStore, d commandRuleDecision, command, harness string) {
	if !d.Matched || !d.Engaged || d.ItemID == "" {
		return
	}
	if len(command) > commandRuleMaxRecorded {
		command = command[:commandRuleMaxRecorded]
	}
	delivered := d.Decision == commandRuleWarned && agentcli.FactsFor(harness).PreToolUseWarn.Available
	payload, err := json.Marshal(commandRulePayload{
		Command: command, RuleIndex: d.Index, Pattern: d.Rule.Pattern, Action: d.Rule.Action,
		Decision: d.Decision, Delivered: delivered, Status: d.Status, Step: d.Step,
	})
	if err != nil {
		return
	}
	a, err := gs.open()
	if err != nil {
		return
	}
	_, _ = a.Store.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: d.ItemID, Kind: ledger.KindCommandRule, Actor: "command-rule",
		Body:    fmt.Sprintf("command rule %d (%s %s) %s a command while %s was at %s", d.Index, d.Rule.Action, d.Rule.Pattern, d.Decision, d.ItemID, d.Status),
		Payload: payload,
	}, time.Now().UTC())
}
