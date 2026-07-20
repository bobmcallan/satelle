package agentstep

import (
	"context"
	"errors"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

func TestIsRateLimitOrUnavailable(t *testing.T) {
	if !IsRateLimitOrUnavailable(errors.New("HTTP 429 rate limit exceeded"), nil) {
		t.Fatal("want rate limit")
	}
	if !IsRateLimitOrUnavailable(nil, []byte("overloaded, please retry")) {
		t.Fatal("want overloaded in stdout")
	}
	if IsRateLimitOrUnavailable(errors.New("syntax error in skill"), nil) {
		t.Fatal("must not classify ordinary errors")
	}
}

type stubRunner struct {
	calls int
	fail  bool
	out   string
	err   error
}

func (s *stubRunner) Name() string    { return "stub" }
func (s *stubRunner) Command() string { return "stub" }
func (s *stubRunner) Run(ctx context.Context, req agentcli.Request) ([]byte, error) {
	s.calls++
	if s.fail {
		return []byte("rate limit exceeded"), errors.New("rate limit exceeded")
	}
	if s.err != nil {
		return nil, s.err
	}
	return []byte(s.out), nil
}

func TestInvokeSecondaryOnRateLimit(t *testing.T) {
	primary := &stubRunner{fail: true}
	secondary := &stubRunner{out: "done"}
	g := New(primary, nil, t.TempDir(), "")
	// Avoid principles/docs path.
	g.SetInjectPrinciples(false)
	g.SetSecondaryResolver(func(section string, b config.AgentBinding) (config.AgentBinding, string, bool) {
		return config.AgentBinding{
			Command:    "secondary-cmd",
			Role:       "agent",
			Principles: config.PrinciplesNone,
		}, "secondary", true
	})
	g.newRunner = func(iface, command string) (agentcli.Runner, error) {
		if command == "secondary-cmd" {
			return secondary, nil
		}
		return primary, nil
	}
	res := g.Invoke(context.Background(), InvokeRequest{
		Section: "reviewer",
		Binding: config.AgentBinding{
			Command:    "primary-cmd",
			Role:       "agent",
			Principles: config.PrinciplesNone,
			Secondary:  "secondary",
		},
		Expect: ExpectPerform,
		Runner: primary,
	})
	if res.Err != nil {
		t.Fatalf("secondary should succeed: %v", res.Err)
	}
	if secondary.calls < 1 {
		t.Fatalf("secondary not invoked; primary=%d secondary=%d", primary.calls, secondary.calls)
	}
	if primary.calls < 1 {
		t.Fatal("primary should have been attempted first")
	}
}

func TestInvokeNoSecondaryOnOrdinaryError(t *testing.T) {
	primary := &stubRunner{err: errors.New("parse failure")}
	secondary := &stubRunner{out: "should-not-run"}
	g := New(primary, nil, t.TempDir(), "")
	g.SetInjectPrinciples(false)
	g.SetSecondaryResolver(func(section string, b config.AgentBinding) (config.AgentBinding, string, bool) {
		return config.AgentBinding{Command: "secondary-cmd", Role: "agent", Principles: config.PrinciplesNone}, "secondary", true
	})
	g.newRunner = func(iface, command string) (agentcli.Runner, error) {
		if command == "secondary-cmd" {
			return secondary, nil
		}
		return primary, nil
	}
	res := g.Invoke(context.Background(), InvokeRequest{
		Section: "reviewer",
		Binding: config.AgentBinding{Command: "primary-cmd", Role: "agent", Principles: config.PrinciplesNone, Secondary: "secondary"},
		Expect:  ExpectPerform,
		Runner:  primary,
	})
	if res.Err == nil {
		t.Fatal("expected primary error")
	}
	if secondary.calls != 0 {
		t.Fatalf("secondary must not run for non-rate-limit errors, calls=%d", secondary.calls)
	}
}
