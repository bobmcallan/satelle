//go:build linux

package agentcli

import (
	"os"
	"strconv"
	"strings"
)

// procTreeCPU sums utime+stime+cutime+cstime (clock ticks) over pid and every descendant,
// read from /proc. ok is false when the root process is already gone.
// Processes vanishing mid-scan are skipped. Wrapper CLIs (node, rust) fork a
// worker, so the root alone under-counts.
func procTreeCPU(pid int) (ticks uint64, ok bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	type proc struct {
		ppid int
		cpu  uint64
	}
	procs := map[int]proc{}
	for _, e := range entries {
		p, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(raw)
		// comm (field 2) may contain spaces and parens: split after the last ')'.
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:]) // f[0]=state f[1]=ppid ... f[11]=utime f[12]=stime
		if len(f) < 15 {
			continue
		}
		ppid, err1 := strconv.Atoi(f[1])
		ut, err2 := strconv.ParseUint(f[11], 10, 64)
		st, err3 := strconv.ParseUint(f[12], 10, 64)
		cut, err4 := strconv.ParseUint(f[13], 10, 64)
		cst, err5 := strconv.ParseUint(f[14], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
			continue
		}
		// cutime/cstime hold the CPU of children already reaped: without them
		// a finished tool child's work would vanish from the sum and the total
		// would drop. Reaping moves a child's CPU into its parent's c-fields,
		// so the tree total never decreases.
		procs[p] = proc{ppid: ppid, cpu: ut + st + cut + cst}
	}
	root, found := procs[pid]
	if !found {
		return 0, false
	}
	ticks = root.cpu
	inTree := map[int]bool{pid: true}
	// Repeat until no new descendant joins (pids are not ordered by ancestry).
	for changed := true; changed; {
		changed = false
		for p, pr := range procs {
			if !inTree[p] && inTree[pr.ppid] {
				inTree[p] = true
				ticks += pr.cpu
				changed = true
			}
		}
	}
	return ticks, true
}
