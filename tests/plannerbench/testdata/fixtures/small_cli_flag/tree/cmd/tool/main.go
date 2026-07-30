// Command tool is the fixture CLI entry point.
package main

import (
	"fmt"
	"os"

	"example.com/tool/internal/cli"
)

func main() {
	code, err := cli.Execute(os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tool:", err)
	}
	os.Exit(code)
}
