package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeDotEnv(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := DotEnvPath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The reader parses comments, blank lines, export, quotes and escapes.
func TestReadDotEnvParses(t *testing.T) {
	dir := writeDotEnv(t, strings.Join([]string{
		"# a comment",
		"",
		"PLAIN=value",
		"export EXPORTED = spaced value  ",
		`DOUBLE="a \"quoted\" line\nnext"`,
		`SINGLE='raw \n # kept'`,
		"TRAILING=kept # dropped",
		"EMPTY=",
		"HASH=a#b",
	}, "\n"))
	got, err := ReadDotEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN": "value", "EXPORTED": "spaced value", "DOUBLE": "a \"quoted\" line\nnext",
		"SINGLE": `raw \n # kept`, "TRAILING": "kept", "EMPTY": "", "HASH": "a#b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// A missing file is an empty map: a repo with no .env is not an error.
func TestReadDotEnvMissingIsEmpty(t *testing.T) {
	got, err := ReadDotEnv(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

// A malformed line is reported by number and never echoes the value.
func TestReadDotEnvErrorsNameTheLineNotTheValue(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no equals", "OK=1\nsecret-looking-text", "line 2"},
		{"bad key", "1BAD=hunter2", "line 1"},
		{"unterminated", "A=1\nB=\"hunter2", "line 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadDotEnv(writeDotEnv(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %s", err, tc.want)
			}
			if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "secret-looking") {
				t.Errorf("error leaks the line: %v", err)
			}
		})
	}
}
