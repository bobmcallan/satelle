// Command costview renders the fixture cost view.
package main

import (
	"fmt"
	"os"

	"example.com/telemetry/internal/costview"
	"example.com/telemetry/internal/ledger"
)

func main() {
	store, err := ledger.Open(os.Getenv("LEDGER_PATH"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "costview:", err)
		os.Exit(1)
	}
	rows, err := store.Rows()
	if err != nil {
		fmt.Fprintln(os.Stderr, "costview:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, costview.Render(rows))
}
