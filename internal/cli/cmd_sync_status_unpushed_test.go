package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Tests for sty_1481ac31: an area is in step only when its files equal its base
// and the base is the hosted snapshot. A change since the last push is unpushed,
// and a base no push or pull recorded is diverged — each naming its files, in
// `sync status` and in doctor.

// wantFinding returns the machine's one doctor finding with the id, failing
// unless its detail names every want.
func (m *snapMachine) wantFinding(id string, want ...string) health.Finding {
	m.t.Helper()
	var got []health.Finding
	for _, x := range m.substrateFindings() {
		if x.ID == id {
			got = append(got, x)
		}
	}
	if len(got) != 1 {
		m.t.Fatalf("want one %s finding, got %v", id, got)
	}
	for _, w := range want {
		if !strings.Contains(got[0].Detail, w) {
			m.t.Errorf("%s finding %q does not name %q", id, got[0].Detail, w)
		}
	}
	return got[0]
}

// wantUnpushed asserts status reports area unpushed at equal snapshot numbers,
// naming path, and records it with the path.
func (m *snapMachine) wantUnpushed(area, path string) {
	m.t.Helper()
	out, err := m.status()
	if err == nil {
		m.t.Fatalf("an unpushed change must exit non-zero:\n%s", out)
	}
	cols := wantState(m.t, out, area, snapsync.StateUnpushed)
	if cols[2] == "-" || cols[2] != cols[3] || cols[4] != "satelle sync push" {
		m.t.Errorf("%s row = %v, want equal snapshot numbers and NEXT satelle sync push", area, cols)
	}
	if !strings.Contains(out, area+": unpushed — "+path) {
		m.t.Errorf("status does not name %s:\n%s", path, out)
	}
	if rec := m.recorded()[area]; rec.State != snapsync.StateUnpushed || !reflect.DeepEqual(rec.Paths, []string{path}) {
		m.t.Errorf("recorded %s = %+v, want unpushed naming %s", area, rec, path)
	}
}

// AC1, AC2: a plain edit since the last push.
func TestSyncStatusUnpushedNamesTheEditedFile(t *testing.T) {
	testutil.IsolateHome(t)
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	a := publishedMachine(t, ts.URL)
	a.write("skills/x.md", "edited\n")
	a.wantUnpushed("skills", "skills/x.md")
	if f := a.wantFinding(health.IDSyncSubstrateUnpushed, "skills/x.md"); !strings.Contains(f.Remediation, "satelle sync push") {
		t.Errorf("remediation = %q, want the push", f.Remediation)
	}
}

// AC1, AC2: the observed defect. The hosted copy of a path is recorded as a kept
// binary default; the file on disk is then edited and carries a stamp consistent
// with its new body. The push view drops it as an unedited default, but it is not
// the binary's and differs from what was synced, so it is unpushed — and a push
// publishes it.
func TestSyncStatusStampedEditOfAKeptDefaultIsUnpushed(t *testing.T) {
	m, f := tasksMachine(t)
	const path = "tasks/tsk_substrate-audit.md"
	audit := embeddedBodyOf(t, "tasks", "tsk_substrate-audit")
	hostSnapshot(t, f, 0, map[string]string{path: stampedDefault(audit + "\nan older default line\n")})
	m.wantPullQuiet()
	if base, _ := m.base("tasks"); base.Defaults[path] == "" {
		t.Fatalf("test premise: the pull records the hosted copy as a kept default: %+v", base)
	}
	edited := stampedDefault(audit + "\nan operator edit\n")
	m.write(path, edited)

	m.wantUnpushed("tasks", path)
	m.wantFinding(health.IDSyncSubstrateUnpushed, path)

	m.pushConfig()
	raw, _, ok := f.cfg.get("probe", snapsync.RecordPath("tasks"), 0)
	if !ok {
		t.Fatal("no tasks snapshot record")
	}
	rec, err := snapsync.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Files[path] != snapsync.SHA([]byte(edited)) {
		t.Errorf("the push did not publish the edit: %v", rec.Files)
	}
	if body, _, ok := f.cfg.get("probe", path, 0); !ok || string(body) != edited {
		t.Errorf("the hosted head of %s is not the edit", path)
	}
	out, err := m.status()
	if err != nil {
		t.Fatalf("status after the push: %v\n%s", err, out)
	}
	wantState(t, out, "tasks", snapsync.StateInStep)
	m.wantTasksQuiet()
}

// AC4: a base marker set without a push or pull, naming the hosted snapshot while
// its files are not that snapshot's, is diverged naming the file — in status, in
// the push refusal and in doctor — never in step or behind.
func TestSyncStatusUnrecordedBaseIsDiverged(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	a.write("skills/x.md", "newer here\n")
	a.adopt("skills", 1, map[string]string{"skills/x.md": snapsync.SHA([]byte("newer here\n"))})

	out, err := a.status()
	if err == nil {
		t.Fatalf("a moved marker must exit non-zero:\n%s", out)
	}
	if cols := wantState(t, out, "skills", snapsync.StateDiverged); !strings.Contains(cols[4], "satelle sync reconcile") {
		t.Errorf("next = %q, want the reconcile", cols[4])
	}
	if !strings.Contains(out, "skills: diverged — skills/x.md") {
		t.Errorf("status does not name the differing file:\n%s", out)
	}
	_, perr := a.tryPushConfig()
	requireReconcileRemedy(t, "moved marker", perr)
	if perr == nil || !strings.Contains(perr.Error(), "skills/x.md") {
		t.Errorf("push refusal = %v, want it to name skills/x.md", perr)
	}
	if f := a.wantFinding(health.IDSyncSubstrateBehind, snapsync.StateDiverged, "skills/x.md"); !strings.Contains(f.Remediation, "satelle sync reconcile") {
		t.Errorf("remediation = %q, want the reconcile", f.Remediation)
	}
}
