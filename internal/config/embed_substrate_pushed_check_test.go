package config_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/structure"
)

const pushedCheck = "satelle-substrate-pushed-check"

// The substrate-pushed check ships as a canonical functional-check default and
// is NOT wired into any embedded route: whether a repo's close demands that the
// substrate it changed be on the hosted copy is the repo's configuration.
func TestEmbeddedSubstratePushedCheckShipsUnwired(t *testing.T) {
	body := embeddedDefaultBody(t, "skills", pushedCheck)
	if !strings.Contains(body, "scope: system") {
		t.Error("an embedded default is scope: system")
	}
	if !strings.Contains(body, "type:functional-check") {
		t.Error("the check must carry the type:functional-check tag")
	}
	if structure.CheckFence(body) == "" {
		t.Error("the check must carry a ```check script")
	}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == pushedCheck {
			continue
		}
		if strings.Contains(d.Body, pushedCheck) {
			t.Errorf("%s/%s names %s: the embedded route must not wire it", d.Kind, d.Name, pushedCheck)
		}
	}
}

// The check script reads the story id from the transition payload on stdin, asks
// `satelle sync status --story <id>`, and accepts only on that command's exit 0.
func TestSubstratePushedCheckScriptFollowsSyncStatusExit(t *testing.T) {
	script := structure.CheckFence(embeddedDefaultBody(t, "skills", pushedCheck))
	if script == "" {
		t.Fatal("no ```check script")
	}
	scriptPath := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(scriptPath, []byte(script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, fakeExit, stdin string) (string, int) {
		t.Helper()
		bin := t.TempDir()
		fake := "#!/usr/bin/env bash\necho \"fake satelle $*\"\n" +
			"if [ \"$1 $2 $3\" != \"sync status --story\" ]; then echo wrong-args; exit 99; fi\n" +
			"echo \"skills unpushed\"\nexit " + fakeExit + "\n"
		if err := os.WriteFile(filepath.Join(bin, "satelle"), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", scriptPath)
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return buf.String(), code
	}
	const payload = `{"story":{"id":"sty_abc12345"},"from":"in_progress","to":"done"}`

	out, code := run(t, "0", payload)
	if code != 0 || !strings.Contains(out, "fake satelle sync status --story sty_abc12345") {
		t.Errorf("exit 0 from sync status must accept and name the story: code=%d\n%s", code, out)
	}
	out, code = run(t, "1", payload)
	if code == 0 || !strings.Contains(out, "skills unpushed") || !strings.Contains(out, "sty_abc12345") {
		t.Errorf("non-zero from sync status must reject and carry its output: code=%d\n%s", code, out)
	}
	if out, code = run(t, "0", `{"from":"in_progress","to":"done"}`); code == 0 || !strings.Contains(out, "could not read the story id") {
		t.Errorf("a payload without a story id must reject: code=%d\n%s", code, out)
	}
}
