package verb

import (
	"encoding/json"
	"fmt"
)

// decode unmarshals a request body into dst, treating an empty/null body as an
// empty object so verbs with all-optional fields accept a missing body.
func decode(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("verb: decode request: %w", err)
	}
	return nil
}
