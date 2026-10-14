package cloudid

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// azure signs a session in as an Azure service principal through the az CLI.
type azure struct{}

func (azure) Name() string { return "azure" }

func (azure) Fields() []Field {
	return []Field{
		{Key: "tenant", Required: true},
		{Key: "client_id", Required: true},
		{Key: "client_secret", Required: true, Secret: true},
		{Key: "subscription"},
	}
}

func (azure) Persistence() string {
	return "Azure: the az CLI stores the service-principal secret in its cache under AZURE_CONFIG_DIR (~/.satelle/cloud/<profile>/azure); az writes it, not satelle"
}

// azureAmbient are the service-principal variables azure SDK tools
// (DefaultAzureCredential) rank above the az login the profile selected.
var azureAmbient = []string{
	"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_CLIENT_CERTIFICATE_PATH",
}

// Prepare runs `az login --service-principal` and `az account set` as short
// children of the launcher, into a satelle-owned AZURE_CONFIG_DIR. Both exit
// before the agent exists, so the secret in their argv is never the agent's argv;
// the agent receives only AZURE_CONFIG_DIR, which az reads its logged-in session
// from.
func (azure) Prepare(ctx context.Context, profile string, cred map[string]string, stateDir string) (Overlay, error) {
	dir := filepath.Join(stateDir, "azure")
	if err := ensurePrivateDir(dir); err != nil {
		return Overlay{}, fmt.Errorf("azure: %w", err)
	}
	env := agentcli.ComposeLaunchEnv(os.Environ(), map[string]string{"AZURE_CONFIG_DIR": dir}, azureAmbient)
	login := []string{"login", "--service-principal", "-u", cred["client_id"], "--tenant", cred["tenant"], "-p", cred["client_secret"], "--output", "none"}
	if err := runQuiet(ctx, "az", login, env); err != nil {
		return Overlay{}, fmt.Errorf("azure: az login for profile %q: %w", profile, err)
	}
	if sub := cred["subscription"]; sub != "" {
		if err := runQuiet(ctx, "az", []string{"account", "set", "--subscription", sub}, env); err != nil {
			return Overlay{}, fmt.Errorf("azure: az account set for profile %q: %w", profile, err)
		}
	}
	return Overlay{Set: map[string]string{"AZURE_CONFIG_DIR": dir}, Unset: azureAmbient}, nil
}

// ensurePrivateDir creates dir (and parents) and makes it owner-only.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// runQuiet runs name with args under env, discarding its output so no provider
// message (which may echo an argument) reaches satelle's stdout or stderr. A
// failure reports the program and its exit code only.
func runQuiet(ctx context.Context, name string, args, env []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s exited %d", name, ee.ExitCode())
		}
		if ne, ok := err.(*exec.Error); ok {
			return fmt.Errorf("%s: %w", name, ne.Err)
		}
		return fmt.Errorf("%s failed", name)
	}
	return nil
}
