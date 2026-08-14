package hewhcl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// §8.5's value rule, in one place.
//
// "Expressions are compared as source text, normalized for whitespace only.
// Hew does not evaluate HCL: `"${var.x}"` and `var.x` are different values
// even where HCL would agree."
//
// Literals are the one exception the corpus forces: `region = "us-west-1"`
// asserts against the transform value `us-west-1` (hcl/set-attribute,
// hcl/repeated-label-ordinal), so a quoted string decodes to its content and a
// number to a number. Everything with a `${…}` in it, and every traversal,
// function call or operator, keeps its source text — which is exactly what
// keeps `"${var.x}"` (the text `${var.x}`) apart from `var.x` (the text
// `var.x`).

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func taggedNode(tag, s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s}
}

// exprValue reads an expression as a hew value.
func (d *doc) exprValue(e hclsyntax.Expression) (hew.Value, error) {
	n, err := d.exprNode(e)
	if err != nil {
		return hew.Value{}, err
	}
	return hew.NodeValue(n), nil
}

func (d *doc) exprNode(e hclsyntax.Expression) (*yaml.Node, error) {
	switch x := e.(type) {
	case *hclsyntax.TemplateExpr:
		return strNode(d.templateText(x)), nil
	case *hclsyntax.TemplateWrapExpr:
		return strNode("${" + d.normSrc(x.Wrapped.Range().Start.Byte, x.Wrapped.Range().End.Byte) + "}"), nil
	case *hclsyntax.LiteralValueExpr:
		return literalNode(x.Val), nil
	case *hclsyntax.ObjectConsExpr:
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, item := range x.Items {
			key, err := d.objectKey(item.KeyExpr)
			if err != nil {
				return nil, err
			}
			val, err := d.exprNode(item.ValueExpr)
			if err != nil {
				return nil, err
			}
			m.Content = append(m.Content, strNode(key), val)
		}
		return m, nil
	case *hclsyntax.TupleConsExpr:
		s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, el := range x.Exprs {
			val, err := d.exprNode(el)
			if err != nil {
				return nil, err
			}
			s.Content = append(s.Content, val)
		}
		return s, nil
	}
	r := e.Range()
	return strNode(d.normSrc(r.Start.Byte, r.End.Byte)), nil
}

// objectKey reads an object-constructor key. HCL's `{a = 1}` names the key
// `a`; `{"a" = 1}` names the same key with a quoted spelling.
func (d *doc) objectKey(e hclsyntax.Expression) (string, error) {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		if kw := hcl2Keyword(k); kw != "" {
			return kw, nil
		}
		return d.objectKey(k.Wrapped)
	}
	n, err := d.exprNode(e)
	if err != nil {
		return "", err
	}
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("object key is not a scalar")
	}
	return n.Value, nil
}

// hcl2Keyword returns a bare-identifier object key, or "" if the key is not
// one. `{aws = {…}}` names the key `aws`, and HCL parses that bare name as a
// one-step traversal.
func hcl2Keyword(k *hclsyntax.ObjectConsKeyExpr) string {
	if k.ForceNonLiteral {
		return ""
	}
	trav, ok := k.Wrapped.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(trav.Traversal) != 1 {
		return ""
	}
	root, ok := trav.Traversal[0].(hcl.TraverseRoot)
	if !ok {
		return ""
	}
	return root.Name
}

// templateText renders a quoted template: literal runs decode, and everything
// interpolated keeps its `${…}` source (§8.5).
func (d *doc) templateText(x *hclsyntax.TemplateExpr) string {
	var b strings.Builder
	for _, part := range x.Parts {
		if lit, ok := part.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String && !lit.Val.IsNull() {
			b.WriteString(lit.Val.AsString())
			continue
		}
		r := part.Range()
		b.WriteString("${")
		b.WriteString(d.normSrc(r.Start.Byte, r.End.Byte))
		b.WriteString("}")
	}
	return b.String()
}

// normSrc is the "normalized for whitespace only" of §8.5.
func (d *doc) normSrc(start, end int) string {
	return strings.Join(strings.Fields(string(d.src[start:end])), " ")
}

func literalNode(v cty.Value) *yaml.Node {
	switch {
	case v.IsNull():
		return taggedNode("!!null", "null")
	case v.Type() == cty.Bool:
		if v.True() {
			return taggedNode("!!bool", "true")
		}
		return taggedNode("!!bool", "false")
	case v.Type() == cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			return taggedNode("!!int", bf.Text('f', -1))
		}
		return taggedNode("!!float", bf.Text('f', -1))
	case v.Type() == cty.String:
		return strNode(v.AsString())
	}
	return strNode(v.GoString())
}

// bodyValue reads a body as a mapping: an attribute contributes its name and
// expression value, a nested block its type and its own body. A block's LABELS
// are addressing, never value (§11.10 reduction 4), so a body that holds two
// same-named children has no value form at all.
func (d *doc) bodyValue(b *bodyNode) (hew.Value, error) {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	seen := map[string]bool{}
	for _, it := range b.items {
		if seen[it.name] {
			return hew.Value{}, fmt.Errorf("body holds more than one %q; a repeated (type, labels) tuple has no value form (§8.5)", it.name)
		}
		seen[it.name] = true
		var child *yaml.Node
		if it.kind == itemAttr {
			n, err := d.exprNode(it.expr)
			if err != nil {
				return hew.Value{}, err
			}
			child = n
		} else {
			v, err := d.bodyValue(it.body)
			if err != nil {
				return hew.Value{}, err
			}
			child = v.Node()
		}
		m.Content = append(m.Content, strNode(it.name), child)
	}
	return hew.NodeValue(m), nil
}

// itemValue is the value of whatever an address resolved to.
func (d *doc) itemValue(it *itemNode) (hew.Value, error) {
	if it.kind == itemAttr {
		return d.exprValue(it.expr)
	}
	return d.bodyValue(it.body)
}

// --- writing ----------------------------------------------------------------

// exprText renders a value as an HCL expression. Strings are always written
// quoted: a bare traversal is not something a hew value can distinguish from
// the string that spells it, and quoting is what every corpus write pins.
func exprText(n *yaml.Node, indent int) (string, error) {
	n = unwrapDoc(n)
	if n == nil {
		return "null", nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return "null", nil
		case "!!bool", "!!int", "!!float":
			return n.Value, nil
		}
		return quoteHCL(n.Value), nil
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			t, err := exprText(c, indent)
			if err != nil {
				return "", err
			}
			parts = append(parts, t)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case yaml.MappingNode:
		return objectText(n, indent)
	}
	return "", fmt.Errorf("value has no HCL expression form")
}

// objectText renders a mapping as a multi-line object constructor, with its
// `=` aligned the way hclfmt aligns a run of attributes.
func objectText(n *yaml.Node, indent int) (string, error) {
	if len(n.Content) == 0 {
		return "{}", nil
	}
	inner := indent + bodyIndent
	names := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		names = append(names, n.Content[i].Value)
	}
	width := maxLen(names)
	var b strings.Builder
	b.WriteString("{\n")
	for i := 0; i+1 < len(n.Content); i += 2 {
		name := n.Content[i].Value
		val, err := exprText(n.Content[i+1], inner)
		if err != nil {
			return "", err
		}
		b.WriteString(strings.Repeat(" ", inner))
		b.WriteString(name)
		b.WriteString(strings.Repeat(" ", width-len(name)+1))
		b.WriteString("= ")
		b.WriteString(val)
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat(" ", indent))
	b.WriteString("}")
	return b.String(), nil
}

// blockText renders a whole block: its header from the ADDRESS (type and
// labels), its body from the value (§11.10 reduction 4, OP-34).
func blockText(typeName string, labels []string, n *yaml.Node, indent int) (string, error) {
	n = unwrapDoc(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return "", fmt.Errorf("a block's value must be a mapping")
	}
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	b.WriteString(pad)
	b.WriteString(blockHeader(typeName, labels))
	body, err := bodyText(n, indent+bodyIndent)
	if err != nil {
		return "", err
	}
	if body == "" {
		b.WriteString("}")
		return b.String(), nil
	}
	b.WriteString("\n")
	b.WriteString(body)
	b.WriteString(pad)
	b.WriteString("}")
	return b.String(), nil
}

func blockHeader(typeName string, labels []string) string {
	var b strings.Builder
	b.WriteString(typeName)
	for _, l := range labels {
		b.WriteString(" ")
		b.WriteString(quoteHCL(l))
	}
	b.WriteString(" {")
	return b.String()
}

// bodyText renders a mapping as a block body: one attribute per entry, the
// run aligned. Nested mappings become object constructors, not nested blocks —
// hcl/add-block pins `aws = {` one level inside `required_providers {`.
func bodyText(n *yaml.Node, indent int) (string, error) {
	if len(n.Content) == 0 {
		return "", nil
	}
	names := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		names = append(names, n.Content[i].Value)
	}
	width := maxLen(names)
	var b strings.Builder
	for i := 0; i+1 < len(n.Content); i += 2 {
		name := n.Content[i].Value
		val, err := exprText(n.Content[i+1], indent)
		if err != nil {
			return "", err
		}
		b.WriteString(strings.Repeat(" ", indent))
		b.WriteString(name)
		b.WriteString(strings.Repeat(" ", width-len(name)+1))
		b.WriteString("= ")
		b.WriteString(val)
		b.WriteString("\n")
	}
	return b.String(), nil
}

func maxLen(names []string) int {
	w := 0
	for _, n := range names {
		if len(n) > w {
			w = len(n)
		}
	}
	return w
}

// quoteHCL writes an HCL quoted string. `${` is NOT escaped: a value carrying
// an interpolation round-trips through exprValue as its own source text, and
// escaping it here would change the expression's meaning (§8.5).
func quoteHCL(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func unwrapDoc(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	return n
}

// isMapping reports whether a value writes as a block body rather than as an
// expression.
func isMapping(v hew.Value) bool {
	n := unwrapDoc(v.Node())
	return n != nil && n.Kind == yaml.MappingNode
}

// describe renders a resolved node for a diagnostic's "found" half.
func (d *doc) describe(it *itemNode) string {
	v, err := d.itemValue(it)
	if err != nil {
		return strconv.Quote(string(d.src[it.start:it.end]))
	}
	return v.String()
}
