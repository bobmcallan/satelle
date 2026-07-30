// Package costview renders persisted cost rows as a stable table.
package costview

import (
	"fmt"
	"strings"

	"example.com/telemetry/internal/ledger"
)

// Render writes the cost table. Columns are append-only: an added column must
// not shift the meaning of an existing one, so old rows keep rendering.
func Render(rows []ledger.Row) string {
	var sb strings.Builder
	sb.WriteString("| id | step | duration_ms | tokens_in | tokens_out |\n")
	sb.WriteString("|---|---|---:|---:|---:|\n")
	for _, r := range rows {
		fmt.Fprintf(&sb, "| %s | %s | %d | %d | %d |\n",
			r.ID, r.Step, r.DurationMS, r.TokensIn, r.TokensOut)
	}
	duration, in, out := total(rows)
	fmt.Fprintf(&sb, "| TOTAL | | %d | %d | %d |\n", duration, in, out)
	return sb.String()
}

func total(rows []ledger.Row) (int64, int, int) {
	var duration int64
	var in, out int
	for _, r := range rows {
		duration += r.DurationMS
		in += r.TokensIn
		out += r.TokensOut
	}
	return duration, in, out
}
