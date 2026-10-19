// Package release owns where satelle's own releases are published: the hosts,
// and the URL shapes `satelle update` and scripts/install.sh build from them.
// Nothing else spells a release URL, so the egress a node needs to install a
// pinned satelle is derived from here and cannot drift from what is fetched.
package release

import "fmt"

// Repo is the repository satelle's releases are published from.
const Repo = "bobmcallan/satelle"

// The origins release URLs name. install.sh names the same two; a test holds the
// script to this list.
const (
	DownloadHost = "github.com"
	APIHost      = "api.github.com"
)

// redirectHosts serve the bytes of a release asset after the download origin
// redirects to them. No URL here names them, but fetching an asset reaches them;
// GitHub has moved the asset host before, hence the wildcard beside the current name.
var redirectHosts = []string{"objects.githubusercontent.com", "*.githubusercontent.com"}

// Origins are the hosts release URLs are built on.
func Origins() []string { return []string{DownloadHost, APIHost} }

// Hosts is every host installing a release reaches: the origins and the hosts
// they redirect asset downloads to.
func Hosts() []string {
	return append(Origins(), redirectHosts...)
}

// DownloadBase is the directory release assets are downloaded from, one
// subdirectory per tag. SATELLE_RELEASE_BASE overrides it with the same meaning.
func DownloadBase(repo string) string {
	return fmt.Sprintf("https://%s/%s/releases/download", DownloadHost, repo)
}

// APIBase is the release API root for repo; LatestAPI names the newest release.
func APIBase(repo string) string {
	return fmt.Sprintf("https://%s/repos/%s/releases", APIHost, repo)
}

// LatestAPI is the URL naming repo's latest release.
func LatestAPI(repo string) string { return APIBase(repo) + "/latest" }

// InstallScriptURL is where the installer is fetched from. With no base it is
// the latest release's, the newest installer, which takes the version to install
// as an argument. With a base (SATELLE_RELEASE_BASE: a mirror, or a test's fake
// release server) it is the copy published beside version's assets under it.
func InstallScriptURL(repo, base, version string) string {
	if base == "" {
		return fmt.Sprintf("https://%s/%s/releases/latest/download/install.sh", DownloadHost, repo)
	}
	return base + "/v" + version + "/install.sh"
}
