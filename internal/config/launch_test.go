package config

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// AC1: every malformed launch declaration is refused at catalog load, with the
// field named and the offending value never echoed.
func TestParseGlobalAgents_LaunchRejections(t *testing.T) {
	// Each body carries a marker value; no message may contain it.
	const marker = "HUNTER2-MARKER"
	cases := []struct {
		name, body, want string
	}{
		{"literal secret", "[profiles.ops.launch]\n[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nsecrets={K=\"" + marker + "\"}\n", "launch.steps[0].secrets.K: literal secret"},
		{"secret with surrounding text", "[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nsecrets={K=\"pre-${S}-" + marker + "\"}\n", "launch.steps[0].secrets.K: literal secret"},
		{"two references in one secret", "[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nsecrets={K=\"${A}${B}" + marker + "\"}\n", "launch.steps[0].secrets.K: literal secret"},
		{"reserved dir as a secret", "[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nsecrets={K=\"${SATELLE_PROFILE_DIR}\"}\n", "SATELLE_PROFILE_DIR is reserved"},
		{"reserved scratch as a secret", "[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nsecrets={K=\"${SATELLE_SCRATCH}\"}\n", "SATELLE_SCRATCH is reserved"},
		{"vars reference in run", "[[profiles.ops.launch.steps]]\nrun=[\"x\", \"--tok=${LEAKY_REF}\"]\n", "launch.steps[0].run[1]: references LEAKY_REF"},
		{"vars reference in step env", "[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nenv={K=\"${LEAKY_REF}\"}\n", "launch.steps[0].env.K: references LEAKY_REF"},
		{"vars reference in set", "[profiles.ops.launch]\nset={K=\"${LEAKY_REF}\"}\n", "launch.set.K: references LEAKY_REF"},
		{"scratch reference in run", "[[profiles.ops.launch.steps]]\nrun=[\"x\", \"${SATELLE_SCRATCH}\"]\n", "launch.steps[0].run[1]: references SATELLE_SCRATCH"},
		{"empty run", "[[profiles.ops.launch.steps]]\nenv={K=\"v\"}\n", "launch.steps[0].run: a command is required"},
		{"blank command", "[[profiles.ops.launch.steps]]\nrun=[\"  \"]\n", "launch.steps[0].run: a command is required"},
		{"newline in run", "[[profiles.ops.launch.steps]]\nrun=[\"x\", \"a\\n" + marker + "\"]\n", "launch.steps[0].run[1]: must be a single line"},
		{"newline in set", "[profiles.ops.launch]\nset={K=\"a\\n" + marker + "\"}\n", "launch.set.K: must be a single line"},
		{"bad env name in set", "[profiles.ops.launch]\nset={\"A B\"=\"" + marker + "\"}\n", "not an environment variable name"},
		{"bad env name in unset", "[profiles.ops.launch]\nunset=[\"A B\"]\n", "launch.unset[0]: not an environment variable name"},
		{"bad secret name", "[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nsecrets={\"A B\"=\"${S}\"}\n", "not an environment variable name"},
		{"key in env and secrets", "[[profiles.ops.launch.steps]]\nrun=[\"x\"]\nenv={K=\"" + marker + "\"}\nsecrets={K=\"${S}\"}\n", "also set under launch.steps[0].env"},
		{"bad profile name", "[profiles.\"a b\".launch]\nset={K=\"v\"}\n", "cannot carry launch steps"},
		{"legacy cloud table", "[profiles.ops.cloud.vendor]\nsecret=\"" + marker + "\"\n", "[profiles.ops.cloud] is the 0.0.672 identity table and is no longer read — declare [profiles.ops.launch] steps instead (satelle help global-agents)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseGlobalAgents(c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
			if strings.Contains(err.Error(), marker) && !strings.Contains(c.want, marker) {
				t.Errorf("error echoes the offending value: %v", err)
			}
		})
	}
}

// AC1: a valid declaration parses and keeps every field.
func TestParseGlobalAgents_LaunchValid(t *testing.T) {
	g := mustCatalog(t, launchCatalog)
	spec := g.Profiles["ops"].Launch
	if spec == nil || len(spec.Steps) != 2 {
		t.Fatalf("launch = %+v, want two steps", spec)
	}
	if spec.Steps[0].Run[0] != "first" || spec.Steps[1].Run[0] != "second" {
		t.Errorf("steps out of order: %+v", spec.Steps)
	}
	if spec.Steps[0].Secrets["TOK"] != "${OPS_TOKEN}" {
		t.Errorf("secret reference not kept unexpanded: %v", spec.Steps[0].Secrets)
	}
	if spec.Set["CFG_DIR"] != "${SATELLE_PROFILE_DIR}/cfg" || len(spec.Unset) != 1 {
		t.Errorf("set/unset not parsed: %+v", spec)
	}
}

// AC1: the 0.0.672 cloud table is also refused on a repo binding, and a repo
// binding cannot carry launch steps at all.
func TestLoadAgents_RepoBindingLaunchRefused(t *testing.T) {
	for _, body := range []string{
		"[executor]\ncommand = \"in-loop\"\n[[executor.launch.steps]]\nrun = [\"x\"]\n",
		"[executor]\ncommand = \"in-loop\"\n[executor.cloud.vendor]\nk = \"v\"\n",
	} {
		_, err := loadAgentsBody(body)
		if err == nil || !strings.Contains(err.Error(), "a repo binding cannot carry launch steps") {
			t.Fatalf("err = %v, want the repo-binding refusal for %q", err, body)
		}
	}
}

// AC1: the in-loop seat's env is inert, so validate warns and names launch.set;
// a dispatched binding, an in-loop seat with no env, and no profile do not warn.
func TestInLoopEnvWarning(t *testing.T) {
	g := mustCatalog(t, launchCatalog+"\n[profiles.withenv]\nrole = \"agent\"\nenv = { ANTHROPIC_BASE_URL = \"https://x.invalid\" }\n")
	cases := []struct {
		name, agents string
		want         bool
	}{
		{"in-loop seat with env", "[executor]\ncommand = \"in-loop\"\nprofile = \"withenv\"\n", true},
		{"in-loop seat without env", "[executor]\ncommand = \"in-loop\"\nprofile = \"ops\"\n", false},
		{"no profile", "[executor]\ncommand = \"in-loop\"\n", false},
		{"seat not in-loop", "[executor]\ncommand = \"claude -p {system}\"\nprofile = \"withenv\"\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := InLoopEnvWarning(effectiveFor(t, g, c.agents), g)
			if c.want {
				if !strings.Contains(got, "launch.set") || !strings.Contains(got, "withenv") {
					t.Fatalf("warning = %q, want it to name the profile and launch.set", got)
				}
				if strings.Contains(got, "x.invalid") {
					t.Errorf("warning echoes an env value: %q", got)
				}
				return
			}
			if got != "" {
				t.Fatalf("warning = %q, want none", got)
			}
		})
	}
}

// AC1: a step's reference to an undefined [vars] entry fails resolution with
// the reference named, not a value.
func TestResolveLaunchProfile_Rejections(t *testing.T) {
	testutil.IsolateHome(t)
	g := mustCatalog(t, launchCatalog)
	if _, ok, err := ResolveLaunchProfile(g, "ops", nil); !ok || err == nil || !strings.Contains(err.Error(), "${OPS_TOKEN}") {
		t.Fatalf("ok=%v err=%v, want ok with an error naming ${OPS_TOKEN}", ok, err)
	}
	if _, ok, err := ResolveLaunchProfile(g, "ops", map[string]string{"OPS_TOKEN": "t"}); !ok || err != nil {
		t.Fatalf("ok=%v err=%v, want a clean resolution", ok, err)
	}
}
