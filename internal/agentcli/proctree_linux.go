//go:build linux

package agentcli

import (
	"os"
	"strconv"
	"strings"
)

// procTreeScanAttempts bounds how many times procTreeCPU re-takes a scan that a
// reap tore. A tree that reaps children continuously would otherwise re-scan
// forever; after this many attempts the highest total seen is returned.
const procTreeScanAttempts = 3

// procStat is one process's /proc/<pid>/stat reading, in clock ticks.
type procStat struct {
	ppid     int
	self     uint64 // utime+stime
	children uint64 // cutime+cstime: CPU of children already reaped
}

// procTreeCPU sums utime+stime+cutime+cstime (clock ticks) over pid and every
// descendant, read from /proc. ok is false when the root process is already
// gone. Processes vanishing mid-scan are skipped. Wrapper CLIs (node, rust)
// fork a worker, so the root alone under-counts.
//
// The /proc scan is not atomic: a descendant reaped mid-scan can be read after
// its parent's stat (so its CPU is not yet in the parent's c-fields) and be gone
// by the time its own stat is read, hiding its CPU from the sum. Reaping moves a
// child's CPU into its parent's c-fields, and the parent is in the tree whenever
// the child was, so the scan re-reads every in-tree process afterwards; if one
// vanished or its c-fields grew the scan was torn and is re-taken, at most
// procTreeScanAttempts times (the highest total seen is returned then).
func procTreeCPU(pid int) (ticks uint64, ok bool) {
	return procTreeCPUWith(pid, listProcPids, readProcStat)
}

// procTreeCPUWith is procTreeCPU over injected /proc readers.
func procTreeCPUWith(pid int, list func() ([]int, error), stat func(pid int) (procStat, bool)) (ticks uint64, ok bool) {
	for attempt := 0; attempt < procTreeScanAttempts; attempt++ {
		pids, err := list()
		if err != nil {
			return 0, false
		}
		procs := map[int]procStat{}
		for _, p := range pids {
			if st, found := stat(p); found {
				procs[p] = st
			}
		}
		root, found := procs[pid]
		if !found {
			return 0, false
		}
		total := root.self + root.children
		inTree := map[int]bool{pid: true}
		// Repeat until no new descendant joins (pids are not ordered by ancestry).
		for changed := true; changed; {
			changed = false
			for p, pr := range procs {
				if !inTree[p] && inTree[pr.ppid] {
					inTree[p] = true
					total += pr.self + pr.children
					changed = true
				}
			}
		}
		ticks = max(ticks, total)
		if !scanTorn(inTree, procs, stat) {
			break
		}
	}
	return ticks, true
}

// scanTorn reports whether a reap landed during the scan that read procs: an
// in-tree process has vanished or its reaped-children CPU has grown since.
func scanTorn(inTree map[int]bool, procs map[int]procStat, stat func(pid int) (procStat, bool)) bool {
	for p := range inTree {
		now, found := stat(p)
		if !found || now.children > procs[p].children {
			return true
		}
	}
	return false
}

// listProcPids returns every numeric entry of /proc, in ascending pid order.
func listProcPids() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		if p, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, p)
		}
	}
	return pids, nil
}

// readProcStat parses /proc/<pid>/stat; found is false when the process is gone
// or its stat is unreadable.
func readProcStat(pid int) (procStat, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, false
	}
	s := string(raw)
	// comm (field 2) may contain spaces and parens: split after the last ')'.
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return procStat{}, false
	}
	f := strings.Fields(s[i+1:]) // f[0]=state f[1]=ppid ... f[11]=utime f[12]=stime
	if len(f) < 15 {
		return procStat{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	ut, err2 := strconv.ParseUint(f[11], 10, 64)
	st, err3 := strconv.ParseUint(f[12], 10, 64)
	cut, err4 := strconv.ParseUint(f[13], 10, 64)
	cst, err5 := strconv.ParseUint(f[14], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		return procStat{}, false
	}
	return procStat{ppid: ppid, self: ut + st, children: cut + cst}, true
}
