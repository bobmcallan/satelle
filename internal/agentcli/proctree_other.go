//go:build !linux

package agentcli

// procTreeCPU is unavailable off Linux: the liveness probe reports that
// explicitly and the command transport keeps its strict no-output behaviour.
func procTreeCPU(pid int) (uint64, bool) { return 0, false }
