package json

import (
	hew "github.com/benjaminabbitt/hew/go"
)

// init registers the JSON binding (Appendix A.6, O35). Importing this package
// — for its symbols or blank, for effect — is what makes a build able to
// detect, apply and diff JSON; nothing else has to be told.
func init() {
	hew.Register(hew.FormatJSON, hew.Binding{
		Applier:  Apply,
		Differ:   DiffTree,
		Document: Document,
		Detect: hew.DetectRule{
			// §8.0's shipped default. It is also the default for .json files
			// known to forbid comments, package.json among them: those are
			// plain JSON, and it is the JSONC-by-convention names that are the
			// exception (see ext/jsonc).
			Extensions: []string{".json"},
		},
	})
}
