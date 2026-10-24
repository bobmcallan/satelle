//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Typed task inputs: a task declares them (inputs_decl: in its file), an
// execution is created with values (--input name=value), and a create that does
// not satisfy the declaration is refused.

const inputsTaskHeader = "---\nid: tsk_inputs\ntype: task\nstatus: active\n"

func writeInputsTask(t *testing.T, repo, declLine string) string {
	t.Helper()
	path := filepath.Join(repo, ".satelle", "tasks", "tsk_inputs.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := inputsTaskHeader + declLine + "---\n\n# Inputs task\n\nACTION: do it. VERIFICATION: done.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// getInputs reads an execution's recorded input values from `execution get`.
func getInputs(t *testing.T, repo, eid string) map[string]any {
	t.Helper()
	out := mustRun(t, testBin, repo, "execution", "get", eid)
	var it struct {
		Inputs map[string]any `json:"inputs"`
	}
	if err := json.NewDecoder(strings.NewReader(out[strings.Index(out, "{"):])).Decode(&it); err != nil {
		t.Fatalf("execution get is not JSON: %v\n%s", err, out)
	}
	return it.Inputs
}

func executionCount(t *testing.T, repo string) int {
	t.Helper()
	return strings.Count(mustRun(t, testBin, repo, "execution", "list"), `"id": "exe_`)
}

func TestExecutionCreateInputs(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeInputsTask(t, repo, "inputs_decl: story_id:string:required, retries:int, dry_run:bool\n")
	mustRun(t, testBin, repo, "reindex")

	create := func(inputs ...string) (string, error) {
		args := []string{"execution", "create", "--parent", "tsk_inputs", "--title", "Run"}
		for _, in := range inputs {
			args = append(args, "--input", in)
		}
		return run(t, testBin, repo, args...)
	}

	t.Run("typed values are recorded", func(t *testing.T) {
		out, err := create("story_id=sty_1", "retries=3", "dry_run=true")
		if err != nil {
			t.Fatalf("create: %v\n%s", err, out)
		}
		got := getInputs(t, repo, extractID(out, "exe_"))
		if got["story_id"] != "sty_1" || got["retries"] != float64(3) || got["dry_run"] != true {
			t.Errorf("inputs = %#v", got)
		}
	})

	t.Run("a value survives the file and a reindex unchanged", func(t *testing.T) {
		tricky := "a, b=c: d  \n---\n tail "
		out, err := create("story_id="+tricky, "retries=7")
		if err != nil {
			t.Fatalf("create: %v\n%s", err, out)
		}
		eid := extractID(out, "exe_")
		mustRun(t, testBin, repo, "reindex")
		got := getInputs(t, repo, eid)
		if got["story_id"] != tricky || got["retries"] != float64(7) {
			t.Errorf("after reindex inputs = %#v", got)
		}
	})

	before := executionCount(t, repo)
	for name, tc := range map[string]struct {
		inputs []string
		want   string
	}{
		"missing required": {[]string{"retries=1"}, "required input"},
		"undeclared":       {[]string{"story_id=x", "nope=1"}, "not declared"},
		"mistyped int":     {[]string{"story_id=x", "retries=abc"}, "wants an int"},
		"mistyped bool":    {[]string{"story_id=x", "dry_run=maybe"}, "wants a bool"},
		"no equals sign":   {[]string{"story_id"}, "name=value"},
	} {
		t.Run("refused: "+name, func(t *testing.T) {
			out, err := create(tc.inputs...)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("want a refusal naming %q, got err=%v\n%s", tc.want, err, out)
			}
		})
	}
	if n := executionCount(t, repo); n != before {
		t.Errorf("a refused create left %d new execution(s)", n-before)
	}

	t.Run("an edited declaration is followed", func(t *testing.T) {
		writeInputsTask(t, repo, "")
		mustRun(t, testBin, repo, "reindex")
		if out, err := create("story_id=x"); err == nil || !strings.Contains(out, "not declared") {
			t.Fatalf("removed declaration must refuse any input, err=%v\n%s", err, out)
		}
		if out, err := create(); err != nil {
			t.Fatalf("an execution with no inputs must create: %v\n%s", err, out)
		}
	})
}

// A task that never declared inputs behaves as before: no inputs line in an
// execution's file, and a create with no --input succeeds.
func TestExecutionWithoutDeclaredInputsIsUnchanged(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	tid := extractID(mustRun(t, testBin, repo, "task", "create", "--title", "Plain",
		"--body", "ACTION: do the thing. VERIFICATION: it is done."), "tsk_")
	eid := extractID(mustRun(t, testBin, repo, "execution", "create", "--parent", tid, "--title", "Run"), "exe_")
	data, err := os.ReadFile(filepath.Join(repo, ".satelle", "tasks", tid, eid+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "inputs") {
		t.Errorf("an input-less execution file grew an inputs line:\n%s", data)
	}
	if out, err := run(t, testBin, repo, "execution", "create", "--parent", tid, "--title", "Run", "--input", "x=1"); err == nil || !strings.Contains(out, "not declared") {
		t.Errorf("an input against an input-less task must be refused, err=%v\n%s", err, out)
	}
}

// satelle validate names a malformed inputs_decl declaration.
func TestValidateReportsMalformedInputsDecl(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeInputsTask(t, repo, "inputs_decl: n:float\n")
	out, err := run(t, testBin, repo, "validate")
	if err == nil {
		t.Fatalf("a malformed declaration must fail validate:\n%s", out)
	}
	if !strings.Contains(out, "tasks/tsk_inputs") || !strings.Contains(out, "inputs_decl") || !strings.Contains(out, "string, int or bool") {
		t.Errorf("validate must name the task and the declaration problem:\n%s", out)
	}

	writeInputsTask(t, repo, "inputs_decl: n:int:required\n")
	if out, err := run(t, testBin, repo, "validate"); err != nil || !strings.Contains(out, "PASS  tasks/tsk_inputs") {
		t.Errorf("a well-formed declaration must pass validate: err=%v\n%s", err, out)
	}
}
