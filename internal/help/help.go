// Package help carries satelle's embedded help topics — product documentation
// that ships in the binary (every repo gets it) and is surfaced by BOTH the CLI
// (`satelle help [topic]`) and the local web server (`/help`). The topics are
// authored markdown under topics/, embedded as the single source so the two
// surfaces never drift (sty_82c456a0). Per the constitution, help-about-the-
// product is binary MECHANISM, not repo substrate.
package help

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed topics
var topicsFS embed.FS

// Topic is one help document: a stable name (the slug), a human title (the first
// heading), and the raw markdown body.
type Topic struct {
	Name  string
	Title string
	Body  string
}

// List returns every embedded help topic, name-sorted.
func List() []Topic {
	var out []Topic
	entries, _ := fs.ReadDir(topicsFS, "topics")
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(path.Ext(e.Name()), ".md") {
			continue
		}
		body, err := topicsFS.ReadFile(path.Join("topics", e.Name()))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		out = append(out, Topic{Name: name, Title: title(string(body)), Body: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the named topic and whether it exists.
func Get(name string) (Topic, bool) {
	for _, t := range List() {
		if t.Name == name {
			return t, true
		}
	}
	return Topic{}, false
}

// title returns the first markdown heading text, or the slug-less first line.
func title(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(t, "#"))
	}
	return ""
}
