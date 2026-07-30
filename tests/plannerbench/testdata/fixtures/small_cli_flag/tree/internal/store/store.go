// Package store performs the fixture tool's only mutation.
package store

import (
	"fmt"
	"sync"
)

var (
	mu      sync.Mutex
	records = map[string]bool{}
)

// Write records name, refusing an existing record unless force is set.
func Write(name string, force bool) error {
	mu.Lock()
	defer mu.Unlock()
	if records[name] && !force {
		return fmt.Errorf("record %q exists (pass --force to overwrite)", name)
	}
	records[name] = true
	return nil
}

// Has reports whether name was written.
func Has(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	return records[name]
}

// Reset clears the store between tests.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	records = map[string]bool{}
}
