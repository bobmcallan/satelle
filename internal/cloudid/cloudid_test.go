package cloudid

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unique sentinels: a test that finds one where it must not be has found a leak.
const (
	sentAzure  = "SENTINEL-AZ-SECRET-7f3a"
	sentAWSKey = "SENTINEL-AWS-KEYID-91bc"
	sentAWSSec = "SENTINEL-AWS-SECRET-44de"
)

// fakeCLI installs a fake provider CLI on PATH that appends its argv and its
// environment to log, then runs tail (a shell fragment, e.g. exit 3 or a JSON
// printf). It models only what the CLI receives, never what the real CLI keeps.
func fakeCLI(t *testing.T, name, tail string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(t.TempDir(), name+".log")
	script := "#!/bin/sh\n{ echo \"ARGV: $*\"; env; echo ---; } >> \"" + log + "\"\n" + tail + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fake CLI was never run: %v", err)
	}
	return string(b)
}

func TestRefNamesAndProfileName(t *testing.T) {
	got := RefNames(map[string]string{"a": "${B}", "b": "${A}", "c": "plain", "d": "${B}"})
	if strings.Join(got, ",") != "A,B" {
		t.Errorf("RefNames = %v, want [A B]", got)
	}
	for _, ok := range []string{"ops", "ops-1", "a.b_c"} {
		if err := ValidProfileName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "a/b", "../x"} {
		if err := ValidProfileName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPersistenceHelpNamesEveryProvider(t *testing.T) {
	h := PersistenceHelp()
	for _, want := range []string{
		"Azure: the az CLI stores the service-principal secret in its cache under AZURE_CONFIG_DIR (~/.satelle/cloud/<profile>/azure)",
		"AWS: the agent receives only STS session credentials via credential_process",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("persistence help lacks %q:\n%s", want, h)
		}
	}
}

func azureCred() map[string]string {
	return map[string]string{"tenant": "t-1", "client_id": "c-1", "client_secret": sentAzure, "subscription": "s-1"}
}

func TestAzurePrepare_SignsInIntoOwnedConfigDir(t *testing.T) {
	log := fakeCLI(t, "az", "exit 0")
	t.Setenv("AZURE_CLIENT_SECRET", "ambient-sp-secret")
	state := filepath.Join(t.TempDir(), "cloud", "ops")

	ov, err := azure{}.Prepare(context.Background(), "ops", azureCred(), state)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "azure")
	out := readLog(t, log)
	for _, want := range []string{
		"ARGV: login --service-principal -u c-1 --tenant t-1 -p " + sentAzure,
		"ARGV: account set --subscription s-1",
		"AZURE_CONFIG_DIR=" + dir,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fake az log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ambient-sp-secret") {
		t.Error("the ambient AZURE_CLIENT_SECRET reached the az child")
	}
	if ov.Set["AZURE_CONFIG_DIR"] != dir || len(ov.Set) != 1 {
		t.Errorf("overlay set = %v, want only AZURE_CONFIG_DIR=%s", ov.Set, dir)
	}
	for _, name := range []string{"AZURE_CLIENT_SECRET", "AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_CERTIFICATE_PATH"} {
		if !contains(ov.Unset, name) {
			t.Errorf("overlay does not unset %s", name)
		}
	}
	for _, v := range ov.Set {
		if strings.Contains(v, sentAzure) {
			t.Error("overlay carries the secret")
		}
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("config dir = %v (%v), want mode 0700", st, err)
	}
}

func TestAzurePrepare_FailureNamesNoSecret(t *testing.T) {
	fakeCLI(t, "az", "echo \"bad credential "+sentAzure+"\" >&2; exit 3")
	_, err := azure{}.Prepare(context.Background(), "ops", azureCred(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "az exited 3") {
		t.Fatalf("err = %v, want az exited 3", err)
	}
	if strings.Contains(err.Error(), sentAzure) {
		t.Errorf("error leaks the secret: %v", err)
	}
}

func TestAzurePrepare_NoSubscriptionSkipsAccountSet(t *testing.T) {
	log := fakeCLI(t, "az", "exit 0")
	cred := azureCred()
	delete(cred, "subscription")
	if _, err := (azure{}).Prepare(context.Background(), "ops", cred, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readLog(t, log), "account set") {
		t.Error("az account set ran without a subscription")
	}
}

func awsCred() map[string]string {
	return map[string]string{"access_key_id": sentAWSKey, "secret_access_key": sentAWSSec, "region": "ap-southeast-2"}
}

// Prepare holds no secret and runs nothing: it writes a credential_process
// config and an empty shared-credentials file, and sets the profile variables.
func TestAWSPrepare_WritesCredentialProcessConfigOnly(t *testing.T) {
	log := fakeCLI(t, "aws", "exit 0")
	t.Setenv("AWS_ACCESS_KEY_ID", "ambient-key")
	state := filepath.Join(t.TempDir(), "cloud", "ops")

	ov, err := aws{}.Prepare(context.Background(), "ops", awsCred(), state)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "aws")
	cfg, _ := os.ReadFile(filepath.Join(dir, "config"))
	for _, want := range []string{"[profile satelle-ops]", "credential_process = satelle agents credential aws ops", "region = ap-southeast-2"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("aws config lacks %q:\n%s", want, cfg)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "credentials")); err != nil || len(b) != 0 {
		t.Errorf("credentials file = %q (%v), want empty", b, err)
	}
	for _, f := range []string{"config", "credentials"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if strings.Contains(string(b), sentAWSKey) || strings.Contains(string(b), sentAWSSec) {
			t.Errorf("%s holds a long-lived key", f)
		}
	}
	want := map[string]string{
		"AWS_CONFIG_FILE":             filepath.Join(dir, "config"),
		"AWS_SHARED_CREDENTIALS_FILE": filepath.Join(dir, "credentials"),
		"AWS_PROFILE":                 "satelle-ops",
	}
	for k, v := range want {
		if ov.Set[k] != v {
			t.Errorf("overlay %s = %q, want %q", k, ov.Set[k], v)
		}
	}
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if !contains(ov.Unset, name) {
			t.Errorf("overlay does not unset %s", name)
		}
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("Prepare ran the aws CLI; minting belongs to credential_process")
	}
}

const stsJSON = `{"Credentials":{"AccessKeyId":"ASIASESSION","SecretAccessKey":"sess-secret","SessionToken":"sess-token","Expiration":"2026-10-15T10:00:00Z"}}`

func TestSessionCredentials_PrintsSessionCredentialsOnly(t *testing.T) {
	log := fakeCLI(t, "aws", "printf '%s' '"+stsJSON+"'")
	t.Setenv("AWS_PROFILE", "satelle-ops")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent/config")

	out, err := SessionCredentials(context.Background(), "ops", awsCred())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"Version":1`, `"AccessKeyId":"ASIASESSION"`, `"SecretAccessKey":"sess-secret"`, `"SessionToken":"sess-token"`, `"Expiration":"2026-10-15T10:00:00Z"`} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %s: %s", want, s)
		}
	}
	if strings.Contains(s, sentAWSKey) || strings.Contains(s, sentAWSSec) {
		t.Errorf("output carries the long-lived keys: %s", s)
	}
	rec := readLog(t, log)
	if !strings.Contains(rec, "ARGV: sts get-session-token --output json") {
		t.Errorf("aws argv: %s", rec)
	}
	if strings.Contains(strings.SplitN(rec, "\n", 2)[0], sentAWSKey) || strings.Contains(strings.SplitN(rec, "\n", 2)[0], sentAWSSec) {
		t.Error("a long-lived key is in the aws argv")
	}
	if !strings.Contains(rec, "AWS_ACCESS_KEY_ID="+sentAWSKey) || !strings.Contains(rec, "AWS_SECRET_ACCESS_KEY="+sentAWSSec) {
		t.Errorf("the aws child did not receive the keys through its environment:\n%s", rec)
	}
	if strings.Contains(rec, "AWS_PROFILE=") || strings.Contains(rec, "AWS_CONFIG_FILE=") {
		t.Errorf("the child could recurse into credential_process:\n%s", rec)
	}
}

func TestSessionCredentials_AssumeRoleAndDuration(t *testing.T) {
	log := fakeCLI(t, "aws", "printf '%s' '"+stsJSON+"'")
	cred := awsCred()
	cred["role_arn"] = "arn:aws:iam::1:role/ops"
	cred["duration"] = "900"
	if _, err := SessionCredentials(context.Background(), "ops", cred); err != nil {
		t.Fatal(err)
	}
	rec := readLog(t, log)
	want := "ARGV: sts assume-role --role-arn arn:aws:iam::1:role/ops --role-session-name satelle-ops --duration-seconds 900 --output json"
	if !strings.Contains(rec, want) {
		t.Errorf("argv lacks %q:\n%s", want, rec)
	}
}

func TestSessionCredentials_Failures(t *testing.T) {
	fakeCLI(t, "aws", "echo "+sentAWSSec+" >&2; echo 'not json'; exit 0")
	_, err := SessionCredentials(context.Background(), "ops", awsCred())
	if err == nil || strings.Contains(err.Error(), sentAWSSec) {
		t.Errorf("unparseable output: err = %v, want a failure that does not echo output", err)
	}
	cred := awsCred()
	cred["duration"] = "an hour"
	if _, err := SessionCredentials(context.Background(), "ops", cred); err == nil {
		t.Error("non-numeric duration accepted")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
