package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/syncstate"
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
	// Engaged is whether a story was engaged when the rule consulted it;
	// ItemID, Status and Step describe it. All empty when it was not
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
		if len(gp.proc.Gate.CommandPolicies) == 0 {
			return gp.proc.Gate.CommandRules, ""
		}
		return append(slices.Clip(gp.proc.Gate.CommandRules), subscribedRules(gp.proc.Gate.CommandPolicies, gp.invoking)...), ""
	}
	var cpe *config.CommandPolicyError
	if errors.As(err, &cpe) {
		// The repo's own rules sit in the same file and cannot be read either, so
		// dropping them would silently turn every authored deny off.
		if cpe.Index < 0 {
			return nil, fmt.Sprintf("satelle: command policy subscriptions in %s failed to load: key %s: %s — every shell command is refused until the file loads; fix it and retry", cpe.Path, cpe.Field, cpe.Reason)
		}
		return nil, fmt.Sprintf("satelle: command policy subscriptions in %s failed to load: subscription %d field %s: %s — every shell command is refused until the file loads; fix it and retry", cpe.Path, cpe.Index, cpe.Field, cpe.Reason)
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

// subscribedRules is the rules of each declared subscription's last good cached
// list, in declaration order, each tagged with its subscription's name. They
// follow the repo's own rules, so a repo rule that matches the same command wins
// (the first match wins). The cache is read at invoking — the root the call's
// git-identity memo already holds — so the key starts no git process; RepoKey
// collapses worktrees, so it is the same file the refresh writes. A missing,
// torn or re-pointed cache entry, or a list that no longer parses, contributes
// no rules and never refuses a command: failing closed on a cache would turn a
// network failure into a block on every shell command.
func subscribedRules(subs []config.CommandPolicy, invoking string) []config.CommandRule {
	st, ok, err := syncstate.LoadPolicy(config.GlobalDir(), invoking)
	if err != nil || !ok {
		return nil
	}
	var out []config.CommandRule
	for _, sub := range subs {
		e, cached := policyCached(sub, st)
		if !cached || e.RulesTOML == "" {
			continue
		}
		rules, perr := config.ParseCommandPolicyList([]byte(e.RulesTOML))
		if perr != nil {
			continue
		}
		for i := range rules {
			rules[i].Source = sub.Name
		}
		out = append(out, rules...)
	}
	return out
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

// patternMatchesArgv reports whether pattern is an argv prefix: a word is equal,
// "*" is any one token, "**" any run of tokens (none included), a program token
// is argv[0] by its last path element, and a contains token is one token holding
// its text. Trailing argv beyond the pattern is ignored. A "**" backtracks to
// its last run only, so matching is linear in pattern and argv, with no
// allocation.
func patternMatchesArgv(pattern []config.CommandRuleToken, argv []string) bool {
	if len(pattern) == 0 {
		return false
	}
	pi, ai, star, starAi := 0, 0, 0, 0
	for pi < len(pattern) {
		t := pattern[pi]
		switch {
		case t.Kind == config.CommandRuleRun:
			pi++
			star, starAi = pi, ai
			continue
		case ai < len(argv) && tokenMatches(t, argv[ai]):
			pi++
			ai++
			continue
		case star > 0 && starAi < len(argv):
			starAi++
			pi, ai = star, starAi
			continue
		}
		return false
	}
	return true
}

// tokenMatches reports whether one non-run pattern token matches one argv token.
// The placeholder for a token that is not a plain word matches only "*".
func tokenMatches(t config.CommandRuleToken, arg string) bool {
	switch t.Kind {
	case config.CommandRuleAny:
		return true
	case config.CommandRuleWord:
		return arg == t.Text
	case config.CommandRuleProgram:
		return arg != unquotedPlaceholder && arg[strings.LastIndexByte(arg, '/')+1:] == t.Text
	case config.CommandRuleContains:
		return arg != unquotedPlaceholder && strings.Contains(arg, t.Text)
	}
	return false
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
	var buf [16]config.CommandRuleToken
	for i, r := range rules {
		pat, err := r.Compiled(buf[:])
		if err != nil {
			continue // load refuses a malformed pattern, so none reaches here
		}
		for _, a := range argvs {
			if patternMatchesArgv(pat, a) {
				return i, true
			}
		}
	}
	return 0, false
}

// commandRuleAllows reports whether the engaged story's status or step name is
// in the rule's allow list. The hook loads no workflow: the engagement already carries
// both names.
func commandRuleAllows(allow []string, info engagement) bool {
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

// evalCommandRules judges command against rules. resolve resolves the engaged story
// and is called only when a rule matched and its verdict depends on the story,
// so with no rules, or no match, no engagement is read. A deny rule with an empty
// allow list refuses without reading the engagement, in every status.
func evalCommandRules(rules []config.CommandRule, command string, resolve func() (engagement, bool, error)) commandRuleDecision {
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
	info, engaged, err := resolve()
	if err != nil {
		if rule.Action == config.CommandRuleDeny {
			d.Decision = commandRuleDenied
			d.Reason = "satelle: " + err.Error()
			return d
		}
		engaged = false
	}
	if engaged {
		d.Engaged, d.ItemID, d.Status, d.Step = true, info.ItemID, engagementStatus(info), info.State
		if commandRuleAllows(rule.Allow, info) {
			d.Decision = commandRuleAllowed
			return d
		}
	}
	if rule.Action == config.CommandRuleWarn {
		d.Decision = commandRuleWarned
		d.Reason = "satelle: " + strings.TrimSpace(rule.Message) + commandRuleOrigin(rule)
		if len(rule.Allow) > 0 {
			d.Reason += " — this command passes without a warning only while a story is engaged at one of: " + strings.Join(rule.Allow, ", ")
		}
		return d
	}
	d.Decision = commandRuleDenied
	engagedAt := ""
	if engaged {
		engagedAt = engagementStatus(info)
	}
	d.Reason = commandRuleDenyText(rule, rule.Allow, engagedAt)
	return d
}

// commandRuleDenyText is the refusal: the rule's message, and what to engage.
func commandRuleDenyText(rule config.CommandRule, allow []string, engagedAt string) string {
	msg := "satelle: " + strings.TrimSpace(rule.Message) + commandRuleOrigin(rule)
	if len(allow) == 0 {
		return msg + " — refused in every status; no engaged story lifts this command rule"
	}
	where := "no story is engaged"
	if engagedAt != "" {
		where = fmt.Sprintf("the engaged story is at %q", engagedAt)
	}
	return fmt.Sprintf("%s — refused: %s. Engage a story at one of: %s", msg, where, strings.Join(allow, ", "))
}

// commandRuleOrigin names the subscription a rule came from, so an agent can see
// that a refusal is a subscribed list's and not the repo's own; "" for a repo rule.
func commandRuleOrigin(rule config.CommandRule) string {
	if rule.Source == "" {
		return ""
	}
	return " (command policy " + rule.Source + ")"
}

// commandRuleSourceRepo is a ledger row's source for a rule the repo authored.
const commandRuleSourceRepo = "repo"

// commandRulePayload is the ledger row for a matched command rule.
type commandRulePayload struct {
	Command   string `json:"command"`
	RuleIndex int    `json:"rule_index"`
	Pattern   string `json:"pattern"`
	// Source is "repo" for a rule the repo authored, else the subscription's name.
	Source    string `json:"source"`
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
	source := commandRuleSourceRepo
	if d.Rule.Source != "" {
		source = d.Rule.Source
	}
	payload, err := json.Marshal(commandRulePayload{
		Command: command, RuleIndex: d.Index, Pattern: d.Rule.Pattern, Source: source, Action: d.Rule.Action,
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
