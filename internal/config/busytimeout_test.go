package config

import (
	"testing"
	"time"
)

// TestResolveBusyTimeout (sty_db62a3b9 AC4): binding → [defaults] → shipped
// default, "0"/"off" disable, malformed or negative values are errors.
func TestResolveBusyTimeout(t *testing.T) {
	def := DefaultBusyTimeout
	ac := AgentsConfig{Defaults: AgentsDefaults{BusyTimeout: "10m"}}
	cases := []struct {
		name string
		ac   AgentsConfig
		b    AgentBinding
		want time.Duration
	}{
		{"binding wins", ac, AgentBinding{BusyTimeout: "3m"}, 3 * time.Minute},
		{"defaults win over shipped", ac, AgentBinding{}, 10 * time.Minute},
		{"shipped default", AgentsConfig{}, AgentBinding{}, def},
		{"binding off", ac, AgentBinding{BusyTimeout: "off"}, 0},
		{"binding zero", ac, AgentBinding{BusyTimeout: "0"}, 0},
		{"defaults off", AgentsConfig{Defaults: AgentsDefaults{BusyTimeout: "off"}}, AgentBinding{}, 0},
	}
	for _, c := range cases {
		got, err := c.ac.ResolveBusyTimeout(c.b, def)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	for _, bad := range []string{"soon", "-5m"} {
		if _, err := (AgentBinding{BusyTimeout: bad}).BusyTimeoutDuration(def); err == nil {
			t.Errorf("busy_timeout %q must be rejected", bad)
		}
		if err := checkBindingTimeout("agents.toml", "x", AgentBinding{BusyTimeout: bad}); err == nil {
			t.Errorf("load-time validation must reject busy_timeout %q", bad)
		}
	}
}
