// Package testutil holds shared test helpers for satelle packages.
//
// IsolateHome is the ergonomic path pointed at by the GlobalDir() test guard
// (sty_c36c211f): every test that resolves runtime state must set SATELLE_HOME
// so unit suites never mint key dirs under the developer's real ~/.satelle.
package testutil

import (
	"testing"
)

// IsolateHome pins SATELLE_HOME to a disposable temp dir for this test and
// returns that dir. Call at the start of any test (or package helper) that
// may open the home-keyed runtime plane. t.Setenv restores the prior value
// when the test ends.
func IsolateHome(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	return home
}
