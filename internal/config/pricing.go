package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/bobmcallan/satelle/internal/costview"
)

// The [pricing] section of ~/.satelle/config.toml prices a story's measured
// tokens at read time (sty_6267abb7). It is configuration the operator owns —
// the binary carries no rate — in exactly one of two modes:
//
//	[pricing.models."<model id>"]   rates mode, USD per million tokens
//	fresh_input = 3.0               (all four keys are required)
//	cache_read = 0.3
//	cache_write = 3.75
//	output = 15.0
//
//	[pricing.subscription]          subscription mode: the plan fee spread over
//	fee_usd = 20.0                  the plan's token allowance
//	tokens_per_period = 1000000
//
// A negative value, both modes, a missing or unknown key is rejected at load,
// naming the key.

var (
	rateKeys         = []string{"fresh_input", "cache_read", "cache_write", "output"}
	subscriptionKeys = []string{"fee_usd", "tokens_per_period"}
)

// validatePricing checks the decoded [pricing] section against what the file
// actually defined: a decoded zero is indistinguishable from an absent key
// without the metadata.
func validatePricing(md toml.MetaData, p costview.Pricing) error {
	for _, k := range md.Undecoded() {
		if len(k) > 0 && k[0] == "pricing" {
			return fmt.Errorf("config: unknown key %s", k)
		}
	}
	if len(p.Models) > 0 && p.Subscription != nil {
		return fmt.Errorf("config: pricing.models and pricing.subscription are both set — choose rates mode or subscription mode, not both")
	}
	ids := make([]string, 0, len(p.Models))
	for id := range p.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := p.Models[id]
		vals := []float64{r.FreshInput, r.CacheRead, r.CacheWrite, r.Output}
		for i, key := range rateKeys {
			full := fmt.Sprintf("pricing.models.%q.%s", id, key)
			if !md.IsDefined("pricing", "models", id, key) {
				return fmt.Errorf("config: %s is missing", full)
			}
			if vals[i] < 0 {
				return fmt.Errorf("config: %s must not be negative (got %v)", full, vals[i])
			}
		}
	}
	if s := p.Subscription; s != nil {
		for i, key := range subscriptionKeys {
			full := "pricing.subscription." + key
			if !md.IsDefined("pricing", "subscription", key) {
				return fmt.Errorf("config: %s is missing", full)
			}
			if v := []float64{s.FeeUSD, s.TokensPerPeriod}[i]; v < 0 {
				return fmt.Errorf("config: %s must not be negative (got %v)", full, v)
			}
		}
		if s.TokensPerPeriod == 0 {
			return fmt.Errorf("config: pricing.subscription.tokens_per_period must be greater than zero")
		}
	}
	return nil
}

// renderPricing is the [pricing] section SaveGlobal appends, so re-rendering the
// managed template never drops the operator's rates. Empty when unconfigured.
func renderPricing(p costview.Pricing) string {
	if p.Mode() == costview.ModeNone {
		return ""
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(struct {
		Pricing costview.Pricing `toml:"pricing"`
	}{p}); err != nil {
		return ""
	}
	return "\n# read-time pricing of a story's tokens (see 'satelle story cost'). Rates are USD per\n# million tokens per token class; or a subscription: fee_usd over tokens_per_period.\n" +
		strings.TrimLeft(buf.String(), "\n")
}

// LoadPricing reads the operator's [pricing] for a read-time cost view. A
// configuration that cannot be read comes back as a Pricing marked Invalid, so
// the view says why it cannot price instead of failing to render or posing as
// unconfigured.
//
// Under `go test` with no SATELLE_HOME it reads nothing — unconfigured — rather
// than panic as GlobalDir does: a cost view is a read-only caller, and the
// refusal exists to keep a test from writing the developer's real ~/.satelle.
func LoadPricing() costview.Pricing {
	if strings.TrimSpace(os.Getenv("SATELLE_HOME")) == "" && testing.Testing() {
		return costview.Pricing{}
	}
	gc, err := LoadGlobal()
	if err != nil {
		return costview.Pricing{Invalid: err.Error()}
	}
	return gc.Pricing
}
