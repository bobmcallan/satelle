package remotedef

import (
	"reflect"
	"strings"
	"testing"
)

func TestLifecycleDefaultsToShutdown(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, DefinitionFile, "lifecycle = \"shutdown\"\n", "")
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("an absent lifecycle must validate: %v", details(fs))
	}
	def, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if def.Lifecycle != "" || def.EffectiveLifecycle() != LifecycleShutdown {
		t.Errorf("absent lifecycle = %q, effective %q; want shutdown", def.Lifecycle, def.EffectiveLifecycle())
	}
}

func TestLifecycleAcceptsIdleAndRefusesOthers(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, DefinitionFile, `lifecycle = "shutdown"`, `lifecycle = "idle"`)
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("idle must validate: %v", details(fs))
	}
	if def, _ := Load(dir); def.EffectiveLifecycle() != LifecycleIdle {
		t.Errorf("effective lifecycle = %q, want idle", def.EffectiveLifecycle())
	}

	replaceIn(t, dir, DefinitionFile, `lifecycle = "idle"`, `lifecycle = "forever"`)
	fs := Validate(dir)
	if !hasFinding(fs, DefinitionFile, "lifecycle") || len(fs) != 1 {
		t.Fatalf("want one finding naming remote.toml: lifecycle, got %v", details(fs))
	}
	if strings.Contains(fs[0].Detail, "forever") == false {
		t.Errorf("the finding should show the bad value: %s", fs[0].Detail)
	}
}

func TestRepoRefusals(t *testing.T) {
	cases := []struct {
		name, repo, key string
	}{
		{"missing url", "ref = \"main\"\n", "repo[0].url"},
		{"not https", "url = \"http://git.example.com/a/b.git\"\nref = \"main\"\n", "repo[0].url"},
		{"ssh", "url = \"ssh://git@git.example.com/a/b.git\"\nref = \"main\"\n", "repo[0].url"},
		{"no host", "url = \"https:///a/b.git\"\nref = \"main\"\n", "repo[0].url"},
		{"credential in url", "url = \"https://user:pw@git.example.com/a/b.git\"\nref = \"main\"\n", "repo[0].url"},
		{"no repo name", "url = \"https://git.example.com/\"\nref = \"main\"\n", "repo[0].url"},
		{"missing ref", "url = \"https://git.example.com/a/b.git\"\n", "repo[0].ref"},
		{"option-like ref", "url = \"https://git.example.com/a/b.git\"\nref = \"--upload-pack=x\"\n", "repo[0].ref"},
		{"spaced ref", "url = \"https://git.example.com/a/b.git\"\nref = \"a b\"\n", "repo[0].ref"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := scaffolded(t)
			replaceIn(t, dir, DefinitionFile, "[network]", "[[repo]]\n"+c.repo+"\n[network]")
			fs := Validate(dir)
			if !hasFinding(fs, DefinitionFile, c.key) {
				t.Fatalf("want a finding naming remote.toml: %s, got %v", c.key, details(fs))
			}
			if strings.Contains(strings.Join(details(fs), "\n"), "pw@") || strings.Contains(strings.Join(details(fs), "\n"), ":pw") {
				t.Errorf("a finding must not echo the credential: %v", details(fs))
			}
		})
	}
}

func TestRepoCollidingDirectoriesRefused(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, DefinitionFile, "[network]", `[[repo]]
url = "https://git.example.com/a/tool.git"
ref = "main"

[[repo]]
url = "https://other.example.com/b/tool"
ref = "main"

[network]`)
	if fs := Validate(dir); !hasFinding(fs, DefinitionFile, "repo[1].url") {
		t.Fatalf("two repos cloning into one directory must be named, got %v", details(fs))
	}
}

func TestRepoAccepted(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, DefinitionFile, "[network]", `[[repo]]
url = "https://git.example.com/a/tool.git"
ref = "v1.2.3"

[network]`)
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("a well-formed repo must validate: %v", details(fs))
	}
	def, _ := Load(dir)
	if len(def.Repos) != 1 || def.Repos[0].Ref != "v1.2.3" || RepoName(def.Repos[0].URL) != "tool" {
		t.Errorf("repos = %+v", def.Repos)
	}
}

func TestNetworkAllowRefusals(t *testing.T) {
	for _, bad := range []string{"https://x.example.com", "x.example.com/path", "x example", "", "*", "a..b"} {
		dir := scaffolded(t)
		replaceIn(t, dir, DefinitionFile, "allow = []", `allow = ["ok.example.com", "`+bad+`"]`)
		fs := Validate(dir)
		if !hasFinding(fs, DefinitionFile, "network.allow[1]") || hasFinding(fs, DefinitionFile, "network.allow[0]") {
			t.Errorf("allow entry %q: want exactly network.allow[1] named, got %v", bad, details(fs))
		}
	}
	dir := scaffolded(t)
	replaceIn(t, dir, DefinitionFile, "allow = []", `allow = ["a.example.com", "*.example.org", "10.0.0.1"]`)
	if fs := Validate(dir); len(fs) != 0 {
		t.Errorf("hostnames, wildcard and address must validate: %v", details(fs))
	}
}

func TestHostNetworkRequiredAndCarriesAllow(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, DefinitionFile, "network = [\"your-host-cli\", \"network\", \"{label}\", \"{allow}\"]\n", "")
	if fs := Validate(dir); !hasFinding(fs, DefinitionFile, "host.network") {
		t.Errorf("a missing host.network must be named, got %v", details(fs))
	}

	dir = scaffolded(t)
	replaceIn(t, dir, DefinitionFile, `"{label}", "{allow}"]`, `"{label}", ""]`)
	fs := Validate(dir)
	if !hasFinding(fs, DefinitionFile, "host.network[3]") || !hasFinding(fs, DefinitionFile, "host.network:") {
		t.Errorf("an empty element and a missing {allow} must both be named, got %v", details(fs))
	}
}

func TestHostBootstrapRefusals(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, DefinitionFile, "bootstrap = []", `bootstrap = [["apt-get", "install", "-y", "git"], [], ["ok", ""]]`)
	fs := Validate(dir)
	if hasFinding(fs, DefinitionFile, "host.bootstrap[0]") ||
		!hasFinding(fs, DefinitionFile, "host.bootstrap[1]") ||
		!hasFinding(fs, DefinitionFile, "host.bootstrap[2][1]") {
		t.Errorf("want host.bootstrap[1] and host.bootstrap[2][1] named and [0] not, got %v", details(fs))
	}
	dir = scaffolded(t)
	replaceIn(t, dir, DefinitionFile, "bootstrap = []", `bootstrap = [["apt-get", "install", "-y", "git"]]`)
	if fs := Validate(dir); len(fs) != 0 {
		t.Errorf("a well-formed bootstrap must validate: %v", details(fs))
	}
}

func TestAllowlistIsTheSortedUnion(t *testing.T) {
	def := Definition{
		Repos: []Repo{
			{URL: "https://git.example.com/a/b.git", Ref: "main"},
			{URL: "https://git.example.com/c/d.git", Ref: "main"},
			{URL: "https://code.example.org:8443/e/f", Ref: "main"},
		},
		Network: Network{Allow: []string{"api.example.net", "git.example.com", "*.cdn.example.net"}},
	}
	got := Allowlist(def, []string{"rel.example.com", "api.rel.example.com", "rel.example.com"}, "srv.example.io")
	want := []string{
		"*.cdn.example.net", "*.srv.example.io", "api.example.net", "api.rel.example.com",
		"code.example.org", "git.example.com", "rel.example.com", "srv.example.io",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Allowlist =\n%v\nwant\n%v", got, want)
	}
	if got := Allowlist(Definition{}, nil, ""); len(got) != 0 {
		t.Errorf("nothing supplied, nothing allowed; got %v", got)
	}
}
