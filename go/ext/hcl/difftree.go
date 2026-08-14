package hcl

import (
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
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
	if len(items) > 1 && labelsExhausted(items, depth) {
		return d.diffBlockSet(items, path)
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

// labelsExhausted reports whether every item's labels run out at depth — the
// blocks share their whole `(type, labels)` tuple, and no further label step
// can tell them apart.
func labelsExhausted(items []*itemNode, depth int) bool {
	for _, it := range items {
		if len(it.labels) != depth || it.kind != itemBlock {
			return false
		}
	}
	return true
}

// diffBlockSet projects a set of blocks sharing one `(type, labels)` tuple —
// the construct §6.4.3 names as the one place hew can silently patch the wrong
// node, and which O45 made addressable by KEY-MATCH on a distinguishing
// attribute (§4.2).
//
// The differ picks that attribute explicitly rather than handing the set to
// §9.4-R4's identity-field inference, because that inference falls back to
// INDEX addressing when it cannot choose, and an index is not an address a
// block set has: the patch would parse, resolve to nothing, and blame the user.
// Where nothing distinguishes the blocks the answer is still §9.4-R6's — say
// so, here, as HEW020 — because the alternative is guessing an ordinal, which
// is a choice a reviewer makes and not one a differ may make (§7.2).
func (d *doc) diffBlockSet(items []*itemNode, path string) (*hew.DiffNode, error) {
	attr, ok := d.identityAttr(items)
	if !ok {
		return nil, &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentDiffer,
			Path: path,
			Detail: "this body holds " + strconv.Itoa(len(items)) + " blocks sharing one (type, labels) tuple and no " +
				"attribute distinguishes them, so §4.2's key-match cannot address one (O45). What is left is the " +
				"ordinal selector of §6.4.3, which the notation writes as a `! match ord=` annotation a reviewer " +
				"chooses (§7.2) — the differ will not guess one (§9.4-R6)"}
	}
	out := &hew.DiffNode{Kind: hew.KindMap, KeyedSet: true}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, it := range items {
		v, _ := d.attrValue(it, attr)
		child, err := d.diffBody(it.body, path+"/"+attr+"="+attrToken(v))
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, hew.DiffChild{
			Key: attrToken(v), MatchField: attr, MatchValue: v, Node: child,
		})
		m.Content = append(m.Content, strNode(attrToken(v)), child.Value.Node())
	}
	out.Value = hew.NodeValue(m)
	return out, nil
}

// identityAttr is §9.4-R4's identity rule over a block set: an attribute every
// block has as a scalar and no two share. It is the same question the applier's
// ambiguity hint asks, answered by the same function, so the address the differ
// WRITES and the address the diagnostic SUGGESTS cannot drift apart.
func (d *doc) identityAttr(items []*itemNode) (string, bool) {
	name, _, ok := d.distinguishingAttr(items)
	return name, ok
}

// attrToken is an identity value as diff-tree text: the spelling §4.2 would
// use for it, which is also what makes two blocks' identities comparable.
func attrToken(v hew.Value) string {
	s, ok := hew.MatchSpelling(v)
	if !ok {
		return ""
	}
	return s
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

// A body that holds the same `(type, labels)` tuple twice used to be refused
// here, unconditionally, by a checkTuples pass that ran before any projection:
// the ordinal selector was the only way to address such a block, the notation
// spells one only as a `! match ord=` annotation a REVIEWER writes (§7.2), and
// a differ that picked one would be guessing. O45 replaced the first half of
// that reasoning — key-match now addresses a block set (§4.2) — so the refusal
// moved into diffBlockSet, which refuses only what is genuinely unaddressable:
// blocks that no attribute tells apart.
