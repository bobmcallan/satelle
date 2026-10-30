package ledger

import (
	"slices"
	"testing"
)

// sty_434d2fd9 AC4: ProgressKinds carries every verdict kind plus the status
// transition, and a status transition is still not a verdict.
func TestProgressKindsCoverVerdictsAndTransitions(t *testing.T) {
	if !slices.Contains(ProgressKinds, KindStatusTransition) {
		t.Errorf("ProgressKinds = %v, want %s in it", ProgressKinds, KindStatusTransition)
	}
	if isVerdictKind(KindStatusTransition) {
		t.Errorf("isVerdictKind(%s) = true, want false", KindStatusTransition)
	}
	for _, kind := range []string{
		KindReviewAccept, KindReviewReject, KindPerformerReject, KindReadinessAccept,
		KindReadinessReuse, KindComment, KindStatusReconcile, KindAgentInvocation,
		KindStoryDocAttached, KindGateSkipped, KindStepSummary, KindDefinitionEdited,
	} {
		if isVerdictKind(kind) && !slices.Contains(ProgressKinds, kind) {
			t.Errorf("verdict kind %s is missing from ProgressKinds %v", kind, ProgressKinds)
		}
	}
}
