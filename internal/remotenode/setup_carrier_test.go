//go:build !windows

package remotenode

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotenode/devtest"
)

// The attach path's carrier is the sealed Setup: the controller sends setup.Run,
// the node's Prepare receives it with the registered run id, and a job the node
// then serves reads through the stub satelle.dev with that id — the run id never
// reaches the node's process environment.
func TestAttachCarriesTheRunInTheSealedSetup(t *testing.T) {
	d := devtest.New(t)
	t.Setenv("SATELLE_REMOTE_KEY", devtest.SeedB64)
	t.Setenv(config.RemoteServerRunEnv, "")

	r := newRig(t)
	rs := NewRunSecrets(secretDef(), "rmt_a", d.URL, "trg_x")
	rs.Secrets = rs.Secrets[:1]

	var mu sync.Mutex
	var got Setup
	node := NewNode()
	node.Handle("read", func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		run := got.Run
		mu.Unlock()
		values, _, err := SecretsForRun(ctx, run, os.Getenv, WithHTTPClient(d.Client()))
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		return json.Marshal(keys)
	})

	run, _ := r.attach(node, func(_ context.Context, s Setup) (Report, error) {
		mu.Lock()
		got = s
		mu.Unlock()
		return Report{Version: "1.2.3", Workflows: []string{"*"}, Allow: s.Allow}, nil
	}, Setup{Run: rs}, nil)

	mu.Lock()
	gotRun := got.Run
	mu.Unlock()
	if gotRun.ServerRunID != "trg_x" || gotRun.RemoteID != "rmt_a" || gotRun.Server != d.URL {
		t.Fatalf("the node's Setup.Run = %+v", gotRun)
	}
	if strings.Contains(gotRun.Identity, devtest.SeedB64) || strings.Contains(strings.Join(gotRun.Keys(), ""), devtest.SecretValue) {
		t.Error("the carrier holds a key or a value")
	}
	if os.Getenv(config.RemoteServerRunEnv) != "" {
		t.Error("the attach path must not carry the run id through the process environment")
	}

	body, err := run.Request(testCtx(t), "read", struct{}{})
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	var keys []string
	_ = json.Unmarshal(body, &keys)
	if len(keys) != 1 || keys[0] != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("keys read = %v", keys)
	}
	calls := d.Calls()
	if len(calls) != 1 || calls[0].Path != "/api/v1/remotes/rmt_a/runs/trg_x/secrets/CLAUDE_CODE_OAUTH_TOKEN" || !calls[0].Verified {
		t.Errorf("the node's read = %+v", calls)
	}
}
