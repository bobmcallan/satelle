package verb

import (
	"context"
	"encoding/json"

	"github.com/bobmcallan/satelle/internal/buildinfo"
)

// VersionInfo is the response shape for the version verb. It mirrors
// buildinfo.Info; kept as the verb's own type so the wire shape is stable
// independent of the buildinfo internals.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

func init() {
	Register(&Verb{
		Name:        "version",
		Description: "Return build info (version, commit, build time)",
		Invoke: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			info := buildinfo.Resolve()
			return json.Marshal(VersionInfo{
				Version:   info.Version,
				Commit:    info.Commit,
				BuildTime: info.BuildTime,
			})
		},
	})
}
