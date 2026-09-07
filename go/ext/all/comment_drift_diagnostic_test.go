package all

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Removing a comment whose text is NOT in the target is DRIFT, not a
// re-application, and the diagnostic has to say so.
//
// Every other address converges here for a good reason: `remove /k` against a
// document with no /k means someone already removed it, because a key's
// identity survives a change to its value, so its absence really is evidence
// of prior application. A COMMENT's identity IS its text (§4.5b). "No comment
// reading X" is therefore equally consistent with the comment having been
// EDITED, or with X never having been there — the one thing it is not is proof
// that this patch already ran.
//
// Reporting HEW011 "already applied ... add ! idempotent" for that sends the
// reader to an annotation which would then silently accept a document the
// patch was never written against — the near-miss the whole comment-identity
// change exists to stop. It is also inconsistent: a paired REPLACE over the
// same drift already reports HEW010.
func TestRemovingAMissingCommentIsDriftNotAlreadyApplied(t *testing.T) {
	for _, c := range []struct{ name, format, doc string }{
		{"yaml", "yaml", "a:\n  # actual\n\n  x: 1\n"},
		{"toml", "toml", "[a]\n# actual\n\nx = 1\n"},
		{"jsonc", "jsonc", "{\n  \"a\": {\n    // actual\n\n    \"x\": 1\n  }\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, ok := hew.Lookup(hew.FormatID(c.format))
			if !ok {
				t.Fatalf("no binding for %s", c.format)
			}
			addr := hew.NewPath(hew.Key("a"), hew.Comment("expected"))
			out, aerr := b.Applier([]byte(c.doc), hew.TransformList{
				Target: "t", Format: hew.FormatID(c.format),
				Transform: []hew.Transform{
					{Op: hew.OpTest, Path: addr, Value: hew.CommentValue("expected")},
					{Op: hew.OpRemove, Path: addr},
				},
			})
			if aerr == nil {
				t.Fatalf("drift was accepted: %q", string(out))
			}
			he, ok := hewerr.As(aerr)
			if !ok {
				t.Fatalf("not a *hewerr.Error: %v", aerr)
			}
			if he.Code != hewerr.CodeStaleTarget {
				t.Fatalf("code: want HEW010 stale-target, got %s (%v)", he.Code, he)
			}
			if strings.Contains(aerr.Error(), "already applied") {
				t.Fatalf("drift must not be reported as a re-application: %v", aerr)
			}
		})
	}
}

// The convergence that IS real still works: with `! idempotent`, re-running a
// comment removal against a document it already ran on succeeds with no edits.
func TestIdempotentCommentRemovalStillConverges(t *testing.T) {
	for _, c := range []struct{ name, format, doc string }{
		{"yaml", "yaml", "a:\n  x: 1\n"},
		{"toml", "toml", "[a]\nx = 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, _ := hew.Lookup(hew.FormatID(c.format))
			addr := hew.NewPath(hew.Key("a"), hew.Comment("gone"))
			out, aerr := b.Applier([]byte(c.doc), hew.TransformList{
				Target: "t", Format: hew.FormatID(c.format),
				Transform: []hew.Transform{
					{Op: hew.OpTest, Path: addr, Value: hew.CommentValue("gone"), Idempotent: true},
					{Op: hew.OpRemove, Path: addr, Idempotent: true},
				},
			})
			if aerr != nil {
				t.Fatalf("an idempotent re-run must converge: %v", aerr)
			}
			if got := string(out); got != c.doc {
				t.Fatalf("converged run must not edit: got %q want %q", got, c.doc)
			}
		})
	}
}
