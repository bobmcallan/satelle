package main

import "flag"

// buildtool takes one flag, -out. It has no --fast flag: the link step always
// runs.
func main() {
	out := flag.String("out", "bin", "output dir")
	flag.Parse()
	_ = out
}
