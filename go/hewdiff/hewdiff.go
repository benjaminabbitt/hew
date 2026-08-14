// Package hewdiff is the differ's format dispatch (§9.4, Appendix A.5/A.6):
// it parses both sides with the binding that owns the format and hands the two
// trees to hew.DiffTrees.
//
// It sits above the core for the same reason the applier dispatch does — the
// core imports no format library — and BELOW the CLI, because §9.4's first
// rule is that the differ's inputs are pure content: no repository, no
// revision, no subprocess. Descriptor resolution (`HEAD:config.yaml`) lives in
// the CLI layer (§9.5, Appendix A.7) and nothing here can reach it.
package hewdiff

import (
	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/hewhcl"
	"github.com/benjaminabbitt/hew/go/hewjson"
	"github.com/benjaminabbitt/hew/go/hewjsonc"
	"github.com/benjaminabbitt/hew/go/hewtoml"
	"github.com/benjaminabbitt/hew/go/hewyaml"
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

func treeBuilder(format hew.FormatID, target string) (func([]byte) (*hew.DiffNode, error), error) {
	switch format {
	case hew.FormatJSON:
		return hewjson.DiffTree, nil
	case hew.FormatJSONC:
		return hewjsonc.DiffTree, nil
	case hew.FormatYAML:
		return hewyaml.DiffTree, nil
	case hew.FormatTOML:
		return hewtoml.DiffTree, nil
	case hew.FormatHCL:
		return hewhcl.DiffTree, nil
	case "":
		return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentDiffer, Target: target,
			Detail: "no format declared and none inferred from the source's extension (§8.0)"}
	default:
		return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentDiffer, Target: target,
			Detail: "no differ for format " + string(format) + " (P4)"}
	}
}

func withTarget(err error, target string) error {
	if he, ok := hewerr.As(err); ok && he.Target == "" {
		he.Target = target
	}
	return err
}
