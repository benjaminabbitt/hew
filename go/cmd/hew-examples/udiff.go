package main

import (
	"fmt"
	"strings"
)

// unifiedDiff renders a conventional line-oriented unified diff of before →
// after. hew's whole point is that it does NOT work this way — but a reader
// checking what an apply did wants the familiar rendering, so the transcript
// pairs every result file with one.
//
// The labels are supplied rather than derived from a path, so nothing about
// the scratch directory can appear in the output.
func unifiedDiff(beforeLabel, afterLabel, before, after string, context int) string {
	a, b := splitLines(before), splitLines(after)
	ops := lcsOps(a, b)
	hunks := groupHunks(ops, context)
	if len(hunks) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n", beforeLabel)
	fmt.Fprintf(&out, "+++ %s\n", afterLabel)
	for _, h := range hunks {
		aCount, bCount := 0, 0
		for _, op := range h.ops {
			if op.kind != '+' {
				aCount++
			}
			if op.kind != '-' {
				bCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", span(h.aStart, aCount), span(h.bStart, bCount))
		for _, op := range h.ops {
			out.WriteByte(byte(op.kind))
			out.WriteString(op.line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func span(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if count == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

// splitLines splits on LF and drops the empty trailing element a
// newline-terminated file produces, so the last real line is the last element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type diffOp struct {
	kind rune // ' ', '-', '+'
	line string
	// aIdx and bIdx are the 0-based positions the op occupies on each side.
	aIdx, bIdx int
}

// lcsOps computes the edit script from the longest common subsequence. The
// files in an example are tens of lines, so the quadratic table is both fast
// enough and considerably easier to be confident in than a Myers
// implementation nobody would review.
func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i], i, j})
			i, j = i+1, j+1
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, diffOp{'-', a[i], i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j], i, j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i], i, j})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j], i, j})
	}
	return ops
}

type hunk struct {
	aStart, bStart int
	ops            []diffOp
}

// groupHunks collects the changed ops with `context` unchanged lines on each
// side, merging runs that would otherwise overlap.
func groupHunks(ops []diffOp, context int) []hunk {
	var changed []int
	for i, op := range ops {
		if op.kind != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	var hunks []hunk
	start := max(0, changed[0]-context)
	end := min(len(ops), changed[0]+context+1)
	for _, idx := range changed[1:] {
		if idx-context <= end {
			end = min(len(ops), idx+context+1)
			continue
		}
		hunks = append(hunks, sliceHunk(ops, start, end))
		start, end = max(0, idx-context), min(len(ops), idx+context+1)
	}
	hunks = append(hunks, sliceHunk(ops, start, end))
	return hunks
}

func sliceHunk(ops []diffOp, start, end int) hunk {
	return hunk{aStart: ops[start].aIdx, bStart: ops[start].bIdx, ops: ops[start:end]}
}
