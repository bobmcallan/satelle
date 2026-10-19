package remotepreflight

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/remotedef"
)

func scanDef(allow ...string) remotedef.Definition {
	return remotedef.Definition{Preflight: remotedef.Preflight{
		EnvProbe: []string{"envp"}, FileProbe: []string{"filep"}, AllowCredentials: allow,
	}}
}

func failures(res Result) []Check {
	var out []Check
	for _, c := range res.Checks {
		if !c.Pass {
			out = append(out, c)
		}
	}
	return out
}

// A credential-shaped variable holding a value fails the preflight and is named;
// the value itself is never recorded. Allowing the name passes it.
func TestEnvCredentialFailsUnlessAllowed(t *testing.T) {
	h := &fakeHost{stdout: map[string]string{"envp": "PATH=/usr/bin\nAPI_TOKEN=abc123\nHOME=/home/u\n"}}
	res := Run(context.Background(), scanDef(), h)
	fs := failures(res)
	if res.Passed || len(fs) != 1 || fs[0].Kind != KindEnv || !strings.Contains(fs[0].Detail, "API_TOKEN") {
		t.Fatalf("result = %+v", res)
	}
	for _, c := range res.Checks {
		if strings.Contains(c.Detail, "abc123") {
			t.Errorf("a check carries the value: %+v", c)
		}
	}

	if res := Run(context.Background(), scanDef("API_TOKEN"), h); !res.Passed {
		t.Errorf("an allowed key must pass, got %+v", res)
	}
}

// A ${VAR} reference is not a value.
func TestEnvReferenceIsNotACredential(t *testing.T) {
	h := &fakeHost{stdout: map[string]string{"envp": "API_TOKEN=${X}\n"}}
	if res := Run(context.Background(), scanDef(), h); !res.Passed {
		t.Errorf("result = %+v", res)
	}
}

func TestFileCredentialFailsUnlessAllowed(t *testing.T) {
	h := &fakeHost{stdout: map[string]string{"filep": "/home/u/.netrc\n"}}
	res := Run(context.Background(), scanDef(), h)
	fs := failures(res)
	if res.Passed || len(fs) != 1 || fs[0].Kind != KindFile || !strings.Contains(fs[0].Detail, "/home/u/.netrc") {
		t.Fatalf("result = %+v", res)
	}
	if res := Run(context.Background(), scanDef("/home/u/.netrc"), h); !res.Passed {
		t.Errorf("an allowed path must pass, got %+v", res)
	}
}

// A scan whose probe exits non-zero found nothing it can vouch for, so it fails.
func TestScanProbeFailureFails(t *testing.T) {
	h := &fakeHost{exit: map[string]int{"envp": 2}}
	if res := Run(context.Background(), scanDef(), h); res.Passed {
		t.Errorf("result = %+v", res)
	}
}

// The stub host runs no scan, and a definition declaring none has nothing to run.
func TestScansNotRunOnStubOrWhenUndeclared(t *testing.T) {
	res := Run(context.Background(), scanDef(), StubHost{})
	if !res.Passed || len(res.Checks) != 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, c := range res.Checks {
		if c.Outcome != OutcomeNotRun || c.Detail != "not run: stub host" {
			t.Errorf("check = %+v", c)
		}
	}
	h := &fakeHost{}
	res = Run(context.Background(), remotedef.Definition{}, h)
	if !res.Passed || len(h.ran) != 0 {
		t.Errorf("result = %+v ran = %v", res, h.ran)
	}
}
