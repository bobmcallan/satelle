package cli

import (
	"strings"
	"testing"
)

// TestServeAliasDeprecationNotice proves satelle serve remains registered and
// documents deprecation (sty_80233c10 AC4).
func TestServeAliasDeprecationNotice(t *testing.T) {
	root := NewRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "serve" {
			found = true
			blob := c.Long + c.Short
			if !strings.Contains(strings.ToLower(blob), "deprecated") {
				t.Error("serve help should mention deprecation")
			}
		}
	}
	if !found {
		t.Fatal("serve command missing")
	}
}
