// Package release owns where satelle's own releases are published: the hosts,
// and the URL shapes `satelle update` and scripts/install.sh build from them.
// Nothing else spells a release URL.
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

// Origins are the hosts release URLs are built on.
func Origins() []string { return []string{DownloadHost, APIHost} }

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
