package remotedef

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// parkDef is a remote that serves pushed → merged and parks at blocked.
func parkDef(park ...string) *Definition {
	d := &Definition{Segments: []Segment{{Entry: "pushed", Exit: "merged"}}, ParkStates: park}
	d.Host.StoryCommand = "agent drive {story}"
	return d
}

// leave runs a wake that leaves sty_a at status and returns the recorded hand-backs.
func leave(t *testing.T, def *Definition, local LocalRoute, status string) []Handback {
	t.Helper()
	st := stories(StoryRef{ID: "sty_a", Status: "pushed"})
	run := &stubRunner{st: st, leaveAt: map[string]string{"sty_a": status}}
	rec := &fakeRecorder{}
	if _, _, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1", StoryID: "sty_a"},
		Wiring{Stories: st, Recorder: rec, Runner: run, Local: local}); err != nil {
		t.Fatal(err)
	}
	return rec.handbacks
}

func TestParkHandbackIsAccepted(t *testing.T) {
	local := fakeLocal{"pushed": true, "merged": true, "blocked": true}
	if backs := leave(t, parkDef("blocked"), local, "blocked"); len(backs) != 0 {
		t.Fatalf("a story parked at the remote workflow's park state is a valid hand-back, got %+v", backs)
	}

	st := stories(StoryRef{ID: "sty_a", Status: "blocked"})
	b, failed, err := checkHandback(context.Background(), parkDef("blocked"), Handoff{Story: "sty_a"}, Wiring{Stories: st, Local: local})
	if err != nil || failed || !b.InContract || !b.LocalKnown || b.HoldsSeat {
		t.Fatalf("checkHandback = %+v failed=%v err=%v, want it to pass with InContract true", b, failed, err)
	}
}

func TestOffContractStatusIsStillReported(t *testing.T) {
	local := fakeLocal{"pushed": true, "merged": true, "blocked": true, "review": true}
	for _, c := range []struct {
		name  string
		def   *Definition
		left  string
		fixed string
	}{
		{"a status that is neither exit nor park", parkDef("blocked"), "review", "outside the contract's exit statuses (merged) and its park state (blocked)"},
		{"a park state the workflow does not declare", parkDef(), "blocked", "outside the contract's exit statuses (merged)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			backs := leave(t, c.def, local, c.left)
			if len(backs) != 1 {
				t.Fatalf("want one reported hand-back, got %+v", backs)
			}
			if b := backs[0]; b.Status != c.left || b.InContract || b.Reason != c.fixed {
				t.Errorf("hand-back = %+v, want status %s outside the contract with reason %q", b, c.left, c.fixed)
			}
		})
	}
}

func TestParkHandbackStillHeldToTheLocalRoute(t *testing.T) {
	t.Run("unknown to the local route", func(t *testing.T) {
		backs := leave(t, parkDef("blocked"), fakeLocal{"pushed": true, "merged": true}, "blocked")
		if len(backs) != 1 {
			t.Fatalf("want one reported hand-back, got %+v", backs)
		}
		if b := backs[0]; !b.InContract || b.LocalKnown || !strings.Contains(b.Reason, "not a status of the local default route") {
			t.Errorf("hand-back = %+v, want a park state in the contract but unknown to the local route", b)
		}
	})
	t.Run("seat-holding on the local route", func(t *testing.T) {
		backs := leave(t, parkDef("blocked"), seatLocal{map[string]bool{"blocked": true}}, "blocked")
		if len(backs) != 1 {
			t.Fatalf("want one reported hand-back, got %+v", backs)
		}
		if b := backs[0]; !b.InContract || !b.HoldsSeat || !strings.Contains(b.Reason, "engagement seat") {
			t.Errorf("hand-back = %+v, want a park state in the contract that holds an engagement seat", b)
		}
	})
}

func TestLoadReadsTheWorkflowsParkState(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(DefinitionFile, "satelle_version = \"0.0.1\"\n")
	if def, err := Load(dir); err != nil || len(def.ParkStates) != 0 {
		t.Fatalf("with no done.toml: park states %v, err %v; want none", def.ParkStates, err)
	}
	write(doneFile, "[\"*\"]\nobligations = [\"raised\", \"closed\"]\npark = { state = \"blocked\", advisor = \"triage\" }\n\n[docs]\nobligations = [\"raised\", \"closed\"]\npark = { state = \"blocked\" }\n")
	def, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"blocked"}; !reflect.DeepEqual(def.ParkStates, want) {
		t.Errorf("park states = %v, want %v", def.ParkStates, want)
	}
	if err := def.ReturnsAt("blocked"); err != nil {
		t.Errorf("ReturnsAt(blocked) = %v, want nil for the declared park state", err)
	}
}
