package wfgovern_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_41f61669 AC3: the version of the governing workflow is a content hash — on
// the derived route over both halves, on a legacy doc its own hash, empty when
// nothing governs.

func TestRouteVersionDerivedRouteHashesBothHalves(t *testing.T) {
	item := workitem.Item{ID: "sty_1", Category: "feature"}
	base := wfgovern.RouteVersion(routeDocs(false), item)
	if len(base) != 64 {
		t.Fatalf("derived route version = %q, want a sha256 hex", base)
	}
	if again := wfgovern.RouteVersion(routeDocs(false), item); again != base {
		t.Errorf("version is not stable: %q then %q", base, again)
	}

	changed := routeDocs(false)
	changed[1].Body = strings.Replace(changed[1].Body, `reviewers = ["done-review"]`, `reviewers = ["done-review", "extra-review"]`, 1)
	if got := wfgovern.RouteVersion(changed, item); got == base {
		t.Error("editing step.toml must change the version")
	}
	changed = routeDocs(false)
	changed[0].Body = strings.Replace(changed[0].Body, `"coded", "closed"`, `"coded", "closed", "coded"`, 1)
	if got := wfgovern.RouteVersion(changed, item); got == base {
		t.Error("editing done.toml must change the version")
	}
}

// A markdown doc whose applies_to is the wildcard governs a legacy repo; with no
// route source in the set the version is that doc's own hash.
func TestRouteVersionLegacyWorkflowIsItsDocHash(t *testing.T) {
	doc := graphDoc()
	doc.Hash = "legacy-hash"
	got := wfgovern.RouteVersion([]docindex.Doc{doc}, workitem.Item{ID: "sty_1", Category: "feature"})
	if got != "legacy-hash" {
		t.Errorf("legacy version = %q, want the doc hash", got)
	}
	doc.Hash = ""
	if got := wfgovern.RouteVersion([]docindex.Doc{doc}, workitem.Item{ID: "sty_1", Category: "feature"}); len(got) != 64 {
		t.Errorf("a doc with no hash falls back to its body hash, got %q", got)
	}
}

func TestRouteVersionEmptyWhenNothingGoverns(t *testing.T) {
	item := workitem.Item{ID: "sty_1", Category: "feature"}
	if got := wfgovern.RouteVersion(nil, item); got != "" {
		t.Errorf("no workflows: version = %q, want empty", got)
	}
	// A route refused as broken is not a resolved workflow.
	broken := routeDocs(false)
	broken[0].Body = "not toml"
	if got := wfgovern.RouteVersion(broken, item); got != "" {
		t.Errorf("a route source that does not parse: version = %q, want empty", got)
	}
}
