package wfdot

import (
	"reflect"
	"strings"
	"testing"
)

// A step declares the read-only context sources offered to the agents dispatched
// for an edge entering it (sty_12499ecc AC1). The names are the repo's; the
// sibling steps keep none.
func TestStepContextCarriesToTheSpecStateOfTheDeclaringStepOnly(t *testing.T) {
	done, step := fixtures(t)
	step = strings.Replace(step, "[integrated]\nstatus = \"integration\"\n", "[integrated]\nstatus = \"integration\"\ncontext = [\"src\", \" other \"]\n", 1)
	if !strings.Contains(step, "context = ") {
		t.Fatal("fixture edit did not apply")
	}
	spec, err := ParseRoute(done, step, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	for _, st := range spec.States {
		switch st.Name {
		case "integration":
			if want := []string{"src", "other"}; !reflect.DeepEqual(st.Context, want) {
				t.Errorf("integration Context = %v, want %v", st.Context, want)
			}
		default:
			if len(st.Context) != 0 {
				t.Errorf("sibling step %q carries context %v", st.Name, st.Context)
			}
		}
	}
}

// The key is part of the step wire: a typo is still the unknown key it is.
func TestStepContextTypoStaysAnUnknownKey(t *testing.T) {
	done, step := fixtures(t)
	step = strings.Replace(step, "[integrated]\nstatus = \"integration\"\n", "[integrated]\nstatus = \"integration\"\ncontxt = [\"src\"]\n", 1)
	if _, err := ParseRoute(done, step, "feature", nil); err == nil || !strings.Contains(err.Error(), "contxt") {
		t.Fatalf("err = %v, want the unknown key named", err)
	}
}
