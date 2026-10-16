package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// Subscribed command-policy lists (sty_041bf275). The example tool is the
// fictional orbctl / the real-looking `aws s3 rm` pattern is only a pattern in a
// list authored by the test: satelle ships none.

const (
	policyListDeny = "[[command_rule]]\npattern = \"**/aws s3 rm\"\naction = \"deny\"\nmessage = \"subscribed: s3 rm\"\n\n" +
		"[[command_rule]]\npattern = \"**/gcloud ** delete\"\naction = \"deny\"\nmessage = \"subscribed: gcloud delete\"\n"
	policyURLSub = "[[gate.command_policy]]\nname = \"cloud\"\nurl = \"%s\"\n"
)

func sha256HexOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// policyServer serves one policy list both as a catalog path (with an ETag the
// test can corrupt) and as a plain URL, and counts every request.
type policyServer struct {
	*httptest.Server
	body     atomic.Value // string
	etag     atomic.Value // string; "" sends none
	requests atomic.Int64
}

func newPolicyServer(t *testing.T, body string) *policyServer {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ps := &policyServer{}
	ps.body.Store(body)
	ps.etag.Store(sha256HexOf(body))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		ps.requests.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "ws-team", "kind": "team", "name": "Acme"}})
	})
	mux.HandleFunc("GET /api/v1/workspaces/ws-team/published/", func(w http.ResponseWriter, r *http.Request) {
		ps.requests.Add(1)
		if e := ps.etag.Load().(string); e != "" {
			w.Header().Set("ETag", `"`+e+`"`)
		}
		w.Header().Set("X-Satelle-Publish-Version", "3")
		_, _ = w.Write([]byte(ps.body.Load().(string)))
	})
	mux.HandleFunc("GET /list.toml", func(w http.ResponseWriter, r *http.Request) {
		ps.requests.Add(1)
		_, _ = w.Write([]byte(ps.body.Load().(string)))
	})
	ps.Server = httptest.NewServer(mux)
	t.Cleanup(ps.Close)
	seedCred(t, ps.URL)
	return ps
}

func (ps *policyServer) set(body string) {
	ps.body.Store(body)
	ps.etag.Store(sha256HexOf(body))
}

const policyCatalogSub = "[[gate.command_policy]]\nname = \"cloud\"\nworkspace = \"Acme\"\npath = \"policy/cloud.toml\"\n"

// policyRepo is a repo whose satelle.toml holds the given subscriptions, with the
// process pointed at it. Returns the repo root.
func policyRepo(t *testing.T, subs string) string {
	t.Helper()
	repo := syncConfigRepo(t, subs)
	pointAt(t, repo)
	return repo
}

func policyCache(t *testing.T, repo string) syncstate.PolicyState {
	t.Helper()
	st, _, err := syncstate.LoadPolicy(config.GlobalDir(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func seedPolicy(t *testing.T, repo, name, source, kind, rules string, fetched time.Time) {
	t.Helper()
	if err := syncstate.RecordPolicy(config.GlobalDir(), repo, name,
		syncstate.PolicySub{Source: source, SourceKind: kind, RulesTOML: rules}, fetched); err != nil {
		t.Fatal(err)
	}
}

func refreshOut(t *testing.T, serverURL string, names ...string) (string, error) {
	t.Helper()
	cmd, buf := testCmd()
	err := runPolicyRefresh(cmd, serverURL, names)
	return buf.String(), err
}

// AC1: with no subscription nothing is applied, read, fetched or started.
func TestCommandPolicy_NoSubscriptionAppliesNothing(t *testing.T) {
	// The binary embeds no list.
	for _, d := range config.EmbeddedDefaults() {
		if strings.Contains(d.Body, "[[command_rule]]") || strings.Contains(d.Body, "command_policy") {
			t.Errorf("embedded substrate %s/%s holds a command rule or policy", d.Kind, d.Name)
		}
	}

	// commitgate's rule set is the repo's own, and the cache is never opened: with
	// no SATELLE_HOME, GlobalDir panics under test.
	t.Setenv("SATELLE_HOME", "")
	_ = os.Unsetenv("SATELLE_HOME")
	own := []config.CommandRule{{Pattern: "a b", Action: config.CommandRuleDeny, Message: "m"}}
	got, refuse := commandRulesFrom(gateProcess{proc: config.Config{Gate: config.GateConfig{CommandRules: own}}, invoking: "/nowhere"})
	if refuse != "" || len(got) != 1 || got[0].Pattern != "a b" || got[0].Source != "" {
		t.Fatalf("rules = %+v refuse = %q, want exactly the repo's own rules", got, refuse)
	}

	// Session start spawns no refresh and reads no cache.
	if lines, refresh := policyAdvisoryAt(nil, syncstate.PolicyState{}, time.Now()); len(lines) != 0 || refresh {
		t.Fatalf("lines %v refresh %v, want neither", lines, refresh)
	}
	spawned := 0
	prev := policyRefresh
	policyRefresh = func(string) { spawned++ }
	t.Cleanup(func() { policyRefresh = prev })
	repo := tempRepo(t)
	t.Chdir(repo)
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if line := policyAdvisory(a); line != "" || spawned != 0 {
		t.Fatalf("line %q spawned %d, want silence and no spawn", line, spawned)
	}

	// `policy refresh` makes no request.
	ps := newPolicyServer(t, policyListDeny)
	policyRepo(t, "")
	out, err := refreshOut(t, ps.URL)
	if err != nil || !strings.Contains(out, "nothing to refresh") || ps.requests.Load() != 0 {
		t.Fatalf("out %q err %v requests %d, want a no-op", out, err, ps.requests.Load())
	}
}

// AC2: a malformed subscription fails `satelle validate`.
func TestCommandPolicy_ValidateRejectsMalformedSubscription(t *testing.T) {
	repo, _ := ruleRepo(t, seatNone, "\n[[gate.command_policy]]\nname = \"a\"\n")
	_ = repo
	out, err := runRootIn(t, "", "validate")
	if err == nil {
		t.Fatalf("validate accepted a subscription with no source:\n%s", out)
	}
	if !strings.Contains(out+err.Error(), "command_policy") {
		t.Fatalf("validate does not name the subscription: %v\n%s", err, out)
	}
}

// AC2: a fetched list is refused unless it parses under the command-rule grammar.
func TestPolicyRefresh_RefusesListsThatDoNotParse(t *testing.T) {
	for name, body := range map[string]string{
		"bad pattern":    "[[command_rule]]\npattern = \"a; b\"\naction = \"deny\"\nmessage = \"m\"\n",
		"unknown action": "[[command_rule]]\npattern = \"a\"\naction = \"block\"\nmessage = \"m\"\n",
		"not toml":       "<html>sign in</html>",
		"no rules":       "x = 1\n",
		"one bad rule among good": policyListDeny +
			"\n[[command_rule]]\npattern = \"c\"\naction = \"nope\"\nmessage = \"m\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			ps := newPolicyServer(t, body)
			repo := policyRepo(t, strings.Replace(policyURLSub, "%s", ps.URL+"/list.toml", 1))
			out, err := refreshOut(t, "")
			if err == nil || !strings.Contains(out, "FAIL  cloud") || !strings.Contains(out, "not a command-rule list") {
				t.Fatalf("out %q err %v, want a refusal", out, err)
			}
			if st := policyCache(t, repo); st.Subs["cloud"].RulesTOML != "" || st.Subs["cloud"].LastError == "" {
				t.Fatalf("a list that does not parse reached the cache: %+v", st.Subs["cloud"])
			}
		})
	}
}

func TestPolicyRefresh_FetchesCatalogAndURLLists(t *testing.T) {
	ps := newPolicyServer(t, policyListDeny)
	repo := policyRepo(t, policyCatalogSub+"\n"+
		"[[gate.command_policy]]\nname = \"vendor\"\nurl = \""+ps.URL+"/list.toml\"\n")
	out, err := refreshOut(t, ps.URL)
	if err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	st := policyCache(t, repo)
	if c := st.Subs["cloud"]; c.RulesTOML != policyListDeny || c.SourceKind != "catalog" || c.SHA256 != sha256HexOf(policyListDeny) || c.FetchedAt.IsZero() {
		t.Fatalf("catalog entry = %+v", c)
	}
	if v := st.Subs["vendor"]; v.RulesTOML != policyListDeny || v.SourceKind != "url" {
		t.Fatalf("url entry = %+v", v)
	}
	if !strings.Contains(out, "unverified source") {
		t.Errorf("a URL refresh does not say it is unverified:\n%s", out)
	}
	// Naming one subscription refreshes only it; an unknown name is refused.
	if _, err := refreshOut(t, ps.URL, "nope"); err == nil {
		t.Fatal("an unknown subscription name was accepted")
	}
}

// AC3: the repo's rules are evaluated first, so they win; a subscribed rule
// still judges a command no repo rule matches.
func TestCommandPolicy_RepoRuleWinsOverSubscribedRule(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	seedPolicy(t, repo, "cloud", "https://example.com/p.toml", "url", policyListDeny, time.Now())
	proc := config.Config{Gate: config.GateConfig{
		CommandRules:    []config.CommandRule{{Pattern: "**/aws s3 rm", Action: config.CommandRuleWarn, Message: "repo: s3 rm"}},
		CommandPolicies: []config.CommandPolicy{{Name: "cloud", URL: "https://example.com/p.toml"}},
	}}
	if err := config.ValidateGateCommandRules(proc, "x"); err != nil {
		t.Fatal(err)
	}
	rules, refuse := commandRulesFrom(gateProcess{proc: proc, invoking: repo})
	if refuse != "" || len(rules) != 3 {
		t.Fatalf("rules = %+v refuse %q, want the repo rule then both subscribed rules", rules, refuse)
	}
	noSeat := func() (seatInfo, bool, error) { return seatInfo{}, false, nil }

	d := evalCommandRules(rules, "aws s3 rm s3://bucket --recursive", noSeat)
	if d.Decision != commandRuleWarned || d.Rule.Source != "" || !strings.Contains(d.Reason, "repo: s3 rm") {
		t.Fatalf("a repo warn must beat a subscribed deny for the same pattern: %+v", d)
	}
	d = evalCommandRules(rules, "gcloud compute instances delete vm1", noSeat)
	if d.Decision != commandRuleDenied || d.Rule.Source != "cloud" || !strings.Contains(d.Reason, "subscribed: gcloud delete") || !strings.Contains(d.Reason, "(command policy cloud)") {
		t.Fatalf("a subscribed deny must still apply to a command no repo rule matches: %+v", d)
	}
	if d = evalCommandRules(rules, "ls", noSeat); d.Matched {
		t.Fatalf("an unrelated command matched: %+v", d)
	}
}

// AC3 through the real hook: a subscribed deny reaches the agent, the ledger row
// says where the rule came from, and the cache can be missing, torn or re-pointed
// without refusing anything.
func TestCommandPolicy_CommitgateAppliesSubscribedList(t *testing.T) {
	const src = "https://example.com/p.toml"
	repo, _ := ruleRepo(t, seatNone, "\n[[gate.command_policy]]\nname = \"cloud\"\nurl = \""+src+"\"\n")
	if out, err := commitgateOn(t, "claude", "aws s3 rm s3://b"); err != nil || out != "" {
		t.Fatalf("no cache yet must pass: %v %q", err, out)
	}
	seedPolicy(t, repo, "cloud", src, "url", policyListDeny, time.Now())
	out, err := commitgateOn(t, "claude", "aws s3 rm s3://b")
	if err == nil {
		t.Fatalf("a subscribed deny did not refuse the command:\n%s", out)
	}
	if reason := ruleDenyReason(t, out); !strings.Contains(reason, "subscribed: s3 rm") || !strings.Contains(reason, "command policy cloud") {
		t.Fatalf("reason = %q", reason)
	}
	if out, err = commitgateOn(t, "claude", "ls"); err != nil || out != "" {
		t.Fatalf("an unrelated command must pass: %v %q", err, out)
	}
	// A list cached for another source is not this subscription's.
	seedPolicy(t, repo, "cloud", "https://elsewhere.example/p.toml", "url", policyListDeny, time.Now())
	if out, err = commitgateOn(t, "claude", "aws s3 rm s3://b"); err != nil || out != "" {
		t.Fatalf("a list from another source applied: %v %q", err, out)
	}
	// A torn cache contributes nothing and never refuses.
	if werr := os.WriteFile(syncstate.PolicyPath(config.GlobalDir(), repo), []byte("{torn"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if out, err = commitgateOn(t, "claude", "aws s3 rm s3://b"); err != nil || out != "" {
		t.Fatalf("a torn cache refused a command: %v %q", err, out)
	}
}

func TestCommandPolicy_LedgerRowNamesTheSubscription(t *testing.T) {
	const src = "https://example.com/p.toml"
	list := "[[command_rule]]\npattern = \"orbctl apply\"\naction = \"deny\"\nallow = [\"in_progress\"]\nmessage = \"apply\"\n"
	repo, _ := ruleRepo(t, seatMine, "\n[[gate.command_policy]]\nname = \"cloud\"\nurl = \""+src+"\"\n")
	seedPolicy(t, repo, "cloud", src, "url", list, time.Now())
	if out, err := commitgateOn(t, "claude", "orbctl apply -f x"); err != nil {
		t.Fatalf("allowed at in_progress: %v\n%s", err, out)
	}
	rows := ledgerRows(t, ledger.KindCommandRule)
	if len(rows) != 1 || !strings.Contains(string(rows[0].Payload), `"source":"cloud"`) {
		t.Fatalf("ledger rows = %+v, want one naming source cloud", rows)
	}
}

// AC3: a malformed subscription refuses every shell command, like a malformed
// rule, instead of silently dropping the repo's denies.
func TestCommandPolicy_MalformedSubscriptionFailsClosed(t *testing.T) {
	ruleRepo(t, seatNone, "\n[[gate.command_policy]]\nname = \"a\"\n")
	out, err := commitgateOn(t, "claude", "ls")
	if err == nil {
		t.Fatalf("a malformed subscription must refuse shell commands:\n%s", out)
	}
	if reason := ruleDenyReason(t, out); !strings.Contains(reason, "command policy subscriptions") || !strings.Contains(reason, "subscription 0") {
		t.Fatalf("reason = %q", reason)
	}
}

// AC4: the session-start decision over explicit inputs.
func TestPolicyAdvisoryAt(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cat := config.CommandPolicy{Name: "cloud", Workspace: "w", Path: "p", MaxAge: "24h"}
	url := config.CommandPolicy{Name: "vendor", URL: "https://example.com/p.toml", MaxAge: "24h"}
	cached := func(sub config.CommandPolicy, age time.Duration, lastErr string) syncstate.PolicySub {
		return syncstate.PolicySub{Source: sub.Source(), SourceKind: policySourceKind(sub),
			RulesTOML: policyListDeny, FetchedAt: now.Add(-age), LastError: lastErr}
	}
	longAgo := now.Add(-time.Hour)
	for _, tc := range []struct {
		name      string
		subs      []config.CommandPolicy
		st        syncstate.PolicyState
		wantLines []string
		refresh   bool
	}{
		{"fresh catalog list: silent", []config.CommandPolicy{cat},
			syncstate.PolicyState{Subs: map[string]syncstate.PolicySub{"cloud": cached(cat, time.Hour, "")}}, nil, false},
		{"missing: warns and refreshes", []config.CommandPolicy{cat}, syncstate.PolicyState{},
			[]string{"not yet fetched; no rules from it apply"}, true},
		{"stale: unverified with the time, and refreshes", []config.CommandPolicy{cat},
			syncstate.PolicyState{Subs: map[string]syncstate.PolicySub{"cloud": cached(cat, 30*time.Hour, "")}},
			[]string{"unverified (last fetched " + now.Add(-30*time.Hour).Format(time.RFC3339) + ")"}, true},
		{"stale inside the debounce: the line, no refresh", []config.CommandPolicy{cat},
			syncstate.PolicyState{CheckedAttempt: now.Add(-time.Minute), Subs: map[string]syncstate.PolicySub{"cloud": cached(cat, 30*time.Hour, "")}},
			[]string{"unverified (last fetched"}, false},
		{"stale past the debounce refreshes", []config.CommandPolicy{cat},
			syncstate.PolicyState{CheckedAttempt: now.Add(-substrateRefreshDebounce - time.Second), Subs: map[string]syncstate.PolicySub{"cloud": cached(cat, 30*time.Hour, "")}},
			[]string{"unverified"}, true},
		{"several stale subscriptions: lines for each", []config.CommandPolicy{cat, url},
			syncstate.PolicyState{CheckedAttempt: longAgo.Add(-24 * time.Hour), Subs: map[string]syncstate.PolicySub{
				"cloud": cached(cat, 30*time.Hour, ""), "vendor": cached(url, 30*time.Hour, "")}},
			[]string{"command policy cloud unverified", "command policy vendor unverified"}, true},
		{"a URL list is named an unverified source", []config.CommandPolicy{url},
			syncstate.PolicyState{Subs: map[string]syncstate.PolicySub{"vendor": cached(url, time.Hour, "")}},
			[]string{"from an unverified source (url, no checksum)"}, false},
		{"failure after a good list names the reason", []config.CommandPolicy{cat},
			syncstate.PolicyState{Subs: map[string]syncstate.PolicySub{"cloud": cached(cat, time.Hour, "server down")}},
			[]string{"refresh failed: server down; keeping last good list from " + now.Add(-time.Hour).Format(time.RFC3339)}, false},
		{"a list cached for another source is missing", []config.CommandPolicy{cat},
			syncstate.PolicyState{Subs: map[string]syncstate.PolicySub{"cloud": {Source: "old:path", SourceKind: "catalog", RulesTOML: "x", FetchedAt: now}}},
			[]string{"not yet fetched"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines, refresh := policyAdvisoryAt(tc.subs, tc.st, now)
			joined := strings.Join(lines, "\n")
			if refresh != tc.refresh {
				t.Errorf("refresh = %v, want %v", refresh, tc.refresh)
			}
			if len(tc.wantLines) == 0 && len(lines) != 0 {
				t.Errorf("lines = %q, want silence", joined)
			}
			for _, w := range tc.wantLines {
				if !strings.Contains(joined, w) {
					t.Errorf("lines lack %q:\n%s", w, joined)
				}
			}
			if len(lines) > 0 && !strings.Contains(joined, "satelle: command policy") {
				t.Errorf("a line does not name a command policy: %q", joined)
			}
		})
	}
}

// AC4: the session-start caller reads the cache, stamps the attempt, starts the
// one refresh through the seam and never waits on it.
func TestPolicyAdvisory_StartsOneRefreshPerWindow(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	toml := strings.Replace(policyURLSub, "%s", "https://example.com/a.toml", 1) +
		"\n[[gate.command_policy]]\nname = \"second\"\nurl = \"https://example.com/b.toml\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte("[review]\ngate_create = false\n\n"+toml), 0o644); err != nil {
		t.Fatal(err)
	}
	var roots []string
	prev := policyRefresh
	policyRefresh = func(root string) { roots = append(roots, root) } // returns at once: nothing waits
	t.Cleanup(func() { policyRefresh = prev })
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	line := policyAdvisory(a)
	if len(roots) != 1 || roots[0] != a.RepoRoot {
		t.Fatalf("spawns = %v, want one for %q", roots, a.RepoRoot)
	}
	if strings.Count(line, "not yet fetched") != 2 {
		t.Fatalf("line = %q, want both subscriptions named", line)
	}
	if st := policyCache(t, a.RepoRoot); st.CheckedAttempt.IsZero() {
		t.Fatal("the attempt stamp was not written")
	}
	if policyAdvisory(a); len(roots) != 1 {
		t.Fatalf("a second session inside the window spawned again: %v", roots)
	}

	// With no stamp the debounce cannot hold, so nothing is spawned: an unwritable
	// cache directory stands in for a failed stamp.
	if err := os.Remove(syncstate.PolicyPath(config.GlobalDir(), a.RepoRoot)); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Dir(syncstate.PolicyPath(config.GlobalDir(), a.RepoRoot))
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("a file where the directory should be"), 0o644); err != nil {
		t.Fatal(err)
	}
	if policyAdvisory(a); len(roots) != 1 {
		t.Fatalf("a failed stamp must not spawn: %v", roots)
	}
}

// AC4: the refresh runs from the repo root, as `satelle policy refresh`.
func TestPolicyRefreshCmdRunsFromTheRepoRoot(t *testing.T) {
	cmd := policyRefreshCmd("/bin/satelle", "/work/repo")
	if cmd.Dir != "/work/repo" || strings.Join(cmd.Args, " ") != "/bin/satelle policy refresh" {
		t.Fatalf("cmd = dir %q args %q", cmd.Dir, cmd.Args)
	}
}

// AC4: the command path makes no network call and starts no process of its own.
func TestCommandRulesFile_NoNetworkOrExecImport(t *testing.T) {
	b, err := os.ReadFile("command_rules.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"net/http"`, `"os/exec"`, `internal/hosted"`, `"net"`} {
		if bytes.Contains(b, []byte(banned)) {
			t.Errorf("command_rules.go imports %s", banned)
		}
	}
	if !bytes.Contains(b, []byte(`internal/syncstate"`)) {
		t.Error("the merge reads the cache through syncstate")
	}
	// syncstate itself reaches no HTTP client.
	out, err := exec.Command("go", "list", "-deps", "github.com/bobmcallan/satelle/internal/syncstate").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "net/http" || strings.HasSuffix(dep, "/internal/hosted") {
			t.Errorf("syncstate depends on %s", dep)
		}
	}
}

// AC5: every failed refresh keeps the last good list unchanged, records the
// reason, exits non-zero, and is named by the next session start.
func TestPolicyRefresh_FailureKeepsLastGoodList(t *testing.T) {
	const second = "[[command_rule]]\npattern = \"other\"\naction = \"deny\"\nmessage = \"m\"\n"
	for _, tc := range []struct {
		name    string
		sub     func(ps *policyServer) string
		corrupt func(ps *policyServer)
		reason  string
	}{
		{"catalog unreachable", func(ps *policyServer) string { return policyCatalogSub },
			func(ps *policyServer) { ps.Close() }, ""},
		{"catalog body differs from its checksum", func(ps *policyServer) string { return policyCatalogSub },
			func(ps *policyServer) { ps.body.Store(second) }, "does not match its checksum"},
		{"catalog returns no checksum", func(ps *policyServer) string { return policyCatalogSub },
			func(ps *policyServer) { ps.etag.Store("") }, "no checksum"},
		{"catalog list does not parse", func(ps *policyServer) string { return policyCatalogSub },
			func(ps *policyServer) { ps.set("not = [valid") }, "not a command-rule list"},
		{"url unreachable", func(ps *policyServer) string {
			return "[[gate.command_policy]]\nname = \"cloud\"\nurl = \"" + ps.URL + "/list.toml\"\n"
		}, func(ps *policyServer) { ps.Close() }, ""},
		{"url list does not parse", func(ps *policyServer) string {
			return "[[gate.command_policy]]\nname = \"cloud\"\nurl = \"" + ps.URL + "/list.toml\"\n"
		}, func(ps *policyServer) { ps.set("<html>") }, "not a command-rule list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := newPolicyServer(t, policyListDeny)
			repo := policyRepo(t, tc.sub(ps))
			if out, err := refreshOut(t, ps.URL); err != nil {
				t.Fatalf("the good refresh failed: %v\n%s", err, out)
			}
			good := policyCache(t, repo).Subs["cloud"]
			tc.corrupt(ps)
			out, err := refreshOut(t, ps.URL)
			if err == nil || !strings.Contains(out, "FAIL  cloud") {
				t.Fatalf("a failed refresh must exit non-zero: err %v\n%s", err, out)
			}
			after := policyCache(t, repo).Subs["cloud"]
			if after.RulesTOML != good.RulesTOML || !after.FetchedAt.Equal(good.FetchedAt) || after.SHA256 != good.SHA256 {
				t.Fatalf("the last good list changed:\nbefore %+v\nafter  %+v", good, after)
			}
			if after.LastError == "" || !strings.Contains(after.LastError, tc.reason) {
				t.Fatalf("last_error = %q, want it to hold %q", after.LastError, tc.reason)
			}
			// The next session start names the subscription and the reason.
			subs, _, _ := policySubscriptions()
			lines, _ := policyAdvisoryAt(subs, policyCache(t, repo), time.Now())
			if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "command policy cloud") ||
				!strings.Contains(joined, "refresh failed: "+after.LastError) || !strings.Contains(joined, "keeping last good list") {
				t.Fatalf("session start lines = %q", joined)
			}
		})
	}
}

// AC5: a first fetch that fails caches nothing, so no half-written list exists.
func TestPolicyRefresh_FailedFirstFetchCachesNoRules(t *testing.T) {
	ps := newPolicyServer(t, policyListDeny)
	ps.etag.Store(strings.Repeat("0", 64))
	repo := policyRepo(t, policyCatalogSub)
	if _, err := refreshOut(t, ps.URL); err == nil {
		t.Fatal("a checksum mismatch was accepted")
	}
	if c := policyCache(t, repo).Subs["cloud"]; c.RulesTOML != "" || !c.FetchedAt.IsZero() || c.LastError == "" {
		t.Fatalf("entry = %+v, want a recorded failure and no list", c)
	}
	lines, _ := policyAdvisoryAt([]config.CommandPolicy{{Name: "cloud", Workspace: "Acme", Path: "policy/cloud.toml"}}, policyCache(t, repo), time.Now())
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "not yet fetched") || !strings.Contains(joined, "refresh failed") {
		t.Fatalf("lines = %q", joined)
	}
}

// A failure reason comes from a remote source and is shown to the agent: it is
// one bounded line.
func TestPolicyReasonIsOneBoundedLine(t *testing.T) {
	got := policyReason(os.ErrInvalid)
	if got == "" || strings.ContainsAny(got, "\r\n") {
		t.Fatalf("reason = %q", got)
	}
	long := policyReason(errString("line one\nline two\x1b[31m " + strings.Repeat("x", 1000)))
	if strings.ContainsAny(long, "\n\x1b") || len([]rune(long)) > policyReasonMax+1 {
		t.Fatalf("reason not bounded/clean: %q", long)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// AC5: a URL source is named as unverified in the session-start warning and in
// the help.
func TestPolicyURLSourceIsNamedUnverified(t *testing.T) {
	sub := config.CommandPolicy{Name: "vendor", URL: "https://example.com/p.toml"}
	st := syncstate.PolicyState{Subs: map[string]syncstate.PolicySub{"vendor": {
		Source: sub.Source(), SourceKind: policySourceURL, RulesTOML: policyListDeny, FetchedAt: time.Now()}}}
	if line := policyLine(sub, st, time.Now()); !strings.Contains(line, "from an unverified source (url, no checksum)") {
		t.Fatalf("line = %q", line)
	}
	out, err := runRootIn(t, "", "policy", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"no checksum or signature", "UNVERIFIED", "satelle help command-rules"} {
		if !strings.Contains(flat, want) {
			t.Errorf("policy --help lacks %q", want)
		}
	}
	topic, ok := help.Get("command-rules")
	if !ok {
		t.Fatal("help topic command-rules missing")
	}
	topicFlat := strings.Join(strings.Fields(topic.Body), " ")
	for _, want := range []string{"A URL list is unverified", "gives no checksum or signature", "satelle publishes no list", "a repo that does not subscribe has none"} {
		if !strings.Contains(topicFlat, want) {
			t.Errorf("help command-rules lacks %q", want)
		}
	}
}

// Every adapter carries the session-start policy line to the agent.
func TestPolicyLineReachesEveryAdapter(t *testing.T) {
	const line = "⚠️ satelle: command policy cloud unverified (last fetched 2026-10-01T00:00:00Z)."
	for _, harness := range []string{agentcli.HarnessClaude, agentcli.HarnessGrok, agentcli.HarnessCursor, agentcli.HarnessPi} {
		out := string(agentcli.SessionContextOutput(harness, "SessionStart", "PRINCIPLES", line))
		var doc any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", harness, err)
		}
		// JSON may escape the emoji; compare the decoded text.
		if !strings.Contains(flattenJSONStrings(doc), "command policy cloud unverified") {
			t.Errorf("%s: the policy line does not reach the agent:\n%s", harness, out)
		}
	}
}

func flattenJSONStrings(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, e := range x {
			b.WriteString(flattenJSONStrings(e))
			b.WriteString("\n")
		}
		return b.String()
	case map[string]any:
		var b strings.Builder
		for _, e := range x {
			b.WriteString(flattenJSONStrings(e))
			b.WriteString("\n")
		}
		return b.String()
	}
	return ""
}

func TestPolicyStatusReadsTheCacheOnly(t *testing.T) {
	ps := newPolicyServer(t, policyListDeny)
	repo := policyRepo(t, strings.Replace(policyURLSub, "%s", ps.URL+"/list.toml", 1))
	cmd, buf := testCmd()
	if err := runPolicyStatus(cmd); err != nil || !strings.Contains(buf.String(), "not yet fetched") {
		t.Fatalf("status = %q err %v", buf.String(), err)
	}
	seedPolicy(t, repo, "cloud", ps.URL+"/list.toml", "url", policyListDeny, time.Now())
	cmd, buf = testCmd()
	if err := runPolicyStatus(cmd); err != nil || !strings.Contains(buf.String(), "2 rule(s)") || !strings.Contains(buf.String(), "unverified source") {
		t.Fatalf("status = %q err %v", buf.String(), err)
	}
	if ps.requests.Load() != 0 {
		t.Fatalf("status made %d request(s)", ps.requests.Load())
	}
}

// `satelle validate` reports each subscription from the cache and never fetches.
func TestValidateReportsPolicyCacheWithoutFetching(t *testing.T) {
	ps := newPolicyServer(t, policyListDeny)
	repo := policyRepo(t, strings.Replace(policyURLSub, "%s", ps.URL+"/list.toml", 1))
	_ = repo
	var out bytes.Buffer
	reportPolicyCache(&out)
	if !strings.Contains(out.String(), "WARN") || !strings.Contains(out.String(), "not yet fetched") {
		t.Fatalf("report = %q", out.String())
	}
	if ps.requests.Load() != 0 {
		t.Fatalf("validate made %d request(s)", ps.requests.Load())
	}
}
