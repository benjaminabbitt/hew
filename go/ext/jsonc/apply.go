package jsonc

import (
	"fmt"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Apply is the JSONC binding's apply half (§8.2, Appendix A.4's Applier.Apply
// for the "jsonc" format). SEQUENTIAL RESOLUTION (§9.2, §9.3, human ruling):
// every transform — `test` included — is resolved and evaluated (or applied)
// against the document AS MODIFIED BY every transform before it, one at a
// time, in list order. There is no longer a fixed "every test against the
// untouched document" pass; a `test` placed after an earlier write in the
// same list sees that write, exactly as an `add` placed after one already
// did (this binding was already reparsing before each MUTATION — see the
// history below — it just was not yet doing so for `test`).
//
// Every transform is planned against the document the transform before it
// produced. That sequencing is not an implementation convenience: a JSONC
// patch may place a member relative to a comment the same patch just added
// (jsonc/add-with-leading-comment lowers to `add /#0 after /port` followed by
// `add /telemetry after /#0`), and the second transform's anchor simply does
// not exist in the bytes the first one was planned against.
//
// Everything happens against an in-memory byte buffer that is only ever
// returned once every transform has succeeded (§10.5's all-or-nothing): an
// error at any step discards the buffer and returns nil bytes.
//
// Byte preservation is structural, not textual: every untouched byte range of
// the source is copied verbatim, and an edit's own replacement text is the
// only place new bytes appear (§6.3) — so indentation, quoting style, numeric
// literal form and every comment outside the edit survive exactly.
func Apply(target []byte, tl hew.TransformList) ([]byte, error) {
	cur := target
	for _, t := range tl.Transform {
		d, err := parseDoc(cur)
		if err != nil {
			return nil, targetParseErr(tl.Target, err)
		}
		if t.Op == hew.OpTest {
			if err := d.evalTest(tl.Target, t); err != nil {
				return nil, err
			}
			continue
		}
		edits, err := d.plan(tl.Target, t)
		if err != nil {
			return nil, err
		}
		if len(edits) == 0 {
			continue
		}
		cur, err = applyEdits(cur, edits)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
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

func targetParseErr(target string, err error) error {
	return &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
		Target: target, Detail: "target does not parse as JSONC: " + err.Error()}
}
