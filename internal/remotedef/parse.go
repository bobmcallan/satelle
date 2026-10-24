package remotedef

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// Parse decodes the secrets, repos and egress allowlist of a remote.toml. It does
// not validate and it does not refuse a key it does not read: the definition a
// node receives is already validated by satelle-server, which may add keys a
// node built earlier has never heard of. It fails only when data is not TOML or
// a field the node reads has the wrong type.
func Parse(data []byte) (Definition, error) {
	var d Definition
	if _, err := toml.Decode(string(data), &d); err != nil {
		return Definition{}, fmt.Errorf("remote.toml: %w", err)
	}
	return d, nil
}
