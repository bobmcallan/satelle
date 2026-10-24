package remotenode

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode/devtest"
)

const (
	testNodeID    = "nod_a1b2"
	testNodeToken = "node-token-1"
)

func newFake(t *testing.T) (*devtest.Node, *NodeClient, []byte) {
	t.Helper()
	bundle := bundleOf(t, definitionFiles(t))
	fake := devtest.NewNode(t, testNodeID, testNodeToken, bundle, "v7")
	return fake, NewNodeClient(fake.URL, testNodeID, testNodeToken, fake.Client()), bundle
}

// AC1: the definition is fetched with the node token under the node's own id, and
// the bytes returned are exactly the stored bundle, with the version header.
func TestDefinitionIsFetchedWithTheNodeToken(t *testing.T) {
	fake, client, bundle := newFake(t)

	got, version, err := client.Definition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bundle) || version != "v7" {
		t.Errorf("Definition = %d bytes at %q, want the stored %d bytes at v7", len(got), version, len(bundle))
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Method != http.MethodGet || calls[0].Path != "/api/v1/remote-nodes/"+testNodeID+"/definition" ||
		calls[0].Auth != "Bearer "+testNodeToken {
		t.Errorf("calls = %+v", calls)
	}
}

// The run descriptor is read from GET .../run with the node token, and a heartbeat
// goes out as PUT .../status carrying only the fields it sets, the reply's
// lost_after_seconds coming back.
func TestRunAndStatusUseTheNodeRoutes(t *testing.T) {
	fake, client, _ := newFake(t)
	fake.Run = map[string]string{"mode": "node", "story_id": "sty_1", "trigger": "nightly", "trigger_source": "schedule"}
	fake.LostAfterSeconds = 90

	d, err := client.Run(context.Background())
	if err != nil || d != (RunDescriptor{Mode: "node", StoryID: "sty_1", Trigger: "nightly", TriggerSource: "schedule"}) {
		t.Fatalf("Run = %+v, %v", d, err)
	}
	reply, err := client.Status(context.Background(), NodeStatus{State: StateRunning, StoryID: "sty_1", RunState: RunQueued})
	if err != nil || reply.LostAfterSeconds != 90 {
		t.Fatalf("Status = %+v, %v", reply, err)
	}
	if _, err := client.Status(context.Background(), NodeStatus{State: StateRunning, RunState: RunConcluded, Outcome: "no-op"}); err != nil {
		t.Fatal(err)
	}

	st := fake.Statuses()
	if len(st) != 2 || st[0]["run_state"] != "queued" || st[0]["story_id"] != "sty_1" || st[0]["step"] != "" || st[0]["outcome"] != "" ||
		st[1]["run_state"] != "concluded" || st[1]["outcome"] != "no-op" {
		t.Errorf("statuses = %v, want the fields each heartbeat set and no others", st)
	}
	for _, c := range fake.Calls() {
		if c.Auth != "Bearer "+testNodeToken {
			t.Errorf("call %+v is not under the node token", c)
		}
	}
	if n := fake.Count(http.MethodGet, "/run"); n != 1 {
		t.Errorf("run reads = %d, want 1", n)
	}
	if n := fake.Count(http.MethodPut, "/status"); n != 2 {
		t.Errorf("status heartbeats = %d, want 2", n)
	}
}

// AC3: a body that does not hash to its ETag, or an answer without the headers
// the contract promises, is not a definition.
func TestDefinitionRefusesWhatIsNotTheStoredBundle(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(*devtest.Node)
		want string
	}{
		"digest mismatch": {func(n *devtest.Node) { n.ETag = strings.Repeat("0", 64) }, "does not match its ETag"},
		"no version":      {func(n *devtest.Node) { n.OmitVersion = true }, "X-Satelle-Definition-Version"},
	} {
		t.Run(name, func(t *testing.T) {
			fake, client, _ := newFake(t)
			tc.set(fake)
			_, _, err := client.Definition(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Definition = %v, want an error containing %q", err, tc.want)
			}
			if name == "digest mismatch" && !errors.Is(err, ErrDefinitionDigest) {
				t.Errorf("err = %v, want ErrDefinitionDigest", err)
			}
		})
	}
}

// AC5: a secret is read with POST under the node's id and the node bearer.
func TestSecretIsReadWithTheNodeBearer(t *testing.T) {
	fake, client, _ := newFake(t)
	fake.Secrets["CLAUDE_CODE_OAUTH_TOKEN"] = devtest.SecretValue

	got, err := client.Secret(context.Background(), "CLAUDE_CODE_OAUTH_TOKEN")
	if err != nil || got != devtest.SecretValue {
		t.Fatalf("Secret = %q, %v", got, err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Method != http.MethodPost || calls[0].Auth != "Bearer "+testNodeToken ||
		calls[0].Path != "/api/v1/remote-nodes/"+testNodeID+"/secrets/CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("calls = %+v", calls)
	}
}

// AC5: a key the server refuses is a SecretRefusal naming it, and the read stops
// there: nothing is returned and no later key is asked for.
func TestSecretsStopAtTheFirstRefusal(t *testing.T) {
	fake, client, _ := newFake(t)
	fake.Secrets["A"] = "a-value"
	fake.Secrets["C"] = "c-value"
	decls := []remotedef.SecretDecl{{Key: "A"}, {Key: "B"}, {Key: "C"}}

	values, err := client.Secrets(context.Background(), decls)
	var refusal *SecretRefusal
	if !errors.As(err, &refusal) || refusal.Key != "B" || refusal.Err.Status != http.StatusForbidden {
		t.Fatalf("Secrets = %v, want a SecretRefusal for B", err)
	}
	if !strings.Contains(err.Error(), "secret B refused") || values != nil {
		t.Errorf("err = %q, values = %v", err, values)
	}
	if fake.Count(http.MethodPost, "/secrets/C") != 0 {
		t.Error("a key after the refused one was read")
	}
}

// A value is returned in memory and goes nowhere else: nothing is written under
// the working directory, and the value is in no error.
func TestReadingNeverWritesTheValueToDisk(t *testing.T) {
	fake, client, _ := newFake(t)
	fake.Secrets["K"] = devtest.SecretValue
	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := client.Secrets(context.Background(), []remotedef.SecretDecl{{Key: "K"}}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("reading a secret wrote %v", entries)
	}
	_, err := client.Secret(context.Background(), "NOT-GRANTED")
	if err == nil || strings.Contains(err.Error(), devtest.SecretValue) {
		t.Errorf("err = %v", err)
	}
}

// AC7: End sends one request however often it is called, and it revokes the token.
func TestEndSendsOneRequest(t *testing.T) {
	fake, client, _ := newFake(t)
	for range 3 {
		if err := client.End(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := fake.Count(http.MethodPost, "/end"); n != 1 {
		t.Errorf("end calls = %d, want 1", n)
	}
	if calls := fake.Calls(); calls[0].Auth != "Bearer "+testNodeToken {
		t.Errorf("end carried %q", calls[0].Auth)
	}
	// The server revoked the token: it answers 401 now.
	fresh := NewNodeClient(fake.URL, testNodeID, testNodeToken, fake.Client())
	if _, _, err := fresh.Definition(context.Background()); !isTokenRefused(err) {
		t.Errorf("Definition after End = %v, want the token refused", err)
	}
}

func isTokenRefused(err error) bool {
	var tr *TokenRefusedError
	return errors.As(err, &tr)
}

// AC8: a 401 says the token was refused or revoked and names the route, and
// nothing is asked of satelle.dev afterwards except at most one end call.
func TestARefusedTokenStopsEveryLaterCall(t *testing.T) {
	fake, client, _ := newFake(t)
	fake.Secrets["K"] = "v"
	fake.Revoke()

	_, _, err := client.Definition(context.Background())
	var tr *TokenRefusedError
	if !errors.As(err, &tr) || tr.Route != "GET /api/v1/remote-nodes/"+testNodeID+"/definition" {
		t.Fatalf("Definition = %v, want the token refused on the definition route", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "refused or revoked") || !strings.Contains(msg, "/definition") {
		t.Errorf("message %q does not say the token was refused or revoked and name the route", msg)
	}
	if _, err := client.Secret(context.Background(), "K"); !isTokenRefused(err) {
		t.Errorf("Secret after a refusal = %v, want the token refused", err)
	}
	if _, _, err := client.Definition(context.Background()); !isTokenRefused(err) {
		t.Errorf("Definition after a refusal = %v, want the token refused", err)
	}
	if err := client.End(context.Background()); err != nil {
		t.Errorf("End after a refusal = %v; a 401 there means the token is already gone", err)
	}
	if calls := fake.Calls(); len(calls) != 2 || calls[0].Status != http.StatusUnauthorized || calls[1].Path != "/api/v1/remote-nodes/"+testNodeID+"/end" {
		t.Errorf("calls = %+v, want the refused definition and one end", calls)
	}
}

// AC10: every route is keyed by the node id, so a node started with another
// --session than the server's id gets a 404.
func TestACallUnderAnotherIDIsNotFound(t *testing.T) {
	fake, _, _ := newFake(t)
	other := NewNodeClient(fake.URL, "nod_other", testNodeToken, fake.Client())

	_, _, err := other.Definition(context.Background())
	var ae *hosted.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Fatalf("Definition under another id = %v, want a 404", err)
	}
	if isTokenRefused(err) {
		t.Error("a 404 is not the token's refusal")
	}
}

// The node id is escaped into the path, so an id with a slash cannot reach
// another route.
func TestTheNodeIDIsEscapedIntoThePath(t *testing.T) {
	c := NewNodeClient("https://x", "a/../b", "t", nil)
	if got := c.path("/end"); got != "/api/v1/remote-nodes/a%2F..%2Fb/end" {
		t.Errorf("path = %q", got)
	}
}
