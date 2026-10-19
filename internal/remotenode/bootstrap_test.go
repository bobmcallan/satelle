package remotenode

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

func TestBootstrapOrderAndContent(t *testing.T) {
	s := Bootstrap{
		Version:     "1.2.3",
		InstallURL:  "https://rel.example.com/install.sh",
		ReleaseBase: "http://mirror.example.com/dl",
		Steps:       [][]string{{"apt-get", "install", "-y", "git"}, {"echo", "it's"}},
		Spec:        remotechan.LaunchSpec{RelayURL: "wss://satelle.example/relay", Session: "sess", Token: "tok"},
	}.Script()

	order := []string{
		"curl -fsSL 'https://rel.example.com/install.sh'",
		"SATELLE_RELEASE_BASE='http://mirror.example.com/dl' sh \"$installer\" '1.2.3'",
		"'apt-get' 'install' '-y' 'git'",
		`'echo' 'it'\''s'`,
		`exec satelle remote join --root "$HOME/satelle-node" --relay 'wss://satelle.example/relay' --session 'sess' --token 'tok'`,
	}
	last := -1
	for _, frag := range order {
		i := strings.Index(s, frag)
		if i < 0 {
			t.Fatalf("script lacks %q:\n%s", frag, s)
		}
		if i < last {
			t.Errorf("%q is out of order in:\n%s", frag, s)
		}
		last = i
	}
	if strings.Contains(strings.SplitN(s, "\n", 2)[0], "|") || strings.Contains(s, "| sh") {
		t.Error("the installer must be downloaded, not piped to a shell")
	}
}

func TestBootstrapWithoutAMirrorUsesTheInstallersOwnBase(t *testing.T) {
	s := Bootstrap{Version: "1.2.3", InstallURL: "https://rel.example.com/install.sh"}.Script()
	if strings.Contains(s, "SATELLE_RELEASE_BASE") {
		t.Errorf("no mirror, no override:\n%s", s)
	}
}

// Whatever the values hold, the script is one well-formed shell program in which
// they stay data: a quote in the token cannot end its argument.
func TestBootstrapQuotesHostileValues(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	s := Bootstrap{
		Version: "1.2.3", InstallURL: "https://rel.example.com/install.sh",
		Spec: remotechan.LaunchSpec{RelayURL: "wss://x/relay", Session: "s", Token: `t'; touch /tmp/pwned; echo '`},
	}.Script()
	if out, err := exec.Command(sh, "-n", "-c", s).CombinedOutput(); err != nil {
		t.Fatalf("script does not parse: %v\n%s", err, out)
	}
	if !strings.Contains(s, `'t'\''; touch /tmp/pwned; echo '\'''`) {
		t.Errorf("token not single-quoted:\n%s", s)
	}
}
