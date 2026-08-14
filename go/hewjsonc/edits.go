package hewjsonc

import (
	"sort"
	"strings"

	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// edit is one byte-range splice against the source the doc was parsed from:
// replace [start,end) with text. An insertion is start==end.
type edit struct {
	start, end int
	text       string
}

// applyEdits splices a batch of non-overlapping edits. The sort is stable so
// that a separator comma queued at the same offset as the content it separates
// stays in front of it.
func applyEdits(src []byte, edits []edit) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end {
			return nil, &hewerr.Error{Code: hewerr.CodeConflict, Component: hewerr.ComponentApplier,
				Detail: "two transforms touch overlapping regions of the target (§10 HEW030)"}
		}
	}
	var b strings.Builder
	pos := 0
	for _, e := range edits {
		b.Write(src[pos:e.start])
		b.WriteString(e.text)
		pos = e.end
	}
	b.Write(src[pos:])
	return []byte(b.String()), nil
}

// placement is where a new child goes: the byte offset, the slot index it will
// occupy, and whether the surrounding source reads before-style (the existing
// slot follows the insertion point) or after-style.
type placement struct {
	pos    int
	idx    int
	before bool
}

// blockStyle reports whether a container is written across lines. A new child
// adopts the container's own layout rather than the patch's (§6.3).
func (d *doc) blockStyle(c *node, slots []slot) bool {
	first := c.end - 1
	if len(slots) > 0 {
		first = slots[0].start
	}
	return containsNewline(d.src, c.start, first)
}

func containsNewline(src []byte, start, end int) bool {
	for i := start; i < end && i < len(src); i++ {
		if src[i] == '\n' {
			return true
		}
	}
	return false
}

func lineIndent(src []byte, pos int) string {
	i := pos
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return string(src[i:pos])
}

// insert computes the edits that put newText into container c at p. isItem
// says whether the new child is a member/element (and so takes part in comma
// separation) rather than a comment node.
//
// Separator commas are placed by looking at what will actually stand on either
// side of the new child in the FINAL document, not at what stands there now:
// that is what lets a comment be added after the last member with no comma
// (nothing follows it yet) and the member added after that comment to supply
// the comma retroactively — the two-step shape jsonc/add-with-leading-comment
// and jsonc/roundtrip-basic both pin.
func (d *doc) insert(c *node, slots []slot, p placement, newText string, isItem bool) []edit {
	if len(slots) == 0 {
		return []edit{d.insertIntoEmpty(c, newText)}
	}
	block := d.blockStyle(c, slots)
	sep := " "
	if block {
		sep = "\n" + lineIndent(d.src, slots[0].start)
	}

	itemFollows := false
	for j := p.idx; j < len(slots); j++ {
		if slots[j].item() {
			itemFollows = true
			break
		}
	}
	prevItem := -1
	for j := p.idx - 1; j >= 0; j-- {
		if slots[j].item() {
			prevItem = j
			break
		}
	}

	var edits []edit
	if prevItem >= 0 && slots[prevItem].commaPos < 0 && (isItem || itemFollows) {
		edits = append(edits, edit{start: slots[prevItem].valEnd, end: slots[prevItem].valEnd, text: ","})
	}
	comma := ""
	if isItem && itemFollows {
		comma = ","
	}
	if p.before {
		edits = append(edits, edit{start: p.pos, end: p.pos, text: newText + comma + sep})
		return edits
	}
	return append(edits, edit{start: p.pos, end: p.pos, text: sep + newText + comma})
}

// insertIntoEmpty seeds an empty container, keeping its flow or block layout.
func (d *doc) insertIntoEmpty(c *node, newText string) edit {
	if containsNewline(d.src, c.start, c.end) {
		outer := lineIndent(d.src, c.start)
		return edit{start: c.start + 1, end: c.end - 1, text: "\n" + outer + "  " + newText + "\n" + outer}
	}
	if c.kind == kObj {
		return edit{start: c.start + 1, end: c.end - 1, text: " " + newText + " "}
	}
	return edit{start: c.start + 1, end: c.end - 1, text: newText}
}

// remove computes the edits that delete slot idx from container c.
//
// In a block container the deletion is whole-line: the removed child occupies
// its own lines, so taking them entire is what leaves every surviving sibling's
// bytes — and any blank line that made a neighbouring comment free (§8.2) —
// exactly as they were. corpus/jsonc/delete-key-with-comment pins that blank
// line surviving.
func (d *doc) remove(c *node, slots []slot, idx int) []edit {
	s := slots[idx]
	if !d.blockStyle(c, slots) {
		return []edit{flowRemove(c, slots, idx)}
	}
	end := s.end
	if s.commaPos+1 > end {
		end = s.commaPos + 1
	}
	edits := []edit{{start: lineStart(d.src, s.start), end: lineEnd(d.src, end), text: ""}}
	if !s.item() {
		return edits
	}
	// The last member of a container must not be left behind a comma.
	for j := idx + 1; j < len(slots); j++ {
		if slots[j].item() {
			return edits
		}
	}
	for j := idx - 1; j >= 0; j-- {
		if slots[j].item() && slots[j].commaPos >= 0 {
			return append(edits, edit{start: slots[j].commaPos, end: slots[j].commaPos + 1, text: ""})
		}
	}
	return edits
}

// lineStart backs pos up to the start of its line when only whitespace stands
// in between, so a child written on its own line is removed with its indent.
func lineStart(src []byte, pos int) int {
	i := pos
	for i > 0 && src[i-1] != '\n' {
		if src[i-1] != ' ' && src[i-1] != '\t' {
			return pos
		}
		i--
	}
	return i
}

// lineEnd advances pos past its line ending when only whitespace stands in
// between, so removing a child takes the newline it left behind with it.
func lineEnd(src []byte, pos int) int {
	i := pos
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r':
			i++
		case '\n':
			return i + 1
		default:
			return pos
		}
	}
	return pos
}

// flowRemove deletes a child from a single-line container, consuming exactly
// one adjoining comma so the surviving siblings keep their spacing.
func flowRemove(c *node, slots []slot, idx int) edit {
	prevEnd := func() int {
		if idx == 0 {
			return c.start + 1
		}
		if slots[idx-1].commaPos >= 0 {
			return slots[idx-1].commaPos + 1
		}
		return slots[idx-1].end
	}
	if idx < len(slots)-1 {
		end := slots[idx].end
		if slots[idx].commaPos >= 0 {
			end = slots[idx].commaPos + 1
		}
		return edit{start: prevEnd(), end: end, text: ""}
	}
	start := c.start + 1
	if idx > 0 {
		start = slots[idx-1].end
		if slots[idx-1].commaPos >= 0 {
			start = slots[idx-1].commaPos
		}
	}
	return edit{start: start, end: slots[idx].end, text: ""}
}
