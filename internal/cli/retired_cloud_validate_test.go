package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// A cloud table is retired schema (sty_c00782a3): both validate verbs exit
// non-zero on one, whether it sits in the machine catalog or a repo binding, and
// the message says satelle no longer manages cloud identity and names the table.
func TestValidateVerbsRefuseRetiredCloudTables(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, repo string)
		table string
	}{
		{"catalog table", func(t *testing.T, _ string) {
			if err := os.WriteFile(config.GlobalAgentsPath(),
				[]byte("[profiles.ops]\ncommand = \"claude\"\n\n[profiles.ops.cloud.sample]\nkey = \"x\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "[profiles.ops.cloud]"},
		{"repo binding table", func(t *testing.T, repo string) {
			body := "[executor]\nharness = \"in-loop\"\n\n[executor.cloud.sample]\nkey = \"x\"\n"
			if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "[executor.cloud]"},
	}
	for _, tc := range cases {
		for _, verb := range [][]string{{"agent", "validate"}, {"validate"}} {
			t.Run(tc.name+"/"+strings.Join(verb, " "), func(t *testing.T) {
				repo := tempRepo(t)
				t.Chdir(repo)
				tc.write(t, repo)
				out, err := runRootIn(t, "", verb...)
				if err == nil {
					t.Fatalf("%v passed with a retired table:\n%s", verb, out)
				}
				got := err.Error() + out
				for _, want := range []string{"no longer manages cloud identity", "delete the " + tc.table + " table"} {
					if !strings.Contains(got, want) {
						t.Errorf("%v output lacks %q:\n%s", verb, want, got)
					}
				}
			})
		}
	}
}

// With a clean catalog the top-level verb reports the catalog check, not a
// cloud-identity one.
func TestValidateAllReportsAgentCatalog(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	out, err := runRootIn(t, "", "validate")
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS  agent catalog") || strings.Contains(out, "cloud identities") {
		t.Errorf("validate output:\n%s", out)
	}
}
