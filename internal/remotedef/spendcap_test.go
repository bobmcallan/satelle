package remotedef

import (
	"strings"
	"testing"
)

func TestSpendCap(t *testing.T) {
	for _, c := range []struct {
		name, line string
		bad        bool
		want       float64
	}{
		{"absent is uncapped", "", false, 0},
		{"positive", "spend_cap_usd = 2.5\n", false, 2.5},
		{"zero", "spend_cap_usd = 0\n", true, 0},
		{"negative", "spend_cap_usd = -1\n", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := scaffolded(t)
			replaceIn(t, dir, "remote.toml", `lifecycle = "shutdown"`, `lifecycle = "shutdown"`+"\n"+c.line)
			fs := Validate(dir)
			if c.bad {
				if !hasFinding(fs, "remote.toml", "spend_cap_usd") {
					t.Errorf("no spend_cap_usd finding; got:\n%s", strings.Join(details(fs), "\n"))
				}
				return
			}
			if len(fs) != 0 {
				t.Fatalf("findings: %v", details(fs))
			}
			def, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case c.want == 0 && def.SpendCapUSD != nil:
				t.Errorf("absent cap = %v, want nil (uncapped)", *def.SpendCapUSD)
			case c.want != 0 && (def.SpendCapUSD == nil || *def.SpendCapUSD != c.want):
				t.Errorf("cap = %v, want %v", def.SpendCapUSD, c.want)
			}
		})
	}
}
