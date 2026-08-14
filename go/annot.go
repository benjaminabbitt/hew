package hew

import (
	"strconv"
	"strings"
)

// The §7 annotation grammar. Annotation lines carry margin `?` (an assertion,
// which can fail) or `!` (a directive, which changes how application works
// and cannot itself fail). They are the second half of the notation: the
// mirror body says what the shape is, and the annotations say what must be
// true about it and which of several identical-looking nodes is meant.
const (
	verbExpect     = "expect"
	verbAbsent     = "absent"
	verbExhaustive = "exhaustive"
	verbCount      = "count"
	verbKind       = "kind"

	verbAnchor     = "anchor"
	verbSurface    = "surface"
	verbOptional   = "optional"
	verbIdempotent = "idempotent"
	verbStrict     = "strict"
	verbUpsert     = "upsert"
	verbDefault    = "default"
)

// annotClass is §7's attachment class.
type annotClass uint8

const (
	// annotFree carries its own path and attaches to nothing.
	annotFree annotClass = iota
	// annotContainer governs the container whose children are at this
	// indentation.
	annotContainer
	// annotLine governs the immediately following body line, or the anchor
	// when it is the hunk's first body line.
	annotLine
)

func classifyAnnot(a annotation) (annotClass, error) {
	if a.margin == marginAssert {
		switch a.verb {
		case verbExpect, verbAbsent, verbCount, verbKind:
			return annotFree, nil
		case verbExhaustive:
			return annotContainer, nil
		}
		return 0, parseErr(a.line, "", "unknown assertion %q; §7.1 defines expect, absent, exhaustive, count and kind", a.verb)
	}
	switch a.verb {
	case verbSurface:
		return annotContainer, nil
	case verbAnchor, verbOptional, verbIdempotent, verbStrict, verbUpsert, verbDefault:
		return annotLine, nil
	}
	return 0, parseErr(a.line, "", "unknown directive %q (§7)", a.verb)
}

// applyDirective folds one `!` directive onto the qualifier set it governs
// (§9.1 step 6: directives emit no transform of their own, they ride the
// affected transform as qualifiers).
func applyDirective(a annotation, q *quals) error {
	switch a.verb {
	case verbAnchor:
		mode, err := oneArg(a)
		if err != nil {
			return err
		}
		switch AnchorMode(mode) {
		case AnchorRewrite, AnchorFork:
			q.anchor = AnchorMode(mode)
		default:
			return parseErr(a.line, "", "`! anchor` takes %q or %q, got %q (§8.3)", AnchorRewrite, AnchorFork, mode)
		}
	case verbOptional:
		if err := noArgs(a); err != nil {
			return err
		}
		q.optional = true
	case verbIdempotent:
		if err := noArgs(a); err != nil {
			return err
		}
		q.idempotent, q.strict = true, false
	case verbStrict:
		if err := noArgs(a); err != nil {
			return err
		}
		q.strict, q.idempotent = true, false
	case verbUpsert:
		if err := noArgs(a); err != nil {
			return err
		}
		q.onConflict = ConflictReplace
	case verbDefault:
		if err := noArgs(a); err != nil {
			return err
		}
		q.onConflict = ConflictKeep
	}
	return nil
}

// parseSurface reads `! surface table|dotted` (§8.4 rule 4).
func parseSurface(a annotation) (Surface, error) {
	arg, err := oneArg(a)
	if err != nil {
		return "", err
	}
	switch Surface(arg) {
	case SurfaceTable, SurfaceDotted:
		return Surface(arg), nil
	}
	return "", parseErr(a.line, "", "`! surface` takes %q or %q, got %q (§8.4)", SurfaceTable, SurfaceDotted, arg)
}

// splitAssertion reads `<path> = <value>` from an assertion's arguments. The
// path is one token — a value may hold spaces, a path may not unless quoted
// — and the rest, after the `=`, is the value text.
func splitAssertion(a annotation) (string, string, error) {
	target, rest := cutToken(a.rest)
	rest = strings.TrimLeft(rest, " \t")
	if target == "" || !strings.HasPrefix(rest, "=") {
		return "", "", parseErr(a.line, "", "`? %s` takes `<path> = <value>`, got %q (§7.1)", a.verb, a.rest)
	}
	val := strings.TrimSpace(rest[1:])
	if val == "" {
		return "", "", parseErr(a.line, "", "`? %s` takes `<path> = <value>`, got %q (§7.1)", a.verb, a.rest)
	}
	return target, val, nil
}

func parseCount(text string, line int) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return 0, parseErr(line, "", "`? count` takes a non-negative integer, got %q (§7.1)", text)
	}
	return n, nil
}

func oneArg(a annotation) (string, error) {
	toks, err := splitTokens(a.rest)
	if err != nil || len(toks) != 1 {
		return "", parseErr(a.line, "", "`! %s` takes exactly one argument, got %q (§7)", a.verb, a.rest)
	}
	return toks[0], nil
}

func noArgs(a annotation) error {
	if strings.TrimSpace(a.rest) != "" {
		return parseErr(a.line, "", "`! %s` takes no arguments, got %q (§7)", a.verb, a.rest)
	}
	return nil
}

// cutToken splits off the first whitespace-delimited token, keeping a quoted
// run whole: an assertion's path is one token, and a key-match segment may
// quote a value that contains spaces (§4.2's `id="a b"`).
func cutToken(s string) (string, string) {
	s = strings.TrimLeft(s, " \t")
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			if c == '\\' {
				i++ // an escaped quote does not close the run
			} else if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
		case c == ' ' || c == '\t':
			return s[:i], s[i:]
		}
	}
	return s, ""
}
