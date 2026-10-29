package verb

import (
	"testing"
	"time"
)

// The init-ownership chain rule and the other-story rule on synthetic rows
// (sty_ce858cb3).
func TestInitOwnedChainRule(t *testing.T) {
	since := time.Date(2026, 10, 29, 5, 0, 0, 0, time.UTC)
	before, during := since.Add(-time.Hour), since.Add(time.Minute)
	cases := []struct {
		name    string
		rows    []SeededFile
		current string
		want    bool
	}{
		{"no row", nil, "a", false},
		{"clean single row", []SeededFile{{BeforeSHA256: "o", SHA256: "a", BeforeMtime: before}}, "a", true},
		{"created by init", []SeededFile{{SHA256: "a"}}, "a", true},
		{"clean two-row chain", []SeededFile{{BeforeSHA256: "o", SHA256: "a", BeforeMtime: before}, {BeforeSHA256: "a", SHA256: "b", BeforeMtime: during}}, "b", true},
		{"dirty start", []SeededFile{{BeforeSHA256: "o", SHA256: "a", BeforeMtime: during}}, "a", false},
		{"dirty start then init-to-init", []SeededFile{{BeforeSHA256: "o", SHA256: "a", BeforeMtime: during}, {BeforeSHA256: "a", SHA256: "b", BeforeMtime: during}}, "b", false},
		{"broken chain", []SeededFile{{BeforeSHA256: "o", SHA256: "a", BeforeMtime: before}, {BeforeSHA256: "x", SHA256: "b", BeforeMtime: during}}, "b", false},
		{"edited after", []SeededFile{{BeforeSHA256: "o", SHA256: "a", BeforeMtime: before}}, "e", false},
		{"gone", []SeededFile{{BeforeSHA256: "o", SHA256: "a", BeforeMtime: before}}, "", false},
	}
	for _, c := range cases {
		if got := initOwned(c.rows, since, c.current); got != c.want {
			t.Errorf("%s: initOwned = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestForeignOwnedRule(t *testing.T) {
	cases := []struct {
		name, atEngagement string
		rows               []gateWrite
		want               bool
	}{
		{"no write", "h", nil, false},
		{"other story from the engaged bytes", "h", []gateWrite{{story: "B", before: "h"}}, true},
		{"other story creates an absent file", "", []gateWrite{{story: "B"}}, true},
		{"this story wrote too", "h", []gateWrite{{story: "B", before: "h"}, {story: "A", before: "b"}}, false},
		{"changed before the other story's write", "h", []gateWrite{{story: "B", before: "x"}}, false},
	}
	for _, c := range cases {
		if got := foreignOwned(c.rows, "A", c.atEngagement); got != c.want {
			t.Errorf("%s: foreignOwned = %v, want %v", c.name, got, c.want)
		}
	}
}
