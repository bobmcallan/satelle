// Command guardcheck reports whether an edit is permitted in a given state.
package main

import (
	"fmt"
	"os"

	"example.com/guard/internal/guard"
	"example.com/guard/internal/workflow"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: guardcheck <dot-file> <state>")
		os.Exit(2)
	}
	graph, err := workflow.ParseFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "guardcheck:", err)
		os.Exit(1)
	}
	decision := guard.Decide(graph, guard.Request{State: os.Args[2]})
	fmt.Fprintln(os.Stdout, decision.Allowed, decision.Reason)
}
