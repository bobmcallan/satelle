package agentstep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// composedSkills is what composing a step's skills yields (sty_23ceeca2): the
// rubric text, the manifest of what was delivered (name and content hash, or
// undelivered with the reason), and each delivered skill's declared evidence.
type composedSkills struct {
	Rubric   string
	Manifest []verb.SkillDelivery
	// evidence holds the delivered skills that declare any, in delivery order.
	evidence []skillEvidenceDecl
}

// skillEvidenceDecl is one delivered skill's evidence declaration.
type skillEvidenceDecl struct {
	Skill    string
	Evidence agentartifact.Evidence
}

// Undelivered names the skills that did not resolve.
func (c composedSkills) Undelivered() []string { return verb.UndeliveredSkills(c.Manifest) }

// composeSkills loads and concatenates a step's skill rubrics in order (spine
// first, then augmentations — sty_8225d8a5) and records each skill's delivery.
// It is the ONE composer for every path that hands a step's skills to a
// performer: a local dispatch, a cloud session, an executor-step run and the
// in-loop session. A skill that does not resolve is in the manifest as
// undelivered and absent from the rubric; the caller decides whether that
// refuses (a dispatched performer cannot be handed half a rubric). Any other
// lookup error surfaces.
//
// strip drops each skill's YAML frontmatter and trims it — a cloud session has no
// use for substrate metadata. The recorded hash is the index's hash of the whole
// body either way, so it can be re-checked against the index.
func (g *Engine) composeSkills(ctx context.Context, names []string, strip bool) (composedSkills, error) {
	var out composedSkills
	var parts []string
	for _, name := range names {
		if name == "" {
			continue
		}
		doc, err := g.docs.Get(ctx, "skills", name)
		if err != nil {
			if errors.Is(err, docindex.ErrNotFound) {
				out.Manifest = append(out.Manifest, verb.SkillDelivery{Name: name, Reason: "unresolved: no such skill in the substrate"})
				continue
			}
			return composedSkills{}, err
		}
		hash := doc.Hash
		if hash == "" {
			sum := sha256.Sum256([]byte(doc.Body))
			hash = hex.EncodeToString(sum[:])
		}
		out.Manifest = append(out.Manifest, verb.SkillDelivery{Name: name, Hash: hash, Delivered: true})
		ev, eerr := agentartifact.ParseEvidence(doc.Body)
		if eerr != nil {
			return composedSkills{}, fmt.Errorf("skill %q evidence declaration: %w", name, eerr)
		}
		if ev.Declared() {
			out.evidence = append(out.evidence, skillEvidenceDecl{Skill: name, Evidence: ev})
		}
		body := doc.Body
		if strip {
			body = strings.TrimSpace(stripFrontmatter(body))
		}
		if len(names) > 1 {
			body = "# Skill: " + name + "\n\n" + body
		}
		parts = append(parts, body)
	}
	out.Rubric = strings.Join(parts, "\n\n---\n\n")
	return out, nil
}

// deliverInLoopSkills is DispatchExecutor's answer for a step the driving session
// performs (agent=executor, or a named agent bound in-loop): nothing runs, but the
// step's skills are composed and handed back as a manifest and a rubric the verb
// records and prints on entry (sty_23ceeca2). A step that names no skill returns
// the empty result.
func (g *Engine) deliverInLoopSkills(ctx context.Context, item workitem.Item, toStatus string, perf performerResolution) (verb.DispatchResult, error) {
	if (perf.Kind != verb.PerformerExecutor && perf.Kind != verb.PerformerInLoop) || perf.target == nil {
		return verb.DispatchResult{}, nil
	}
	names := perf.spec.ExecutorSkillsFor(toStatus, item.Tags)
	if len(names) == 0 && perf.target.Skill != "" {
		names = []string{perf.target.Skill}
	}
	if len(names) == 0 {
		return verb.DispatchResult{}, nil
	}
	c, err := g.composeSkills(ctx, names, false)
	if err != nil {
		return verb.DispatchResult{}, err
	}
	res := verb.DispatchResult{
		Kind: perf.Kind, Skills: c.Manifest, InLoopRubric: c.Rubric,
		StrictSkills: g.skillEvidenceMode(ctx) == verb.SkillEvidenceReject,
	}
	for _, d := range c.evidence {
		res.TraceCommands = res.TraceCommands || len(d.Evidence.Commands) > 0
		res.TraceTools = res.TraceTools || len(d.Evidence.Tools) > 0
	}
	return res, nil
}

// undeliveredRefusal is the refusal for a dispatched step whose declared skill
// did not resolve, under the workflow's skill_evidence = reject: the performer
// would run on a rubric missing a piece of its charter, so nothing runs. Under
// the default (record) the step runs and the gap is recorded and shown instead.
// It returns nil when nothing is missing or the setting is record.
func (g *Engine) undeliveredRefusal(ctx context.Context, c composedSkills, wfName, step, agent string) error {
	missing := c.Undelivered()
	if len(missing) == 0 || g.skillEvidenceMode(ctx) != verb.SkillEvidenceReject {
		return nil
	}
	return fmt.Errorf(
		"workflow %q step %q cannot be performed by %q: its declared skill(s) did not resolve at dispatch time — %s (skill_evidence = reject). Author them under .satelle/skills (or embed them), or remove them from the step; the gap is on the invocation row as undelivered",
		wfName, step, agent, strings.Join(missing, ", "))
}

// skillEvidenceMode reads the workflow's `skill_evidence` key: how strictly a
// delivery or evidence gap blocks — verb.SkillEvidenceRecord (the default: the
// gap is recorded) or verb.SkillEvidenceReject (the transition is refused). The
// key lives in the route source's [meta], beside create_review and amend_review.
// An unreadable workflow set or an unrecognised value reads as the default;
// `satelle validate` is what rejects the unrecognised value.
func (g *Engine) skillEvidenceMode(ctx context.Context) string {
	workflows, err := g.docs.List(ctx, "workflows")
	if err != nil {
		return verb.SkillEvidenceRecord
	}
	for _, w := range workflows {
		if !wfgovern.IsRouteSource(w.Name) {
			continue
		}
		lines, ok := docindex.Frontmatter(w.Body)
		if !ok {
			continue
		}
		for _, line := range lines {
			key, val, found := strings.Cut(line, ":")
			if !found || strings.TrimSpace(key) != SkillEvidenceKey {
				continue
			}
			if v := strings.Trim(strings.TrimSpace(val), `"'`); v == verb.SkillEvidenceReject {
				return verb.SkillEvidenceReject
			}
			return verb.SkillEvidenceRecord
		}
	}
	return verb.SkillEvidenceRecord
}

// SkillEvidenceKey is the workflow [meta] key that sets the strictness.
const SkillEvidenceKey = "skill_evidence"
