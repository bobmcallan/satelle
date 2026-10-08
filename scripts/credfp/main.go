// Command credfp is the helper scripts/credguard.sh calls to decide whether the
// host credentials file changed in a way that matters (sty_5ba68e2c). It wraps
// hosted.CaptureIdentity and hosted.DiffIdentity — the same fingerprint and diff
// the integration suite's host-surface guard uses — so the Makefile guard and
// the suite cannot disagree on one before/after pair.
//
//	credfp snapshot <credentials.toml>      print the identity snapshot
//	credfp diff <before-snapshot> <after-snapshot>
//	                                         print one reason per change; exit 1 on any
//	credfp host-snapshot <out>              write the real ~/.satelle top level and the
//	                                         installed binaries' snapshot (hostguard)
//	credfp host-diff <before> <after>       print one line per host change; exit 1 on any
//
// Exit 2 is a usage or I/O error (the caller fails closed).
package main

import (
	"fmt"
	"os"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/hostguard"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	switch {
	case len(args) == 2 && args[0] == "snapshot":
		text, err := hosted.CaptureIdentity(args[1]).MarshalText()
		if err != nil {
			fmt.Fprintln(os.Stderr, "credfp:", err)
			return 2
		}
		os.Stdout.Write(text)
		return 0
	case len(args) == 3 && args[0] == "diff":
		before, err := readSnapshot(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "credfp:", err)
			return 2
		}
		after, err := readSnapshot(args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "credfp:", err)
			return 2
		}
		reasons := hosted.DiffIdentity(before, after)
		for _, r := range reasons {
			fmt.Println(r)
		}
		if len(reasons) > 0 {
			return 1
		}
		return 0
	case len(args) == 2 && args[0] == "host-snapshot":
		snap, err := hostguard.Capture(hostguard.ResolveRoots())
		if err != nil {
			fmt.Fprintln(os.Stderr, "credfp:", err)
			return 2
		}
		if err := os.WriteFile(args[1], snap.Marshal(), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "credfp:", err)
			return 2
		}
		return 0
	case len(args) == 3 && args[0] == "host-diff":
		before, err := readHostSnapshot(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "credfp:", err)
			return 2
		}
		after, err := readHostSnapshot(args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "credfp:", err)
			return 2
		}
		lines := hostguard.Diff(before, after)
		for _, l := range lines {
			fmt.Println(l)
		}
		if len(lines) > 0 {
			return 1
		}
		return 0
	}
	fmt.Fprintln(os.Stderr, "usage: credfp snapshot <path> | credfp diff <before> <after> | credfp host-snapshot <out> | credfp host-diff <before> <after>")
	return 2
}

func readHostSnapshot(path string) (hostguard.Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return hostguard.Snapshot{}, err
	}
	snap, err := hostguard.Parse(b)
	if err != nil {
		return hostguard.Snapshot{}, fmt.Errorf("%s: %w", path, err)
	}
	return snap, nil
}

func readSnapshot(path string) (hosted.Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return hosted.Identity{}, err
	}
	var id hosted.Identity
	if err := id.UnmarshalText(b); err != nil {
		return hosted.Identity{}, fmt.Errorf("%s: %w", path, err)
	}
	return id, nil
}
