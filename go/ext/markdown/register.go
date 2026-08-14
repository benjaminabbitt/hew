// Package markdown is hew's Markdown extension (§8.6): the prose-document
// family's vocabulary, detection rule and declared node kinds.
//
// It is deliberately a partial binding. §8.7's evaluation of the Hew dialect
// against a plain unified diff is an OPEN question (O29), so this package
// registers no applier and no differ: a Markdown target detects, and a Markdown
// patch parses, and applying one is HEW021 from the seam that needed the half
// that is not there. That is the same answer the hand-rolled dispatch gave
// before the registry existed, and it is now stated as data rather than as a
// missing switch case.
//
// The package is also what makes O29's severability concrete. §8.8 moved every
// Markdown-specific construct out of the core, so dropping the family is
// deleting this directory rather than auditing a claim.
package markdown

import (
	hew "github.com/benjaminabbitt/hew/go"
)

// The node kinds Markdown declares (§8.6, OP-28). `section` is a heading's
// span and `block` is one prose block within it; neither means anything in a
// config format, which is exactly why the core's NodeKind enum no longer names
// them (§8.8). The `.hewt` spellings are unchanged.
const (
	KindSection hew.NodeKind = "section"
	KindBlock   hew.NodeKind = "block"
)

func init() {
	hew.Register(hew.FormatMarkdown, hew.Binding{
		// Applier and Differ are nil: see the package comment.
		Detect: hew.DetectRule{
			Extensions: []string{".md", ".markdown"},
		},
		Kinds: []hew.NodeKind{KindSection, KindBlock},
	})
}
