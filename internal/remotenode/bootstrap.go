package remotenode

import (
	"strings"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

// NodeRoot is the working root on the node, as the node's shell spells it.
const NodeRoot = "~/satelle-node"

// Bootstrap is the script host.run runs on a fresh node: install the pinned
// satelle, run the definition's own bootstrap commands, then join.
type Bootstrap struct {
	// Version is the satelle version to install (MAJOR.MINOR.PATCH).
	Version string
	// InstallURL is where the installer is fetched from (see release.InstallScriptURL).
	InstallURL string
	// ReleaseBase, when set, is exported to the installer as SATELLE_RELEASE_BASE.
	ReleaseBase string
	// Steps are host.bootstrap, each an argv list, run in order after the install.
	Steps [][]string
	// Spec is the relay, session and single-use token the node enrolls with.
	Spec remotechan.LaunchSpec
}

// Script renders the commands as one POSIX shell script. The enrollment token is
// on the node's command line for the few moments before join redeems it; it is
// single use and expires, and nothing else secret is ever written to the node.
func (b Bootstrap) Script() string {
	var s strings.Builder
	s.WriteString("set -eu\n")
	s.WriteString("installer=$(mktemp)\n")
	s.WriteString("curl -fsSL " + shellQuote(b.InstallURL) + " -o \"$installer\"\n")
	if b.ReleaseBase != "" {
		s.WriteString("SATELLE_RELEASE_BASE=" + shellQuote(b.ReleaseBase) + " ")
	}
	s.WriteString("sh \"$installer\" " + shellQuote(b.Version) + "\n")
	s.WriteString("rm -f \"$installer\"\n")
	s.WriteString("PATH=\"$HOME/.local/bin:$PATH\"\nexport PATH\n")
	for _, step := range b.Steps {
		s.WriteString(shellJoin(step) + "\n")
	}
	s.WriteString("mkdir -p \"$HOME/satelle-node\"\n")
	s.WriteString("exec satelle remote join --root \"$HOME/satelle-node\"" +
		" --relay " + shellQuote(b.Spec.RelayURL) +
		" --session " + shellQuote(b.Spec.Session) +
		" --token " + shellQuote(b.Spec.Token) + "\n")
	return s.String()
}

func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
