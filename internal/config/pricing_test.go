package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/testutil"
)

func writeGlobal(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(GlobalDir(), GlobalConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const ratesToml = `[pricing.models."m-big"]
fresh_input = 3.0
cache_read = 0.3
cache_write = 3.75
output = 15.0
`

// TestPricingLoadsEitherMode pins AC2's two accepted shapes, and that no
// section at all is simply unconfigured.
func TestPricingLoadsEitherMode(t *testing.T) {
	testutil.IsolateHome(t)
	gc, err := LoadGlobal()
	if err != nil || gc.Pricing.Mode() != costview.ModeNone {
		t.Fatalf("absent config: mode %q err %v", gc.Pricing.Mode(), err)
	}

	writeGlobal(t, ratesToml)
	gc, err = LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if gc.Pricing.Mode() != costview.ModeRates || gc.Pricing.Models["m-big"] != (costview.Rates{FreshInput: 3, CacheRead: 0.3, CacheWrite: 3.75, Output: 15}) {
		t.Errorf("rates mode = %+v", gc.Pricing)
	}

	writeGlobal(t, "[pricing.subscription]\nfee_usd = 20\ntokens_per_period = 1000000\n")
	gc, err = LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if gc.Pricing.Mode() != costview.ModeSubscription || gc.Pricing.Subscription.FeeUSD != 20 || gc.Pricing.Subscription.TokensPerPeriod != 1_000_000 {
		t.Errorf("subscription mode = %+v", gc.Pricing)
	}
}

// TestPricingRejectedAtLoadNamingTheKey pins AC2's rejections.
func TestPricingRejectedAtLoadNamingTheKey(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"negative rate", strings.Replace(ratesToml, "cache_read = 0.3", "cache_read = -0.3", 1), `pricing.models."m-big".cache_read`},
		{"missing rate", strings.Replace(ratesToml, "output = 15.0\n", "", 1), `pricing.models."m-big".output`},
		{"negative fee", "[pricing.subscription]\nfee_usd = -1\ntokens_per_period = 10\n", "pricing.subscription.fee_usd"},
		{"missing period", "[pricing.subscription]\nfee_usd = 1\n", "pricing.subscription.tokens_per_period"},
		{"missing fee", "[pricing.subscription]\ntokens_per_period = 10\n", "pricing.subscription.fee_usd"},
		{"zero period", "[pricing.subscription]\nfee_usd = 1\ntokens_per_period = 0\n", "pricing.subscription.tokens_per_period"},
		{"both modes", ratesToml + "[pricing.subscription]\nfee_usd = 1\ntokens_per_period = 10\n", "both set"},
		{"unknown key", ratesToml + "[pricing.models.\"m-two\"]\nfresh_input = 1\ncache_read = 1\ncache_write = 1\noutput = 1\nfresh_inputt = 1\n", "fresh_inputt"},
	}
	for _, c := range cases {
		testutil.IsolateHome(t)
		writeGlobal(t, c.body)
		_, err := LoadGlobal()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to name %q", c.name, err, c.want)
		}
		if p := LoadPricing(); p.Invalid == "" || !strings.Contains(p.Invalid, c.want) {
			t.Errorf("%s: LoadPricing().Invalid = %q", c.name, p.Invalid)
		}
	}
}

// TestSaveGlobalKeepsPricing pins that re-rendering the managed template (any
// `satelle service`/`agent` save) does not silently drop the operator's rates.
func TestSaveGlobalKeepsPricing(t *testing.T) {
	testutil.IsolateHome(t)
	writeGlobal(t, ratesToml)
	gc, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	gc.UI.Theme = "dark"
	if err := SaveGlobal(gc); err != nil {
		t.Fatal(err)
	}
	again, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if again.UI.Theme != "dark" || again.Pricing.Models["m-big"] != gc.Pricing.Models["m-big"] {
		t.Errorf("pricing lost or changed across SaveGlobal: %+v", again.Pricing)
	}
}
