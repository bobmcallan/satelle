package cli

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

// EnvServerEndpoint is the CLI override for [server] endpoint discovery and
// push (sty_5aa08259). The name and its parser live in internal/config so the
// serve-side reconciler honours the same off-switch (sty_e6e467fe).
const EnvServerEndpoint = config.EnvServerEndpoint

// HeaderSatelleInstance is the serve /healthz response header carrying the
// home-derived instance id (sty_5aa08259).
const HeaderSatelleInstance = "X-Satelle-Instance"

// resolveServerEndpointEnv inspects SATELLE_SERVER_ENDPOINT via the shared
// parser. disabled=true means discovery and push must not run. When disabled is
// false and endpoint is non-empty, callers should use it instead of config /
// probe.
func resolveServerEndpointEnv() (endpoint string, disabled bool, set bool) {
	return config.ServerEndpointEnv()
}

// effectiveServerEndpoint merges env override with configured [server] endpoint.
// When the env disables push, returns "" even if config has a value.
func effectiveServerEndpoint(cfgEndpoint string) string {
	if ep, disabled, set := resolveServerEndpointEnv(); set {
		if disabled {
			return ""
		}
		return ep
	}
	return strings.TrimSpace(cfgEndpoint)
}

// healthzInstanceOK reports whether url (…/healthz) answers 200 with body
// containing "ok" and, when wantInstance is non-empty, X-Satelle-Instance
// matches. Missing header when wantInstance is set is a mismatch (fail closed
// for auto-bootstrap — foreign or pre-identity serves are not adopted).
func healthzInstanceOK(url, wantInstance string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(strings.ToLower(string(buf[:n])), "ok") {
		return false
	}
	if wantInstance == "" {
		return true
	}
	got := strings.TrimSpace(resp.Header.Get(HeaderSatelleInstance))
	return got != "" && got == wantInstance
}

// seedSkipNotice is the operator/test-facing message when registration
// succeeded but the mirror was not seeded (exit 0 — sty_5aa08259 AC3).
func seedSkipNotice(reason string) string {
	return fmt.Sprintf("workspace add: registered; seed skipped (%s) — project will not appear on the landing until seeded", reason)
}

// instanceMismatchReason names why auto-bootstrap refused a live serve.
func instanceMismatchReason(candidate, want string) string {
	return fmt.Sprintf(
		"serve at %s did not match this SATELLE_HOME instance (want X-Satelle-Instance=%s) — not auto-adopting a foreign mirror",
		candidate, want)
}
