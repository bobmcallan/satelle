package cli

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/bobmcallan/satelle/internal/verb"
)

// dispatch invokes a verb with the given request value (marshalled to JSON) and
// prints the response as indented JSON to the command's stdout. This is the one
// path every data command takes — CLI command → verb.Dispatch → store —
// mirroring how the web server will render from the same verbs.
func dispatch(cmd *cobra.Command, name string, req any) error {
	var body json.RawMessage
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = b
	}
	resp, err := verb.Dispatch(cmd.Context(), name, body)
	if err != nil {
		return err
	}
	return printJSON(cmd, resp)
}

// printJSON pretty-prints a raw JSON message to the command's stdout.
func printJSON(cmd *cobra.Command, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		// Not valid JSON for some reason — emit it verbatim rather than failing.
		fmt.Fprintln(cmd.OutOrStdout(), string(raw))
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), buf.String())
	return nil
}

// putIf adds key→val to req when val is non-empty. Used to build create/list
// request bodies, omitting unset flags so verb defaults apply.
func putIf(req map[string]any, key, val string) {
	if val != "" {
		req[key] = val
	}
}

// putChanged copies a string flag into req[key] only if the user set it —
// giving `set` partial-update semantics (an unpassed flag leaves the field
// untouched, distinct from passing an empty value to clear it).
func putChanged(req map[string]any, f *pflag.FlagSet, flag, key string) {
	if f.Changed(flag) {
		v, _ := f.GetString(flag)
		req[key] = v
	}
}
