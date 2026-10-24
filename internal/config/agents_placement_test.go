package config

import (
	"strings"
	"testing"
)

// placement = "local" | "remote" | "team:<path>" (sty_7028d996): declared on the
// binding, default local, refused at load for anything else.

func TestResolvedPlacement(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantPath string
		label    string
	}{
		{"", PlacementLocal, "", "local"},
		{"local", PlacementLocal, "", "local"},
		{"REMOTE", PlacementRemote, "", "remote"},
		{" remote ", PlacementRemote, "", "remote"},
		{"team:acme/ops", PlacementTeam, "acme/ops", "team:acme/ops"},
		{"Team:Acme/Ops", PlacementTeam, "Acme/Ops", "team:Acme/Ops"},
		{"team:", "team:", "", "team:"},
		{"bogus", "bogus", "", "bogus"},
	}
	for _, tc := range cases {
		b := AgentBinding{Placement: tc.in}
		kind, path := b.ResolvedPlacement()
		if kind != tc.wantKind || path != tc.wantPath {
			t.Errorf("ResolvedPlacement(%q) = (%q, %q), want (%q, %q)", tc.in, kind, path, tc.wantKind, tc.wantPath)
		}
		if got := b.PlacementLabel(); got != tc.label {
			t.Errorf("PlacementLabel(%q) = %q, want %q", tc.in, got, tc.label)
		}
	}
}

const placementCmd = "command = \"claude -p {system}\"\n"

func TestPlacementLoadsInTheRepoFile(t *testing.T) {
	ac, err := loadAgentsText(t, strings.Join([]string{
		"[plain]", placementCmd,
		"[far]", "placement = \"remote\"", "remote = \"r1\"", placementCmd,
		"[team]", "placement = \"team:acme/ops\"", placementCmd,
		"[near]", "placement = \"local\"", placementCmd,
	}, "\n"))
	if err != nil {
		t.Fatalf("valid placements must load: %v", err)
	}
	for name, want := range map[string]string{"plain": "local", "far": "remote", "team": "team:acme/ops", "near": "local"} {
		if got := ac.Agents[name].PlacementLabel(); got != want {
			t.Errorf("[%s] placement = %q, want %q", name, got, want)
		}
	}
	if ac.Agents["far"].Remote != "r1" {
		t.Errorf("remote = %q, want r1", ac.Agents["far"].Remote)
	}
}

func TestPlacementRefusedInTheRepoFile(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"unknown value":        {"placement = \"bogus\"\n", "placement=bogus is not local|remote|team:<path>"},
		"team without a path":  {"placement = \"team:\"\n", "placement=team: is not local|remote|team:<path>"},
		"remote names nothing": {"placement = \"remote\"\n", "placement=remote names no remote"},
		"remote on the cloud":  {"placement = \"remote\"\nremote = \"r1\"\ninterface = \"cloud\"\n", "cannot be combined with interface=cloud"},
		"remote= on a local":   {"remote = \"r1\"\n", "only a placement=remote binding names a remote"},
		"remote= on a team":    {"placement = \"team:a/b\"\nremote = \"r1\"\n", "only a placement=remote binding names a remote"},
	}
	for name, tc := range cases {
		_, err := loadAgentsText(t, "[coder]\nrole = \"agent\"\n"+placementCmd+tc.body)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, tc.want)
		}
	}
}

func TestPlacementInTheCatalog(t *testing.T) {
	g, err := ParseGlobalAgents("[profiles.p]\nrole = \"agent\"\nplacement = \"team:acme/ops\"\n")
	if err != nil {
		t.Fatalf("a catalog profile may declare a placement: %v", err)
	}
	if got := g.Profiles["p"].PlacementLabel(); got != "team:acme/ops" {
		t.Errorf("placement = %q", got)
	}
	for name, body := range map[string]string{
		"unknown":        "placement = \"bogus\"\n",
		"team no path":   "placement = \"team:\"\n",
		"remote no id":   "placement = \"remote\"\n",
		"remote + cloud": "placement = \"remote\"\nremote = \"r\"\ninterface = \"cloud\"\n",
	} {
		_, err := ParseGlobalAgents("[profiles.p]\nrole = \"agent\"\n" + body)
		if err == nil || !strings.Contains(err.Error(), "placement") {
			t.Errorf("catalog %s: err = %v, want a placement refusal", name, err)
		}
	}
}

func TestOverlayBindingCarriesPlacement(t *testing.T) {
	out, src := overlayBinding(AgentBinding{Model: "m"}, nil, AgentBinding{Placement: "remote", Remote: "r1"}, "T")
	if out.Placement != "remote" || out.Remote != "r1" || out.Model != "m" || src["placement"] != "T" {
		t.Fatalf("overlay = %+v %v", out, src)
	}
	out, _ = overlayBinding(out, src, AgentBinding{}, "U")
	if out.Placement != "remote" {
		t.Errorf("an empty tier must not clear placement: %+v", out)
	}
}
