package config

import "testing"

// sty_2e86c0cc: LoadRepoProcess describes the repo it is handed, never the
// session's SATELLE_CONFIG.
func TestLoadRepoProcessIgnoresSatelleConfigEnv(t *testing.T) {
	root := t.TempDir()
	writeSatelleToml(t, root, ConfigName, "[vars]\nwho = \"root\"\n")
	elsewhere := writeSatelleToml(t, t.TempDir(), ConfigName, "[vars]\nwho = \"elsewhere\"\n")
	t.Setenv("SATELLE_CONFIG", elsewhere)

	proc, procRoot, _, err := LoadRepoProcess(root)
	if err != nil {
		t.Fatal(err)
	}
	if proc.Vars["who"] != "root" || !sameResolvedPath(procRoot, root) {
		t.Fatalf("LoadRepoProcess = (%v, %q), want root's own config and root", proc.Vars, procRoot)
	}
}
