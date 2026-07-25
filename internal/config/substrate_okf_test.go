package config

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/structure"
)

// Manifest tests: which audit tasks/skills MUST ship. A table over what exists
// cannot assert what SHOULD exist — that is a curated manifest (sty_6830e78e AC6).
// Substring/repo-agnostic pins retired into Tier 1 conformance + dogfood waiver.

// TestEmbeddedSubstrateAuditTask asserts the embedded default tasks kind carries
// the repo-agnostic substrate-audit task (sty_d4360e90): EmbeddedDefaults surfaces
// it and it passes structure.CheckTask.
func TestEmbeddedSubstrateAuditTask(t *testing.T) {
	var got *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "tasks" && d.Name == "tsk_substrate-audit" {
			dd := d
			got = &dd
			break
		}
	}
	if got == nil {
		t.Fatal("EmbeddedDefaults does not surface tasks/tsk_substrate-audit")
	}
	if problems := structure.CheckTask(got.Body); len(problems) > 0 {
		t.Errorf("embedded audit task fails CheckTask: %v", problems)
	}
	// repo-agnostic path ban is Tier 1 banned-path + dogfood rows; keep only
	// the internal/config/substrate ban as a property tables express via the
	// general dogfood/path rules once empty — until then assert here with comment:
	// Surviving pin: audit task must not name the embed source tree (dev-only path).
	// Tier 1 has no general "no internal/ paths" row; this protects that invariant.
	if strings.Contains(got.Body, "internal/config/substrate") {
		t.Errorf("embedded audit task is not repo-agnostic (references internal/config/substrate)")
	}
}

// TestEmbeddedContextAuditTask asserts the embedded context-audit task + skill
// ship as repo-agnostic defaults (epic order:8).
func TestEmbeddedContextAuditTask(t *testing.T) {
	var got *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "tasks" && d.Name == "tsk_context-audit" {
			dd := d
			got = &dd
			break
		}
	}
	if got == nil {
		t.Fatal("EmbeddedDefaults does not surface tasks/tsk_context-audit")
	}
	if problems := structure.CheckTask(got.Body); len(problems) > 0 {
		t.Errorf("embedded context-audit task fails CheckTask: %v", problems)
	}
	// Surviving pin: audit task must not name the embed source tree.
	if strings.Contains(got.Body, "internal/config/substrate") {
		t.Errorf("embedded context-audit task is not repo-agnostic (references internal/config/substrate)")
	}
	var skill *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-context-audit" {
			dd := d
			skill = &dd
			break
		}
	}
	if skill == nil {
		t.Fatal("EmbeddedDefaults does not surface skills/satelle-context-audit")
	}
	// Surviving pin: skill must name the two CLI surfaces it audits — not a
	// corpus-wide property the conformance table can express without hardcoding
	// this skill's contract.
	if !strings.Contains(skill.Body, "satelle hook context") || !strings.Contains(skill.Body, "principle validate") {
		t.Error("context-audit skill must reference hook context + principle validate")
	}
}

// TestEmbeddedReviewerObjectiveAuditTask asserts the reviewer-objective-audit
// task + skill ship as embedded defaults.
func TestEmbeddedReviewerObjectiveAuditTask(t *testing.T) {
	var got *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "tasks" && d.Name == "tsk_reviewer-objective-audit" {
			dd := d
			got = &dd
			break
		}
	}
	if got == nil {
		t.Fatal("EmbeddedDefaults does not surface tasks/tsk_reviewer-objective-audit")
	}
	if problems := structure.CheckTask(got.Body); len(problems) > 0 {
		t.Errorf("embedded reviewer-objective-audit task fails CheckTask: %v", problems)
	}
	if strings.Contains(got.Body, "internal/config/substrate") {
		t.Errorf("embedded reviewer-objective-audit task is not repo-agnostic (references internal/config/substrate)")
	}
	var skill *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-reviewer-objective-audit" {
			dd := d
			skill = &dd
			break
		}
	}
	if skill == nil {
		t.Fatal("EmbeddedDefaults does not surface skills/satelle-reviewer-objective-audit")
	}
	// Surviving pin: primary-objective phrasing is this skill's rubric identity;
	// the conformance table does not assert per-skill prose contracts.
	if !strings.Contains(skill.Body, "Given what was presented") {
		t.Error("embedded skill missing primary-objective phrasing")
	}
}
