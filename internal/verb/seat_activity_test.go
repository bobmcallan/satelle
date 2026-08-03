package verb

import (
	"os"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
)

// TestSeatListExposesActivity (sty_598a8e1b AC1): seatRowFromLease carries
// activity fields for a live in-flight lease and omits them when the driver is dead.
func TestSeatListExposesActivity(t *testing.T) {
	now := time.Now().UTC()
	live := lease.Lease{
		ItemID: "sty_live", Kind: "story", StorySeat: true, Owner: "alice", State: "plan",
		AcquiredAt: now, HeartbeatAt: now,
		InFlight: true, InFlightAt: now, InFlightPid: os.Getpid(),
		ActivityLabel: "satelle-story-plan-review", ActivityIndex: 2, ActivityTotal: 3,
		ActivityAt: now.Add(-30 * time.Second),
	}
	row := seatRowFromLease(live, now)
	if !row.InFlight || row.Activity != "satelle-story-plan-review" || row.ActivityIndex != 2 || row.ActivityTotal != 3 {
		t.Fatalf("live row = %+v", row)
	}
	if row.ActivityAge == "" {
		t.Fatal("activity_age must be set")
	}
	dead := live
	dead.InFlightPid = 1<<30 - 1
	row = seatRowFromLease(dead, now)
	if row.InFlight {
		t.Fatal("dead pid must not be in_flight")
	}
	if row.Activity != "" || row.ActivityIndex != 0 || row.ActivityTotal != 0 {
		t.Fatalf("dead pid must omit activity: %+v", row)
	}
}
