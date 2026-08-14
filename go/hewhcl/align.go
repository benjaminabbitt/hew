package hewhcl

import (
	"strings"

	"github.com/hew-format/hew/internal/hewerr"
)

// §8.5's alignment rule: "`hclwrite` re-aligns `=` within a body. Hew adopts
// the backend's alignment on any body it modified, and leaves untouched bodies
// byte-identical. The corpus pins this."
//
// That is a MIXED rule, and it is why this binding does not hand the document
// to hclwrite: hclwrite's emitter formats the whole token stream on Bytes(),
// so adopting its alignment for one body means adopting its formatting for
// every body — the half of the rule that says untouched bodies stay
// byte-identical would be lost. Instead the alignment hclfmt would produce is
// computed here and applied, as ordinary splices, only to the bodies hew
// actually modified.
//
// The rule reproduced is hclfmt's: within a run of consecutive attribute
// lines, the `=` signs line up one column past the widest name. A run is
// broken by anything that is not an attribute line — a nested block, a blank
// line, a comment on its own line.

// realign re-runs alignment over every modified body of the spliced output.
// It re-parses rather than tracking spans through the splice, because the
// alignment of a body is a property of the body's FINAL text — including the
// members this run just added, which is what makes hcl/repeated-label-ordinal
// widen `alias  =` to `alias   =` when `profile` arrives.
func (r *run) realign(out []byte, marks []mark) ([]byte, error) {
	if len(r.touched) == 0 {
		return out, nil
	}
	nd, err := parseDoc(out)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: r.target, Detail: "the spliced result does not parse as HCL: " + err.Error()}
	}
	var edits []edit
	for _, open := range r.touched {
		b := nd.root
		if open != rootBody {
			b = nd.findBody(shift(marks, open))
		}
		if b == nil {
			continue
		}
		edits = append(edits, nd.alignEdits(b)...)
	}
	res, _, err := applyEdits(out, edits)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// alignEdits computes the padding rewrites one body needs.
func (d *doc) alignEdits(b *bodyNode) []edit {
	var edits []edit
	var group []*itemNode
	flush := func() {
		edits = append(edits, d.alignGroup(group)...)
		group = nil
	}
	for _, it := range b.items {
		if it.kind != itemAttr {
			flush()
			continue
		}
		if len(group) > 0 && group[len(group)-1].endLine+1 != it.startLine {
			flush()
		}
		group = append(group, it)
	}
	flush()
	return edits
}

func (d *doc) alignGroup(group []*itemNode) []edit {
	if len(group) == 0 {
		return nil
	}
	width := 0
	for _, it := range group {
		if len(it.name) > width {
			width = len(it.name)
		}
	}
	var edits []edit
	for _, it := range group {
		want := strings.Repeat(" ", width-len(it.name)+1)
		got := string(d.src[it.nameEnd:it.eqStart])
		if got == want || strings.TrimLeft(got, " ") != "" {
			// Already aligned, or something other than plain spaces sits
			// between the name and its `=`; either way, leave the bytes alone.
			continue
		}
		edits = append(edits, edit{start: it.nameEnd, end: it.eqStart, text: want})
	}
	return edits
}
