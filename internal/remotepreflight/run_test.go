package remotepreflight

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/remotedef"
)

// fakeHost executes nothing; it answers each argv by its first word.
type fakeHost struct {
	exit   map[string]int
	stdout map[string]string
	err    map[string]error
	ran    []string
}

func (f *fakeHost) Exec(_ context.Context, argv []string) (Outcome, error) {
	f.ran = append(f.ran, strings.Join(argv, " "))
	if err := f.err[argv[0]]; err != nil {
		return Outcome{}, err
	}
	return Outcome{Ran: true, Exit: f.exit[argv[0]], Stdout: f.stdout[argv[0]]}, nil
}

func probeDef(expect string, argv ...string) remotedef.Definition {
	return remotedef.Definition{Steps: []remotedef.Step{{
		Name: "in_progress", Probes: []remotedef.Probe{{Argv: argv, Expect: expect}},
	}}}
}

// The stub host runs nothing: each probe is reported as not run and passes, in
// definition order.
func TestStubHostRunsNothing(t *testing.T) {
	def := remotedef.Definition{Steps: []remotedef.Step{
		{Name: "a", Probes: []remotedef.Probe{{Argv: []string{"one"}, Expect: remotedef.ExpectRefused}, {Argv: []string{"two"}, Expect: remotedef.ExpectSucceed}}},
		{Name: "b", Probes: []remotedef.Probe{{Argv: []string{"three"}, Expect: remotedef.ExpectSucceed}}},
	}}
	res := Run(context.Background(), def, StubHost{})
	if !res.Passed || len(res.Checks) != 3 {
		t.Fatalf("result = %+v", res)
	}
	for i, want := range []string{"one", "two", "three"} {
		c := res.Checks[i]
		if c.Argv[0] != want || c.Outcome != OutcomeNotRun || c.Detail != "not run: stub host" || !c.Pass {
			t.Errorf("check %d = %+v", i, c)
		}
	}
}

func TestOutcomeAgainstExpectation(t *testing.T) {
	cases := []struct {
		expect string
		exit   int
		pass   bool
	}{
		{remotedef.ExpectSucceed, 0, true},
		{remotedef.ExpectRefused, 1, true},
		{remotedef.ExpectSucceed, 1, false},
		{remotedef.ExpectRefused, 0, false},
	}
	for _, tc := range cases {
		h := &fakeHost{exit: map[string]int{"probe": tc.exit}}
		res := Run(context.Background(), probeDef(tc.expect, "probe"), h)
		if res.Passed != tc.pass || res.Checks[0].Pass != tc.pass {
			t.Errorf("expect %s, exit %d: result = %+v, want pass=%v", tc.expect, tc.exit, res, tc.pass)
		}
		if !tc.pass && res.Checks[0].Outcome != OutcomeFail {
			t.Errorf("a mismatch must be outcome fail, got %q", res.Checks[0].Outcome)
		}
	}
}

func TestRunnerErrorIsAFailure(t *testing.T) {
	h := &fakeHost{err: map[string]error{"probe": errors.New("no such host")}}
	res := Run(context.Background(), probeDef(remotedef.ExpectRefused, "probe"), h)
	c := res.Checks[0]
	if res.Passed || c.Outcome != OutcomeError || !strings.Contains(c.Detail, "no such host") {
		t.Errorf("result = %+v", res)
	}
}

// A step holding data, untrusted-input and outbound legs fails; two of three, or
// none, pass. The rule is pure definition data, so the stub host sees it too.
func TestStepHoldingAllThreeLegsFails(t *testing.T) {
	def := remotedef.Definition{Steps: []remotedef.Step{
		{Name: "all", Legs: []string{remotedef.LegData, remotedef.LegUntrusted, remotedef.LegOutbound}},
		{Name: "two", Legs: []string{remotedef.LegData, remotedef.LegOutbound}},
		{Name: "none"},
	}}
	res := Run(context.Background(), def, StubHost{})
	if res.Passed || len(failures(res)) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if c := failures(res)[0]; c.Kind != KindLegs || c.Step != "all" {
		t.Errorf("check = %+v", c)
	}
}
