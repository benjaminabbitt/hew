// Package hewdiff is the differ's format dispatch (§9.4, Appendix A.5/A.6):
// it parses both sides with the binding that owns the format and hands the two
// trees to hew.DiffTrees.
//
// It sits above the core for the same reason the applier dispatch does — the
// core imports no format library — and BELOW the CLI, because §9.4's first
// rule is that the differ's inputs are pure content: no repository, no
// revision, no subprocess. Descriptor resolution (`HEAD:config.yaml`) lives in
// the CLI layer (§9.5, Appendix A.7) and nothing here can reach it.
//
// It names no format package either: the binding arrives from the registry
// (Appendix A.6), so which differs exist is decided by what the program imports
// for effect — `_ ext/all`, or exactly the formats it edits.
package hewdiff

import (
	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Diff computes the transform list that turns old into new (§9.4).
// Deterministic: the same (old, new, format, opt) yields the same list.
//
// An empty list is a legitimate result — the two documents are the same —
// and is not an error; Appendix B.3 makes "a patch was produced, and it may be
// empty" exit 0.
func Diff(old, new []byte, format hew.FormatID, opt hew.DiffOptions) (hew.TransformList, error) {
	build, err := treeBuilder(format, opt.Target)
	if err != nil {
		return hew.TransformList{}, err
	}
	oldTree, err := build(old)
	if err != nil {
		return hew.TransformList{}, withTarget(err, opt.Target)
	}
	newTree, err := build(new)
	if err != nil {
		return hew.TransformList{}, withTarget(err, opt.Target)
	}
	return hew.DiffTrees(oldTree, newTree, format, opt)
}

// treeBuilder asks the registry for the format's diff half (Appendix A.6). The
// binding it returns is the same one the applier came from, which is what
// §9.4's "the differ and the applier are format-side inverses" requires and
// what a switch per call site could only promise.
func treeBuilder(format hew.FormatID, target string) (hew.Differ, error) {
	if format == "" {
		return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentDiffer, Target: target,
			Detail: "no format declared and none inferred from the source's extension (§8.0)"}
	}
	b, ok := hew.Lookup(format)
	if !ok || b.Differ == nil {
		return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentDiffer, Target: target,
			Detail: "no differ for format " + string(format) + " (P4)"}
	}
	return b.Differ, nil
}

func withTarget(err error, target string) error {
	if he, ok := hewerr.As(err); ok && he.Target == "" {
		he.Target = target
	}
	return err
}
