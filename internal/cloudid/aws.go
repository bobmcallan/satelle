package cloudid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// aws gives a session AWS STS SESSION credentials only. The long-lived access
// key never reaches the agent: the agent's AWS profile names a credential_process
// (`satelle agents credential aws`) that mints session credentials on demand.
type aws struct{}

func (aws) Name() string { return "aws" }

func (aws) Fields() []Field {
	return []Field{
		{Key: "access_key_id", Required: true, Secret: true},
		{Key: "secret_access_key", Required: true, Secret: true},
		{Key: "region"},
		{Key: "role_arn"},
		{Key: "duration"},
	}
}

func (aws) Persistence() string {
	return "AWS: the agent receives only STS session credentials via credential_process; the long-lived keys are never written"
}

// awsAmbient are the environment credentials the AWS chain ranks above
// AWS_PROFILE; left in place they would override the selected identity.
var awsAmbient = []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}

// AWSProfileName is the profile written into the satelle-owned AWS config.
func AWSProfileName(profile string) string { return "satelle-" + profile }

// CredentialProcess is the credential_process command line for profile.
func CredentialProcess(profile string) string {
	return "satelle agents credential aws " + profile
}

// Prepare writes a satelle-owned AWS config naming the credential_process, and
// an empty shared-credentials file so ~/.aws/credentials is not consulted. It
// holds no secret and runs no provider command: nothing is minted until the
// agent's first AWS call.
func (aws) Prepare(_ context.Context, profile string, cred map[string]string, stateDir string) (Overlay, error) {
	dir := filepath.Join(stateDir, "aws")
	if err := ensurePrivateDir(dir); err != nil {
		return Overlay{}, fmt.Errorf("aws: %w", err)
	}
	name := AWSProfileName(profile)
	cfg := fmt.Sprintf("[profile %s]\ncredential_process = %s\n", name, CredentialProcess(profile))
	if r := cred["region"]; r != "" {
		cfg += "region = " + r + "\n"
	}
	cfgPath := filepath.Join(dir, "config")
	credPath := filepath.Join(dir, "credentials")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return Overlay{}, fmt.Errorf("aws: write %s: %w", cfgPath, err)
	}
	if err := os.WriteFile(credPath, nil, 0o600); err != nil {
		return Overlay{}, fmt.Errorf("aws: write %s: %w", credPath, err)
	}
	return Overlay{
		Set: map[string]string{
			"AWS_CONFIG_FILE":             cfgPath,
			"AWS_SHARED_CREDENTIALS_FILE": credPath,
			"AWS_PROFILE":                 name,
		},
		Unset: awsAmbient,
	}, nil
}

// SessionCredentials mints STS session credentials from the long-lived keys in
// cred and returns them as the credential_process JSON (Version 1) — the ONLY
// thing the caller prints. The keys reach the aws child through its environment
// alone (never argv), with the satelle-owned profile variables removed so the
// child cannot call back into credential_process.
func SessionCredentials(ctx context.Context, profile string, cred map[string]string) ([]byte, error) {
	args := []string{"sts"}
	if arn := cred["role_arn"]; arn != "" {
		args = append(args, "assume-role", "--role-arn", arn, "--role-session-name", AWSProfileName(profile))
	} else {
		args = append(args, "get-session-token")
	}
	if d := cred["duration"]; d != "" {
		if _, err := strconv.Atoi(d); err != nil {
			return nil, fmt.Errorf("aws: duration %q is not a number of seconds", d)
		}
		args = append(args, "--duration-seconds", d)
	}
	args = append(args, "--output", "json")
	set := map[string]string{
		"AWS_ACCESS_KEY_ID":     cred["access_key_id"],
		"AWS_SECRET_ACCESS_KEY": cred["secret_access_key"],
	}
	if r := cred["region"]; r != "" {
		set["AWS_REGION"] = r
	}
	unset := []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_SESSION_TOKEN"}
	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Env = agentcli.ComposeLaunchEnv(os.Environ(), set, unset)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("aws sts exited %d", ee.ExitCode())
		}
		return nil, fmt.Errorf("aws: %w", err)
	}
	var resp struct {
		Credentials struct {
			AccessKeyID     string `json:"AccessKeyId"`
			SecretAccessKey string `json:"SecretAccessKey"`
			SessionToken    string `json:"SessionToken"`
			Expiration      string `json:"Expiration"`
		} `json:"Credentials"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || resp.Credentials.SessionToken == "" {
		return nil, fmt.Errorf("aws sts returned no session credentials")
	}
	c := resp.Credentials
	return json.Marshal(map[string]any{
		"Version":         1,
		"AccessKeyId":     c.AccessKeyID,
		"SecretAccessKey": c.SecretAccessKey,
		"SessionToken":    c.SessionToken,
		"Expiration":      c.Expiration,
	})
}
