package remotenode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotesign"
)

// TestLiveSecretReadPersonal proves the personal secrets path against a real
// satelle.dev end to end (sty_0b1c72a9 AC6): register a throwaway local,
// owner_only remote; register its Ed25519 key; push a throwaway personal secret
// and grant it; register an owner run; then read the value the way a node does,
// signed and bound to that run. A read for a run that was never registered is
// refused. Everything it creates is removed again. It is skipped unless
// SATELLE_LIVE_SERVER, SATELLE_LIVE_PROJECT and SATELLE_TOKEN are set.
func TestLiveSecretReadPersonal(t *testing.T) {
	server, project, token := os.Getenv("SATELLE_LIVE_SERVER"), os.Getenv("SATELLE_LIVE_PROJECT"), os.Getenv(hosted.SessionTokenEnv)
	if server == "" || project == "" || token == "" {
		t.Skip("set SATELLE_LIVE_SERVER, SATELLE_LIVE_PROJECT and SATELLE_TOKEN to run against a real satelle-server")
	}
	server = strings.TrimRight(server, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := hosted.NewClient(server, hosted.DefaultStore(), nil)

	suffix := randomHex(t, 6)
	remoteID := registerLocalRemote(t, ctx, server, project, token, "secret-proof-"+suffix)
	t.Cleanup(func() { _ = client.RetireRemote(context.Background(), remoteID) })

	seed, public, err := remotesign.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetRemoteKey(ctx, remoteID, public); err != nil {
		t.Fatalf("register the remote's key: %v", err)
	}

	key := "SATELLE_LIVE_PROOF_" + strings.ToUpper(suffix)
	value := "proof-" + randomHex(t, 16)
	if err := client.PutSecret(ctx, "personal", "", key, value, time.Time{}); err != nil {
		t.Fatalf("push the personal secret: %v", err)
	}
	t.Cleanup(func() { deleteSecret(server, token, key) })
	if err := client.GrantSecret(ctx, "personal", "", key, remoteID); err != nil {
		t.Fatalf("grant the secret to the remote: %v", err)
	}

	run, err := client.CreateRun(ctx, remoteID)
	if err != nil {
		t.Fatalf("register the owner run: %v", err)
	}
	t.Cleanup(func() { _ = client.EndOwnerRun(context.Background(), remoteID, run.RunID) })

	const keyEnv = "SATELLE_LIVE_PROOF_IDENTITY"
	getenv := func(name string) string {
		if name == keyEnv {
			return seed
		}
		return ""
	}
	rs := RunSecrets{
		ServerRunID: run.RunID,
		RemoteID:    remoteID,
		Server:      server,
		Identity:    "key:" + keyEnv,
		Secrets:     []SecretRef{{Key: key, Scope: "personal"}},
	}
	values, _, err := SecretsForRun(ctx, rs, getenv)
	if err != nil {
		t.Fatalf("signed read for the registered run: %v", err)
	}
	if values[key] != value {
		t.Fatalf("the signed read returned a different value for %s", key)
	}
	t.Logf("signed read of %s for run %s on %s returned the pushed value", key, run.RunID, remoteID)
	assertAuditedRead(t, ctx, server+"/api/v1/secrets/"+key+"/reads", token, remoteID, run.RunID)

	bogus := rs
	bogus.ServerRunID = "trg_" + randomHex(t, 6)
	_, _, err = SecretsForRun(ctx, bogus, getenv)
	var refusal *SecretRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("a read for an unregistered run must be refused by the server, got %v", err)
	}
	t.Logf("read for an unregistered run refused: %s", refusal.Err)
}

// TestLiveSecretReadTeam is the team half of AC6: a team-scope secret, pushed into
// the team workspace by one of its admins and granted to a remote whose project is
// in that workspace, is read by the node for its run. It is skipped unless
// SATELLE_LIVE_WORKSPACE (a team workspace id the token administers) is set as
// well as the personal test's variables.
func TestLiveSecretReadTeam(t *testing.T) {
	server, project, token, ws := os.Getenv("SATELLE_LIVE_SERVER"), os.Getenv("SATELLE_LIVE_PROJECT"), os.Getenv(hosted.SessionTokenEnv), os.Getenv("SATELLE_LIVE_WORKSPACE")
	if server == "" || project == "" || token == "" || ws == "" {
		t.Skip("set SATELLE_LIVE_SERVER, SATELLE_LIVE_PROJECT, SATELLE_LIVE_WORKSPACE and SATELLE_TOKEN to run against a real satelle-server")
	}
	server = strings.TrimRight(server, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := hosted.NewClient(server, hosted.DefaultStore(), nil)

	suffix := randomHex(t, 6)
	remoteID := registerLocalRemote(t, ctx, server, project, token, "secret-proof-team-"+suffix)
	t.Cleanup(func() { _ = client.RetireRemote(context.Background(), remoteID) })
	seed, public, err := remotesign.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetRemoteKey(ctx, remoteID, public); err != nil {
		t.Fatalf("register the remote's key: %v", err)
	}
	key := "SATELLE_LIVE_TEAM_PROOF_" + strings.ToUpper(suffix)
	value := "team-proof-" + randomHex(t, 16)
	if err := client.PutSecret(ctx, "team", ws, key, value, time.Time{}); err != nil {
		t.Fatalf("push the team secret: %v", err)
	}
	t.Cleanup(func() { deleteTeamSecret(server, token, ws, key) })
	if err := client.GrantSecret(ctx, "team", ws, key, remoteID); err != nil {
		t.Fatalf("grant the team secret to the remote: %v", err)
	}
	run, err := client.CreateRun(ctx, remoteID)
	if err != nil {
		t.Fatalf("register the owner run: %v", err)
	}
	t.Cleanup(func() { _ = client.EndOwnerRun(context.Background(), remoteID, run.RunID) })
	const keyEnv = "SATELLE_LIVE_PROOF_IDENTITY"
	rs := RunSecrets{
		ServerRunID: run.RunID,
		RemoteID:    remoteID,
		Server:      server,
		Identity:    "key:" + keyEnv,
		Secrets:     []SecretRef{{Key: key, Scope: "team", Workspace: ws}},
	}
	values, _, err := SecretsForRun(ctx, rs, func(name string) string {
		if name == keyEnv {
			return seed
		}
		return ""
	})
	if err != nil {
		t.Fatalf("signed read of the team secret: %v", err)
	}
	if values[key] != value {
		t.Fatalf("the signed read returned a different value for %s", key)
	}
	t.Logf("signed read of team secret %s in %s for run %s on %s returned the pushed value", key, ws, run.RunID, remoteID)
	// satelle.dev grants a team secret only to a remote whose project is in that
	// workspace, so the accepted grant above is the proof the remote is the team's.
	assertAuditedRead(t, ctx, server+"/api/v1/workspaces/"+ws+"/secrets/"+key+"/reads", token, remoteID, run.RunID)
}

// assertAuditedRead checks the owner-visible audit of a secret records an allowed
// read by remoteID for runID.
func assertAuditedRead(t *testing.T, ctx context.Context, url, token, remoteID, runID string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read audit %s: HTTP %d", url, resp.StatusCode)
	}
	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, r := range rows {
		b, _ := json.Marshal(r)
		s := string(b)
		if strings.Contains(s, remoteID) && strings.Contains(s, runID) && strings.Contains(s, "allowed") {
			t.Logf("audited read: %s", s)
			return
		}
	}
	t.Fatalf("the read audit has no allowed read by %s for run %s: %v", remoteID, runID, rows)
}

func deleteTeamSecret(server, token, ws, key string) {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/workspaces/%s/secrets/%s", server, ws, key), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

func registerLocalRemote(t *testing.T, ctx context.Context, server, project, token, name string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"name":            name,
		"wake_url":        server + "/interop-test-sink",
		"wake_secret_key": "SATELLE_INTEROP_TEST_UNSET",
		"orchestrator":    "local",
		"owner_only":      true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/v1/projects/"+project+"/remotes", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created struct {
		ID string `json:"id"`
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("register a throwaway local remote: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("the registration answered no remote id: %v", err)
	}
	return created.ID
}

func deleteSecret(server, token, key string) {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/secrets/%s", server, key), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
