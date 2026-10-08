package verb

import (
	"os"
	"reflect"
	"testing"
	"unsafe"
)

// startWiring is verb's wiring as the process left package init, recorded before
// any test runs so a test that reads it does not depend on shuffle order.
var startWiring struct {
	funcs    map[string]unsafe.Pointer // closure word of each func seam with a production default
	parallel string
}

func TestMain(m *testing.M) {
	startWiring.funcs = map[string]unsafe.Pointer{}
	for _, w := range wiringVars {
		switch w.name {
		case "verdictRecord", "dispatchLogsDir", "changelogPath", "driverWindowReader", "driverSnapshotter":
			startWiring.funcs[w.name] = funcWord(reflect.ValueOf(w.ptr).Elem())
		}
	}
	startWiring.parallel = engagementParallel
	os.Exit(m.Run())
}
