package cli

import (
	"bytes"
	"strings"
	"testing"

	"example.com/tool/internal/store"
)

func TestParseFlagsRequiresName(t *testing.T) {
	if _, err := ParseFlags(nil); err == nil {
		t.Fatal("missing --name accepted")
	}
	opts, err := ParseFlags([]string{"--name", "alpha", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Name != "alpha" || !opts.Force {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestExecuteWritesOnce(t *testing.T) {
	store.Reset()
	var out bytes.Buffer
	if code, err := Execute([]string{"--name", "alpha"}, &out); code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(out.String(), "wrote alpha") {
		t.Fatalf("out = %q", out.String())
	}
	if code, _ := Execute([]string{"--name", "alpha"}, &out); code != 1 {
		t.Fatalf("second write should fail without --force, code=%d", code)
	}
}
