// Package testutil holds shared test helpers for satelle packages.
//
// IsolateHome is the ergonomic path pointed at by the GlobalDir() test guard
// (sty_c36c211f): every test that resolves runtime state must set SATELLE_HOME
// so unit suites never mint key dirs under the developer's real ~/.satelle.
package testutil

import (
	"os"
	"strings"
	"testing"
)

// IsolateHome pins SATELLE_HOME to a disposable temp dir for this test and
// returns that dir. Call at the start of any test (or package helper) that
// may open the home-keyed runtime plane. t.Setenv restores the prior value
// when the test ends.
//
// When SATELLE_SERVER_ENDPOINT is unset it is set to "none" so an empty
// machine config cannot default UI push to the operator's live satelled
// on :8787 (sty_cb74c03b). A test that already chose an endpoint keeps it.
func IsolateHome(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	if strings.TrimSpace(os.Getenv("SATELLE_SERVER_ENDPOINT")) == "" {
		t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	}
	return home
}
