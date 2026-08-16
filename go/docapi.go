package hew

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/afero"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// --- the document API (Appendix A.0, O34/O43/O49) ----------------------------
//
// A host program that wants to change a config file does not want to build a
// []Transform by hand. This is the surface that replaces that, and it is three
// rules, each a restatement of one the format already has:
//
//	Rule 1 — format appears at the OPEN BOUNDARY and nowhere else, detected
//	         from the NAME (§8.0), overridable only by As.
//	Rule 2 — addressing is the §4 path, one language in two encodings, with
//	         typed holes for runtime values (O43, segarg.go).
//	Rule 3 — READS BECOME ASSERTS: an operation that names an existing node
//	         records the value it found as a `test` beside the write, which is
//	         §9.1's lowering with the before-image taken from the document.
//
// The output is ordinary IR (§9.2 abstract form), indistinguishable from
// parsed-patch IR: this is a patch producer that happens to be able to apply
// its own output.

// Doc is an open document: a target to resolve addresses against, plus the
// transforms recorded so far. Nothing happens until a terminal is called.
type Doc struct {
	name   string
	format FormatID
	src    []byte
	bind   Binding

	// The write destination, and exactly one of them is set (O49). A Doc from
	// OpenBytes has neither and says so rather than guessing one.
	fsys afero.Fs
	file afero.File

	// doc is the parsed read-only view (A.4), built on first read and reused:
	// every read in one recording session sees ONE before-image, which is what
	// makes the recorded asserts a coherent statement about one document.
	doc    Document
	docErr error

	steps []*step
	err   error // §10.4: first error wins
}

// OpenOption is a constructor option. The only one v0 defines is As.
type OpenOption func(*openOpts)

type openOpts struct {
	format FormatID
}

// As overrides detection, and is the exact analogue of a target line's
// `format=` attribute (§2.2) — the one place the spec already lets a human
// out-vote the extension.
func As(format FormatID) OpenOption {
	return func(o *openOpts) { o.format = format }
}

// Open reads path from fsys and returns a document bound to the format §8.0
// detects FROM THE PATH. Content is never sniffed: a path whose format cannot
// be determined is HEW021, fixable at this call site and only here.
func Open(fsys afero.Fs, path string, opts ...OpenOption) (*Doc, error) {
	src, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver,
			Target: path, Detail: err.Error()}
	}
	d, err := newDoc(path, src, opts)
	if err != nil {
		return nil, err
	}
	d.fsys = fsys
	return d, nil
}

// OpenFile adopts an ALREADY-OPEN handle. Detection reads f.Name(); a handle
// with no usable name takes As, exactly as an ambiguous path does.
//
// The caller owns the handle: hew does not open it, does not lock it, and
// NEVER CLOSES A HANDLE IT DID NOT OPEN. This is the constructor for a program
// that has already taken a lock, already resolved a symlink, or already
// stat-ed and validated the file it is about to edit — which is what a careful
// config writer does, and what it must not be made to redo.
//
// The one thing hew does to the handle is seek it to the start before reading:
// a caller that has already validated the content is holding a handle at EOF,
// and a document API that read nothing from it would be useless.
func OpenFile(f afero.File, opts ...OpenOption) (*Doc, error) {
	name := f.Name()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver,
			Target: name, Detail: err.Error()}
	}
	src, err := afero.ReadAll(f)
	if err != nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver,
			Target: name, Detail: err.Error()}
	}
	d, err := newDoc(name, src, opts)
	if err != nil {
		return nil, err
	}
	d.file = f
	return d, nil
}

// OpenBytes is the same for content already in hand. name is still required
// and still the only thing detection reads: bytes have no name of their own,
// and inventing one from their shape is the sniffing §8.0 forbids.
func OpenBytes(name string, src []byte, opts ...OpenOption) (*Doc, error) {
	return newDoc(name, src, opts)
}

func newDoc(name string, src []byte, opts []OpenOption) (*Doc, error) {
	var o openOpts
	for _, opt := range opts {
		opt(&o)
	}
	format := o.format
	if format == "" {
		var ok bool
		if format, ok = DetectFormat(name); !ok {
			return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentResolver,
				Target: name,
				Detail: fmt.Sprintf("cannot determine a format from the name %q, and content is never sniffed (§8.0); pass hew.As", name)}
		}
	}
	b, ok := Lookup(format)
	if !ok {
		return nil, &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentResolver,
			Target: name, Detail: fmt.Sprintf("no binding registered for format %q", string(format))}
	}
	return &Doc{name: name, format: format, src: append([]byte(nil), src...), bind: b}, nil
}

// Name is the document's name: the path, the handle's name, or the label
// OpenBytes was given. It is what detection read and what the produced
// TransformList carries as its target.
func (d *Doc) Name() string { return d.name }

// Format is the format this document is bound to. After the open boundary, no
// method mentions a format again, ever.
func (d *Doc) Format() FormatID { return d.format }

// At parses pattern — and ONLY pattern — as a §4 path with holes, filling
// each "{}" with the next SegmentArg (O43). A malformed pattern, or a hole
// count that does not match the argument count, is HEW001 attributed to this
// call and latched: nothing has happened yet, so there is nothing to fail
// except the terminal.
func (d *Doc) At(pattern string, args ...SegmentArg) *Sel {
	p, err := buildPath(d.format, pattern, args)
	if err != nil {
		d.fail(err)
		return &Sel{d: d, dead: true}
	}
	return d.AtPath(p)
}

// AtPath is the fully pre-built form, for an address that is programmatic all
// the way down. AtPath(ParsePath(s)) is At(s): one language, two encodings.
func (d *Doc) AtPath(p Path) *Sel {
	if p.IsZero() || p.IsRelative() {
		d.fail(patternErr(p.String(), "AtPath needs an absolute §4 path"))
		return &Sel{d: d, dead: true}
	}
	return &Sel{d: d, path: p}
}

// Sel is a selection: one address, plus the transforms recorded against it.
// Every method returns the Sel, so operations and qualifiers chain, and a Sel
// whose At failed is dead — its methods record nothing, because the error is
// already latched on the document.
type Sel struct {
	d    *Doc
	path Path
	dead bool

	// q holds the qualifiers this Sel carries. They are applied at the
	// TERMINAL, to every transform this Sel recorded, which is what makes
	// `.Remove().Optional()` and `.Optional().Remove()` the same patch — a `!`
	// directive rides a whole hunk regardless of where in it the author wrote
	// the line.
	q quals

	// sibling is Add's relative placement (OP-11 … OP-13). Its SPELLING is
	// checked at the call, like a pattern's; which container a bare segment
	// belongs to is settled at the terminal, where the document is open.
	sibling    Segment
	sibPath    Path
	hasSibling bool
	before     bool
}

// step is one recorded operation, plus what the terminal must read from the
// document before it becomes IR. Reads are deferred to the terminal on
// purpose: the qualifiers that decide whether a missing node is an error
// (§7.6) may be written after the operation they qualify.
type step struct {
	sel  *Sel
	t    Transform
	read readKind
}

type readKind uint8

const (
	readNone readKind = iota
	// readBefore emits `test`(current value) ahead of the write — Rule 3's
	// whole content, and §9.1 step 2 with the before-image taken from the
	// document instead of from a `-` line.
	readBefore
	// readCount fills `? exhaustive`'s paired count (§9.1 step 3) from the
	// container's own children.
	readCount
)

// record appends one step. It is a no-op once an error is latched: §10.4's
// first-error-wins, spelled as "later calls do nothing".
func (s *Sel) record(t Transform, read readKind) *Sel {
	if s.dead || s.d.err != nil {
		return s
	}
	t.Path = s.path
	s.d.steps = append(s.d.steps, &step{sel: s, t: t, read: read})
	return s
}

// value encodes an operand. A value the IR cannot hold is HEW020 — the same
// verdict Render gives a transform the notation cannot spell.
func (s *Sel) value(v any) (Value, bool) {
	val, err := ValueOf(v)
	if err != nil {
		s.d.fail(&hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentParser,
			Target: s.d.name, Path: s.path.String(), Detail: "value cannot be encoded: " + err.Error()})
		return Value{}, false
	}
	return val, true
}

func (s *Sel) write(op OpKind, v any, onConflict OnConflict, read readKind) *Sel {
	if s.dead || s.d.err != nil {
		return s
	}
	val, ok := s.value(v)
	if !ok {
		return s
	}
	return s.record(Transform{Op: op, Value: val, OnConflict: onConflict}, read)
}

// Replace is OP-01: the asserted write. It records the value it found as a
// `test` beside the `replace`, so the patch carries the same loud staleness a
// hand-written hunk carries.
func (s *Sel) Replace(v any) *Sel { return s.write(OpReplace, v, "", readBefore) }

// Set is OP-03, `! upsert`: add, or replace whatever is there. It is the
// mapping write that asserts NOTHING about the prior state and therefore
// cannot detect drift — §7.7's warning applies verbatim, and a linter that
// warns on `! upsert` should warn on Set for the same reason.
func (s *Sel) Set(v any) *Sel { return s.write(OpAdd, v, ConflictReplace, readNone) }

// Default is OP-04, `! default`: add only if absent, leave an existing value
// alone. Unasserted, and no write at all if the node is present.
func (s *Sel) Default(v any) *Sel { return s.write(OpAdd, v, ConflictKeep, readNone) }

// Add is OP-02: the node must not exist.
func (s *Sel) Add(v any) *Sel { return s.write(OpAdd, v, ConflictFail, readNone) }

// Remove is OP-05. Like Replace it asserts the value it found — unless it is
// `Optional`, which is OP-06's remove-if-present and may have nothing to read.
func (s *Sel) Remove() *Sel { return s.record(Transform{Op: OpRemove}, readBefore) }

// After places an Add after a sibling (OP-13). The sibling is an ABSTRACT
// address, never an index: either a whole path, or a single §4 segment
// naming a child of the same container.
func (s *Sel) After(sibling string) *Sel { return s.place(sibling, false) }

// Before places an Add before a sibling (OP-13).
func (s *Sel) Before(sibling string) *Sel { return s.place(sibling, true) }

func (s *Sel) place(sibling string, before bool) *Sel {
	if s.dead || s.d.err != nil {
		return s
	}
	s.sibPath, s.sibling = Path{}, Segment{}
	if strings.HasPrefix(sibling, "/") {
		p, err := ParsePathIn(s.d.format, sibling)
		if err != nil {
			return s.reject(s.badf("placement sibling %q: %s", sibling, detailOf(err)))
		}
		s.sibPath = p
	} else {
		// The document's own format, matching the whole-path branch above:
		// both halves of a placement sibling read in one grammar (O46).
		seg, err := parseSegment(sibling, formatScope(s.d.format))
		if err != nil {
			return s.reject(s.badf("placement sibling %q: %s", sibling, err.Error()))
		}
		s.sibling = seg
	}
	s.hasSibling, s.before = true, before
	return s
}

// Assert is OP-24's `? expect`: an assert-only transform (§7.4) that writes
// nothing.
func (s *Sel) Assert(v any) *Sel {
	if s.dead || s.d.err != nil {
		return s
	}
	val, ok := s.value(v)
	if !ok {
		return s
	}
	return s.record(Transform{Op: OpTest, Value: val}, readNone)
}

// AssertAbsent is OP-25's `? absent`.
func (s *Sel) AssertAbsent() *Sel { return s.record(Transform{Op: OpTest, Absent: true}, readNone) }

// AssertCount is OP-27's `? count`.
func (s *Sel) AssertCount(n int) *Sel {
	if s.dead || s.d.err != nil {
		return s
	}
	if n < 0 {
		s.d.fail(s.badf("count must not be negative"))
		return s
	}
	return s.record(Transform{Op: OpTest, Count: &n}, readNone)
}

// AssertKind is OP-28's `? kind`. The vocabulary is the universal three plus
// whatever the registered extensions declare (§8.8).
func (s *Sel) AssertKind(k NodeKind) *Sel {
	if s.dead || s.d.err != nil {
		return s
	}
	if !k.Valid() {
		s.d.fail(s.badf("unknown kind %q", string(k)))
		return s
	}
	return s.record(Transform{Op: OpTest, NodeKind: &k}, readNone)
}

// AssertExhaustive is OP-26's `? exhaustive`: the container's listed children
// are all of them. §9.1 step 3 always pairs it with a count, and because the
// document is open that count is one more read that becomes an assert.
func (s *Sel) AssertExhaustive() *Sel {
	return s.record(Transform{Op: OpTest, Exhaustive: true}, readCount)
}

// Optional is §7.6's `! optional`.
func (s *Sel) Optional() *Sel { return s.qualify(func(q *quals) { q.optional = true }) }

// Idempotent is §7.5's `! idempotent`.
func (s *Sel) Idempotent() *Sel { return s.qualify(func(q *quals) { q.idempotent = true }) }

// Anchor is the YAML alias policy (§8.3, OP-40 / OP-41).
func (s *Sel) Anchor(mode AnchorMode) *Sel {
	if mode != AnchorRewrite && mode != AnchorFork {
		return s.reject(s.badf("unknown anchor %q", string(mode)))
	}
	if err := s.owns("anchor"); err != nil {
		return s.reject(err)
	}
	return s.qualify(func(q *quals) { q.anchor = mode })
}

// Surface is the TOML placement directive (§8.4, OP-38).
func (s *Sel) Surface(sf Surface) *Sel {
	if sf != SurfaceTable && sf != SurfaceDotted {
		return s.reject(s.badf("unknown surface %q", string(sf)))
	}
	if err := s.owns("surface"); err != nil {
		return s.reject(err)
	}
	return s.qualify(func(q *quals) { q.surface = sf })
}

func (s *Sel) qualify(set func(*quals)) *Sel {
	if s.dead || s.d.err != nil {
		return s
	}
	set(&s.q)
	return s
}

func (s *Sel) reject(err error) *Sel {
	s.d.fail(err)
	return s
}

// owns checks a qualifier against the registered binding's declaration (§8.8).
// A qualifier this format does not own is HEW020 at the format side, never
// ignored: an ignored qualifier is a silent change of strictness.
func (s *Sel) owns(q string) error {
	if OwnsQualifier(s.d.format, q) {
		return nil
	}
	return &hewerr.Error{Code: hewerr.CodeInexpressible, Component: hewerr.ComponentApplier,
		Target: s.d.name, Path: s.path.String(),
		Detail: fmt.Sprintf("format %q does not support the %q qualifier", string(s.d.format), q)}
}

func (s *Sel) badf(format string, args ...any) error {
	return &hewerr.Error{Code: hewerr.CodeParse, Component: hewerr.ComponentParser,
		Target: s.d.name, Path: s.path.String(), Detail: fmt.Sprintf(format, args...)}
}

// --- reading the open document -----------------------------------------------

// node resolves an address against the open document, through the same walk
// Resolve (§9.2) uses — so the document API and `hew apply --ops` agree about
// what an address selects, including a key-match's ambiguity (HEW012) and a
// miss (HEW013).
func (d *Doc) node(p Path) (Node, error) {
	doc, err := d.document()
	if err != nil {
		return nil, err
	}
	root := doc.Root()
	if root == nil {
		return nil, &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: d.name, Detail: "document has no root node"}
	}
	r := &resolver{target: d.name, root: root}
	_, node, err := r.walk(p, 0)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, &hewerr.Error{Code: hewerr.CodeNoMatch, Component: hewerr.ComponentApplier,
			Target: d.name, Path: p.String(), Detail: "this address names a position, not a node"}
	}
	return node, nil
}

// --- the terminals -----------------------------------------------------------

// Transforms is the IR (§9.2 abstract form). Everything else is derived from
// it, and it is ORDINARY IR: nothing distinguishes a list built here from one
// a parser lowered out of a .hew file.
func (d *Doc) Transforms() (TransformList, error) {
	if d.err != nil {
		return TransformList{}, d.err
	}
	out := make([]Transform, 0, len(d.steps)*2)
	for _, st := range d.steps {
		ts, err := st.build(d)
		if err != nil {
			return TransformList{}, err
		}
		out = append(out, ts...)
	}
	// The emptiness guard is the AUTHORING surface's, not the IR's. O38 made an
	// empty transform list legal at the IR layer, because a patch with no hunks
	// must apply as a no-op — but a Doc opened and never operated on is a caller
	// bug, and returning an empty patch would hide it.
	//
	// The test is on the STEPS RECORDED, not on the transforms produced. Today
	// every step yields at least one transform, so the two agree; they stop
	// agreeing the moment a step can legitimately lower to nothing, and then
	// "your operations amounted to no change" is O38's no-op rather than an
	// error. Only "you never asked for anything" is the caller bug.
	if len(d.steps) == 0 {
		return TransformList{}, irErr(d.name, "no operations (§A.0)")
	}
	tl := TransformList{Target: d.name, Format: d.format, Transform: out}
	if err := tl.Validate(); err != nil {
		return TransformList{}, err
	}
	return tl, nil
}

// RenderPatch writes a reviewable .hew file. Because the document is open,
// the renderer has real siblings to draw context from: the patch is produced
// by the DIFFER, from the document's own before-image and the after-image the
// recorded transforms produce, so it carries genuine context lines at
// §9.4-R2's radius — the same artifact `hew diff` produces, from the same
// renderer.
//
// RenderPatch is not a debugging aid. It is how a program that edits a user's
// file leaves behind a reviewable statement of what it did, in the same
// notation a human would have written by hand.
//
// One consequence is worth stating rather than hiding: a patch derived from
// two byte images describes the CHANGE, so an unasserted write that landed on
// an existing node renders as the asserted form (§7.7's `! upsert` is a
// property of how the change was computed, not of the file it produced). When
// the recording changes no bytes at all — an assert-only patch (§7.4), or a
// `Default` on a node that was already there — there is nothing for the differ
// to describe, and the recorded transforms are rendered directly.
func (d *Doc) RenderPatch(opt RenderOptions) ([]byte, error) {
	tl, err := d.Transforms()
	if err != nil {
		return nil, err
	}
	after, err := d.apply(tl)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(after, d.src) {
		return Render(tl, opt)
	}
	if d.bind.Differ == nil {
		return nil, d.noHalf("diff")
	}
	oldTree, err := d.bind.Differ(d.src)
	if err != nil {
		return nil, err
	}
	newTree, err := d.bind.Differ(after)
	if err != nil {
		return nil, err
	}
	diffed, err := DiffTrees(oldTree, newTree, d.format, DiffOptions{Context: opt.Context, Target: d.name})
	if err != nil {
		return nil, err
	}
	if len(diffed.Transform) == 0 {
		return Render(tl, opt)
	}
	return Render(diffed, opt)
}

// Bytes applies the recorded transforms to the opened content and returns the
// patched bytes. All-or-nothing (§10.5): on any error the result is nil.
func (d *Doc) Bytes() ([]byte, error) {
	tl, err := d.Transforms()
	if err != nil {
		return nil, err
	}
	return d.apply(tl)
}

func (d *Doc) apply(tl TransformList) ([]byte, error) {
	if d.bind.Applier == nil {
		return nil, d.noHalf("apply")
	}
	out, err := d.bind.Applier(d.src, tl)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Doc) noHalf(half string) error {
	return &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentResolver,
		Target: d.name, Detail: fmt.Sprintf("this build cannot %s %q documents", half, string(d.format))}
}

// WriteOption is an option to Doc.Write. v0 defines one, DryRun.
type WriteOption func(*writeOpts)

type writeOpts struct{ dryRun bool }

// DryRun does everything Write does — including the apply, and so including
// every assertion the recording carries — and writes nothing.
func DryRun() WriteOption { return func(o *writeOpts) { o.dryRun = true } }

// Write commits Bytes() back through the same afero.Fs or afero.File the
// document was opened from, via §10.5's temp-and-rename. A Doc from OpenBytes
// has no destination and returns an error naming that rather than guessing
// one.
//
// The atomicity caveat, stated rather than implied (O49): through an arbitrary
// afero.Fs the guarantee is BEST-EFFORT, because rename semantics belong to
// the backend, not to hew. On afero.OsFs a rename within one filesystem is the
// atomic operation §10.5 assumes; on a backend layered over object storage it
// may be copy-then-delete, which has a window §10.5's contract does not
// survive. hew cannot detect that and does not pretend to. What holds on every
// backend is that a DETECTABLE failure writes nothing at all, because staging
// happens entirely in memory.
//
// Through an adopted handle (OpenFile) there is no directory to stage in and
// no Fs to rename through, so the commit is a rewrite in place: the caller
// that owns the handle owns the atomicity strategy too, which is the point of
// having taken the lock itself.
func (d *Doc) Write(opt ...WriteOption) error {
	var o writeOpts
	for _, f := range opt {
		f(&o)
	}
	if d.fsys == nil && d.file == nil {
		return &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver,
			Target: d.name,
			Detail: "a document opened with OpenBytes has no destination; use Open or OpenFile, or write Bytes() yourself"}
	}
	out, err := d.Bytes()
	if err != nil {
		return err
	}
	if o.dryRun {
		return nil
	}
	if d.fsys != nil {
		return commitFS(d.fsys, d.name, out)
	}
	return commitHandle(d.file, out)
}

// commitFS is the §10.5 commit. It stays a package variable so a test can
// substitute a failing commit; the commit rule itself is WriteAtomic, shared
// with hewfs so both file-writing surfaces cannot drift apart.
var commitFS = WriteAtomic

// commitHandle writes through an adopted handle. hew never closes it.
func commitHandle(f afero.File, data []byte) error {
	if err := f.Truncate(0); err != nil {
		return writeErr(f.Name(), err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return writeErr(f.Name(), err)
	}
	if _, err := f.Write(data); err != nil {
		return writeErr(f.Name(), err)
	}
	return nil
}

func writeErr(name string, err error) error {
	return &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver,
		Target: name, Detail: err.Error()}
}

// build turns one recorded step into the transforms it stands for, reading
// the document where Rule 3 says it must.
func (st *step) build(d *Doc) ([]Transform, error) {
	t := st.t
	q := st.sel.q

	var out []Transform
	switch st.read {
	case readBefore:
		cur, err := d.current(t.Path)
		if err != nil {
			// OP-06: an optional remove is precisely the case where there may
			// be nothing to read. The remove stands alone, which is the IR a
			// `- key` line under `! optional` would have lowered to had the
			// key been absent from the author's before-image too.
			if q.optional && t.Op == OpRemove && isMissing(err) {
				break
			}
			return nil, err
		}
		test := Transform{Op: OpTest, Path: t.Path, Value: cur}
		q.applyTo(&test)
		out = append(out, test)
	case readCount:
		node, err := d.node(t.Path)
		if err != nil {
			return nil, err
		}
		n := node.Len()
		t.Count = &n
	}

	if t.Op == OpAdd && st.sel.hasSibling {
		sib := st.sel.siblingPath()
		if st.sel.before {
			t.Before = sib
		} else {
			t.After = sib
		}
	}
	q.applyTo(&t)
	return append(out, t), nil
}

// current is Rule 3's read: the value the document holds at this address.
func (d *Doc) current(p Path) (Value, error) {
	node, err := d.node(p)
	if err != nil {
		return Value{}, err
	}
	return node.Value(), nil
}

// isMissing reports whether an error is "there is nothing at this address" as
// opposed to "this address is ambiguous" or "this document will not parse".
// Only the first is something `! optional` tolerates.
func isMissing(err error) bool {
	he, ok := hewerr.As(err)
	return ok && he.Code == hewerr.CodeNoMatch
}

// siblingPath is the placement address the IR carries. A sibling written as a
// whole address is that address; a bare segment names a child of the same
// container this Sel's address sits in.
func (s *Sel) siblingPath() Path {
	if !s.sibPath.IsZero() {
		return s.sibPath
	}
	return s.containerPath().Append(s.sibling)
}

// containerPath is the container an added node lands in: the address itself
// when it already IS a sequence or ends in "-", and its parent otherwise —
// the same signal the resolver uses to tell a sequence-style add from a
// map-style one (§9.2).
func (s *Sel) containerPath() Path {
	segs := s.path.Segments()
	if n := len(segs); n > 0 && segs[n-1].Kind == SegAppend {
		if p, ok := s.path.Parent(); ok {
			return p
		}
	}
	if node, err := s.d.node(s.path); err == nil && node.Kind() == KindSeq {
		return s.path
	}
	if p, ok := s.path.Parent(); ok {
		return p
	}
	return s.path
}

func detailOf(err error) string {
	if he, ok := hewerr.As(err); ok {
		return he.Detail
	}
	return err.Error()
}

// fail latches the first error (§10.4).

// fail latches the first error (§10.4). Later calls become no-ops and the
// error surfaces at the terminal, which is the only place anything happens.
func (d *Doc) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// document parses the target once, on the first read.
func (d *Doc) document() (Document, error) {
	if d.doc != nil || d.docErr != nil {
		return d.doc, d.docErr
	}
	if d.bind.Document == nil {
		d.docErr = &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentResolver,
			Target: d.name, Detail: fmt.Sprintf("this build cannot read %q documents", string(d.format))}
		return nil, d.docErr
	}
	doc, err := d.bind.Document(d.name, d.src)
	if err != nil {
		d.docErr = err
		return nil, err
	}
	if doc == nil {
		d.docErr = &hewerr.Error{Code: hewerr.CodeTargetParse, Component: hewerr.ComponentApplier,
			Target: d.name, Detail: "the binding returned no document"}
		return nil, d.docErr
	}
	d.doc = doc
	return doc, nil
}
