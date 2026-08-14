package hew

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// --- the format registry (Appendix A.6, §8.8) --------------------------------
//
// A format is a PLUGIN (§8.8). Everything the core would otherwise have had to
// know about one — how to apply, how to diff, how to project a read-only
// document, which filenames it claims, which node kinds it declares, which
// transform qualifiers it owns — arrives here, at registration, and nowhere
// else. That is what makes §12's documented-only families addable without
// touching the core, and it is why FormatID.Valid() is a lookup in this map
// rather than a switch over the six v0 formats (O48's live defect).
//
// Bindings self-register from init() on import (O35), which is Go's own answer
// to this problem — image/png, database/sql, net/http/pprof — and makes
// "linked" and "capable" the same fact:
//
//	import _ "github.com/benjaminabbitt/hew/go/ext/all"   // every v0 extension
//	import _ "github.com/benjaminabbitt/hew/go/ext/json"  // or exactly one
//
// The stated cost is real: an unused blank import is invisible to goimports,
// and a program that links no extension can detect no format.

// Applier is a format binding's apply half (§9.3, Appendix A.4): IR plus target
// bytes in, patched bytes out. It never sees .hew text, a margin, a hunk or an
// annotation, and it is all-or-nothing — on any error the bytes are nil.
type Applier func(target []byte, tl TransformList) ([]byte, error)

// Differ is a format binding's diff half (§9.4, Appendix A.5). It is the tree
// builder rather than the whole differ: §9.4's comparison is format-neutral and
// lives in DiffTrees, so what a binding supplies is the parse of ONE side into
// the neutral tree. hewdiff.Diff builds both sides with the same function,
// which is what keeps the differ and the applier format-side inverses.
type Differ func(src []byte) (*DiffNode, error)

// DetectRule is one format's claim on filenames (§8.0). It is DATA, not spec
// (O4, finished by O48): §8.0's table is a non-normative record of what the
// shipped extensions put here, and the normative statement is the mechanism
// plus "detection reads the name, never the content".
type DetectRule struct {
	// Extensions are the filename extensions this format claims, leading dot
	// included and lower-cased (".yaml", ".yml").
	Extensions []string

	// WellKnownNames are whole base names this format claims regardless of
	// their extension — "tsconfig.json" is JSONC by convention. They beat an
	// extension claim, because that is the only reason to state one.
	WellKnownNames []string
}

// Binding is a format's halves plus everything O48 moved out of the core. A
// nil half is not an error: a format may be detectable and parseable long
// before it can be applied or diffed, which is exactly the state Markdown is in
// (§8.7's evaluation is open), and the seam that needs a missing half is the
// one that reports HEW021.
type Binding struct {
	// Applier applies a transform list to target bytes (§9.3). Nil means this
	// build cannot apply this format.
	Applier Applier

	// Differ parses one side into the neutral diff tree (§9.4). Nil means this
	// build cannot diff this format.
	Differ Differ

	// Document parses target bytes into the read-only view Resolve (§9.2) and
	// the document API (Appendix A.0) project against. name is the target's
	// label, for diagnostics only — no I/O happens here.
	Document func(name string, src []byte) (Document, error)

	// Detect is this format's filename claim (§8.0).
	Detect DetectRule

	// Kinds are the node-kind names this extension DECLARES beyond the
	// universal map/seq/scalar (§8.8): "block" for HCL, "block"/"section" for
	// Markdown. `? kind` (OP-28) carries a string and the registered set is
	// what makes it valid; an unknown kind is HEW001, exactly as before.
	Kinds []NodeKind

	// Segments are the segment SHAPES this extension claims (§8.8). The core
	// grammar defines five universal lexical forms — key, index, `-`, key-match
	// and the quoted segment — and lexes everything else into a raw token it
	// offers to the registered extensions before defaulting to `key`. A form
	// that claims a token owns its meaning; the core only carries the bytes.
	Segments []SegmentForm

	// Qualifiers are the transform qualifier keys this extension OWNS —
	// "anchor" for YAML (§9.6), "surface" for TOML. O48's first recorded
	// tension is why this is a declaration and not a relocation: the field's
	// spelling and ordering stay in §9.6, because the corpus pins those bytes
	// and an opaque bag could not be canonicalized deterministically. What
	// moves is ownership, and this is where it is recorded.
	Qualifiers []string
}

// SegmentForm is one extension-claimed segment shape (§8.8). Name is what the
// form is called — it travels in the parsed Segment so a resolver can tell
// which of its own shapes it is looking at — and Claim decides whether a lexed
// token is one of them.
//
// Claim receives the token with the universal suffixes already stripped (the
// `?` of §4.4 and the IR-only `[n]` of §9.6), so a form describes only its own
// shape. Its three answers are distinct on purpose:
//
//   - (true, nil)   — mine, and well formed.
//   - (false, nil)  — not mine; the core keeps looking, and `key` is the floor.
//   - (_, non-nil)  — mine, and MALFORMED. "@" is the standing example: it is
//     unmistakably a marker and unmistakably nameless, and reporting it as an
//     ordinary key would hide the typo behind a lookup failure much later.
type SegmentForm struct {
	Name  string
	Claim func(raw string) (bool, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[FormatID]Binding{}
)

// Register records a format's binding. It is meant to be called from an
// extension package's init(), so its failure modes are programmer errors and
// it panics rather than returning them: a duplicate registration means two
// packages claim one format id, and neither the core nor the caller can pick a
// winner. Panicking at init is database/sql's rule for the same reason.
func Register(id FormatID, b Binding) {
	if id == "" {
		panic("hew: Register called with an empty format id")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[id]; dup {
		panic("hew: duplicate binding registered for format " + string(id))
	}
	registry[id] = b
}

// Lookup returns the binding registered for id. A false second result means no
// extension for this format is linked into this build — which is the same fact
// as "this build cannot handle this format" (O35).
func Lookup(id FormatID) (Binding, bool) {
	if id == "" {
		return Binding{}, false
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	b, ok := registry[id]
	return b, ok
}

// Formats lists every registered format, sorted. It is what a --format error
// message needs: "which formats can this build actually do" is only answerable
// because registration has one entry point.
func Formats() []FormatID {
	registryMu.RLock()
	out := make([]FormatID, 0, len(registry))
	for id := range registry {
		out = append(out, id)
	}
	registryMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// DetectFormat implements §8.0 over the registered bindings' DetectRules. It
// reads the NAME and never the content — there is nowhere for content to enter
// this function, which is the property stated as a signature.
//
// A well-known base name beats an extension claim. Two bindings claiming one
// name, or no binding claiming it, reports false: the caller's cue to say
// `format=` (or hew.As), which is the only override §8.0 allows. Detection
// never guesses between two claimants.
func DetectFormat(filename string) (FormatID, bool) {
	if filename == "" {
		return "", false
	}
	base := strings.ToLower(filepath.Base(filepath.ToSlash(filename)))
	ext := strings.ToLower(filepath.Ext(base))

	registryMu.RLock()
	defer registryMu.RUnlock()

	byName, nameHits := FormatID(""), 0
	byExt, extHits := FormatID(""), 0
	for id, b := range registry {
		for _, n := range b.Detect.WellKnownNames {
			if strings.ToLower(n) == base {
				byName, nameHits = id, nameHits+1
				break
			}
		}
		if ext == "" {
			continue
		}
		for _, e := range b.Detect.Extensions {
			if strings.ToLower(e) == ext {
				byExt, extHits = id, extHits+1
				break
			}
		}
	}
	switch {
	case nameHits == 1:
		return byName, true
	case nameHits > 1:
		return "", false // ambiguous: HEW021, never a guess
	case extHits == 1:
		return byExt, true
	}
	return "", false
}

// OwnsQualifier reports whether the extension registered for f declares the
// transform qualifier key q (§9.6's `anchor:`, `surface:`). It is the ownership
// half of O48's restructure: the spelling stays in §9.6 and the enforcement
// stays where it already was — HEW001 at parse for a qualifier no format
// declares, HEW020 at apply for one this format cannot support (§9.3).
func OwnsQualifier(f FormatID, q string) bool {
	b, ok := Lookup(f)
	if !ok {
		return false
	}
	for _, k := range b.Qualifiers {
		if k == q {
			return true
		}
	}
	return false
}

// claimSegment offers a lexed token to the registered extensions' segment
// forms and reports the name of the form that claimed it (§8.8).
//
// It asks EVERY registered extension rather than the one format the patch
// declares, and that boundary is the honest one to state. §8.8's design
// direction says the core offers the token to the ACTIVE format's extension,
// and the format is indeed known from the `--- ` line before any hunk is read —
// but ParsePath and the emitting seams' spellability guard take no FormatID,
// and giving them one is a change to the §4 parsing API that O41's
// canonical-rendering work owns. Until then a claim is scoped to the BUILD:
// a heading is a heading in any patch a markdown-linked program parses, which
// is exactly the behaviour the hardcoded grammar had.
//
// Iteration is over sorted format ids so that two extensions claiming one
// shape resolve the same way in every run rather than by map order.
func claimSegment(raw string) (string, bool, error) {
	for _, id := range Formats() {
		b, ok := Lookup(id)
		if !ok {
			continue
		}
		for _, f := range b.Segments {
			claimed, err := f.Claim(raw)
			if err != nil {
				return f.Name, true, err
			}
			if claimed {
				return f.Name, true, nil
			}
		}
	}
	return "", false, nil
}

// declaresKind reports whether any registered extension declares k. The core
// cannot narrow this to the ACTIVE extension at every seam that asks: `? kind`
// is validated during lowering (§9.1), which by §9's own rule knows no format —
// see the note on NodeKind.Valid.
func declaresKind(k NodeKind) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, b := range registry {
		for _, dk := range b.Kinds {
			if dk == k {
				return true
			}
		}
	}
	return false
}
