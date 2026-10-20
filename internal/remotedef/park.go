package remotedef

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// parkStates reads the park state each category of the remote's own workflow
// declares (done.toml, park = { state = … }), sorted and de-duplicated. A story
// parked there is a hand-back the remote's own workflow made on purpose, not a
// status it strayed to. A workflow that is missing or does not parse declares
// none: Validate is what reports that, and Load must stay usable for the callers
// that only need the values.
func parkStates(dir string) []string {
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(doneFile)))
	if err != nil {
		return nil
	}
	lists, err := wfdot.ParseDone(string(body))
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, l := range lists {
		if s := strings.TrimSpace(l.Park); s != "" {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
