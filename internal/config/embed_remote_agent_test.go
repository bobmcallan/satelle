package config_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// The shipped default workflows opt nobody into remote placement
// (sty_dde8b6a4): remote_agent and local_tags are a repo's own configuration, so
// a repo that does not opt in behaves exactly as before. Checked on the raw
// step catalogue and on every category's derived route.
func TestEmbeddedWorkflowsDeclareNoRemoteAgent(t *testing.T) {
	var done, step string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "workflows" {
			continue
		}
		switch d.Name {
		case "done":
			done = d.Body
		case "step":
			step = d.Body
		}
	}
	for _, key := range []string{"remote_agent", "local_tags"} {
		if strings.Contains(step, key) {
			t.Errorf("the embedded step catalogue must not declare %s", key)
		}
	}
	lists, err := wfdot.ParseDone(done)
	if err != nil {
		t.Fatalf("parse embedded done: %v", err)
	}
	if len(lists) == 0 {
		t.Fatal("the embedded route declares no category")
	}
	for _, l := range lists {
		for _, st := range embeddedRoute(t, l.Category).States {
			if st.RemoteAgent != "" || len(st.LocalTags) > 0 {
				t.Errorf("category %q step %q declares remote placement (%q, %v)", l.Category, st.Name, st.RemoteAgent, st.LocalTags)
			}
		}
	}
}
