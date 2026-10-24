package doctor

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// checkSyncHealth reports recorded workstate-push state. It never contacts
// satelle.dev and never re-attempts a push (sty_30696eeb).
func checkSyncHealth(repoRoot, dataDir, globalDir string, now time.Time) health.Findings {
	cfgPath := filepath.Join(dataDir, config.ConfigName)
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		return health.Findings{health.Warn(health.IDSyncConfig, "Workstate sync config",
			"could not load satelle.toml; sync health not judged").About(cfgPath)}
	}
	return append(retiredSyncKeyFindings(cfg, cfgPath), checkWorkstatePush(cfg, cfgPath, repoRoot, globalDir, now)...)
}

// retiredSyncKeyFindings is the one-line notice for a [sync] key that hosted
// holds once read and nothing reads now (sty_40853d02). It loads and is ignored;
// the notice only says so, once.
func retiredSyncKeyFindings(cfg config.Config, cfgPath string) health.Findings {
	if !config.RetiredSyncKeyPresent(cfg) {
		return nil
	}
	return health.Findings{health.Info(health.IDSyncRetired, "Retired [sync] key",
		"[sync] "+config.RetiredSyncHoldStaleAfterKey+" is retired (hosted story holds were removed) and ignored").
		About(cfgPath).WithRemediation("delete it, or run `satelle migrate --yes`")}
}

// checkWorkstatePush judges the recorded work-state push against cfg.
func checkWorkstatePush(cfg config.Config, cfgPath, repoRoot, globalDir string, now time.Time) health.Findings {

	allLocal := true
	for _, area := range config.WorkstateAreas {
		sc, serr := config.ScopeFor(cfg, area)
		if serr != nil {
			return health.Findings{health.Error(health.IDSyncConfig, "Workstate sync config", serr.Error()).About(cfgPath)}
		}
		if sc != config.LocalScope {
			allLocal = false
			break
		}
	}
	if allLocal {
		return health.Findings{health.Info(health.IDSyncLocal, "Work state is local-only",
			"work-state areas are local; not flagged as unbacked").About(repoRoot)}
	}

	st, _, lerr := syncstate.Load(globalDir, repoRoot)
	if lerr != nil {
		return health.Findings{health.Warn(health.IDSyncConfig, "Workstate sync state",
			lerr.Error()).About(repoRoot)}
	}

	var out health.Findings
	if st.PushReason != "" {
		out = append(out, health.Error(health.IDSyncFailing, "Workstate push failing",
			st.PushReason).About(repoRoot).WithRemediation("run `satelle sync workstate push`"))
	}

	threshold, has, terr := config.WorkstateStaleAfter(cfg)
	if terr != nil {
		out = append(out, health.Error(health.IDSyncConfig, "Workstate sync config", terr.Error()).About(cfgPath))
		return out
	}
	if !has {
		out = append(out, health.Warn(health.IDSyncConfig, "Workstate staleness not judged",
			"no [sync] stale_after; run `satelle init` or `satelle migrate --yes` to seed it").About(cfgPath))
		if !st.PushLastSuccess.IsZero() {
			out = append(out, health.Info(health.IDSyncOk, "Workstate push",
				fmt.Sprintf("last successful push %s", st.PushLastSuccess.UTC().Format(time.RFC3339))).About(repoRoot))
		}
		return out
	}

	if st.PushLastSuccess.IsZero() {
		out = append(out, health.Error(health.IDSyncUnbacked, "Workstate unbacked",
			fmt.Sprintf("%s: last successful push never (threshold %s)", repoRoot, threshold)).
			About(repoRoot).WithRemediation("run `satelle sync workstate push`"))
		return out
	}
	age := now.Sub(st.PushLastSuccess)
	if age > threshold {
		out = append(out, health.Error(health.IDSyncUnbacked, "Workstate unbacked",
			fmt.Sprintf("%s: last successful push %s ago (threshold %s)", repoRoot, formatAge(age), threshold)).
			About(repoRoot).WithRemediation("run `satelle sync workstate push`"))
		return out
	}
	out = append(out, health.Info(health.IDSyncOk, "Workstate push",
		fmt.Sprintf("last successful push %s", st.PushLastSuccess.UTC().Format(time.RFC3339))).About(repoRoot))
	return out
}

func formatAge(d time.Duration) string {
	if d < time.Hour {
		if d < time.Minute {
			return d.Round(time.Second).String()
		}
		return d.Round(time.Minute).String()
	}
	h := int(d.Hours())
	if h < 48 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd", h/24)
}
