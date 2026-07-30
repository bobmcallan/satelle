//go:build plannerbench

package plannerbench

import (
	"strings"
	"testing"
)

// testDims is a fully-recorded dimension set. Tests mutate one field at a time
// so a failure names exactly the dimension under test.
func testDims(bindingID, fixtureName string, run int) sampleDims {
	return sampleDims{
		StudyID: "study", BindingID: bindingID, Provider: "provider-a", Model: "model-a",
		Effort: "high", EffortClass: "high", Interface: "command",
		Topology: topologyIsolated, ToolPolicy: shippedPolicyName, ToolGrant: "Read,Grep",
		Fixture: fixtureName, ContextBytes: 4096, ContextBucket: "small",
		RunOrder: run, Run: run, HarnessVersion: "plannerbench-schema-2+study-abc",
		Collection: collectionInstrumented,
	}
}

func TestDimsValidateNamesEveryUnrecordedDimension(t *testing.T) {
	if findings := testDims("b", "f", 1).validate(); len(findings) != 0 {
		t.Fatalf("a complete dimension set must validate: %v", findings)
	}
	cases := map[string]func(*sampleDims){
		"provider":            func(d *sampleDims) { d.Provider = "" },
		"model":               func(d *sampleDims) { d.Model = "" },
		"effort":              func(d *sampleDims) { d.Effort = "" },
		"effort_class":        func(d *sampleDims) { d.EffortClass = "" },
		"interface":           func(d *sampleDims) { d.Interface = "" },
		"tool_policy":         func(d *sampleDims) { d.ToolPolicy = "" },
		"tool_grant":          func(d *sampleDims) { d.ToolGrant = "" },
		"fixture":             func(d *sampleDims) { d.Fixture = "" },
		"context_size_bucket": func(d *sampleDims) { d.ContextBucket = "" },
		"context_bytes":       func(d *sampleDims) { d.ContextBytes = 0 },
		"run_order":           func(d *sampleDims) { d.RunOrder = 0 },
		"harness_version":     func(d *sampleDims) { d.HarnessVersion = "" },
	}
	for dimension, blank := range cases {
		dims := testDims("b", "f", 1)
		blank(&dims)
		findings := dims.validate()
		if len(findings) == 0 {
			t.Errorf("%s: a missing dimension must fail validation", dimension)
			continue
		}
		if !strings.Contains(strings.Join(findings, "; "), dimension) {
			t.Errorf("%s: findings do not name the dimension: %v", dimension, findings)
		}
	}
}

func TestDimsRejectUndeclaredTopologyAndCollection(t *testing.T) {
	dims := testDims("b", "f", 1)
	dims.Topology = "hybrid"
	if findings := dims.validate(); len(findings) == 0 ||
		!strings.Contains(findings[0], "topology") {
		t.Fatalf("an undeclared topology must fail: %v", findings)
	}
	dims = testDims("b", "f", 1)
	dims.Collection = "guessed"
	if findings := dims.validate(); len(findings) == 0 ||
		!strings.Contains(strings.Join(findings, ";"), "collection") {
		t.Fatalf("an undeclared collection method must fail: %v", findings)
	}
}

func TestDimensionValueLookupIsClosed(t *testing.T) {
	dims := testDims("b", "f", 2)
	for _, name := range dimensionNames {
		if _, ok := dims.value(name); !ok {
			t.Errorf("declared dimension %q is not readable", name)
		}
	}
	if _, ok := dims.value("vibes"); ok {
		t.Fatal("an unknown dimension must not resolve — a study could then hold a field no record carries")
	}
}

func TestEffortClassPrefersTheDeclaredValueAndNeverGuesses(t *testing.T) {
	if got := effortClassFor("medium", "high"); got != "medium" {
		t.Fatalf("a binding's declared effort_class must win: %q", got)
	}
	for effort, want := range map[string]string{
		"low": "low", "minimal": "low", "medium": "medium", "high": "high", "xhigh": "high",
	} {
		if got := effortClassFor("", effort); got != want {
			t.Errorf("effort %q classed %q, want %q", effort, got, want)
		}
	}
	// An unrecognized effort stays unknown so an unmatched pair lands in
	// Confounded rather than being compared as if the efforts matched.
	if got := effortClassFor("", "turbo"); got != "unknown" {
		t.Fatalf("an unrecognized effort must not be assigned a class: %q", got)
	}
}

func TestBucketForUsesStudyDeclaredThresholds(t *testing.T) {
	buckets := []contextBucket{
		{Name: "large", MaxBytes: 0}, {Name: "small", MaxBytes: 100}, {Name: "medium", MaxBytes: 1000},
	}
	for bytes, want := range map[int]string{1: "small", 100: "small", 101: "medium", 1000: "medium", 1001: "large"} {
		if got := bucketFor(buckets, bytes); got != want {
			t.Errorf("%d bytes bucketed %q, want %q", bytes, got, want)
		}
	}
}

func TestCellKeyIsBindingAndFixture(t *testing.T) {
	if key := testDims("claude-command-opus", "small_cli_flag", 3).cellKey(); key != "claude-command-opus/small_cli_flag" {
		t.Fatalf("cell key = %q", key)
	}
}
