package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// newHostedClient builds an authenticated client for a bound-repo sync/story
// call and attaches this checkout's location id (sty_e88d77ce). Login, project
// create/list, publish, and profile backup keep using hosted.NewClient so they
// do not stamp x-satelle-location (those are not sync/story calls).
//
// Registration failure other than ErrLoginRequired is non-fatal: a location
// registry outage must not block sync. The header is still sent.
func newHostedClient(ctx context.Context, server, repoRoot string) *hosted.Client {
	c := hosted.NewClient(server, hosted.FileStore{}, nil)
	if repoRoot == "" {
		return c
	}
	id, err := hosted.LocationID(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "satelle: checkout location: %v\n", err)
		return c
	}
	c.SetLocation(id)
	if err := hosted.EnsureLocationRegistered(ctx, c, repoRoot, server); err != nil && !errors.Is(err, hosted.ErrLoginRequired) {
		fmt.Fprintf(os.Stderr, "satelle: register checkout location: %v\n", err)
	}
	return c
}
