package harness

import (
	"bytes"
	"fmt"
	"strings"
)

// DiffBytes compares want and got with ZERO normalization (runner obligation
// 3: no whitespace folding, no trailing-newline tolerance). It returns "" on
// equality; otherwise a report designed so trailing-newline and CRLF bugs are
// legible from the failure message alone: lengths, first-divergence offset
// with line/column, whitespace-visible excerpts, and a short hex window.
func DiffBytes(want, got []byte) string {
	if bytes.Equal(want, got) {
		return ""
	}
	n := min(len(want), len(got))
	off := 0
	for off < n && want[off] == got[off] {
		off++
	}
	line, col := 1, 1
	for _, b := range want[:min(off, len(want))] {
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "byte mismatch: want %d bytes, got %d bytes; first divergence at offset %d (line %d, col %d)\n", len(want), len(got), off, line, col)
	fmt.Fprintf(&b, "  want: %s\n", excerpt(want, off))
	fmt.Fprintf(&b, "  got:  %s\n", excerpt(got, off))
	fmt.Fprintf(&b, "  want hex: % x\n", window(want, off, 16))
	fmt.Fprintf(&b, "  got hex:  % x", window(got, off, 16))
	return b.String()
}

// excerpt renders the line containing off with whitespace made visible.
func excerpt(b []byte, off int) string {
	if off > len(b) {
		off = len(b)
	}
	start := off
	for start > 0 && b[start-1] != '\n' {
		start--
	}
	end := off
	for end < len(b) && b[end] != '\n' && end-start < 120 {
		end++
	}
	if end < len(b) && b[end] == '\n' {
		end++ // include the newline so it shows as ␊
	}
	if start == end {
		return "<end of content>"
	}
	return visible(b[start:end])
}

func visible(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		switch c {
		case '\t':
			s.WriteString("␉")
		case '\n':
			s.WriteString("␊")
		case '\r':
			s.WriteString("␍")
		case ' ':
			s.WriteString("·")
		default:
			s.WriteByte(c)
		}
	}
	return s.String()
}

func window(b []byte, off, size int) []byte {
	if off >= len(b) {
		if len(b) == 0 {
			return nil
		}
		off = len(b) - 1
	}
	end := off + size
	if end > len(b) {
		end = len(b)
	}
	return b[off:end]
}
