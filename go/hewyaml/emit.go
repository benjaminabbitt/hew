package hewyaml

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file renders a transform's value as YAML source text. It deliberately
// does not use yaml.Marshal: the encoder re-wraps long lines, re-indents, and
// re-quotes by its own rules, which would rewrite bytes the patch never asked
// to touch. Rendering here is line-oriented and indentation-free — the
// insertion site adds the indent — so a value can be spliced into a block
// container at any depth.

// emitMember renders "key: value" as one or more unindented lines. flow asks
// for the single-line rendering a flow container's siblings use (§8.3: an
// added node adopts the style of its siblings).
func (d *doc) emitMember(key string, v *yaml.Node, flow bool) []string {
	k := yamlKey(key)
	if flow || v == nil || v.Kind == yaml.ScalarNode || isEmptyCollection(v) {
		return []string{k + ": " + d.flowText(v)}
	}
	switch v.Kind {
	case yaml.MappingNode:
		return append([]string{k + ":"}, indentEach(d.emitMapBody(v), d.indent)...)
	case yaml.SequenceNode:
		return append([]string{k + ":"}, indentEach(d.emitSeqBody(v), d.indent)...)
	}
	return []string{k + ": " + d.flowText(v)}
}

// emitElement renders one sequence element as unindented lines, the "- "
// marker included.
func (d *doc) emitElement(v *yaml.Node, flow bool) []string {
	if flow || v == nil || v.Kind == yaml.ScalarNode || isEmptyCollection(v) {
		return []string{"- " + d.flowText(v)}
	}
	var body []string
	switch v.Kind {
	case yaml.MappingNode:
		body = d.emitMapBody(v)
	case yaml.SequenceNode:
		body = d.emitSeqBody(v)
	default:
		return []string{"- " + d.flowText(v)}
	}
	out := []string{"- " + body[0]}
	// Continuation lines align under the first, past the two columns "- "
	// occupies — not under the document's own indent step.
	return append(out, indentEach(body[1:], 2)...)
}

func (d *doc) emitMapBody(v *yaml.Node) []string {
	var out []string
	for i := 0; i+1 < len(v.Content); i += 2 {
		out = append(out, d.emitMember(v.Content[i].Value, v.Content[i+1], false)...)
	}
	return out
}

func (d *doc) emitSeqBody(v *yaml.Node) []string {
	var out []string
	for _, c := range v.Content {
		out = append(out, d.emitElement(c, false)...)
	}
	return out
}

// flowText renders a node on a single line.
func (d *doc) flowText(v *yaml.Node) string {
	if v == nil {
		return "null"
	}
	switch v.Kind {
	case yaml.ScalarNode:
		return scalarText(v)
	case yaml.MappingNode:
		var parts []string
		for i := 0; i+1 < len(v.Content); i += 2 {
			parts = append(parts, yamlKey(v.Content[i].Value)+": "+d.flowText(v.Content[i+1]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case yaml.SequenceNode:
		var parts []string
		for _, c := range v.Content {
			parts = append(parts, d.flowText(c))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case yaml.AliasNode:
		return "*" + v.Value
	}
	return "null"
}

func isEmptyCollection(v *yaml.Node) bool {
	return v != nil && (v.Kind == yaml.MappingNode || v.Kind == yaml.SequenceNode) && len(v.Content) == 0
}

// scalarText renders a scalar as YAML source: a non-string keeps its literal
// form, a string is quoted only when a plain scalar would not read back as
// the same string.
func scalarText(n *yaml.Node) string {
	if n.ShortTag() != "!!str" {
		if n.Value == "" {
			return "null"
		}
		return n.Value
	}
	if needsQuote(n.Value) {
		return strconv.Quote(n.Value)
	}
	return n.Value
}

func yamlKey(k string) string {
	if needsQuote(k) {
		return strconv.Quote(k)
	}
	return k
}

// needsQuote reports whether a string must be quoted to survive as itself:
// empty, whitespace-sensitive, opening with a YAML indicator, carrying a
// key/comment separator, or reading back as some other type.
func needsQuote(s string) bool {
	if s == "" || s != strings.TrimSpace(s) {
		return true
	}
	if strings.ContainsAny(s, "\n\t") {
		return true
	}
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
		return true
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") {
		return true
	}
	return plainTag(s) != "!!str"
}

// plainTag is the tag YAML's own resolver gives an unquoted token, which is
// how "true", "8080" and "null" are recognized as needing quotes when they
// are meant as strings.
func plainTag(s string) string {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil || len(n.Content) == 0 {
		return ""
	}
	return n.Content[0].ShortTag()
}

func indentEach(lines []string, n int) []string {
	if n <= 0 || len(lines) == 0 {
		return lines
	}
	pad := strings.Repeat(" ", n)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = pad + l
	}
	return out
}

// indentBlock joins rendered lines into text indented at col, with no
// trailing newline.
func indentBlock(lines []string, col int) string {
	return strings.Join(indentEach(lines, col), "\n")
}

// reindent shifts a raw source block (a copied member, comments included)
// from one indentation to another, leaving its internal relative shape alone.
func reindent(text string, from, to int) string {
	if from == to {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		cut := 0
		for cut < from && cut < len(l) && l[cut] == ' ' {
			cut++
		}
		lines[i] = strings.Repeat(" ", to) + l[cut:]
	}
	return strings.Join(lines, "\n")
}
