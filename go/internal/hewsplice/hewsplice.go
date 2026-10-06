// Package hewsplice applies byte-range edits to a source document.
//
// Every format binding applies its transforms the same way — resolve each one
// to a byte range and a replacement, then splice them all into the original
// bytes at once — so the splice itself is not a per-format concern. It lived
// as four copies, one per binding, and the copies had already drifted: three
// sorted stably and one did not, which is the kind of difference that changes
// output without changing behaviour anyone tests for.
package hewsplice

import (
	"sort"
	"strings"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Edit is one byte-range splice against the source the document was parsed
// from: replace [Start,End) with Text. An insertion is Start==End.
type Edit struct {
	Start, End int
	Text       string
}

// Apply splices a batch of non-overlapping edits into src.
//
// The sort is STABLE, and that is a correctness property rather than a
// preference: an insertion is a zero-width range, so several edits can share
// one offset, and their authored order is the only thing that says which comes
// first. A separator comma queued at the same offset as the content it
// separates has to stay in front of it; an unstable sort is free to swap them,
// producing a differently-ordered document from the same transforms.
//
// Overlapping ranges are refused rather than resolved. Two transforms that
// touch the same bytes have no defined composition — whichever won would
// silently discard the other — so this is HEW030 (§10) and the caller writes
// nothing.
func Apply(src []byte, edits []Edit) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Start < edits[j].Start })
	for i := 1; i < len(edits); i++ {
		if edits[i].Start < edits[i-1].End {
			return nil, &hewerr.Error{Code: hewerr.CodeConflict, Component: hewerr.ComponentApplier,
				Detail: "two transforms touch overlapping regions of the target (§10 HEW030)"}
		}
	}
	var b strings.Builder
	pos := 0
	for _, e := range edits {
		b.Write(src[pos:e.Start])
		b.WriteString(e.Text)
		pos = e.End
	}
	b.Write(src[pos:])
	return []byte(b.String()), nil
}

// SeedBlock is the edit that writes child into the empty container spanning
// [open,end) — brackets included — when that container is written across
// lines.
//
// An empty container has no child whose indentation a new one can copy, so the
// indentation comes from the line the container OPENS on: the closer goes back
// to that line's leading whitespace, and the child sits one of the file's own
// indent steps deeper. Whatever the interior already holds (a comment the
// format does not count as a child) is kept, ahead of the child; only the
// whitespace before the closer is re-laid.
func SeedBlock(src []byte, open, end int, child string) Edit {
	outer := LineIndent(src, open)
	kept := strings.TrimRight(string(src[open+1:end-1]), " \t\r\n")
	return Edit{Start: open + 1, End: end - 1, Text: kept + "\n" + outer + IndentUnit(src) + child + "\n" + outer}
}

// LineIndent is the leading whitespace of the line holding pos, and only that:
// for a position that follows content on its line (a bracket after `"a": `),
// the bytes between the line start and pos are content, not indentation.
func LineIndent(src []byte, pos int) string {
	i := pos
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	j := i
	for j < pos && (src[j] == ' ' || src[j] == '\t') {
		j++
	}
	return string(src[i:j])
}

// IndentUnit is the file's own indent step: the leading whitespace of its first
// indented line that carries content, or two spaces when no line is indented.
func IndentUnit(src []byte) string {
	for _, line := range strings.Split(string(src), "\n") {
		body := strings.TrimLeft(line, " \t")
		if body != "" && body != "\r" && len(body) < len(line) {
			return line[:len(line)-len(body)]
		}
	}
	return "  "
}
