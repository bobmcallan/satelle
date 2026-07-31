// Package serve runs the push-fed mirror UI HTTP server (sty_80233c10).
// Shared by the dedicated satelle-serve binary and the deprecated `satelle serve`
// CLI alias so behaviour cannot drift.
package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/logfile"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/web"
)

// Options configures the mirror HTTP server.
type Options struct {
	Addr string // host, e.g. 127.0.0.1 or 0.0.0.0
	Port int    // 0 → global service config → DefaultWebPort
}

// Run opens the machine-wide mirror store and serves the RO UI until ctx ends.
func Run(ctx context.Context, opts Options) error {
	port := opts.Port
	if port == 0 {
		if gc, err := config.LoadGlobal(); err == nil && gc.Service.Port > 0 {
			port = gc.Service.Port
		} else {
			port = config.DefaultWebPort
		}
	}
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1"
	}
	listenAddr := fmt.Sprintf("%s:%d", addr, port)

	mirrorPath := mirror.DefaultPath(config.GlobalDir())
	ms, err := mirror.Open(mirrorPath)
	if err != nil {
		return fmt.Errorf("open mirror store: %w", err)
	}
	defer ms.Close()

	webSrv := web.NewMirror(ms)
	serverLog := filepath.Join(config.GlobalDir(), mirror.DefaultDirName, "server.log")
	logCfg := logfile.Config{MaxSizeBytes: config.DefaultLogsMaxSizeKB * 1024, MaxFiles: config.DefaultLogsMaxFiles}
	handler := web.RequestLog(webSrv.Handler, serverLog, logCfg)

	srv := &http.Server{Addr: listenAddr, Handler: handler}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Bind before serving so the repair loop's first pass — which re-requests
	// snapshots through this very endpoint — cannot race the listener.
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	// Repair loop for pushes that never landed (sty_e6e467fe). Runs beside the
	// server, off the request path, and stops with ctx.
	rec := &Reconciler{
		Targets: mirrorTargets(ms, config.GlobalDir()),
		Log: func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			fmt.Fprintln(os.Stderr, "satelle serve: "+line)
			_ = logfile.Append(time.Now(), serverLog, logCfg, line)
		},
	}
	go rec.Loop(ctx)

	fmt.Printf("satelle serve (push-fed mirror) http://%s  mirror=%s  (Ctrl-C to stop)\n", listenAddr, mirrorPath)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
