package hewhcl

import (
	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// DiffTree parses source bytes into the format-neutral tree hew.DiffTrees
// walks (§9.4), over the SAME span-tracked parse Apply splices against.
//
// Two things about HCL shape the projection.
//
// A block's ADDRESS is its `(type, labels)` tuple (§4.3), not a member name,
// so `provider "google" { … }` becomes a `provider` child holding one LABEL
// child per label step. That is what makes the differ anchor a hunk at
// /provider/"google" — the address the applier resolves — rather than at
// /provider, which names every provider block in the file.
//
// An attribute's VALUE is an expression, and §8.5 compares expressions as
// source text without evaluating them. Hew has no address that reaches inside
// one, so an attribute is a LEAF here whatever its expression looks like: two
// different object constructors are two different values, and changing one
// replaces the whole attribute rather than producing a nested hunk the applier
// could not resolve.
//
// Two P4 limits, recorded here because neither is visible from the call site.
// (1) A BARE expression — a traversal, a call, an operator — reads as the
// string that spells it (§8.5, exprText's own note), so a diff that REWRITES
// one writes `x = "var.new"` where the new document has `x = var.new`. The
// value model has no spelling that keeps them apart; comparison is unaffected,
// because both sides decode the same way.
// (2) Adding or removing a whole BLOCK needs the mirror grammar's
// `type "label" { … }` body form, which the renderer does not write yet; a
// label addressed as a body line is refused there rather than approximated
// (§9.4-R6), and a whole-block add still renders as an attribute.
func DiffTree(src []byte) (*hew.DiffNode, error) {
	d, err := parseDoc(src)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
			Detail: "source does not parse as HCL: " + err.Error()}
	}
	return d.diffBody(d.root, "")
}

func (d *doc) diffBody(b *bodyNode, path string) (*hew.DiffNode, error) {
	if err := d.checkTuples(b, path); err != nil {
		return nil, err
	}
	out := &hew.DiffNode{Kind: hew.KindMap}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	// Blocks sharing a type are ONE child, because they share the first step
	// of their address: `provider "a"` and `provider "b"` are /provider/"a"
	// and /provider/"b", so a differ that made two `provider` children would
	// address either of them as /provider — which names both, and which the
	// applier must refuse as HEW012.
	for _, g := range groupByName(b.items) {
		child, err := d.diffTuples(g.items, 0, path+"/"+g.name)
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, hew.DiffChild{Key: g.name, Node: child})
		m.Content = append(m.Content, strNode(g.name), child.Value.Node())
	}
	out.Value = hew.NodeValue(m)
	return out, nil
}

// nameGroup is one address name and the items that answer to it, in the order
// the body writes them.
type nameGroup struct {
	name  string
	items []*itemNode
}

func groupByName(items []*itemNode) []nameGroup {
	var out []nameGroup
	at := map[string]int{}
	for _, it := range items {
		i, ok := at[it.name]
		if !ok {
			at[it.name] = len(out)
			out = append(out, nameGroup{name: it.name})
			i = len(out) - 1
		}
		out[i].items = append(out[i].items, it)
	}
	return out
}

// diffTuples projects the items sharing an address prefix, depth label steps
// in. It bottoms out at the single item whose labels are exhausted — an
// attribute's expression, or a block's body — and otherwise splits on the next
// label, which becomes one LABEL child per distinct value (§4.3).
func (d *doc) diffTuples(items []*itemNode, depth int, path string) (*hew.DiffNode, error) {
	if len(items) == 1 && len(items[0].labels) == depth {
		return d.diffItem(items[0], path)
	}
	out := &hew.DiffNode{Kind: hew.KindMap}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, g := range groupByLabel(items, depth) {
		if g.name == "" {
			return nil, &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentDiffer,
				Path: path, Detail: "a block here is a label-prefix of another block of the same type; " +
					"one address would have to name both, and hew will not pick one (§4.3, §9.4-R6)"}
		}
		child, err := d.diffTuples(g.items, depth+1, path+"/"+quoteHCL(g.name))
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, hew.DiffChild{Key: g.name, Label: true, Node: child})
		m.Content = append(m.Content, strNode(g.name), child.Value.Node())
	}
	out.Value = hew.NodeValue(m)
	return out, nil
}

// groupByLabel splits items on their label at depth, first-seen order. An item
// with no label that deep reports the empty name, which diffTuples refuses.
func groupByLabel(items []*itemNode, depth int) []nameGroup {
	var out []nameGroup
	at := map[string]int{}
	for _, it := range items {
		label := ""
		if depth < len(it.labels) {
			label = it.labels[depth]
		}
		i, ok := at[label]
		if !ok {
			at[label] = len(out)
			out = append(out, nameGroup{name: label})
			i = len(out) - 1
		}
		out[i].items = append(out[i].items, it)
	}
	return out
}

// diffItem projects one fully-addressed item: an attribute's expression, or a
// block's body.
func (d *doc) diffItem(it *itemNode, path string) (*hew.DiffNode, error) {
	if it.kind == itemAttr {
		v, err := d.exprValue(it.expr)
		if err != nil {
			return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentDiffer,
				Path: path, Detail: "attribute " + it.name + ": " + err.Error()}
		}
		return &hew.DiffNode{Kind: hew.KindScalar, Value: v}, nil
	}
	return d.diffBody(it.body, path)
}

// checkTuples refuses a body that holds the same `(type, labels)` tuple twice.
//
// §6.4.3 settles that collision with an ordinal selector, and the notation
// spells one only as a `! match ord=` annotation on a hunk anchor (§7.2) — a
// choice a REVIEWER makes about which of two identical addresses was meant.
// A differ that picked an ordinal on its own would be guessing, and a differ
// that emitted the bare tuple would be handing the applier an address it must
// refuse as HEW012. §9.4-R6 leaves one honest answer: say so, here, as HEW020.
func (d *doc) checkTuples(b *bodyNode, path string) error {
	seen := make(map[string]bool, len(b.items))
	for _, it := range b.items {
		key := tupleString(it.name, it.labels)
		if seen[key] {
			if path == "" {
				path = "/"
			}
			return &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentDiffer,
				Path: path,
				Detail: "this body holds more than one " + key + "; addressing repeated blocks needs the ordinal " +
					"selector of §6.4.3, which the notation writes as a `! match ord=` annotation a reviewer chooses " +
					"(§7.2) — the differ will not guess one (§9.4-R6)"}
		}
		seen[key] = true
	}
	return nil
}
