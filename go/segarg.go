package hew

import (
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// --- typed holes (Appendix A.0, O43) -----------------------------------------
//
// SegmentArg is how a runtime value gets into a path. It is a SEALED
// interface — its method is unexported, so it is implementable only inside
// this module and a caller cannot supply something that pretends to be a
// segment — and its constructors are the typed ones below.
//
// The invariant the whole design exists for:
//
//	User data supplied through a typed constructor is NEVER parsed as path
//	text. It enters the path as struct data, so there is no escaping step to
//	get wrong and no injection channel — a value cannot introduce a segment
//	boundary, a key-match, an ordinal or an optional marker, because by the
//	time it exists it is already one segment and the grammar has already been
//	applied to everything around it.
//
// The precedent is database/sql: parameters travel out-of-band from the
// statement text, and a placeholder is not a paste site. Two alternatives are
// recorded rejected in A.0 — printf-style escaping (an escaper cannot know
// whether its argument stands for a key, a label, a match field or a match
// value, and those escape differently) and concatenation detection (false
// positives on legitimately computed paths train the reader to route around
// the warning). String concatenation into At is a DEFECT, stated plainly
// rather than guarded against: At("/deps/" + pkg) is broken for "@scope/pkg",
// for "8080" and for "-".
//
// Soundness depends on O41: a structural segment holding hostile data is safe
// in memory for free, but the path is later written into a .hew, a .hewt or an
// error message and read back, so canonical rendering's bijection is the other
// half of this mechanism.
type SegmentArg interface {
	// segmentArg returns the segment this argument stands for. It is
	// unexported: that is the seal.
	segmentArg() Segment
}

// A Segment is its own SegmentArg. The constructors below are the typed entry
// points a caller should reach for — they are what make the comparison's type
// visible at the construction site — but the model underneath is the same one
// the parser and the differ build, and a path assembled from raw segments is
// no less structural than one assembled from constructors.
func (s Segment) segmentArg() Segment { return s }

// Key is an object member name (§4.1). The string is opaque data: Key("a/b"),
// Key("8080") and Key("{}") each name exactly one member, spelled exactly
// that way.
func Key(name string) SegmentArg { return Segment{Kind: SegKey, Name: name} }

// Index is a sequence index (§4.1).
func Index(i int) SegmentArg { return Segment{Kind: SegIndex, Index: i} }

// Append is §4.1's "-", the position one past the last element.
func Append() SegmentArg { return Segment{Kind: SegAppend} }

// MatchKey is a key-match segment (§4.2) comparing a field against a STRING.
// It always produces a quoted string scalar (O42), so `MatchKey("port",
// "8080")` addresses the string and not the number — and deliberately does not
// take an `any` and guess, because a guess would address a different node than
// the one the caller named, with no error anywhere.
func MatchKey(field, value string) SegmentArg {
	return Segment{Kind: SegMatch, Name: field, Value: Scalar{Kind: ScalarString, Text: value, Quoted: true}}
}

// MatchKeyNumber compares a field against a NUMBER, written as its literal.
func MatchKeyNumber(field, literal string) SegmentArg {
	return Segment{Kind: SegMatch, Name: field, Value: Scalar{Kind: ScalarNumber, Text: literal}}
}

// MatchKeyBool compares a field against a boolean.
func MatchKeyBool(field string, v bool) SegmentArg {
	return Segment{Kind: SegMatch, Name: field, Value: Scalar{Kind: ScalarBool, Text: strconv.FormatBool(v)}}
}

// MatchKeyNull compares a field against null.
func MatchKeyNull(field string) SegmentArg {
	return Segment{Kind: SegMatch, Name: field, Value: Scalar{Kind: ScalarNull, Text: "null"}}
}

// MatchValue is §4.2's `=value` form: it compares the ELEMENT itself against a
// string, and quotes for the same reason MatchKey does.
func MatchValue(value string) SegmentArg {
	return Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarString, Text: value, Quoted: true}}
}

// MatchValueNumber is `=value` against a number literal.
func MatchValueNumber(literal string) SegmentArg {
	return Segment{Kind: SegMatch, Value: Scalar{Kind: ScalarNumber, Text: literal}}
}

// Quoted is the double-quoted segment form (§4.1): a key said literally. It is
// the permanent escape hatch for any spelling the bare grammar cannot carry —
// `/deps/"@scope/pkg"`, a digit-only key, the empty key.
func Quoted(s string) SegmentArg { return Segment{Kind: SegKey, Name: s, Quoted: true} }

// Comment addresses a container's n'th comment child (§4.5b).
func Comment(ord int) SegmentArg { return Segment{Kind: SegComment, Index: ord} }

// TrailingComment is §4.5b's "#t", the comment on a node's own line.
func TrailingComment() SegmentArg { return Segment{Kind: SegComment, Trailing: true} }

// Optional is §4.4's trailing "?" — match it, or create it. Legal only on a
// path's last segment.
func Optional(s SegmentArg) SegmentArg {
	seg := s.segmentArg()
	seg.Optional = true
	return seg
}

// NewPath builds an absolute path from typed segment arguments (A.0, review
// point 20). NewPath() is RootPath.
//
// This is the constructor for an address that is programmatic all the way
// down — built in a loop, stored, or assembled from more parts than a pattern
// reads well with. There is ONE constructor set, not two: the same arguments
// fill At's holes.
func NewPath(args ...SegmentArg) Path {
	segs := make([]Segment, len(args))
	for i, a := range args {
		segs[i] = a.segmentArg()
	}
	return Path{origin: originAbsolute, segs: segs}
}

// --- the pattern language: §4 plus holes -------------------------------------

// hole is the placeholder At recognizes. It is a whole SEGMENT: "{}" inside a
// larger token, and the quoted form `"{}"`, are the literal key §4 already
// spells that way.
const hole = "{}"

// holeMark is the sentinel At swaps a hole for while the PATTERN — and only
// the pattern — goes through the §4 grammar. It is hew's own token, not the
// caller's data: no argument is ever spliced into this text, which is the
// whole point. A pattern carrying a NUL is refused before this can collide.
const holeMark = "\x00"

func patternErr(pattern, detail string) error {
	return &hewerr.Error{
		Code:      hewerr.CodeParse,
		Component: hewerr.ComponentParser,
		Path:      pattern,
		Detail:    detail,
	}
}

// buildPath parses pattern as §4-plus-holes and fills each hole with the next
// argument, positionally.
//
// The parse is ParsePath's, over a pattern in which every hole has become a
// sentinel key: the grammar lives in one place, and the arguments never touch
// text. Filling is a structural swap afterwards.
func buildPath(format FormatID, pattern string, args []SegmentArg) (Path, error) {
	if strings.Contains(pattern, holeMark) {
		return Path{}, patternErr(pattern, "pattern contains a NUL byte")
	}
	parts := strings.Split(pattern, "/")
	holes := 0
	for i, part := range parts {
		if part == hole {
			parts[i] = holeMark
			holes++
		}
	}
	if holes != len(args) {
		return Path{}, patternErr(pattern, "pattern has "+strconv.Itoa(holes)+" hole(s) and "+
			strconv.Itoa(len(args))+" argument(s); a mismatch is an error, not a partial path")
	}
	// Scoped to the document's own format (O46): a segment form another
	// extension claims is not this document's to read that way.
	p, err := ParsePathIn(format, strings.Join(parts, "/"))
	if err != nil {
		return Path{}, patternErr(pattern, detailOf(err))
	}
	// Fill positionally: the skeleton's sentinel keys are in pattern order,
	// and nothing else can be one, because the pattern was NUL-free.
	segs, filled := p.Segments(), 0
	for i, s := range segs {
		if s.Kind == SegKey && s.Name == holeMark {
			segs[i] = args[filled].segmentArg()
			filled++
		}
	}
	if filled != len(args) {
		return Path{}, patternErr(pattern, "pattern's holes did not survive parsing")
	}
	// §4.4 again, because a hole can carry the flag the pattern could not:
	// ParsePath enforced this over the skeleton, and the fill may have moved
	// an Optional off the end.
	for i, s := range segs {
		if s.Optional && i != len(segs)-1 {
			return Path{}, patternErr(pattern, `trailing "?" is legal only on the last segment (§4.4)`)
		}
	}
	return Path{origin: p.origin, segs: segs}, nil
}
