// Package textdiff renders a unified diff between two texts. It is a leaf
// package so the reconcile/workflow CLI and the verb layer share one renderer:
// a story's gitignored substrate has no git blob, so git cannot diff it.
package textdiff

import (
	"fmt"
	"io"
	"strings"
)

// diffOp is one line of a diff: ' ' shared, '-' only in old, '+' only in new.
type diffOp struct {
	kind byte
	line string
}

// maxDiffCells bounds the LCS table; two changed regions larger than this print
// as one replaced block rather than exhausting memory.
const maxDiffCells = 16_000_000

// Unified writes old against new as a unified diff with three lines of
// context. Lines keep their newline, so a file differing only by its last
// newline still shows a difference.
func Unified(w io.Writer, oldLabel, newLabel, oldS, newS string) {
	ops := diffLines(splitKeepNewline(oldS), splitKeepNewline(newS))
	fmt.Fprintf(w, "--- %s\n+++ %s\n", oldLabel, newLabel)
	const ctx = 3
	oldAt, newAt := make([]int, len(ops)+1), make([]int, len(ops)+1)
	oldAt[0], newAt[0] = 1, 1
	for i, op := range ops {
		oldAt[i+1], newAt[i+1] = oldAt[i], newAt[i]
		if op.kind != '+' {
			oldAt[i+1]++
		}
		if op.kind != '-' {
			newAt[i+1]++
		}
	}
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start, last := i-ctx, i
		if start < 0 {
			start = 0
		}
		j := i
		for ; j < len(ops) && j-last <= 2*ctx; j++ {
			if ops[j].kind != ' ' {
				last = j
			}
		}
		end := last + ctx + 1
		if end > len(ops) {
			end = len(ops)
		}
		oc, nc := oldAt[end]-oldAt[start], newAt[end]-newAt[start]
		oStart, nStart := oldAt[start], newAt[start]
		if oc == 0 {
			oStart--
		}
		if nc == 0 {
			nStart--
		}
		fmt.Fprintf(w, "@@ -%d,%d +%d,%d @@\n", oStart, oc, nStart, nc)
		for _, op := range ops[start:end] {
			fmt.Fprintf(w, "%c%s", op.kind, op.line)
			if !strings.HasSuffix(op.line, "\n") {
				fmt.Fprint(w, "\n\\ No newline at end of file\n")
			}
		}
		i = end
	}
}

// splitKeepNewline splits s into lines that keep their trailing newline; the
// last line has none when s does not end in one.
func splitKeepNewline(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLines is a longest-common-subsequence diff of two line lists.
func diffLines(a, b []string) []diffOp {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	var ops []diffOp
	for _, l := range a[:p] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, lcsOps(a[p:len(a)-s], b[p:len(b)-s])...)
	for _, l := range a[len(a)-s:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	var ops []diffOp
	if (n+1)*(m+1) > maxDiffCells {
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}
	// t[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	t := make([]int32, (n+1)*(m+1))
	at := func(i, j int) *int32 { return &t[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				*at(i, j) = *at(i+1, j+1) + 1
			case *at(i+1, j) >= *at(i, j+1):
				*at(i, j) = *at(i+1, j)
			default:
				*at(i, j) = *at(i, j+1)
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case *at(i+1, j) >= *at(i, j+1):
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}
