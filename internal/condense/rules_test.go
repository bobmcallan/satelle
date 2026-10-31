package condense

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestParseRulesLayersAnOverrideKeyByKey(t *testing.T) {
	base := config.EmbeddedCondense()
	over := []byte("drop_phrases = [\"zorp\"]\n")
	r, err := ParseRules(base, over)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.DropPhrases) != 1 || r.DropPhrases[0] != "zorp" {
		t.Errorf("the override must replace drop_phrases, got %v", r.DropPhrases)
	}
	def := embeddedRules(t)
	if strings.Join(r.Ladder, ",") != strings.Join(def.Ladder, ",") || len(r.ProtectedPatterns) != len(def.ProtectedPatterns) {
		t.Errorf("a key the override omits keeps its default: %+v", r)
	}
}

func TestAnOverrideChangesTheOutput(t *testing.T) {
	body := "# T\n\nThe first sentence stands. A zorp second sentence follows here. Another plain one."
	def := embeddedRules(t)
	over, err := ParseRules(config.EmbeddedCondense(), []byte("protected_patterns = ['(?i)\\bzorp\\b']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a, b := lead(body, def), lead(body, over); a == b || !strings.Contains(b, "zorp") || strings.Contains(a, "zorp") {
		t.Errorf("a repo-protected token must be kept and the default must drop it:\n default: %q\n override: %q", a, b)
	}
}

func TestDefaultProtectedPatternsHoldNegationsNumbersPathsAndCommands(t *testing.T) {
	r := embeddedRules(t)
	for _, protected := range []string{
		"never skip a gate", "do not edit", "no schedule", "it cannot run", "without a story", "it doesn't write", "it doesn’t write",
		"step 3", "edit internal/cli/app.go", "run `satelle story set`", "satelle story wave exits",
	} {
		if !holdsProtected(protected, r) {
			t.Errorf("%q must be protected", protected)
		}
	}
	for _, plain := range []string{
		"the constitution says so", "another instance of it", "a plain closing sentence",
	} {
		if holdsProtected(plain, r) {
			t.Errorf("%q must not be protected", plain)
		}
	}
}

func TestRulesAreValidated(t *testing.T) {
	cases := map[string]string{
		"no ladder":         "ladder = []\n",
		"full not first":    "ladder = [\"lead\", \"full\"]\n",
		"pointer not last":  "ladder = [\"full\", \"pointer\", \"lead\"]\n",
		"unknown rung":      "ladder = [\"full\", \"squash\"]\n",
		"bad pattern":       "protected_patterns = ['(']\n",
		"unparseable layer": "ladder = \n",
	}
	for name, over := range cases {
		if _, err := ParseRules(config.EmbeddedCondense(), []byte(over)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestTheLadderOrderIsConfiguration(t *testing.T) {
	r, err := ParseRules(config.EmbeddedCondense(), []byte("ladder = [\"full\", \"lead\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	forms := formsFor(Item{Name: "d", Body: prose("d", 6), Pointer: "- `d`"}, r, bytesOf)
	for _, f := range forms {
		if f.rung == RungFiller || f.pointer {
			t.Errorf("a ladder without %s/pointer offers %+v", f.rung, f)
		}
	}
}

// TestCondenseSourceHoldsNoRuleLiterals proves the rules live in condense.toml
// and nowhere in Go: no string or character literal in this package's source
// equals a drop phrase, a protected pattern, a clause delimiter, a sentence
// terminator, an abbreviation, or a bare negation or protected word.
func TestCondenseSourceHoldsNoRuleLiterals(t *testing.T) {
	r := embeddedRules(t)
	banned := map[string]string{}
	add := func(kind string, vals []string) {
		for _, v := range vals {
			banned[strings.ToLower(v)] = kind
		}
	}
	add("drop phrase", r.DropPhrases)
	add("protected pattern", r.ProtectedPatterns)
	add("clause delimiter", r.ClauseDelimiters)
	add("sentence terminator", r.SentenceTerminators)
	add("sentence exception", r.SentenceExceptions)
	// The words the protected patterns are built from (a negation such as "not",
	// "never" or "no", or "satelle"), so a switch on a bare token is caught too.
	word := regexp.MustCompile(`[A-Za-z]{2,}`)
	for _, p := range r.ProtectedPatterns {
		for _, w := range word.FindAllString(p, -1) {
			if len(w) > 2 && strings.ToLower(w) != "satelle" || strings.ToLower(w) == "no" {
				banned[strings.ToLower(w)] = "word of a protected pattern"
			}
		}
	}
	for _, neg := range []string{"not", "never", "no", "must not", "do not"} {
		banned[neg] = "negation"
	}
	delete(banned, "ladder")

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || (lit.Kind != token.STRING && lit.Kind != token.CHAR) {
				return true
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if kind, bad := banned[strings.ToLower(val)]; bad {
				t.Errorf("%s: literal %s is a %s that belongs in condense.toml", fset.Position(lit.Pos()), lit.Value, kind)
			}
			return true
		})
	}
	if checked < 4 {
		t.Fatalf("scanned %d source files, expected the whole package", checked)
	}
}
