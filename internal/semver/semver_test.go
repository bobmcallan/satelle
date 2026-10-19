package semver

import "testing"

func TestValid(t *testing.T) {
	for _, ok := range []string{"0.0.702", "v1.2.3", "10.20.30"} {
		if !Valid(ok) {
			t.Errorf("Valid(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"1.2", "1.2.x", "latest", "", "v", "1.2.3.4", "1..3", "-1.2.3", "1.2.3-rc1", "vv1.2.3"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true, want false", bad)
		}
	}
}

func TestParseStrictAndLess(t *testing.T) {
	a, ok := ParseStrict("1.2.3")
	if !ok || a != [3]int{1, 2, 3} {
		t.Fatalf("ParseStrict(1.2.3) = %v, %v", a, ok)
	}
	if _, ok := ParseStrict("v1.2.3"); ok {
		t.Error("ParseStrict must not accept a prefix; callers trim their own")
	}
	b, _ := ParseStrict("1.10.0")
	if !Less(a, b) || Less(b, a) || Less(a, a) {
		t.Errorf("Less must order 1.2.3 < 1.10.0 numerically")
	}
}

func TestLenientCmp(t *testing.T) {
	if Cmp("0.0.10", "0.0.9") <= 0 {
		t.Error("0.0.10 must sort after 0.0.9")
	}
	if Cmp("v1.2.3", "1.2.3") != 0 {
		t.Error("a leading v must not matter")
	}
	if Cmp("1.2", "1.2.0") != 0 {
		t.Error("a missing part reads as 0")
	}
}
