//go:build plannerbench

package plannerbench

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// importsAgentArtifact reports whether ANY file in this package imports the
// package that owns the transition validator. The oracle's independence (AC8) is
// an import-graph fact, so it is checked as one rather than by reading the
// oracle's code and trusting it.
const transitionValidatorPackage = "github.com/bobmcallan/satelle/internal/agentartifact"

func importsAgentArtifact() bool {
	entries, err := os.ReadDir(".")
	if err != nil {
		return true // fail closed: an unreadable package cannot be cleared
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			return true
		}
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) == transitionValidatorPackage {
				return true
			}
		}
	}
	return false
}
