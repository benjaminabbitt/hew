package jsonc

import (
	"fmt"
	"github.com/benjaminabbitt/hew/go/internal/hewsplice"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewapply"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// binding is this format's variant of the shared apply loop (hewapply): the
// loop owns the SEQUENCE — reparse per transform, hints neither assert nor
// edit, an empty plan is a no-op — and everything below is what JSONC itself
// answers.
type binding struct{}

func (binding) FormatName() string { return "JSONC" }

// Unsupported has nothing to refuse: this binding implements every qualifier
// §9.3 defines for it, so pass 0 passes.
func (binding) Unsupported(string, hew.Transform) error { return nil }

func (binding) NewRun(src []byte, ctx hewapply.RunContext) (hewapply.Run, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, err
	}
	d.adv = ctx.Adv
	return docRun{d: d, target: ctx.Target}, nil
}

// docRun carries the target alongside the document because this binding's
// evaluators take it per call rather than holding it.
type docRun struct {
	d      *doc
	target string
}

func (r docRun) EvalTest(t hew.Transform) error { return r.d.evalTest(r.target, t) }

func (r docRun) PlanOne(t hew.Transform) ([]hewsplice.Edit, error) {
	return r.d.plan(r.target, t)
}

func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	return hewapply.Apply(target, tl, binding{})
}

// plan computes one mutating transform's byte edits against the current
// document. A nil edit list is a transform satisfied with no change at all
// (`! default` over an existing key, `! idempotent` over a matching value,
// `! optional` over an absent one — §7.5, §7.6, §7.7).
func (d *doc) plan(target string, t hew.Transform) ([]edit, error) {
	switch t.Op {
	case hew.OpAdd:
		return d.planAdd(target, t)
	case hew.OpRemove:
		return d.planRemove(target, t)
	case hew.OpReplace:
		return d.planReplace(target, t)
	case hew.OpCopy:
		return d.planCopy(target, t)
	default:
		return nil, &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentApplier,
			Target: target, Path: t.Path.String(), Detail: fmt.Sprintf("unsupported op %q", t.Op)}
	}
}
