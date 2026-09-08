// Package hewcomment resolves a comment address against a container's
// comments.
//
// A comment is a KEYLESS member, so §4.5b addresses it by the digest of its own
// text and identical comments collide exactly as identical set members do —
// resolved by the same scored locator rather than by a rule of their own. None
// of that is format-specific: the digest is over the comparison form, which is
// the marker-stripped text, so `// note` in JSONC and `# note` in YAML are the
// same comment and hash alike.
//
// What stays with the binding is everything structural — which node carries a
// trailing comment, what a comment node IS — so this package takes the texts
// and returns an index into them.
package hewcomment

import (
	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
	"github.com/benjaminabbitt/hew/go/internal/hewresolve"
)

// Pick returns the index of the comment seg names among texts, in document
// order, or the failure to report.
//
// A collision is not resolved by position alone: the advisory disambiguates
// duplicates, and when it cannot the answer is a REFUSAL rather than a guess,
// carrying the locator's own explanation. That refusal is Final — the step
// decided the question, and a transform reinterpreting it as its own drift
// error would replace a precise diagnostic with a vaguer one.
func Pick(seg hew.Segment, texts []string, adv hew.Advisory) (int, *hewresolve.Err) {
	if seg.Hash == "" {
		// The ordinal is gone (§4.5b) and the parser refuses it, so this is
		// only reachable from a Segment built in code. Refusing beats falling
		// back to an index, which would resolve position 0 for every such
		// segment — silently addressing the wrong comment.
		return -1, hewresolve.NoMatch(
			"a comment is addressed by the digest of its text, `#hew:comment=<hex>`")
	}
	var cands []hew.Candidate
	for i, text := range texts {
		if seg.MatchesComment(text) {
			cands = append(cands, hew.Candidate{Index: i})
		}
	}
	if len(cands) == 0 {
		return -1, hewresolve.NoMatch("no comment matches %s", seg.String())
	}
	pick := hew.Locate(cands, len(texts), adv)
	if !pick.OK {
		return -1, &hewresolve.Err{
			Code:   hewerr.CodeAmbiguousMatch,
			Final:  true,
			Detail: pick.Explain(seg),
		}
	}
	return pick.Index, nil
}
