package web

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/buildinfo"
)

func TestFooterUsesProductName(t *testing.T) {
	// Ensure the footer template source brands via {{product}}, not a hard-coded satelle.
	if !strings.Contains(templatesSrc, "{{product}} {{version}}") {
		t.Fatalf("footer must render {{product}} {{version}}; got no match in templatesSrc")
	}
	// Default resolve name is satelle; satelled stamps Name=satelled via ldflags.
	if buildinfo.Resolve().Name == "" {
		t.Fatal("buildinfo.Name empty")
	}
}
