// Command satelle-serve is the dedicated push-fed mirror UI binary
// (sty_80233c10 / epic:serve-adoption). It has no CLI verb surface and cannot
// mutate repo state — only ingest + read-only render.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/serve"
)

func main() {
	// Bare `go build` without ldflags still brands as satelle-serve (Makefile /
	// release.yml stamp Name=satelle-serve for release builds).
	if buildinfo.Name == "satelle" || buildinfo.Name == "" {
		buildinfo.Name = "satelle-serve"
	}
	addr := flag.String("addr", "127.0.0.1", "bind address")
	port := flag.Int("port", 0, "listen port (default from global service config or 8787)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		info := buildinfo.Resolve()
		fmt.Printf("%s %s (commit %s, built %s)\n", info.Name, info.Version, info.Commit, info.BuildTime)
		os.Exit(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := serve.Run(ctx, serve.Options{Addr: *addr, Port: *port}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
