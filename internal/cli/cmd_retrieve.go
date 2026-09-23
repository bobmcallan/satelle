package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/verb"
)

func init() {
	c := &cobra.Command{
		Use:   "retrieve <hash>",
		Short: "Print the exact original bytes a compressor stored under a CCR content hash",
		Long: `A compressor that drops content stores the original under a content hash and
leaves a marker (<<ccr:HASH>>, or a "Retrieve more: satelle retrieve HASH" line)
in its condensed output. This command prints the exact original bytes for that
hash to stdout — no trailing newline, no formatting.

Read-only: it never appends to the ledger and never touches an engagement seat,
so a read-only reviewer whose grant is Bash(satelle:*) can run it. See
satelle help retrieve.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			hash := args[0]
			req, err := json.Marshal(map[string]any{"hash": hash})
			if err != nil {
				return err
			}
			resp, err := verb.Dispatch(cmd.Context(), "retrieve-get", req)
			if err != nil {
				return err
			}
			var out struct {
				DataBase64 string `json:"data_base64"`
			}
			if err := json.Unmarshal(resp, &out); err != nil {
				return err
			}
			b, err := base64.StdEncoding.DecodeString(out.DataBase64)
			if err != nil {
				return fmt.Errorf("retrieve: decode stored bytes: %w", err)
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
	register(c)
}
