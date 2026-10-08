package cli

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

// resetFlagState must make a second Execute of the same command tree in one
// process independent of the first: a stale SetOut on a child must not swallow
// the second run's output, and slice flags must not accumulate across runs.
func TestResetFlagStateIsolatesRepeatedExecutes(t *testing.T) {
	var gotTags []string
	var gotName string
	root := &cobra.Command{Use: "root", SilenceUsage: true}
	child := &cobra.Command{
		Use: "child",
		RunE: func(c *cobra.Command, _ []string) error {
			gotTags, _ = c.Flags().GetStringSlice("tags")
			gotName, _ = c.Flags().GetString("name")
			fmt.Fprint(c.OutOrStdout(), "ran")
			return nil
		},
	}
	child.Flags().StringSlice("tags", nil, "")
	child.Flags().String("name", "dflt", "")
	root.AddCommand(child)

	run := func(out *bytes.Buffer, args ...string) {
		t.Helper()
		resetFlagState(root)
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}

	// First run: set flags and leave a stale writer directly on the child, as a
	// test that calls cmd.SetOut would.
	var stale bytes.Buffer
	child.SetOut(&stale)
	var first bytes.Buffer
	run(&first, "child", "--tags", "lane:trunk", "--name", "x")
	if !reflect.DeepEqual(gotTags, []string{"lane:trunk"}) || gotName != "x" {
		t.Fatalf("first run tags=%v name=%q", gotTags, gotName)
	}
	if first.String() != "ran" || stale.Len() != 0 {
		t.Fatalf("first run output = %q (stale = %q): the stale SetOut was not dropped", first.String(), stale.String())
	}

	// Second run: nothing from the first may leak.
	var second bytes.Buffer
	run(&second, "child", "--tags", "other")
	if !reflect.DeepEqual(gotTags, []string{"other"}) {
		t.Fatalf("second run tags = %v, want [other] (slice flag built up across runs)", gotTags)
	}
	if gotName != "dflt" {
		t.Fatalf("second run name = %q, want the default", gotName)
	}
	if second.String() != "ran" {
		t.Fatalf("second run output = %q, want it on the root writer", second.String())
	}

	// Third run with no flags: back to defaults.
	var third bytes.Buffer
	run(&third, "child")
	if len(gotTags) != 0 {
		t.Fatalf("third run tags = %v, want none", gotTags)
	}
}
