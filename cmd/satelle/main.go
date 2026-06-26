package main

import (
	"os"

	"github.com/bobmcallan/satelle/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
