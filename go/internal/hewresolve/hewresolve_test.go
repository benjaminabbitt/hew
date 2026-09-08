package hewresolve

import (
	"testing"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

func TestNoMatch_CarriesTheNoMatchCode(t *testing.T) {
	e := NoMatch("no key %q", "timeout")
	if e.Code != hewerr.CodeNoMatch {
		t.Fatalf("code = %v, want %v", e.Code, hewerr.CodeNoMatch)
	}
	if e.Error() != `no key "timeout"` {
		t.Fatalf("detail = %q", e.Error())
	}
	if e.Final {
		t.Fatal("an ordinary no-match is not final; the asking transform may report it in its own terms")
	}
}

func TestAmbiguous_CarriesTheAmbiguousCode(t *testing.T) {
	e := Ambiguous("%d elements match", 3)
	if e.Code != hewerr.CodeAmbiguousMatch {
		t.Fatalf("code = %v, want %v", e.Code, hewerr.CodeAmbiguousMatch)
	}
	if e.Error() != "3 elements match" {
		t.Fatalf("detail = %q", e.Error())
	}
}

// A detail that already contains a percent sign must survive being carried,
// which is what the "%s" spelling at the call sites is for: pre-formatted
// explanations from the locator are DATA, not a format string.
func TestErr_PreformattedDetailIsNotReinterpreted(t *testing.T) {
	e := NoMatch("%s", "100% of candidates were rejected")
	if e.Error() != "100% of candidates were rejected" {
		t.Fatalf("detail = %q", e.Error())
	}
}

// Err satisfies error, so a binding can still hand it to anything taking one —
// the point of the shared type is that a STEP's signature names it exactly.
func TestErr_IsAnError(t *testing.T) {
	var err error = NoMatch("x")
	if err.Error() != "x" {
		t.Fatalf("Error() = %q", err.Error())
	}
}
