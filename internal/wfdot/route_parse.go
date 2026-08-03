// TOML front end for the route constructor: done.toml and step.toml in, a Spec
// out (sty_81bb0dde).
//
// The route source is a STRUCTURED file — records with terse commentary — and it
// is now written in a structured format. It used to be markdown parsed by a
// hand-rolled key/value reader, which was the wrong shape twice over:
//
//   - The reader was bespoke. Its own header claimed it avoided adding a TOML
//     dependency; `github.com/BurntSushi/toml` had been in go.mod the whole time
//     for satelle.toml and agents.toml. It also skipped only a line STARTING
//     with `<!--`, so a multi-line HTML comment failed on every line after the
//     first — a real defect (sty_0ba83c4e, sty_8a7987de) whose remedy was a
//     prose convention rather than a parser.
//   - The markdown was a costume. A survey of every authored and embedded route
//     source found zero wikilinks and exactly three constructs in use: `---`
//     frontmatter, `##` headings, `<!-- -->` comments. TOML has `[meta]`,
//     `[[table]]` and `#`. The `.md` extension existed so docindex would ingest
//     the file, which is an indexer limitation, not a property of the document.
//
// The AUTHORED grammar is now TOML's. What this file adds is the mapping onto
// the exported Spec-side types, which did NOT change — that is what keeps
// route.go, wfroute, wfgovern, workflow show and the web panel untouched.
//
// Unknown keys are an ERROR, never a silent drop: a typo'd `reviewrs =` that
// parsed as "no reviewers" would lose a gate, and a route that quietly loses a
// gate is the one failure this representation must not have.
package wfdot

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// --- wire structs -----------------------------------------------------------
//
// Deliberately separate from the exported Step/List/RouteGate types. The wire
// shape serves TOML decoding; the Spec-side shape serves the constructor. Fusing
// them would let a decoding concern (a *int for set-vs-unset, say) leak into
// every consumer of a Step.

// roleRef is `{ state = "blocked", gate = "…" }` — a role state and the reviewer
// that admits it. They are a pair; splitting them across keys invites declaring
// one without the other.
type roleRef struct {
	State string `toml:"state"`
	Gate  string `toml:"gate"`
	// Advisor / AdvisorSkill name an agent the ORCHESTRATOR consults on this role
	// state. A declaration, never a dispatch.
	Advisor      string `toml:"advisor"`
	AdvisorSkill string `toml:"advisor_skill"`
}

// recoverRef is `{ step = "in_progress", from = ["integration", "release"] }`.
type recoverRef struct {
	Step string   `toml:"step"`
	From []string `toml:"from"`
}

// adviseRef is a step's `advise = { agent = "…", skill = "…" }`.
type adviseRef struct {
	Agent string `toml:"agent"`
	Skill string `toml:"skill"`
}

// tagObligation is `[[category.tag_obligation]]` — an obligation appended when
// the story carries the tag.
type tagObligation struct {
	Tag        string `toml:"tag"`
	Obligation string `toml:"obligation"`
}

type categoryWire struct {
	Obligations    []string        `toml:"obligations"`
	Park           *roleRef        `toml:"park"`
	Cancel         *roleRef        `toml:"cancel"`
	Recover        *recoverRef     `toml:"recover"`
	TagObligations []tagObligation `toml:"tag_obligation"`
}

type stepWire struct {
	// Status is the STAGE NAME — the status an item holds while in this step.
	// It is a field, not the key, because stage names repeat by design: this
	// repo's catalogue declares `done` four times and `in_progress` three, one
	// per route family. The KEY is the obligation, which is the step's actual
	// identity and is unique by construction — BuildRoute already refuses two
	// steps providing the same obligation in one route (sty_81bb0dde).
	Status        string   `toml:"status"`
	Agent         string   `toml:"agent"`
	Skills        []string `toml:"skills"`
	Reviewers     []string `toml:"reviewers"`
	ReviewerAgent string   `toml:"reviewer_agent"`
	// Parallel is a POINTER on purpose: TOML's zero value is indistinguishable
	// from an absent key, and an authored `parallel = 0` (serial) must not read
	// as "unset" (which defaults to DefaultParallelCap concurrent). This is the
	// old ParallelSet bool, preserved deliberately.
	Parallel  *int       `toml:"parallel"`
	Provides  string     `toml:"provides"`
	Requires  []string   `toml:"requires"`
	AppliesTo []string   `toml:"applies_to"`
	Advise    *adviseRef `toml:"advise"`
	Start     bool       `toml:"start"`
	Terminal  bool       `toml:"terminal"`
}

type gateWire struct {
	Skill     string   `toml:"skill"`
	Agent     string   `toml:"agent"`
	On        []string `toml:"on"`
	AppliesTo []string `toml:"applies_to"`
	For       []string `toml:"for"`
	Mandatory bool     `toml:"mandatory"`
}

// recordsOf splits a route-source body into its top-level tables: the reserved
// ones (meta, and gate in a step catalogue) and everything else, which IS the
// record set — keyed by the record's own identity, the way agents.toml keys a
// binding by its section name.
//
// Two-pass, because that keying is what the format is FOR: the key is data, so
// no fixed struct can name it. Pass one takes every top-level table as an
// undecoded Primitive; pass two decodes each into its wire struct. BurntSushi
// tracks decoded keys across PrimitiveDecode, so Undecoded() at the end still
// catches an unknown key INSIDE a record — the strictness that stops a typo'd
// `reviewrs =` from silently costing a gate.
func recordsOf(body, role string, reserved ...string) (recs, resv map[string]toml.Primitive, md toml.MetaData, err error) {
	var raw map[string]toml.Primitive
	md, err = toml.Decode(body, &raw)
	if err != nil {
		return nil, nil, md, fmt.Errorf("%s: %w", role, err)
	}
	skip := map[string]bool{metaKey: true}
	for _, r := range reserved {
		skip[r] = true
	}
	recs = make(map[string]toml.Primitive, len(raw))
	resv = make(map[string]toml.Primitive, len(skip))
	for k, v := range raw {
		if skip[k] {
			resv[k] = v
			continue
		}
		recs[k] = v
	}
	return recs, resv, md, nil
}

// metaKey is the document header table. It is not a record: its keys vary by
// kind and the frontmatter reader owns them, so the strict unknown-key pass must
// never report on it.
const metaKey = "meta"

// sortedKeys keeps record order deterministic. Route ORDER is derived from
// requires/provides, never from declaration order, so this is about stable
// output and reproducible errors — not about the route.
func sortedKeys(m map[string]toml.Primitive) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// undecodedErr reports any key no wire struct claimed, across the whole file —
// except under [meta].
//
// The [meta] exemption is by KEY PATH, not by decoding the table into a sink.
// The sink was the first attempt and it silently half-worked: decoding into a
// `map[string]any` leaves the elements of an array-of-tables unmarked, so the
// moment a route source declared its create hook the block way — `[[meta.hooks]]`
// — every field inside it was reported as an unknown key and the whole file
// failed to parse (sty_81bb0dde). A route source that declares a lifecycle hook
// is ordinary substrate, so that was a refusal to read a legal file.
func undecodedErr(md toml.MetaData, role string) error {
	un := md.Undecoded()
	keys := make([]string, 0, len(un))
	for _, k := range un {
		if len(k) > 0 && k[0] == metaKey {
			continue
		}
		keys = append(keys, k.String())
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return fmt.Errorf("%s: unknown key(s): %s", role, strings.Join(keys, ", "))
}

// ParseDone reads a declaration of done. One TABLE per story category, keyed by
// the category name — `["*"]` for the wildcard, quoted because `*` is not a bare
// key:
//
//	[meta]
//	name = "done"
//
//	[feature]
//	obligations = ["raised", "planned", "coded"]
//	park    = { state = "blocked", gate = "satelle-story-blocked-review" }
//	cancel  = { state = "cancelled", gate = "satelle-story-cancel-review" }
//	recover = { step = "in_progress", from = ["integration"] }
//
//	[[feature.tag_obligation]]
//	tag = "surface:ui"
//	obligation = "design-reviewed"
//
// A duplicate category is a TOML decode error rather than something Validate has
// to catch, which is the point of keying by identity.
func ParseDone(body string) ([]List, error) {
	recs, _, md, err := recordsOf(body, "done.toml")
	if err != nil {
		return nil, err
	}
	out := make([]List, 0, len(recs))
	for _, name := range sortedKeys(recs) {
		var c categoryWire
		if err := md.PrimitiveDecode(recs[name], &c); err != nil {
			return nil, fmt.Errorf("done.toml: category %q: %w", name, err)
		}
		l := List{Category: name, Obligations: c.Obligations}
		if c.Park != nil {
			l.Park, l.ParkGate = c.Park.State, c.Park.Gate
			l.ParkAdvisor, l.ParkAdvisorSkill = c.Park.Advisor, c.Park.AdvisorSkill
		}
		if c.Cancel != nil {
			l.Cancel, l.CancelGate = c.Cancel.State, c.Cancel.Gate
		}
		if c.Recover != nil {
			l.Recover, l.RecoverFrom = c.Recover.Step, c.Recover.From
		}
		for _, t := range c.TagObligations {
			if t.Tag == "" || t.Obligation == "" {
				return nil, fmt.Errorf("done.toml: [[%s.tag_obligation]] needs both tag and obligation", name)
			}
			if l.TagObligations == nil {
				l.TagObligations = map[string][]string{}
			}
			l.TagObligations[t.Tag] = append(l.TagObligations[t.Tag], t.Obligation)
		}
		out = append(out, l)
	}
	if err := undecodedErr(md, "done.toml"); err != nil {
		return nil, err
	}
	return out, nil
}

// ParseSteps reads a step catalogue. One TABLE per step, keyed by the OBLIGATION
// it discharges, plus `[[gate]]` entries for the always-on gates:
//
//	[coded]
//	status = "in_progress"
//	agent = "executor"
//	skills = ["code"]
//	requires = ["planned"]
//
//	[[gate]]
//	skill = "satelle-build-unit-check"
//	on = ["integration"]
//
// Keyed by obligation, NOT by stage name: `done` is four different steps in this
// repo's catalogue and `in_progress` is three, because different route families
// reach the same status by discharging different obligations. The obligation is
// the identity, and it is unique by construction.
//
// Gates stay an ARRAY, and that is not an inconsistency: the same skill
// legitimately repeats under different scope — `satelle-step-summary` runs on
// one binding for the spine and another for the container lanes. A gate has no
// unique identity to key on.
func ParseSteps(body string) (Catalogue, error) {
	recs, resv, md, err := recordsOf(body, "step.toml", "gate")
	if err != nil {
		return Catalogue{}, err
	}
	var cat Catalogue
	for _, provides := range sortedKeys(recs) {
		var s stepWire
		if err := md.PrimitiveDecode(recs[provides], &s); err != nil {
			return Catalogue{}, fmt.Errorf("step.toml: step %q: %w", provides, err)
		}
		if strings.TrimSpace(s.Status) == "" {
			return Catalogue{}, fmt.Errorf("step.toml: step %q has no status (the stage name an item holds here)", provides)
		}
		st := Step{
			Name:          s.Status,
			Provides:      provides,
			Agent:         s.Agent,
			Skills:        s.Skills,
			Reviewers:     s.Reviewers,
			ReviewerAgent: s.ReviewerAgent,
			Requires:      s.Requires,
			AppliesTo:     s.AppliesTo,
			Start:         s.Start,
			Terminal:      s.Terminal,
		}
		if s.Parallel != nil {
			st.Parallel, st.ParallelSet = *s.Parallel, true
		}
		if s.Advise != nil {
			st.Advisor, st.AdvisorSkill = s.Advise.Agent, s.Advise.Skill
		}
		cat.Steps = append(cat.Steps, st)
	}
	var gates []gateWire
	if g, ok := resv["gate"]; ok {
		if err := md.PrimitiveDecode(g, &gates); err != nil {
			return Catalogue{}, fmt.Errorf("step.toml: [[gate]]: %w", err)
		}
	}
	for i, g := range gates {
		if strings.TrimSpace(g.Skill) == "" {
			return Catalogue{}, fmt.Errorf("step.toml: [[gate]] #%d has no skill", i+1)
		}
		cat.Gates = append(cat.Gates, RouteGate{
			Skill: g.Skill, Agent: g.Agent, On: g.On,
			AppliesTo: g.AppliesTo, For: g.For, Mandatory: g.Mandatory,
		})
	}
	if err := undecodedErr(md, "step.toml"); err != nil {
		return Catalogue{}, err
	}
	return cat, nil
}

// ParseRoute is the single entry point: the two authored bodies plus a category
// and the story's tags, in; a Spec, out. An unknown category is an error rather
// than an empty route — silently producing a workflow with no gates is the worst
// available failure.
func ParseRoute(doneBody, stepBody, category string, tags []string) (Spec, error) {
	lists, err := ParseDone(doneBody)
	if err != nil {
		return Spec{}, err
	}
	cat, err := ParseSteps(stepBody)
	if err != nil {
		return Spec{}, err
	}
	l, err := ListFor(lists, category)
	if err != nil {
		return Spec{}, err
	}
	return BuildRoute(l, cat, tags)
}

// WildcardCategory is the declaration of done that governs any category with no
// section of its own — the route-source spelling of a workflow's
// `applies_to: ["*"]`.
const WildcardCategory = "*"

// ListFor picks the declaration of done governing a category: an exact entry
// first, then the wildcard. That is the same precedence a workflow's applies_to
// already gets (a category-specific workflow beats a wildcard one), so the two
// front doors agree rather than each having a rule of its own.
//
// A category with neither is an ERROR, not an empty route. Enumerating every
// category a repo will ever use is brittle — the next new one would silently
// resolve to a workflow with no gates, which is the worst failure available. The
// wildcard is how a repo says "this route governs everything else"; its absence
// is how it says "refuse".
func ListFor(lists []List, category string) (List, error) {
	var wild *List
	for i := range lists {
		switch lists[i].Category {
		case category:
			return lists[i], nil
		case WildcardCategory:
			wild = &lists[i]
		}
	}
	if wild != nil {
		return *wild, nil
	}
	var known []string
	for _, l := range lists {
		known = append(known, l.Category)
	}
	return List{}, fmt.Errorf("no declaration of done for category %q and no %q section (have: %s)",
		category, WildcardCategory, strings.Join(known, ", "))
}
