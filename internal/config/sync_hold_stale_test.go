package config

import (
	"testing"
	"time"
)

// [sync] hold_stale_after: missing means the 2h default, a duration sets it,
// anything else is a config error, and it is never minted as an area
// (sty_6e5fc41c).
func TestHoldStaleAfter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sync    SyncTable
		want    time.Duration
		wantErr bool
	}{
		{"missing", nil, 2 * time.Hour, false},
		{"blank", SyncTable{"hold_stale_after": " "}, 2 * time.Hour, false},
		{"set", SyncTable{"hold_stale_after": "30m"}, 30 * time.Minute, false},
		{"not a duration", SyncTable{"hold_stale_after": "soon"}, 0, true},
		{"zero", SyncTable{"hold_stale_after": "0s"}, 0, true},
		{"negative", SyncTable{"hold_stale_after": "-1h"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HoldStaleAfter(Config{Sync: tc.sync})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("HoldStaleAfter = %v, want %v", got, tc.want)
			}
		})
	}
	if !reservedSyncKeys[syncHoldStaleAfterKey] {
		t.Error("hold_stale_after must be a reserved [sync] key")
	}
	for _, a := range SyncAreas {
		if a == syncHoldStaleAfterKey {
			t.Error("hold_stale_after minted as a sync area")
		}
	}
}
