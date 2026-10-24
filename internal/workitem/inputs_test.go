package workitem

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseInputDecls(t *testing.T) {
	got, err := ParseInputDecls("story_id:string:required, retries:int, dry_run:bool:optional")
	if err != nil {
		t.Fatal(err)
	}
	want := []InputDecl{
		{Name: "story_id", Type: "string", Required: true},
		{Name: "retries", Type: "int"},
		{Name: "dry_run", Type: "bool"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if again, _ := ParseInputDecls(FormatInputDecls(got)); !reflect.DeepEqual(again, want) {
		t.Fatalf("format/parse round trip: %+v", again)
	}
	if d, err := ParseInputDecls(""); d != nil || err != nil {
		t.Fatalf("empty line = %v, %v", d, err)
	}
}

func TestParseInputDeclsMalformed(t *testing.T) {
	for line, frag := range map[string]string{
		"n:float":              "must be string, int or bool",
		"n":                    "name:type",
		"n:int:maybe":          "required or optional",
		"Bad:int":              "must match",
		"1n:int":               "must match",
		"a:int, a:string":      "declared twice",
		"a:int,,b:int":         "name:type",
		"a:int:required:extra": "name:type",
	} {
		_, err := ParseInputDecls(line)
		if err == nil || !strings.Contains(err.Error(), frag) {
			t.Errorf("%q: err = %v, want %q", line, err, frag)
		}
	}
}

func TestCoerceInputs(t *testing.T) {
	decls, _ := ParseInputDecls("story_id:string:required, retries:int, dry_run:bool")
	got, err := CoerceInputs(decls, map[string]string{"story_id": "sty_1", "retries": "3", "dry_run": "true"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"story_id": "sty_1", "retries": int64(3), "dry_run": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for name, tc := range map[string]struct {
		raw  map[string]string
		frag string
	}{
		"missing required": {map[string]string{"retries": "1"}, "required input"},
		"undeclared":       {map[string]string{"story_id": "x", "nope": "1"}, "not declared"},
		"bad int":          {map[string]string{"story_id": "x", "retries": "abc"}, "wants an int"},
		"bad bool":         {map[string]string{"story_id": "x", "dry_run": "maybe"}, "wants a bool"},
	} {
		if _, err := CoerceInputs(decls, tc.raw); err == nil || !strings.Contains(err.Error(), tc.frag) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.frag)
		}
	}
	// A task with no declarations accepts no inputs and yields none.
	if v, err := CoerceInputs(nil, nil); v != nil || err != nil {
		t.Fatalf("no decls, no inputs = %v, %v", v, err)
	}
	if _, err := CoerceInputs(nil, map[string]string{"x": "1"}); err == nil {
		t.Fatal("an input against a task with no declarations must be refused")
	}
}

func TestInputEnv(t *testing.T) {
	if InputEnv(nil) != nil {
		t.Fatal("no values must add no env")
	}
	env := InputEnv(map[string]any{"story_id": "sty_1", "retries": int64(3), "dry_run": true})
	want := map[string]string{
		"SATELLE_INPUT_STORY_ID": "sty_1",
		"SATELLE_INPUT_RETRIES":  "3",
		"SATELLE_INPUT_DRY_RUN":  "true",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("got %v, want %v", env, want)
	}
}

// Marshal/Parse keep an execution's values lossless, whatever the strings hold.
func TestInputValuesMarkdownRoundTrip(t *testing.T) {
	values := map[string]any{
		"tricky":  "a, b=c: d  \n---\n  tail  ",
		"padded":  "  spaced  ",
		"retries": int64(3),
		"on":      true,
	}
	in := Item{ID: "exe_1", Kind: KindExecution, Title: "run", Status: "backlog", InputValues: values}
	out, err := Parse(Marshal(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.InputValues, values) {
		t.Fatalf("got %#v, want %#v", out.InputValues, values)
	}
	if out.Title != "run" {
		t.Fatalf("a value broke the frontmatter: title = %q", out.Title)
	}
}

func TestInputDeclsMarkdownRoundTrip(t *testing.T) {
	decls, _ := ParseInputDecls("a:string:required, b:int")
	in := Item{ID: "tsk_1", Kind: KindTask, Title: "t", Status: "active", Inputs: decls}
	out, err := Parse(Marshal(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Inputs, decls) {
		t.Fatalf("got %+v, want %+v", out.Inputs, decls)
	}
}

// A malformed declaration does not fail Parse; it leaves Inputs nil for
// structure.CheckTask to report from the file.
func TestParseMalformedDeclLeavesInputsNil(t *testing.T) {
	out, err := Parse([]byte("---\nid: tsk_1\ntype: task\nstatus: active\ninputs_decl: n:float\n---\n\n# t\n"))
	if err != nil || out.Inputs != nil {
		t.Fatalf("got %+v, %v", out.Inputs, err)
	}
}

// An item with no inputs marshals byte-identically to before they existed.
func TestNoInputsMarshalUnchanged(t *testing.T) {
	b := string(Marshal(Item{ID: "tsk_1", Kind: KindTask, Title: "t", Status: "active"}))
	if strings.Contains(b, "inputs") {
		t.Fatalf("an input-less item grew an inputs line:\n%s", b)
	}
}

func TestStoreInputsPersistAndKeepRules(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	decls, _ := ParseInputDecls("story_id:string:required")

	task, err := st.Create(ctx, CreateInput{Kind: KindTask, Title: "task", Inputs: decls}, now)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := st.Create(ctx, CreateInput{
		Kind: KindExecution, Title: "run", ParentID: task.ID,
		InputValues: map[string]any{"story_id": "sty_1", "n": int64(3)},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	gotTask, _ := st.Get(ctx, task.ID)
	gotExe, _ := st.Get(ctx, exe.ID)
	if !reflect.DeepEqual(gotTask.Inputs, decls) {
		t.Fatalf("task decls = %+v", gotTask.Inputs)
	}
	if !reflect.DeepEqual(gotExe.InputValues, exe.InputValues) {
		t.Fatalf("execution values = %#v", gotExe.InputValues)
	}

	// An execution upserted without values keeps the stored ones.
	blank := gotExe
	blank.InputValues = nil
	if _, err := st.Upsert(ctx, blank, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(ctx, exe.ID); !reflect.DeepEqual(got.InputValues, exe.InputValues) {
		t.Fatalf("Upsert wiped input values: %#v", got.InputValues)
	}

	// A task upserted without a declaration clears it: the file is the authority.
	bare := gotTask
	bare.Inputs = nil
	if _, err := st.Upsert(ctx, bare, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(ctx, task.ID); len(got.Inputs) != 0 {
		t.Fatalf("Upsert kept a removed declaration: %+v", got.Inputs)
	}
}
