//go:build integration

package tests

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmbeddedExecutorDeliverablesDiscoverable proves that after a clean init,
// satelle-executor-deliverables is served from the binary defaults path
// (embedded:…), not an on-disk override (sty_fd4c3466).
func TestEmbeddedExecutorDeliverablesDiscoverable(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	out := mustRun(t, testBin, repo, "doc", "get", "principles", "satelle-executor-deliverables")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("doc get json: %v\n%s", err, out)
	}
	path, _ := doc["path"].(string)
	if !strings.HasPrefix(path, "embedded:") {
		t.Errorf("want embedded: path prefix, got %q", path)
	}
	body, _ := doc["body"].(string)
	if !strings.Contains(body, "# Executor deliverables") {
		t.Errorf("body missing principle title:\n%s", body)
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "deliverables") {
		t.Errorf("body missing deliverables rule text:\n%s", body)
	}
	// Core rule: never how gates decide.
	if !strings.Contains(lower, "never") || !strings.Contains(lower, "gate") {
		t.Errorf("body missing never-gate-criteria rule:\n%s", body)
	}
	// Ondemand residency: no principles:session tag in frontmatter tags line.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
			if strings.Contains(line, "principles:session") {
				t.Error("embedded principle must stay ondemand (no principles:session)")
			}
			break
		}
	}
}
