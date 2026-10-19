package remotedef

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// PlaceholderAllow is the host.network placeholder the egress allowlist fills.
const PlaceholderAllow = "{allow}"

// hostPattern is a hostname or address, optionally with a leading "*." wildcard:
// dot-separated labels of letters, digits and hyphens, no scheme, port or path.
var hostPattern = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)

// repoNamePattern is a directory name a clone can safely have.
var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func argvHas(argv []string, placeholder string) bool {
	for _, a := range argv {
		if strings.Contains(a, placeholder) {
			return true
		}
	}
	return false
}

func (v *validation) checkLifecycle(def Definition) {
	switch def.Lifecycle {
	case "", LifecycleShutdown, LifecycleIdle:
	default:
		v.fail(DefinitionFile, "lifecycle", fmt.Sprintf("%q is not %q or %q", def.Lifecycle, LifecycleShutdown, LifecycleIdle))
	}
}

// checkRepos holds each repo to an https URL with a host, no credential in it,
// a usable clone directory name, and a ref git cannot read as an option.
func (v *validation) checkRepos(def Definition) {
	seen := map[string]int{}
	for i, r := range def.Repos {
		urlKey := fmt.Sprintf("repo[%d].url", i)
		refKey := fmt.Sprintf("repo[%d].ref", i)
		switch u, err := url.Parse(strings.TrimSpace(r.URL)); {
		case strings.TrimSpace(r.URL) == "":
			v.fail(DefinitionFile, urlKey, "required: the https URL of the repository")
		case err != nil || u.Scheme != "https" || u.Hostname() == "":
			v.fail(DefinitionFile, urlKey, "must be an https URL with a host")
		case u.User != nil:
			v.fail(DefinitionFile, urlKey, "must not carry a credential; the node reads a repository without one")
		case !hostPattern.MatchString(u.Hostname()):
			v.fail(DefinitionFile, urlKey, "host is not a well-formed hostname")
		default:
			name := RepoName(r.URL)
			if !repoNamePattern.MatchString(name) || name == ".." {
				v.fail(DefinitionFile, urlKey, "has no usable repository name in its path")
			} else if j, dup := seen[name]; dup {
				v.fail(DefinitionFile, urlKey, fmt.Sprintf("clones into the same directory %q as repo[%d]", name, j))
			} else {
				seen[name] = i
			}
		}
		switch ref := r.Ref; {
		case strings.TrimSpace(ref) == "":
			v.fail(DefinitionFile, refKey, "required: the branch, tag or commit to check out")
		case strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\r\n"):
			v.fail(DefinitionFile, refKey, "is not a plain ref (no leading - and no whitespace)")
		}
	}
}

func (v *validation) checkAllow(def Definition) {
	for i, h := range def.Network.Allow {
		if !hostPattern.MatchString(h) {
			v.fail(DefinitionFile, fmt.Sprintf("network.allow[%d]", i), "is not a hostname or *.domain")
		}
	}
}

// RepoName is the directory a repo is cloned into: the last element of its URL
// path without a .git suffix.
func RepoName(repoURL string) string {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(path.Base(strings.TrimRight(u.Path, "/")), ".git")
}

// RepoHost is the hostname a repo URL reaches, or "" for an unparseable URL.
func RepoHost(repoURL string) string {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Allowlist is the egress a node needs, as a sorted, de-duplicated list: the
// release hosts the caller supplies (where the pinned satelle comes from), the
// hosted server and its subdomains, each repo's host and the definition's own
// [network] allow. The engine names no host: every one arrives as an argument or
// from the definition.
func Allowlist(def Definition, releaseHosts []string, serverHost string) []string {
	set := map[string]bool{}
	add := func(h string) {
		if h = strings.TrimSpace(h); h != "" {
			set[h] = true
		}
	}
	for _, h := range releaseHosts {
		add(h)
	}
	if serverHost = strings.TrimSpace(serverHost); serverHost != "" {
		add(serverHost)
		add("*." + serverHost)
	}
	for _, r := range def.Repos {
		add(RepoHost(r.URL))
	}
	for _, h := range def.Network.Allow {
		add(h)
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}
