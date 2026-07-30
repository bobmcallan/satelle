package cli

import "fmt"

// Real server availability, shared by every surface that reports it (sty_fb5e6d96).
//
// The defect this replaces: `satelle status` printed the CONFIGURED port and
// nothing else — a value that reads like confirmation but cannot fail. What a
// user needs is the URL to open plus whether anything is actually answering on
// it, and those two facts must agree wherever satelle states them.
//
// Two reuse rules hold this file to one small increment:
//
//   - Port resolution goes through servicePort / servicePortResolved (the global
//     service port, cmd_update.go) — the port the service binds and the URL
//     `satelle service status` already prints. The repo overlay's
//     Config.ResolveWebPort stays what it is: `satelle serve`'s port. No third
//     resolver.
//   - Reachability goes through the existing healthzOK seam
//     (runtime_liveness.go). No second probe, no new HTTP client. A single port
//     is probed — the one whose URL is shown — so "live" can never refer to a
//     different port than the link.

// webAvailability is the answer to "where is the server and is it up".
// Resolved is false when the global config could not be read at all, which is
// the only honest "unknown": it is never rendered as live.
type webAvailability struct {
	Port     int
	Live     bool
	Resolved bool
}

// healthzURL is the loopback probe target for port. Probing is always 127.0.0.1
// (what defaultHealthzOK dials); the human-facing URL uses localhost.
func healthzURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
}

// probeWebAvailability resolves the user-facing port and probes it once.
func probeWebAvailability() webAvailability {
	port, ok := servicePortResolved()
	if !ok || port <= 0 {
		return webAvailability{Port: port}
	}
	return webAvailability{Port: port, Live: healthzOK(healthzURL(port)), Resolved: true}
}

// URL is the address a user opens.
func (a webAvailability) URL() string {
	return fmt.Sprintf("http://localhost:%d", a.Port)
}

// statusValue is the value column of `satelle status`'s web-service row. Live
// and configured-but-dead differ by token, never by port alone.
func (a webAvailability) statusValue() string {
	if !a.Resolved {
		return "unknown — global config unreadable"
	}
	if a.Live {
		return a.URL() + "  live"
	}
	return a.URL() + "  not answering"
}

// hookLine is the single SessionStart availability line. One line, because it
// is always-resident context paid for on every session start in every harness.
func (a webAvailability) hookLine() string {
	if !a.Resolved {
		return "satelle web service: availability unknown (global config unreadable)"
	}
	if a.Live {
		return "satelle web service: " + a.URL() + " — live"
	}
	return "satelle web service: " + a.URL() + " — not answering (start it: `satelle service start`)"
}
