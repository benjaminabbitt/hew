// Package hewmatch compares a binding's document node against a patch value.
//
// §6.1's tolerance rules are a property of the FORMAT-INDEPENDENT value model,
// not of any one binding: a mapping matches as a SUBSET (every listed key
// present and equal, unlisted keys free), a sequence as an ORDERED
// SUBSEQUENCE, and a scalar exactly after format-native decoding. Every
// binding was therefore writing the same traversal over its own node type, and
// the copies had begun to differ in ways that mean nothing — one carried a
// receiver it never read, one spelled the mapping kind `nTable` and the other
// `nMap`.
//
// What is genuinely per-format is the SCALAR comparison — "exact after
// format-native decoding" is a question only the format can answer, since it
// is what makes TOML's `0x1e` equal `30` — and the shape of the node itself.
// Both are supplied by the binding through Node; the traversal is here.
package hewmatch

import (
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"gopkg.in/yaml.v3"
)

// Kind is a document node's structural kind, the three the value model has.
type Kind int

const (
	// Other is any node that is none of the three — it matches nothing.
	Other Kind = iota
	Scalar
	Mapping
	Sequence
)

// Node is one document node, as the matcher needs to see it.
//
// A binding implements this over its own node type. Lookup and Elems return
// nil for an absent child rather than a typed nil, because the matcher tests
// the interface for nil and a typed nil inside an interface is not nil.
type Node interface {
	// Kind reports the node's structural kind.
	Kind() Kind
	// ScalarEquals is §6.1's "exact, after format-native decoding". Only the
	// binding can answer it: the reader has already normalized the format's
	// own spellings, and what counts as the same number differs by format.
	ScalarEquals(want *yaml.Node) bool
	// Lookup returns the mapping child stored under key, nil when absent.
	Lookup(key string) Node
	// Elems returns the sequence's children in document order.
	Elems() []Node
	// Len is the child count Equals measures exactness against: entries for a
	// mapping, elements for a sequence.
	Len() int
}

// Matches implements §6.1's tolerance rules for a BEFORE-IMAGE value: the node
// need only be a superset of what the patch listed. This is what lets a
// context line name one key of a large mapping, or one element of a longer
// sequence, and still hold.
func Matches(n Node, want *yaml.Node) bool {
	return walk(n, want, false)
}

// Equals is Matches without the tolerance: the node is exactly the value, not
// merely a superset of it. This is what "the after-image holds" means (§10.6)
// — a mapping that gained the patched key plus six others has not already had
// this patch applied to it.
func Equals(n Node, want *yaml.Node) bool {
	return walk(n, want, true)
}

// walk is the one traversal. exact says whether a container must also match in
// SIZE, which is the only difference between the two rules: with it a mapping
// may hold no unlisted key and a sequence no unlisted element, so a sequence
// compares element-for-element instead of scanning forward for the next match.
func walk(n Node, want *yaml.Node, exact bool) bool {
	if n == nil || want == nil {
		return false
	}
	switch want.Kind {
	case yaml.ScalarNode:
		return n.Kind() == Scalar && n.ScalarEquals(want)

	case yaml.MappingNode:
		if n.Kind() != Mapping {
			return false
		}
		// Content is a flat key,value,key,value list.
		if exact && n.Len()*2 != len(want.Content) {
			return false
		}
		for i := 0; i+1 < len(want.Content); i += 2 {
			if !walk(n.Lookup(want.Content[i].Value), want.Content[i+1], exact) {
				return false
			}
		}
		return true

	case yaml.SequenceNode:
		if n.Kind() != Sequence {
			return false
		}
		elems := n.Elems()
		if exact {
			if len(elems) != len(want.Content) {
				return false
			}
			for i, w := range want.Content {
				if !walk(elems[i], w, true) {
					return false
				}
			}
			return true
		}
		// Ordered SUBSEQUENCE: each wanted element must appear after the one
		// before it, but the document may carry others in between.
		i := 0
		for _, w := range want.Content {
			for i < len(elems) && !walk(elems[i], w, false) {
				i++
			}
			if i >= len(elems) {
				return false
			}
			i++
		}
		return true
	}
	return false
}

// ScalarNode renders a path segment's identity Scalar as a YAML scalar node,
// so a key-match segment compares against the value model rather than against
// text: `port=8080` compares as the number and `port="8080"` as the string.
//
// It is not format-specific despite living next to code that is — the mapping
// runs from hew's own Scalar kinds onto the tags of the shared value model, so
// every binding had the identical function and nothing about a format could
// have changed it.
func ScalarNode(s hew.Scalar) *yaml.Node {
	tag := "!!str"
	switch s.Kind {
	case hew.ScalarBool:
		tag = "!!bool"
	case hew.ScalarNull:
		tag = "!!null"
	case hew.ScalarNumber:
		if strings.ContainsAny(s.Text, ".eE") {
			tag = "!!float"
		} else {
			tag = "!!int"
		}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s.Text}
}
