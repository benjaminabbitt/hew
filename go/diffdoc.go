package hew

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// hashScalar is the content-hash identity of a scalar set/sequence member
// (satisfied-recoil): the SHA-256, hex-encoded, of the member's canonical scalar
// token. scalarToken is format-independent — a YAML `beta` and a JSON "beta" both
// canonicalize to `!!str:beta` — so the differ hashing a DiffNode and an applier
// hashing the element it reparsed produce the SAME digest. It is a cryptographic
// hash on purpose: the digest decides WHERE a write lands in a file whose content
// is partly attacker-influenceable, so a findable collision would be a targeted
// mislocation, not a statistical curiosity.
func hashScalar(v Value) string {
	sum := sha256.Sum256([]byte(scalarToken(v)))
	return hex.EncodeToString(sum[:])
}

// hashSegment builds the `#hew:sha256=<hex>` fragment segment that addresses a
// scalar member by its content hash.
func hashSegment(v Value) Segment {
	return Segment{Kind: SegHash, Form: "hew", Name: "sha256", Hash: hashScalar(v)}
}

// MatchesHash reports whether the value v is the member this SegHash addresses:
// its canonical hash equals the fragment's digest (satisfied-recoil). It is the
// applier's side of set/sequence hash addressing — every binding calls it so the
// hash agreement between differ and applier lives in one place. Only "sha256" is
// defined in v0; any other algorithm never matches.
func (s Segment) MatchesHash(v Value) bool {
	return s.Kind == SegHash && s.Name == "sha256" && hashScalar(v) == s.Hash
}

// DiffNode is one node of the format-neutral document tree the differ walks
// (§9.4). A format binding builds it from the SAME tree its applier parses —
// the differ and the applier are format-side inverses (§9), and two parsers
// with two opinions about a document would break that closure.
//
// It is deliberately not hew.Node: Node is the read-only projection Resolve
// addresses through, and it exposes neither a map's key ORDER nor a
// container's comment children (§4.5b) — the two things a diff cannot be
// computed without.
type DiffNode struct {
	// Kind is KindMap, KindSeq or KindScalar. A comment is not a node kind
	// here: comments are positional CHILDREN of a container, which is what
	// their `/container/#n` address says they are.
	Kind NodeKind

	// Children are the container's positional children in SOURCE ORDER,
	// comments interleaved where they stand. Empty for a scalar.
	Children []DiffChild

	// Value is the node's whole value, carrying the source's own scalar
	// quoting and block/flow style so that §9.4-R5 — an added node renders
	// from the new document's own bytes — survives into the rendered patch.
	Value Value
}

// DiffChild is one positional child of a container: a map member, a sequence
// element, or a standalone comment.
type DiffChild struct {
	// Key is the member name. Empty for a sequence element and for a comment.
	Key string

	// Comment marks a standalone comment child (§4.5b); Text is its content,
	// with the marker and one leading space already stripped, which is the
	// form §6.1 compares. Node is nil.
	Comment bool
	Text    string
	// Node is the child's value, nil for a comment.
	Node *DiffNode
}

// scalarNode reports the child's identity value for a named field, or ok=false
// when the field is missing or is not a scalar (§9.4-R4's "present on every
// element, scalar").
func (n *DiffNode) member(name string) (*DiffNode, bool) {
	if n == nil || n.Kind != KindMap {
		return nil, false
	}
	for _, c := range n.Children {
		if !c.Comment && c.Key == name {
			return c.Node, true
		}
	}
	return nil, false
}

// canonical renders a node as a deterministic token string. Node equality
// (§9.4-R1's "Myers over node equality") is defined as equality of these
// tokens, which makes equality total, comment-aware — two JSONC objects that
// differ only in a comment are NOT the same node — and independent of the
// order the bindings happen to build their trees in.
func (n *DiffNode) canonical() string {
	var b strings.Builder
	writeCanonical(&b, n)
	return b.String()
}

func writeCanonical(b *strings.Builder, n *DiffNode) {
	if n == nil {
		b.WriteString("~")
		return
	}
	switch n.Kind {
	case KindMap, KindSeq:
		if n.Kind == KindMap {
			b.WriteByte('{')
		} else {
			b.WriteByte('[')
		}
		for _, c := range n.Children {
			if c.Comment {
				b.WriteByte('#')
				writeToken(b, c.Text)
				continue
			}
			b.WriteByte('k')
			writeToken(b, c.Key)
			writeCanonical(b, c.Node)
		}
		if n.Kind == KindMap {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	default:
		b.WriteString(scalarToken(n.Value))
	}
}

// writeToken writes a length-prefixed string, so that no concatenation of
// child tokens can be read two ways.
func writeToken(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
}

// scalarToken is a value's canonical token: its resolved tag and its text, so
// that the number 8080 and the string "8080" are different nodes (§4.2's
// format-native decoding, applied to equality).
func scalarToken(v Value) string {
	n := v.Node()
	if n == nil {
		return "!!absent:"
	}
	// A COMMENT is a keyless member like a set element, and its identity is its
	// text — with the marker and one leading space already stripped (§4.5b), so
	// a `// note` in JSONC and a `# note` in YAML are the same comment and hash
	// alike. Without this a comment value would hash through its rendered
	// {comment: …} mapping, which is neither stable nor meaningful.
	if txt, ok := CommentText(v); ok {
		return "!!comment:" + txt
	}
	if n.Kind != yaml.ScalarNode {
		return "!!node:" + v.String()
	}
	return n.ShortTag() + ":" + n.Value
}

// sameNode reports node equality, the relation the sequence diff runs over.
func sameNode(a, b *DiffNode) bool { return a.canonical() == b.canonical() }

// MemberToken is the address token of a keyless member: the digest its
// `#hew:sha256=` address carries. Exported because a binding must name a
// candidate's NEIGHBOURS with it (§4.5d) and only the binding can walk its own
// document; the core cannot reach a jNode or a yaml.Node.
func MemberToken(v Value) string { return hashScalar(v) }

// commentSegment addresses a comment by the digest of its text rather than by an
// ordinal (§4.5b). A comment has no key, exactly like a set member, so it locates
// the way one does — and the ordinal it replaces was the single position-based
// address in a design whose §4 says it has none.
func commentSegment(text string) Segment {
	return Segment{Kind: SegComment, Form: "hew", Name: "comment",
		Hash: hashScalar(CommentValue(text))}
}

// MatchesComment reports whether text is the comment this segment addresses: its
// canonical digest equals the fragment's. It is the applier's side of comment
// hash addressing, exactly as MatchesHash is a set member's — a comment is a
// keyless member and locates the same way.
func (s Segment) MatchesComment(text string) bool {
	return s.Kind == SegComment && s.Hash != "" && hashScalar(CommentValue(text)) == s.Hash
}
