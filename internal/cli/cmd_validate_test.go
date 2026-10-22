package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReservedKeepFile(t *testing.T) {
	for _, fn := range []string{"README.md", "index.md", "log.md"} {
		if !reservedKeepFile(fn) {
			t.Errorf("%s should be a reserved keep-file (exempt)", fn)
		}
	}
	for _, fn := range []string{"satelle-story-review.md", "my-skill.md", "anything.md"} {
		if reservedKeepFile(fn) {
			t.Errorf("%s must NOT be exempt — it is an authored doc", fn)
		}
	}
}

const scheduleRouteDone = `["*"]
obligations = ["raised", "ready", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`

func scheduleRouteStep(scheduleLines string) string {
	return `[raised]
status = "backlog"
start = true

[ready]
status = "ready"
waits_on_children = true
` + scheduleLines + `requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["ready"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
}

// The deprecated `schedule` key keeps working, and validate names it deprecated
// in favour of omitting it or setting sequential = true — as a WARN, so the
// verb still exits zero. A route that authors neither prints no schedule warning.
func TestValidateWarnsOnTheDeprecatedScheduleKey(t *testing.T) {
	cases := []struct {
		name, lines string
		warn        bool
	}{
		{"schedule parallel", "schedule = \"parallel\"\n", true},
		{"schedule sequential", "schedule = \"sequential\"\n", true},
		{"omitted", "", false},
		{"sequential true", "sequential = true\n", false},
	}
	for _, tc := range cases {
		for _, verb := range [][]string{{"validate"}, {"workflow", "validate"}} {
			t.Run(tc.name+"/"+strings.Join(verb, " "), func(t *testing.T) {
				repo := tempRepo(t)
				t.Chdir(repo)
				writeRoute(t, filepath.Join(repo, ".satelle", "workflows"), scheduleRouteDone, scheduleRouteStep(tc.lines))
				out, err := runRootIn(t, "", verb...)
				if err != nil {
					t.Fatalf("%v must exit zero (the key still works): %v\n%s", verb, err, out)
				}
				warned := strings.Contains(out, "deprecated")
				if warned != tc.warn {
					t.Fatalf("%v: schedule warning present = %v, want %v:\n%s", verb, warned, tc.warn, out)
				}
				if !tc.warn {
					return
				}
				for _, want := range []string{"WARN", "workflows/step.toml", `step "ready"`, "schedule =", "omit it (parallel is the default)", "sequential = true"} {
					if !strings.Contains(out, want) {
						t.Errorf("%v: warning must contain %q:\n%s", verb, want, out)
					}
				}
			})
		}
	}
}
