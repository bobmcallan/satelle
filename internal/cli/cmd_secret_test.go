package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/hosted/secretstest"
)

const verbSecret = "planted-verb-secret-55b2"

// secretVerbRig is a developer repo with a .env, a stand-in server and the secret
// verbs pointed at it.
func secretVerbRig(t *testing.T, dotenv string) *secretstest.Server {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	srv := secretstest.New(t)
	client := hosted.NewClient(srv.URL(), &e2eStore{cred: hosted.Credential{ServerURL: srv.URL(), AccessToken: "owner"}}, nil)
	prev := secretHostedClient
	secretHostedClient = func(string) *hosted.Client { return client }
	t.Cleanup(func() { secretHostedClient = prev })
	if dotenv != "" {
		path := config.DotEnvPath(filepath.Join(repo, ".satelle"))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(dotenv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return srv
}

// AC1: push uploads each key of .satelle/config/.env with its value and prints
// only names; list shows has_value and personal and no value.
func TestSecretPushAndListNeverPrintAValue(t *testing.T) {
	srv := secretVerbRig(t, "# comment\nAPI_TOKEN="+verbSecret+"\nOTHER_KEY=\"two words\"\n")

	out, err := runRoot(t, "secret", "push", "--server", srv.URL())
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if out != "pushed API_TOKEN\npushed OTHER_KEY\n" {
		t.Errorf("push output = %q", out)
	}
	if v, _ := srv.Value("API_TOKEN"); v != verbSecret {
		t.Errorf("the server holds %q", v)
	}
	if v, _ := srv.Value("OTHER_KEY"); v != "two words" {
		t.Errorf("the server holds %q for OTHER_KEY", v)
	}
	var puts int
	for _, q := range srv.Requests() {
		if q.Method == http.MethodPut {
			puts++
			if !strings.HasSuffix(q.Path, "/API_TOKEN") && !strings.HasSuffix(q.Path, "/OTHER_KEY") || !strings.Contains(q.Body, `"personal":false`) {
				t.Errorf("put = %+v", q)
			}
		}
	}
	if puts != 2 {
		t.Errorf("%d puts, want 2", puts)
	}

	list, err := runRoot(t, "secret", "list", "--server", srv.URL())
	if err != nil {
		t.Fatalf("list: %v\n%s", err, list)
	}
	for _, want := range []string{"KEY", "HAS_VALUE", "PERSONAL", "READERS", "API_TOKEN", "true", "false"} {
		if !strings.Contains(list, want) {
			t.Errorf("list lacks %q:\n%s", want, list)
		}
	}
	if strings.Contains(list+out, verbSecret) || strings.Contains(list, "two words") {
		t.Errorf("a value was printed:\n%s%s", out, list)
	}
}

// AC1/AC9: --personal marks every key pushed personal.
func TestSecretPushPersonal(t *testing.T) {
	srv := secretVerbRig(t, "SUB_TOKEN="+verbSecret+"\n")
	if out, err := runRoot(t, "secret", "push", "--personal", "--server", srv.URL()); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	list, _ := runRoot(t, "secret", "list", "--server", srv.URL())
	if !strings.Contains(list, "SUB_TOKEN  true       true") {
		t.Errorf("want SUB_TOKEN listed as personal:\n%s", list)
	}
}

// Nothing to push is an error, not a silent success.
func TestSecretPushWithNoEnvFileFails(t *testing.T) {
	srv := secretVerbRig(t, "")
	if out, err := runRoot(t, "secret", "push", "--server", srv.URL()); err == nil {
		t.Errorf("push with no .env succeeded:\n%s", out)
	}
	if srv.Calls() != 0 {
		t.Error("the server was called with nothing to push")
	}
}

// AC2: grant and revoke set and remove the remote's read, and list shows the
// remote as a reader only while it is granted.
func TestSecretGrantAndRevoke(t *testing.T) {
	srv := secretVerbRig(t, "API_TOKEN="+verbSecret+"\n")
	if out, err := runRoot(t, "secret", "push", "--server", srv.URL()); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}

	out, err := runRoot(t, "secret", "grant", "API_TOKEN", "--remote", "edge", "--personal", "--server", srv.URL())
	if err != nil || out != "granted API_TOKEN to remote edge\n" {
		t.Fatalf("grant: %v\n%s", err, out)
	}
	reqs := srv.Requests()
	if g := reqs[len(reqs)-1]; g.Method != http.MethodPut || g.Path != "/api/v1/secrets/API_TOKEN/grants/edge" || g.Body != `{"personal":true}` {
		t.Errorf("grant request = %+v", g)
	}
	list, _ := runRoot(t, "secret", "list", "--server", srv.URL())
	if !strings.Contains(list, "API_TOKEN  true       true      edge") {
		t.Errorf("want edge listed as a reader:\n%s", list)
	}

	out, err = runRoot(t, "secret", "revoke", "API_TOKEN", "--remote", "edge", "--server", srv.URL())
	if err != nil || out != "revoked API_TOKEN from remote edge\n" {
		t.Fatalf("revoke: %v\n%s", err, out)
	}
	reqs = srv.Requests()
	if g := reqs[len(reqs)-1]; g.Method != http.MethodDelete || g.Path != "/api/v1/secrets/API_TOKEN/grants/edge" {
		t.Errorf("revoke request = %+v", g)
	}
	list, _ = runRoot(t, "secret", "list", "--server", srv.URL())
	if strings.Contains(list, "edge") {
		t.Errorf("edge is still listed after revoke:\n%s", list)
	}

	if _, err := runRoot(t, "secret", "grant", "API_TOKEN", "--remote", "Bad Name", "--server", srv.URL()); err == nil {
		t.Error("a malformed remote name was accepted")
	}
	if _, err := runRoot(t, "secret", "grant", "NO_SUCH", "--remote", "edge", "--server", srv.URL()); err == nil {
		t.Error("granting a key the server does not hold succeeded")
	}
}

// `secret get` off a node has no socket to ask and says so.
func TestSecretGetOutsideANode(t *testing.T) {
	secretVerbRig(t, "")
	out, err := runRoot(t, "secret", "get", "API_TOKEN")
	if err == nil || !strings.Contains(err.Error(), "remote node") {
		t.Errorf("get outside a node: %v\n%s", err, out)
	}
}
