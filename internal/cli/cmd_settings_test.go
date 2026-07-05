package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestSettingsServerSetPrintClear proves `satelle settings server` manages the
// global hosted server WITHOUT any login, and never writes a token (sty_432bdeb7).
func TestSettingsServerSetPrintClear(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())

	// Unset → a helpful "not configured" line.
	cmd, buf := testCmd()
	if err := runSettingsServer(cmd, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no global hosted server") {
		t.Fatalf("unset print unexpected: %q", buf.String())
	}

	// Set (no auth) → lands in the global config, normalized.
	cmd, _ = testCmd()
	if err := runSettingsServer(cmd, []string{"https://h/"}, false); err != nil {
		t.Fatal(err)
	}
	gc, _ := config.LoadGlobal()
	if gc.Hosted.ResolveServer() != "https://h" {
		t.Fatalf("server not set: %+v", gc.Hosted)
	}

	// Print → shows the set value.
	cmd, buf = testCmd()
	if err := runSettingsServer(cmd, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "https://h") {
		t.Fatalf("print set value unexpected: %q", buf.String())
	}

	// No token is ever written to the global config.
	body, _ := os.ReadFile(config.GlobalConfigPath())
	if strings.Contains(string(body), "token") {
		t.Fatalf("global config must not contain a token:\n%s", body)
	}

	// Clear → removed.
	cmd, _ = testCmd()
	if err := runSettingsServer(cmd, nil, true); err != nil {
		t.Fatal(err)
	}
	gc, _ = config.LoadGlobal()
	if gc.Hosted.ResolveServer() != "" {
		t.Fatalf("server not cleared: %+v", gc.Hosted)
	}
}
