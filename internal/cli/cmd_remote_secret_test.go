package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
)

const plantedSecret = "planted-e2e-secret-9c41"

// useRigForSecrets points the secret verbs at the rig's signed-in client.
func (r *remoteRig) useRigForSecrets() {
	prev := secretHostedClient
	secretHostedClient = func(string) *hosted.Client { return remoteHostedClient("") }
	r.t.Cleanup(func() { secretHostedClient = prev })
}

// editRemote rewrites a file of the rig's remote definition.
func (r *remoteRig) editRemote(rel, old, repl string) {
	r.t.Helper()
	path := filepath.Join(r.repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", filepath.FromSlash(rel))
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		r.t.Fatalf("%s does not contain %q", rel, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, repl, 1)), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// ledgerRows is every ledger row of kind in the developer's repo.
func (r *remoteRig) ledgerRows(kind string) []ledger.Entry {
	r.t.Helper()
	a, err := app.Open()
	if err != nil {
		r.t.Fatal(err)
	}
	defer a.Close()
	rows, err := a.Store.Ledger.List(context.Background(), ledger.ListFilter{Kind: kind})
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

// AC3/AC4/AC5/AC8: a real node, started by `remote start`, whose agent binding
// references ${API_TOKEN}. Its value reaches the agent's environment at launch
// and `satelle secret get` returns it mid-step, both fetched through local
// satelle from the server; a listed key with no grant, an unlisted key and the
// definition's wake key are refused with their reasons; and afterwards the
// planted value is nowhere on the node, in the host log, the ledger, the output
// or the sync payload set.
func TestRemoteSecretsEndToEnd(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	r.useRigForSecrets()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	r.editRemote("remote.toml", `secrets = []`, `secrets = ["API_TOKEN", "OTHER"]`)
	r.editRemote("remote.toml", `wake_key = "REMOTE_WAKE_KEY"`, `wake_key = "MY_WAKER"`)
	r.editRemote("workflows/agents.toml", `tools = "read_file"`, "tools = \"read_file\"\nenv = { FROM_ENV = \"${API_TOKEN}\" }")
	if out, err := runRoot(t, "remote", "validate", "edge"); err != nil {
		t.Fatalf("the edited remote must validate: %v\n%s", err, out)
	}

	cli := "SATELLE_CLI_REEXEC=1 '" + exe + "'"
	agent := strings.Join([]string{
		"#!/bin/sh",
		"cat >/dev/null",
		`got=$(` + cli + ` secret get API_TOKEN)`,
		`[ "$got" = '` + plantedSecret + `' ] && echo GET_OK=yes || echo GET_OK=no`,
		`[ "$FROM_ENV" = '` + plantedSecret + `' ] && echo ENV_OK=yes || echo ENV_OK=no`,
		`[ -z "$OTHER" ] && echo OTHER_UNSET=yes || echo OTHER_UNSET=no`,
		`echo "DENIED_OTHER=$(` + cli + ` secret get OTHER 2>&1)"`,
		`echo "DENIED_UNLISTED=$(` + cli + ` secret get NOT_LISTED 2>&1)"`,
		`echo "DENIED_WAKE=$(` + cli + ` secret get MY_WAKER 2>&1)"`,
		"",
	}, "\n")
	agentPath, err := exec.LookPath("fake-agent")
	if err != nil {
		t.Fatal(err)
	}
	writeExec(t, agentPath, agent)

	// The developer's .env, then the CLI verbs: push, then grant API_TOKEN only.
	envFile := config.DotEnvPath(filepath.Join(r.repo, ".satelle"))
	if err := os.MkdirAll(filepath.Dir(envFile), 0o755); err != nil {
		t.Fatal(err)
	}
	dotenv := "API_TOKEN=" + plantedSecret + "\nOTHER=" + plantedSecret + "-other\nMY_WAKER=" + plantedSecret + "-wake\n"
	if err := os.WriteFile(envFile, []byte(dotenv), 0o600); err != nil {
		t.Fatal(err)
	}
	var verbOut strings.Builder
	for _, args := range [][]string{
		{"secret", "push", "--server", r.server},
		{"secret", "grant", "API_TOKEN", "--remote", "edge", "--server", r.server},
		{"secret", "list", "--server", r.server},
	} {
		out, err := runRoot(t, args...)
		if err != nil {
			t.Fatalf("satelle %v: %v\n%s", args, err, out)
		}
		verbOut.WriteString(out)
	}

	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--message", "work")
	if err != nil {
		t.Fatalf("remote start: %v\n%s", err, out)
	}
	for _, want := range []string{"GET_OK=yes", "ENV_OK=yes", "OTHER_UNSET=yes",
		"DENIED_OTHER=secret OTHER refused: remote edge holds no grant on OTHER",
		"DENIED_UNLISTED=secret NOT_LISTED refused: the key is not in remote edge's definition",
		"DENIED_WAKE=secret MY_WAKER refused: the wake key is never readable by an agent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the agent's report lacks %q:\n%s", want, out)
		}
	}

	// The server answered the two reads of the granted key and refused the
	// ungranted one; the unlisted key and the wake key never reached it.
	allowed, denied := 0, map[string]bool{}
	for _, a := range r.secrets.Audit() {
		switch {
		case a.Outcome == "allowed" && a.Key == "API_TOKEN" && a.Remote == "edge":
			allowed++
		case a.Outcome == "denied":
			denied[a.Key] = true
		default:
			t.Errorf("unexpected audit entry %+v", a)
		}
	}
	if allowed != 2 || len(denied) != 1 || !denied["OTHER"] {
		t.Errorf("server audit: %d allowed reads of API_TOKEN, denied %v; want 2 and only OTHER", allowed, denied)
	}
	if runs := r.secrets.Runs(); len(runs) != 1 || r.secrets.Live(runs[0]) {
		t.Errorf("runs = %v: want one run, ended with the job", runs)
	}
	if !strings.Contains(string(jsonBytes(t, r.secrets.Requests())), `\"wake_key\":\"MY_WAKER\"`) {
		t.Error("the run was not registered with the definition's wake key")
	}

	// Local satelle recorded each request without a value.
	byOutcome := map[string]map[string]bool{"allowed": {}, "denied": {}}
	for _, row := range r.ledgerRows(ledger.KindSecretRead) {
		var ev struct{ Key, Remote, Run, Outcome string }
		if err := json.Unmarshal(row.Payload, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Remote != "edge" || ev.Run == "" {
			t.Errorf("secret.read %s", row.Payload)
		}
		byOutcome[ev.Outcome][ev.Key] = true
	}
	if len(byOutcome["allowed"]) != 1 || !byOutcome["allowed"]["API_TOKEN"] ||
		len(byOutcome["denied"]) != 3 || !byOutcome["denied"]["OTHER"] || !byOutcome["denied"]["NOT_LISTED"] || !byOutcome["denied"]["MY_WAKER"] {
		t.Errorf("secret.read by outcome = %v", byOutcome)
	}

	// AC8: the planted values are nowhere this run could have left them.
	planted := []string{plantedSecret}
	scan := func(what string, body []byte) {
		for _, p := range planted {
			if bytes.Contains(body, []byte(p)) {
				t.Errorf("%s carries a secret value", what)
			}
		}
	}
	scan("the output of remote start and the secret verbs", []byte(out+verbOut.String()))
	scan("the host log", []byte(r.log()))
	err = filepath.WalkDir(r.nodeHome, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || !d.Type().IsRegular() {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil {
			scan("node file "+p, b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{ledger.KindSecretRead, ledger.KindRemoteStart, ledger.KindRemoteStop} {
		for _, row := range r.ledgerRows(kind) {
			scan("ledger "+kind, jsonBytes(t, row))
		}
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := config.ConfigFiles(cfg, r.repo)
	if err != nil {
		t.Fatal(err)
	}
	docs, _, err := config.DocumentFiles(cfg, r.repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(bundle.Files, docs.Files...) {
		if strings.HasSuffix(f.Path, ".env") {
			t.Errorf("the sync payload set carries %s", f.Path)
		}
		scan("sync payload "+f.Path, f.Content)
	}
}

func jsonBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
