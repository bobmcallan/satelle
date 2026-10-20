package remotenode

import (
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/filebundle"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// SubstrateDirs are the directories of a remote definition that become the node's
// substrate, relative to the definition and to the node's data dir alike.
var SubstrateDirs = []string{"workflows", "skills", "principles"}

// DefinitionFile is the definition's own file in the bundle. The node reads it
// (remotedef.Parse) and does not install it: host commands are satelle-server's
// business and the node has no use for them.
const DefinitionFile = "remote.toml"

// DefinitionRoots are the only paths a definition bundle may carry: the
// definition file and the substrate directories.
var DefinitionRoots = append([]string{DefinitionFile}, SubstrateDirs...)

// InstallDefinition replaces the SubstrateDirs under dataDir with the substrate
// files of bundle, so the node's workflows, skills and principles are exactly the
// remote's own and nothing a fresh init scaffolded survives beside them, and
// returns what the node reads of the bundle's remote.toml. Everything is checked
// before anything is written: a bundle of another format, a path outside
// DefinitionRoots, a missing remote.toml or one the node cannot read installs
// nothing.
func InstallDefinition(dataDir string, bundle []byte) (remotedef.Definition, error) {
	entries, err := filebundle.Decode(bundle, DefinitionRoots)
	if err != nil {
		return remotedef.Definition{}, err
	}
	var def remotedef.Definition
	var haveDef bool
	var substrate []filebundle.Entry
	for _, e := range entries {
		switch {
		case e.Path == DefinitionFile:
			if def, err = remotedef.Parse([]byte(e.Content)); err != nil {
				return remotedef.Definition{}, err
			}
			haveDef = true
		case underSubstrateDir(e.Path):
			substrate = append(substrate, e)
		default:
			return remotedef.Definition{}, fmt.Errorf("bundle path %q is outside %s", e.Path, strings.Join(SubstrateDirs, ", "))
		}
	}
	if !haveDef {
		return remotedef.Definition{}, fmt.Errorf("the bundle has no %s", DefinitionFile)
	}
	if err := filebundle.Write(dataDir, substrate, true, underSubstrateDir); err != nil {
		return remotedef.Definition{}, err
	}
	return def, nil
}

// underSubstrateDir: a slash path inside one of the SubstrateDirs.
func underSubstrateDir(rel string) bool {
	for _, d := range SubstrateDirs {
		if strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}
