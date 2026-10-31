package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// hostedClient builds a client for the server the --server flag, or else the
// configured one, names, or fails with ErrLoginRequired when there is none. Unlike
// newHostedClient it stamps no location id: profile backup and the remote
// commands are not sync/story calls.
func hostedClient(serverFlag string) (*hosted.Client, error) {
	server := resolveServer(serverFlag)
	if server == "" {
		return nil, fmt.Errorf("%w — run \"satelle login\" first (no hosted server configured)", hosted.ErrLoginRequired)
	}
	return hosted.NewClient(server, hosted.DefaultStore(), nil), nil
}

// newHostedClient builds an authenticated client for a bound-repo sync/story
// call and attaches this checkout's location id (sty_e88d77ce). Login, project
// create/list, publish, and profile backup keep using hosted.NewClient so they
// do not stamp x-satelle-location (those are not sync/story calls).
//
// Registration failure other than ErrLoginRequired is non-fatal: a location
// registry outage must not block sync. The header is still sent.
func newHostedClient(ctx context.Context, server, repoRoot string) *hosted.Client {
	c := hosted.NewClient(server, hosted.DefaultStore(), nil)
	if repoRoot == "" {
		return c
	}
	// A story worktree is this machine's checkout: it sends and registers the
	// main tree's location rather than minting one of its own (sty_9ece9394).
	repoRoot = config.CanonicalRepoRoot(repoRoot)
	id, err := hosted.LocationID(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "satelle: checkout location: %v\n", err)
		return c
	}
	c.SetLocation(id)
	err = hosted.EnsureLocationRegistered(ctx, c, repoRoot, server)
	switch {
	case errors.Is(err, hosted.ErrSessionTokenRefused):
		// Not "not signed in": the session token was refused, and the caller's
		// next call fails with the same error — say so once, up front.
		fmt.Fprintf(os.Stderr, "satelle: %v\n", err)
	case err != nil && !errors.Is(err, hosted.ErrLoginRequired):
		fmt.Fprintf(os.Stderr, "satelle: register checkout location: %v\n", err)
	}
	return c
}
