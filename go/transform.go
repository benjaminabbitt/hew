package hew

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// FormatID names a target format (§8.0).
type FormatID string

const (
	FormatJSON     FormatID = "json"
	FormatJSONC    FormatID = "jsonc"
	FormatYAML     FormatID = "yaml"
	FormatTOML     FormatID = "toml"
	FormatMarkdown FormatID = "markdown"
)

// Valid reports whether a binding for f is registered (Appendix A.6).
//
// It is a REGISTRY LOOKUP and not a switch over the six v0 formats, which is
// O48's live defect: a hardcoded switch refuses a correctly-registered seventh
// extension at the PARSER, before any binding is consulted, and makes §12's
// documented-only families unaddable without touching the core. The six above
// get no special standing: unregistered, they are as unknown as any other name,
// which is what makes "linked" and "capable" the same fact (O35).
func (f FormatID) Valid() bool {
	_, ok := Lookup(f)
	return ok
}

// NodeKind is the vocabulary of `? kind` / `test`+`kind` (§7.1, OP-28).
//
// Three kinds are universal and live here. The others — HCL's `block`,
// Markdown's `section` — are EXTENSION-DECLARED (§8.8): an extension names them
// in its Binding.Kinds and the registry is what makes them assertable. The
// `.hewt` spelling is unchanged, and an unknown kind is HEW001 exactly as
// before.
type NodeKind string

const (
	KindMap    NodeKind = "map"
	KindSeq    NodeKind = "seq"
	KindScalar NodeKind = "scalar"
)

// Valid reports whether k is a universal kind or one a registered extension
// declares.
//
// It cannot narrow to the ACTIVE extension, and that boundary is worth stating
// rather than hiding: `? kind` is validated during lowering (§9.1), and the
// lowerer holds a hunk, not a file section, so it has no format in hand. O48's
// second recorded tension draws the line at the parser consulting an
// extension's grammar; narrowing this check to one format would mean threading
// the format through lowering, which is a change to §9's shape and not one this
// ruling made.
func (k NodeKind) Valid() bool {
	switch k {
	case KindMap, KindSeq, KindScalar:
		return true
	}
	return k != "" && declaresKind(k)
}

// OpKind is one of the reduced core's five operations (§9.6, §11.10).
type OpKind string

const (
	OpTest    OpKind = "test"
	OpAdd     OpKind = "add"
	OpRemove  OpKind = "remove"
	OpReplace OpKind = "replace"
	OpCopy    OpKind = "copy"

	// There is deliberately no OpMove. "move" is accepted on .hewt input as
	// sugar and normalized to OpCopy + OpRemove (§11.10 reduction 1); it
	// never appears in a Transform or in emitted output, which
	// corpus/json/ir-move-normalized pins.
	opMove OpKind = "move"
)

// Valid reports whether op is one of the five core operations. "move" is not:
// it is input sugar, not an operation.
func (op OpKind) Valid() bool {
	switch op {
	case OpTest, OpAdd, OpRemove, OpReplace, OpCopy:
		return true
	}
	return false
}

// OnConflict is `add`'s add-semantics variant (OP-02 / OP-03 / OP-04).
type OnConflict string

const (
	ConflictFail    OnConflict = "fail"    // default, OP-02
	ConflictKeep    OnConflict = "keep"    // OP-04, defaulting
	ConflictReplace OnConflict = "replace" // OP-03, upsert
)

// AnchorMode is the YAML alias policy (§8.3, OP-40 / OP-41).
type AnchorMode string

const (
	AnchorRewrite AnchorMode = "rewrite"
	AnchorFork    AnchorMode = "fork"
)

// Surface is the TOML placement directive (§8.4, OP-38).
type Surface string

const (
	SurfaceTable  Surface = "table"
	SurfaceDotted Surface = "dotted"
)

// Transform is one record of the reduced core (§9.6, §11.10). Sugar — move,
// exhaustive, comment attachment, ordinals — is desugared before it reaches
// this type.
type Transform struct {
	Op    OpKind
	Path  Path  // abstract hew path; ALL addressing richness lives here
	From  Path  // OpCopy only
	Value Value // OpAdd, OpReplace, OpTest

	// Placement (OP-11 … OP-13). Abstract, never a numeric index: the parser
	// has no target to count against. Mutually exclusive; both zero means
	// append at end.
	Before Path
	After  Path

	// Exactly one of Value, Absent, Count, NodeKind and Exhaustive selects the
	// assertion mode on OpTest.
	Absent   bool
	Count    *int
	NodeKind *NodeKind

	// Exhaustive represents `? exhaustive` (§7.1): the listed children are the
	// COMPLETE child set of the governed container (§9.1 step 3: "exhaustive
	// -> test with exhaustive: true"). Appendix A.1's Transform has no field
	// for this — a spec gap found while implementing, flagged in the P2
	// report. The parser always pairs it with Count (the number of children
	// asserted elsewhere in the same hunk at this level), so the applier can
	// evaluate it as a count check while still naming it "exhaustive" in a
	// failure's diagnostic (corpus message_contains requires the word).
	Exhaustive bool

	OnConflict OnConflict // OpAdd only
	Anchor     AnchorMode // YAML alias policy
	Surface    Surface    // OpAdd only, TOML placement

	// The two tolerance flags, and there are no others.
	Optional   bool // §7.6
	Idempotent bool // §7.5

	// PatchLine is provenance into the .hew file, for diagnostics. It does
	// not survive .hewt serialization: §9.6 declares `line` emitted and
	// ignored on input, and no corpus fixture carries it, so the codec
	// neither writes nor reads it.
	PatchLine int

	// AnchorPath and AnchorLine are the hunk this transform was lowered from:
	// its `@@ … @@` address and the line that address is written on. They
	// are provenance, like PatchLine — never serialized, never compared —
	// and they exist because a resolution failure INSIDE the anchor is the
	// ANCHOR's failure and must be reported where the reviewer can fix it.
	// The corpus pins that reporting in two places, hcl/repeated-label-ambiguous
	// (HEW012 at the `@@` line, not at the context line that asked first) and
	// markdown/duplicate-heading; Appendix A.1's Transform has no field for
	// it, which is a spec gap found while implementing the HCL binding.
	AnchorPath Path
	AnchorLine int
}

// TransformList is the IR (§9.2, abstract form): the boundary between the
// notation side and the format side, the interop surface, and the unit the
// corpus pins.
type TransformList struct {
	Target    string   // target path, as declared by "--- " or "target:"
	Format    FormatID // "" = infer from the target's extension (§8.0)
	Transform []Transform
}

// Equal reports whether two lists are the same IR.
func (tl TransformList) Equal(o TransformList) bool {
	if tl.Target != o.Target || tl.Format != o.Format || len(tl.Transform) != len(o.Transform) {
		return false
	}
	for i := range tl.Transform {
		if !tl.Transform[i].Equal(o.Transform[i]) {
			return false
		}
	}
	return true
}

// Equal reports whether two transforms are the same record. PatchLine,
// AnchorPath and AnchorLine are provenance, not content, and are not compared.
func (t Transform) Equal(o Transform) bool {
	if t.Op != o.Op || t.Absent != o.Absent || t.OnConflict != o.OnConflict ||
		t.Anchor != o.Anchor || t.Surface != o.Surface ||
		t.Optional != o.Optional || t.Idempotent != o.Idempotent ||
		t.Exhaustive != o.Exhaustive {
		return false
	}
	if !t.Path.Equal(o.Path) || !t.From.Equal(o.From) ||
		!t.Before.Equal(o.Before) || !t.After.Equal(o.After) {
		return false
	}
	if !t.Value.Equal(o.Value) {
		return false
	}
	if !eqIntPtr(t.Count, o.Count) {
		return false
	}
	if (t.NodeKind == nil) != (o.NodeKind == nil) {
		return false
	}
	return t.NodeKind == nil || *t.NodeKind == *o.NodeKind
}

func eqIntPtr(a, b *int) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func irErr(path, format string, args ...any) error {
	return &hewerr.Error{
		Code:      hewerr.CodeParse,
		Component: hewerr.ComponentParser,
		Path:      path,
		Detail:    fmt.Sprintf(format, args...),
	}
}

// Validate checks the whole list: a target, a known format, and every record's
// field/op compatibility.
//
// An EMPTY transform sequence is valid (§9.6 as amended by O38): a document
// with a target and no transforms is the IR of a no-op patch, and it applies as
// one. What is still refused is a list with no target — "no transforms" is a
// complete statement about a named file, while "no file" is not a statement at
// all (§10.2's amended table). The `transforms` key's own presence is the
// codec's business, not Validate's: a list built in memory has no key to omit.
func (tl TransformList) Validate() error {
	if tl.Target == "" {
		return irErr("", "transform list has no target")
	}
	if tl.Format != "" && !tl.Format.Valid() {
		return &hewerr.Error{
			Code:      hewerr.CodeUnsupportedFormat,
			Component: hewerr.ComponentParser,
			Target:    tl.Target,
			Detail:    fmt.Sprintf("unknown format %q", string(tl.Format)),
		}
	}
	for i := range tl.Transform {
		if err := tl.Transform[i].Validate(); err != nil {
			if he, ok := hewerr.As(err); ok {
				he.Target = tl.Target
				he.Detail = fmt.Sprintf("transform %d: %s", i, he.Detail)
			}
			return err
		}
	}
	return nil
}

// --- the residual spellability guard (O41) ------------------------------------
//
// The seams that turn a Path back into TEXT — MarshalTransforms and
// MarshalTransformStream for .hewt, Render for .hew — run this guard first. It
// is NOT part of Validate: Validate also runs on the way IN, and inbound text
// that parsed is spellable by construction (§4). The guard is the emitting
// side's, and it exists so that an address that would mean something else is
// refused loudly rather than written (§9.3).
//
// Before O41 this guard fired on ORDINARY DOCUMENT KEYS — "8080", "@scope/pkg",
// "-" — and the differ ran it too, because the differ builds key segments
// straight from a target's own keys. That is over: the quoted form spells every
// key, the canonical-rendering rule emits it, and json/diff-scoped-key pins the
// differ writing `/dependencies/"@scope/pkg"` instead of refusing. The differ's
// call is gone with it.
//
// What is left is the narrow residue path.go's spellability note enumerates:
// IR that is malformed as data (a negative index, a ScalarNumber whose text is
// not a number, an extension token no linked extension claims) and a name
// holding a LINE BREAK, which §4.1's two escapes cannot spell and which would
// tear the `@@` line or the .hewt scalar it was written on in half.

// checkSpellable reports the first path-valued field of tl the v0 §4 grammar
// cannot spell, as HEW020 at comp.
func (tl TransformList) checkSpellable(comp hewerr.Component) error {
	for i := range tl.Transform {
		if err := tl.Transform[i].checkSpellable(comp, tl.Target, i); err != nil {
			return err
		}
	}
	return nil
}

// checkSpellable walks one record's four path-valued fields in emission order.
// An absent path has nothing to spell and is skipped by firstUnspellable.
func (t Transform) checkSpellable(comp hewerr.Component, target string, i int) error {
	for _, f := range []struct {
		name string
		path Path
	}{
		{keyPath, t.Path},
		{keyFrom, t.From},
		{keyBefore, t.Before},
		{keyAfter, t.After},
	} {
		seg, bad := f.path.firstUnspellable()
		if !bad {
			continue
		}
		return inexpressiblePath(comp, target, f.name, i, f.path, seg)
	}
	return nil
}

// inexpressiblePath is HEW020 for a path v0 cannot spell. Path carries the
// best-effort spelling — the very text that would have been written — so the
// diagnostic shows the corruption it prevented, and Detail names the offending
// datum as data rather than folding it into prose.
func inexpressiblePath(comp hewerr.Component, target, field string, i int, p Path, seg Segment) error {
	datum := seg.Name
	if datum == "" {
		datum = seg.String()
	}
	return &hewerr.Error{
		Code:      hewerr.CodeInexpressible,
		Component: comp,
		Target:    target,
		Path:      p.String(),
		Detail: fmt.Sprintf("transform %d: %s: the §4 path grammar cannot spell this %s %q: %s. "+
			"Every KEY has a spelling since O41 — the literal form, %s — so what is left here is "+
			"data no address can carry; hew refuses rather than emit a path that addresses "+
			"something else (§9.3)",
			i, field, seg.Kind, datum, spellFailure(seg), `/x/"…"`),
	}
}

// spellFailure says how a segment's spelling betrays it, in the words a
// reviewer needs to see why the refusal is not pedantry.
func spellFailure(seg Segment) string {
	if strings.ContainsAny(seg.Name, "\r\n") || strings.ContainsAny(seg.Value.Text, "\r\n") {
		return "it holds a line break, and §4.1's quoted form escapes only `\\\"` and `\\\\` — " +
			"a path is written on one line, so there is nowhere to put it"
	}
	spelling := strconv.Quote(seg.String())
	got, err := parseSegment(seg.String(), true, buildScope)
	switch {
	case err != nil:
		return "its spelling " + spelling + " is not a legal segment"
	case got.Kind != seg.Kind || got.Form != seg.Form:
		return "its spelling " + spelling + " re-reads as a " + got.describe() +
			" segment, not a " + seg.describe()
	case !seg.Equal(got):
		return "its spelling " + spelling + " re-reads as a different " + seg.describe() + " segment"
	}
	return "its spelling " + spelling + " does not survive in this position"
}

// Validate checks one record's field/op compatibility (§9.6: "fields whose
// combination is meaningless are HEW001").
func (t Transform) Validate() error {
	p := t.Path.String()
	if !t.Op.Valid() {
		if t.Op == "" {
			return irErr(p, "missing op")
		}
		return irErr(p, "unknown op %q", string(t.Op))
	}
	if t.Path.IsZero() {
		return irErr("", "op %s: missing path", t.Op)
	}

	// from — copy only, and required there.
	switch {
	case t.Op == OpCopy && t.From.IsZero():
		return irErr(p, "op copy: missing from")
	case t.Op != OpCopy && !t.From.IsZero():
		return irErr(p, "op %s: from is valid only on copy", t.Op)
	}

	// value — add, replace and test only; required on add and replace.
	switch t.Op {
	case OpAdd, OpReplace:
		if t.Value.IsZero() {
			return irErr(p, "op %s: missing value", t.Op)
		}
	case OpTest:
	default:
		if !t.Value.IsZero() {
			return irErr(p, "op %s: value is valid only on add, replace and test", t.Op)
		}
	}

	// Placement — add and copy only, mutually exclusive.
	if !t.Before.IsZero() || !t.After.IsZero() {
		if t.Op != OpAdd && t.Op != OpCopy {
			return irErr(p, "op %s: before/after are valid only on add and copy", t.Op)
		}
		if !t.Before.IsZero() && !t.After.IsZero() {
			return irErr(p, "before and after are mutually exclusive")
		}
	}

	// The assertion modes — test only, exactly one. Exhaustive pairs with
	// Count (the parser always sets both together, §9.1 step 3) and counts as
	// one mode, not two.
	modes := 0
	if !t.Value.IsZero() {
		modes++
	}
	if t.Absent {
		modes++
	}
	if t.Count != nil && !t.Exhaustive {
		modes++
	}
	if t.NodeKind != nil {
		modes++
	}
	if t.Exhaustive {
		modes++
	}
	if t.Op == OpTest {
		if modes != 1 {
			return irErr(p, "op test: exactly one of value, absent, count, kind and exhaustive, got %d", modes)
		}
		if t.Exhaustive && t.Count == nil {
			return irErr(p, "op test: exhaustive requires count")
		}
	} else {
		if t.Absent {
			return irErr(p, "op %s: absent is valid only on test", t.Op)
		}
		if t.Count != nil {
			return irErr(p, "op %s: count is valid only on test", t.Op)
		}
		if t.NodeKind != nil {
			return irErr(p, "op %s: kind is valid only on test", t.Op)
		}
		if t.Exhaustive {
			return irErr(p, "op %s: exhaustive is valid only on test", t.Op)
		}
	}
	if t.Count != nil && *t.Count < 0 {
		return irErr(p, "count must not be negative")
	}
	if t.NodeKind != nil && !t.NodeKind.Valid() {
		return irErr(p, "unknown kind %q", string(*t.NodeKind))
	}

	// on_conflict and surface — add only.
	if t.OnConflict != "" {
		if t.Op != OpAdd {
			return irErr(p, "op %s: on_conflict is valid only on add", t.Op)
		}
		switch t.OnConflict {
		case ConflictFail, ConflictKeep, ConflictReplace:
		default:
			return irErr(p, "unknown on_conflict %q", string(t.OnConflict))
		}
	}
	if t.Surface != "" {
		if t.Op != OpAdd {
			return irErr(p, "op %s: surface is valid only on add", t.Op)
		}
		switch t.Surface {
		case SurfaceTable, SurfaceDotted:
		default:
			return irErr(p, "unknown surface %q", string(t.Surface))
		}
	}
	if t.Anchor != "" && t.Anchor != AnchorRewrite && t.Anchor != AnchorFork {
		return irErr(p, "unknown anchor %q", string(t.Anchor))
	}

	// The two tolerance flags.
	if t.Optional && t.Op != OpRemove && t.Op != OpTest {
		return irErr(p, "op %s: optional is valid only on remove and test", t.Op)
	}
	// Idempotent rides the WRITE (§7.5's convergence) and also the
	// before-image ASSERT the same hunk emitted: §7.5's rule is stated over
	// the hunk ("if the before-image does not match but the after-image
	// does"), so the test that would otherwise fail loudly has to carry the
	// tolerance too. Copy is the one op it cannot qualify — a copy asserts
	// nothing about its destination.
	if t.Idempotent && t.Op == OpCopy {
		return irErr(p, "op %s: idempotent is valid only on test, add, remove and replace", t.Op)
	}
	return nil
}
