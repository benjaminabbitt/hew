package hew

// editKind is one step of a sequence edit script.
type editKind uint8

const (
	editEqual  editKind = iota // a[A] and b[B] are the same node
	editDelete                 // a[A] is not in b
	editInsert                 // b[B] is not in a
)

// editStep is one step of the edit script, in merged order.
type editStep struct {
	Kind editKind
	A, B int // index into a / b; -1 where the step names no element
}

// myers computes the edit script between two identity-token sequences with
// Myers's O((N+M)D) greedy algorithm — the algorithm §9.4-R1 names, because a
// diff case can only be pinned in the corpus if every implementation picks the
// SAME minimal script out of the many that exist.
//
// Ties break toward the earlier deletion, which is R1's other half and which
// falls out of the standard formulation's `v[k-1] < v[k+1]` test: on a tie the
// else branch is taken, and the else branch is the rightward (deletion) move.
func myers(a, b []string) []editStep {
	trace, offset := myersTrace(a, b)
	return myersBacktrack(trace, offset, len(a), len(b))
}

// myersTrace runs the forward pass, keeping the furthest-reaching frontier at
// the START of each round so the backtrack can walk it in reverse.
func myersTrace(a, b []string) ([][]int, int) {
	n, m := len(a), len(b)
	max := n + m
	// One slot of slack on each side, so that k+1 is addressable at k == d
	// even when both inputs are empty and max is 0.
	offset := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1] // down: b[y] is an insertion
			} else {
				x = v[offset+k-1] + 1 // right: a[x-1] is a deletion
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return trace, offset
			}
		}
	}
	return trace, offset
}

func myersBacktrack(trace [][]int, offset, x, y int) []editStep {
	var rev []editStep
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			rev = append(rev, editStep{Kind: editEqual, A: x - 1, B: y - 1})
			x--
			y--
		}
		if x == prevX {
			rev = append(rev, editStep{Kind: editInsert, A: -1, B: prevY})
		} else {
			rev = append(rev, editStep{Kind: editDelete, A: prevX, B: -1})
		}
		x, y = prevX, prevY
	}
	for x > 0 && y > 0 {
		rev = append(rev, editStep{Kind: editEqual, A: x - 1, B: y - 1})
		x--
		y--
	}
	out := make([]editStep, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	return out
}
