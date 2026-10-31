package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// Operational data is pushed append-only (sty_52b93691): an execution is never
// held back. These tests fake only the hosted server, which has no hold surface
// (sty_40853d02) — any hold route the push dialled would fail the test.

const (
	appendOnlyToml = "[sync]\nhold_unpushed = false\n\n[hosted]\nproject = \"probe\"\n"
	// holdBackToml leaves hold_unpushed at its default, so held-back
	// classification runs over the batch.
	holdBackToml = "[sync]\n\n[hosted]\nproject = \"probe\"\n"
)

func createExecutionID(t *testing.T, title string) string {
	t.Helper()
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	it, err := db.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindExecution, Title: title}, time.Now())
	if err != nil {
		t.Fatalf("create execution %q: %v", title, err)
	}
	return it.ID
}

// An execution is delivered by a push, with no skip line.
func TestPushDeliversAnExecution(t *testing.T) {
	fx := newHoldFixture(t, appendOnlyToml)
	exe := createExecutionID(t, "run")

	out := fx.push(t)

	if !fx.f.hasItem("probe", exe) {
		t.Fatalf("the execution was not delivered:\n%s", out)
	}
	if strings.Contains(out, "skip "+exe) || strings.Contains(out, "held elsewhere") {
		t.Errorf("an execution must not be reported skipped:\n%s", out)
	}
}

// With held-back classification on, an execution is neither held back nor
// re-checked: it is delivered.
func TestPushNeverHoldsBackAnExecution(t *testing.T) {
	fx := newHoldFixture(t, holdBackToml)
	exe := createExecutionID(t, "run")

	out := fx.push(t)

	if !fx.f.hasItem("probe", exe) {
		t.Fatalf("the execution was not delivered:\n%s", out)
	}
	if strings.Contains(out, "held "+exe) {
		t.Errorf("an execution was held back:\n%s", out)
	}
}
