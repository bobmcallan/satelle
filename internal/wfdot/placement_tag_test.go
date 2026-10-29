package wfdot

import "testing"

// placement_tag, delivery_heading and lane_tag (sty_c931cd90) are step keys that
// reach the route's state, so the engine and wave read the repo's vocabulary
// from the route and the binary names none.
func TestPlacementTagKeysReachTheRouteState(t *testing.T) {
	body := reworkStepBody(`placement_tag = " placement: "
delivery_heading = "Delivery"
lane_tag = "lane:"
`)
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "coded")
	if st.PlacementTag != "placement:" || st.DeliveryHeading != "Delivery" || st.LaneTag != "lane:" {
		t.Errorf("step = %q/%q/%q, want placement:/Delivery/lane:", st.PlacementTag, st.DeliveryHeading, st.LaneTag)
	}
	spec, err := ParseRoute(reworkDone, body, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	state, ok := spec.StateNamed("in_progress")
	if !ok || state.PlacementTag != "placement:" || state.DeliveryHeading != "Delivery" || state.LaneTag != "lane:" {
		t.Errorf("route state = %q/%q/%q (found %v), want the keys carried through", state.PlacementTag, state.DeliveryHeading, state.LaneTag, ok)
	}

	absent, err := ParseSteps(reworkStepBody(""))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if st := stepProviding(t, absent, "coded"); st.PlacementTag != "" || st.DeliveryHeading != "" || st.LaneTag != "" {
		t.Errorf("a step without the keys carries %q/%q/%q, want none", st.PlacementTag, st.DeliveryHeading, st.LaneTag)
	}
}
