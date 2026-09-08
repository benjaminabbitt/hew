// Package hewresolve carries the failure of one path-resolution step.
//
// Resolving a path is the same job in every binding — walk the segments,
// and when one does not resolve, say which HEW0xx code it failed with and
// why — so the failure it produces is one type rather than four. The four had
// already drifted into two shapes: TOML and YAML classified with a
// hewerr.Code and a finality flag, while JSON and JSONC carried a single
// `ambiguous bool`, which is that code with only two of its values reachable.
//
// The bindings also disagreed on how to RETURN it. Two returned the concrete
// type and two returned a bare `error` that the caller immediately asserted
// back with `err.(*resolveErr)` — an unchecked assertion that turns any future
// error from a step into a panic. A step's failure is always this type, so the
// signatures say so and the assertions are gone.
package hewresolve

import (
	"fmt"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Err classifies one step's failure.
type Err struct {
	// Code is the HEW0xx the failure carries: CodeNoMatch when the step found
	// nothing, CodeAmbiguousMatch when it found more than one.
	Code hewerr.Code
	// Detail is the human half of the diagnostic, already formatted.
	Detail string
	// Final marks a code the CALLER MUST NOT REINTERPRET. Most step failures
	// are reported by the transform that asked, in its own terms; a final one
	// was decided at the step that raised it (HEW012, HEW040, the merge-key
	// HEW013) and reinterpreting it would replace a precise diagnostic with a
	// vaguer one from further away.
	Final bool
}

func (e *Err) Error() string { return e.Detail }

// NoMatch is the ordinary failure: the step resolved to nothing.
func NoMatch(format string, args ...any) *Err {
	return &Err{Code: hewerr.CodeNoMatch, Detail: fmt.Sprintf(format, args...)}
}

// Ambiguous is the other one: the step resolved to more than one candidate and
// refuses rather than picking. Not final by default — a transform may still
// report it in its own terms — so a step that has decided the question sets
// Final itself.
func Ambiguous(format string, args ...any) *Err {
	return &Err{Code: hewerr.CodeAmbiguousMatch, Detail: fmt.Sprintf(format, args...)}
}
