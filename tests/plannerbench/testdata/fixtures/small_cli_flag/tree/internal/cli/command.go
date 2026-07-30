// Package cli parses the fixture tool's arguments and runs its one command.
package cli

import (
	"flag"
	"fmt"
	"io"

	"example.com/tool/internal/store"
)

// Options carries the parsed command line.
type Options struct {
	Name  string
	Force bool
}

// ParseFlags parses argv into Options. New flags belong here.
func ParseFlags(argv []string) (Options, error) {
	var opts Options
	fs := flag.NewFlagSet("tool", flag.ContinueOnError)
	fs.StringVar(&opts.Name, "name", "", "record name to write")
	fs.BoolVar(&opts.Force, "force", false, "overwrite an existing record")
	if err := fs.Parse(argv); err != nil {
		return Options{}, err
	}
	if opts.Name == "" {
		return Options{}, fmt.Errorf("--name is required")
	}
	return opts, nil
}

// Execute parses argv and performs the command, writing progress to out. It is
// the single seam between flag parsing and the mutation the tool performs.
func Execute(argv []string, out io.Writer) (int, error) {
	opts, err := ParseFlags(argv)
	if err != nil {
		return 2, err
	}
	if err := store.Write(opts.Name, opts.Force); err != nil {
		return 1, err
	}
	fmt.Fprintf(out, "wrote %s\n", opts.Name)
	return 0, nil
}

// Usage renders the tool's help text. Every flag must be documented here.
func Usage(out io.Writer) {
	fmt.Fprint(out, "usage: tool --name <name> [--force]\n")
}
