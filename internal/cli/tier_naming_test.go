package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestLocalTierHelpNamesSatelled pins AC2/AC5/AC6: serve and service help
// describe the local daemon as satelled, keep the `satelle serve` verb, and
// never call the local tier "satelle-serve", "satelle web server", or "the
// satelle server". Hosted-tier "satelle-server" is allowed.
func TestLocalTierHelpNamesSatelled(t *testing.T) {
	root := NewRootCmd()
	for _, path := range [][]string{{"serve"}, {"service"}, {"service", "install"}, {"service", "status"}, {"update"}} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil {
			t.Fatalf("find %v: %v", path, err)
		}
		text := cmd.Short + "\n" + cmd.Long + "\n" + cmd.Use
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			text += "\n" + f.Usage
		})
		if path[0] == "serve" || path[0] == "service" {
			if !strings.Contains(text, "satelled") {
				t.Errorf("%v help must name satelled:\n%s", path, text)
			}
		}
		if forbidden := localTierForbiddenPhrase(text); forbidden != "" {
			t.Errorf("%v help contains forbidden local-tier phrase %q:\n%s", path, forbidden, text)
		}
	}
	// AC5: the CLI verb is still `satelle serve`.
	if _, _, err := root.Find([]string{"serve"}); err != nil {
		t.Fatal("satelle serve must remain a command")
	}
}

func TestLocalTierTreeHasNoAmbiguousServer(t *testing.T) {
	root := NewRootCmd()
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		t.Helper()
		text := c.Short + "\n" + c.Long
		c.Flags().VisitAll(func(f *pflag.Flag) {
			text += "\n" + f.Usage
		})
		if forbidden := localTierForbiddenPhrase(text); forbidden != "" {
			t.Errorf("command %q help contains forbidden local-tier phrase %q", c.CommandPath(), forbidden)
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// localTierForbiddenPhrase returns the first phrase that would call the local
// tier a "server" in a way that could be read as the hosted tier. Hosted-qualified
// forms (and the out-of-scope [server] endpoint / SATELLE_SERVER_ENDPOINT key
// names) are stripped first so login/sync/settings help does not trip the check.
func localTierForbiddenPhrase(text string) string {
	stripped := strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, allow := range []string{
		"satelle-server",
		"hosted-server",
		"hosted server",
		"[server]",
		"--server",
		"--global server",
		"settings server",
		"no server",
		"server-side",
		"the server",
		"configured server",
		"contacting the server",
		"server url",
		"global/repo server",
		"which server",
		"satelle_server_endpoint",
		"server <url>",
		"current server",
		"sets the server",
		"configuring the server",
		"server and authenticates",
		"server is separate",
		"server manages",
		"print the current",
	} {
		stripped = strings.ReplaceAll(stripped, allow, "")
	}
	for _, p := range []string{"satelle-serve", "satelle web server", "the satelle server", "server link", "web server"} {
		if strings.Contains(stripped, p) {
			return p
		}
	}
	if i := strings.Index(stripped, "server"); i >= 0 {
		start := i - 24
		if start < 0 {
			start = 0
		}
		end := i + 30
		if end > len(stripped) {
			end = len(stripped)
		}
		return strings.TrimSpace(stripped[start:end])
	}
	return ""
}
