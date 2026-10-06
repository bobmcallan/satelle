package wfgovern_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// unreadableDocs is what a loader returns for a workflows dir that exists and
// cannot be read: the sentinel and nothing else — in particular no embedded
// route to govern by.
func unreadableDocs() []docindex.Doc {
	return []docindex.Doc{docindex.UnreadableDoc("workflows", "/repo/.satelle/workflows", errors.New("not a directory"))}
}

// sty_d6e209aa AC1: unreadable is an explicit state of the one precedence rule,
// never read as absent. The refusal names the path, the embedded route that would
// otherwise have governed, and that the story's gates would not be the repo's.
func TestRouteGovernsErrRefusesAnUnreadableAuthoredDir(t *testing.T) {
	rs := wfgovern.RouteSourceOf(unreadableDocs())
	if rs.Present() {
		t.Fatal("an unreadable dir carries no route")
	}
	if !errors.Is(rs.Err(), wfgovern.ErrAuthoredProcessUnreadable) {
		t.Fatalf("RouteSource.Err = %v, want ErrAuthoredProcessUnreadable", rs.Err())
	}
	_, ok, err := wfgovern.RouteGovernsErr(unreadableDocs(), "feature")
	if ok || !errors.Is(err, wfgovern.ErrAuthoredProcessUnreadable) {
		t.Fatalf("RouteGovernsErr = ok %v, err %v; want a refusal", ok, err)
	}
	for _, want := range []string{
		"/repo/.satelle/workflows", "not a directory",
		"embedded default route", "binary-shipped", `lane "default"`,
		"gates would not be the repository's gates",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must carry %q, got %v", want, err)
		}
	}
	// The same state reaches every route resolver — not only RouteGovernsErr.
	item := workitem.Item{ID: "sty_1", Category: "feature"}
	if _, _, _, serr := wfgovern.SpecFor(unreadableDocs(), item); !errors.Is(serr, wfgovern.ErrAuthoredProcessUnreadable) {
		t.Errorf("SpecFor = %v, want ErrAuthoredProcessUnreadable", serr)
	}
	if _, _, rerr := wfgovern.RouteFor(unreadableDocs(), item); !errors.Is(rerr, wfgovern.ErrAuthoredProcessUnreadable) {
		t.Errorf("RouteFor = %v, want ErrAuthoredProcessUnreadable", rerr)
	}
	if errors.Is(err, wfgovern.ErrNoWorkflow) {
		t.Error("unreadable must not collapse into ErrNoWorkflow (a fresh repo)")
	}
}

// The embedded route must not be reachable through an unreadable dir even when
// a caller appends it beside the sentinel: the state wins.
func TestUnreadableBeatsAnEmbeddedRouteInTheSameSet(t *testing.T) {
	docs := append(unreadableDocs(), routeDocs(true)...)
	if _, ok, err := wfgovern.RouteGovernsErr(docs, "feature"); ok || err == nil {
		t.Fatalf("RouteGovernsErr = ok %v, err %v; the unreadable state must win", ok, err)
	}
}

// An absent dir is not unreadable: the embedded backstop governs as before, and
// the provenance helper says the gates are the binary's.
func TestAbsentDirKeepsTheEmbeddedBackstopAndSaysSo(t *testing.T) {
	docs := routeDocs(true)
	if _, ok, err := wfgovern.RouteGovernsErr(docs, "feature"); !ok || err != nil {
		t.Fatalf("RouteGovernsErr = ok %v, err %v; the embedded backstop must govern", ok, err)
	}
	embedded, lane := wfgovern.RouteProvenance(docs, workitem.Item{ID: "sty_1", Category: "feature"})
	if !embedded || lane != wfgovern.DerivedRouteName {
		t.Errorf("RouteProvenance = %v, %q; want the embedded %q lane", embedded, lane, wfgovern.DerivedRouteName)
	}
	msg := wfgovern.AbsentMessage("/repo/.satelle/workflows")
	for _, want := range []string{"/repo/.satelle/workflows", "embedded default route", "binary's defaults, not authored ones"} {
		if !strings.Contains(msg, want) {
			t.Errorf("absent report must carry %q, got %s", want, msg)
		}
	}
}
