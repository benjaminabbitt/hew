package hewjson

// childSpan generalizes an object member and an array element for the
// shared insert/remove byte-splicing logic: start is the first byte of the
// child overall (a member's KEY start, or an element's value start), end is
// its content's last byte (comma-exclusive), and commaPos is its own
// trailing comma's position, or -1.
type childSpan struct{ start, end, commaPos int }

func objChildren(n *jNode) []childSpan {
	out := make([]childSpan, len(n.members))
	for i, m := range n.members {
		out[i] = childSpan{start: m.keyStart, end: m.valEnd, commaPos: m.commaPos}
	}
	return out
}

func arrChildren(n *jNode) []childSpan {
	out := make([]childSpan, len(n.elems))
	for i, e := range n.elems {
		out[i] = childSpan{start: e.valStart, end: e.valEnd, commaPos: e.commaPos}
	}
	return out
}

func lineIndent(src []byte, pos int) string {
	i := pos
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return string(src[i:pos])
}

func containsNewline(src []byte, start, end int) bool {
	for i := start; i < end; i++ {
		if src[i] == '\n' {
			return true
		}
	}
	return false
}

// genericInsert computes the byte edit to insert newText as a new child of a
// container spanning [containerStart,containerEnd) ('{'/'}' or '['/']'
// included), relative to an existing child at afterIdx or beforeIdx
// (both -1 means append at the end), preserving the container's existing
// single-line (flow) or multi-line (block) style.
func genericInsert(src []byte, containerStart, containerEnd int, children []childSpan, afterIdx, beforeIdx int, newText string, padded bool) edit {
	firstContent := containerEnd - 1
	if len(children) > 0 {
		firstContent = children[0].start
	}
	block := containsNewline(src, containerStart, firstContent)

	if len(children) == 0 {
		if block {
			indent := lineIndent(src, containerStart) + "  "
			closeIndent := lineIndent(src, containerStart)
			return edit{start: containerStart + 1, end: containerEnd - 1, text: "\n" + indent + newText + "\n" + closeIndent}
		}
		if padded {
			return edit{start: containerStart + 1, end: containerEnd - 1, text: " " + newText + " "}
		}
		return edit{start: containerStart + 1, end: containerEnd - 1, text: newText}
	}

	if block {
		indent := lineIndent(src, children[0].start)
		switch {
		case afterIdx >= 0:
			c := children[afterIdx]
			return edit{start: c.end, end: c.end, text: ",\n" + indent + newText}
		case beforeIdx >= 0:
			c := children[beforeIdx]
			return edit{start: c.start, end: c.start, text: newText + ",\n" + indent}
		default:
			c := children[len(children)-1]
			return edit{start: c.end, end: c.end, text: ",\n" + indent + newText}
		}
	}
	switch {
	case afterIdx >= 0:
		c := children[afterIdx]
		return edit{start: c.end, end: c.end, text: ", " + newText}
	case beforeIdx >= 0:
		c := children[beforeIdx]
		return edit{start: c.start, end: c.start, text: newText + ", "}
	default:
		c := children[len(children)-1]
		return edit{start: c.end, end: c.end, text: ", " + newText}
	}
}

// genericRemove computes the byte edit to delete child idx from a container,
// consuming exactly one adjoining comma and the removed child's own leading
// gap, so a surviving sibling's formatting is untouched (§6.3).
func genericRemove(containerStart int, children []childSpan, idx int) edit {
	n := len(children)
	if idx < n-1 {
		start := containerStart + 1
		if idx > 0 {
			start = children[idx-1].end
			if children[idx-1].commaPos >= 0 {
				start = children[idx-1].commaPos + 1
			}
		}
		end := children[idx].end
		if children[idx].commaPos >= 0 {
			end = children[idx].commaPos + 1
		}
		return edit{start: start, end: end, text: ""}
	}
	start := containerStart + 1
	if idx > 0 {
		if children[idx-1].commaPos >= 0 {
			start = children[idx-1].commaPos
		} else {
			start = children[idx-1].end
		}
	}
	return edit{start: start, end: children[idx].end, text: ""}
}

func insertIntoObj(src []byte, obj *jNode, afterIdx, beforeIdx int, newMemberText string) *edit {
	e := genericInsert(src, obj.start, obj.end, objChildren(obj), afterIdx, beforeIdx, newMemberText, true)
	return &e
}

func insertIntoArr(src []byte, arr *jNode, afterIdx, beforeIdx int, newElemText string) *edit {
	e := genericInsert(src, arr.start, arr.end, arrChildren(arr), afterIdx, beforeIdx, newElemText, false)
	return &e
}

func removeObjMember(src []byte, obj *jNode, idx int) *edit {
	e := genericRemove(obj.start, objChildren(obj), idx)
	return &e
}

func removeArrElem(src []byte, arr *jNode, idx int) *edit {
	e := genericRemove(arr.start, arrChildren(arr), idx)
	return &e
}
