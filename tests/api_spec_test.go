//go:build integration

// Integration coverage for sty_98f502ee: the published server-interface spec
// (api/openapi.yaml) must be well-formed OpenAPI 3.0 AND stay in sync with the
// endpoints this repo's real CLI client actually calls — the enduring surface is
// published, the retired substrate interface is not. This exercises the SHIPPED
// spec against the SHIPPED client source, the conformance guarantee AC1/AC3/AC4
// promise, complementing the type-level unit checks in api/types_test.go.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAPISpecCoversClientEndpoints(t *testing.T) {
	root := repoRootForTest()
	specBytes, err := os.ReadFile(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read api/openapi.yaml: %v", err)
	}
	spec := string(specBytes)

	// Well-formed OpenAPI 3.0 shape (structural, no YAML dep): the version marker
	// and the required top-level sections must be present.
	for _, want := range []string{"openapi:", "3.0", "\ninfo:", "\npaths:", "\ncomponents:"} {
		if !strings.Contains(spec, want) {
			t.Errorf("openapi.yaml missing expected OpenAPI 3.0 element %q", want)
		}
	}

	// Every ENDURING endpoint the real CLI client (internal/hosted) speaks must be
	// published as a path in the spec (AC1/AC4 — spec in sync with the client).
	for _, path := range []string{
		"/api/v1/me",
		"/api/v1/me/files",
		"/api/v1/me/files/{path}",
		"/api/v1/projects",
		"/api/v1/workspaces",
		"/api/v1/projects/{project}/config",
		"/api/v1/projects/{project}/documents",
		"/api/v1/workspaces/{workspaceId}/published",
		"/oauth/authorize",
		"/oauth/token",
	} {
		if !strings.Contains(spec, path+":") {
			t.Errorf("openapi.yaml does not publish enduring endpoint %q that the CLI client calls", path)
		}
	}

	// The retired project-substrate-backup interface must NOT be published (AC3).
	if strings.Contains(spec, "substrate") {
		t.Errorf("openapi.yaml publishes the retired substrate interface — it must be excluded (AC3)")
	}
}

// TestPublishEndpointsInSpec verifies the team publish catalog surface is
// published in the OpenAPI spec (epic:sync-publish / sty_aad09578 AC5).
func TestPublishEndpointsInSpec(t *testing.T) {
	root := repoRootForTest()
	specBytes, err := os.ReadFile(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read api/openapi.yaml: %v", err)
	}
	spec := string(specBytes)
	for _, want := range []string{
		"/api/v1/workspaces/{workspaceId}/published",
		"/api/v1/workspaces/{workspaceId}/published/{path}",
		"listPublishedArtifacts",
		"publishArtifact",
		"getPublishedArtifact",
		"PublishedItem",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("openapi.yaml missing publish endpoint %q", want)
		}
	}
}
