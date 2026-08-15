// Package hewfs is hew's filesystem boundary (spec Appendix A.8): the layer
// that turns transform lists plus files into written bytes, honouring §10.5's
// atomicity.
//
// It imports stdlib, afero, and hew. Nothing else, and in particular NO HOST
// PROJECT (ruling O36). The draft of A.8 spelled this contract in ctxloom's own
// vocabulary — `agent.WithFileLock`, `iox.WriteFileAtomicFs` — and that text is
// superseded: §10.5 requires a PROPERTY (atomic temp-and-rename, no backup
// file), which is not a helper, and a host that wants its own locking wraps
// this package from the outside.
//
// The filesystem type is afero.Fs (ruling O49), which is what makes this
// package's own tests run on afero.MemMapFs rather than on a temp directory.
// The honest caveat the ruling states, restated here because this is where a
// reader will look for it: TEMP-AND-RENAME ATOMICITY IS THE BACKEND'S. Rename
// semantics belong to the afero backend, hew cannot detect a backend whose
// rename is copy-then-delete, and it does not pretend to. What holds on every
// backend is the property staging buys: a DETECTABLE failure writes nothing at
// all, because every section is applied in memory before any file is touched.
package hewfs

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/afero"

	hew "github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/hewdiff"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// WriteOptions controls a write path (Appendix A.8).
type WriteOptions struct {
	DryRun bool
	Format hew.FormatID // override detection for every target (§8.0)

	// RecordPath, if set, writes the §9.7 application record there.
	RecordPath string

	// AppliedAt pins the record's applied_at (§9.7, O37). The zero time means
	// "now". The environment fallbacks HEW_APPLIED_AT and SOURCE_DATE_EPOCH are
	// deliberately NOT read here: §9.7's precedence puts the library caller
	// first, and a library that read the environment behind its caller's back
	// would make that precedence unimplementable. The CLI reads them and passes
	// the result down as data (Appendix B.1).
	AppliedAt time.Time

	// Patch names the patch these transform lists came from, for the record's
	// §9.7 patch.source / patch.digest. A transform list does not carry its own
	// provenance, so a caller that wants a record supplies it.
	Patch RecordPatch

	// Output redirects a single section's result away from its own target
	// (Appendix B.1's `-o`). Empty means "write each target in place". The
	// literal "-" means the CALLER takes the bytes from FileResult.After and
	// writes them itself: a stream is not a path, and a filesystem abstraction
	// has no stdout. Either spelling requires exactly one file section, because
	// one destination cannot receive several results.
	//
	// The record and the reversal patch are unaffected — those are files either
	// way, and where the patched bytes went does not change what happened.
	Output string

	// ReversalPath, if set, writes the reversal patch there after a successful
	// mutation (O40). Opt-in always; empty writes nothing.
	ReversalPath string

	// Reversal turns the reversal patch on with DERIVED names — Appendix B.1's
	// `--reversal` with no value, which names each target's artifact
	// "<target>.undo.hew". It is a second field rather than a sentinel path
	// because a multi-target apply writes one reversal file per target, and one
	// path cannot name several files.
	Reversal bool
}

// FileResult is one target's outcome.
type FileResult struct {
	Target   string
	Changed  bool
	Written  bool
	Reversal string // path of the reversal patch written, or "" (O40)
	Ops      []hew.Transform

	// After is the staged after-image. It is here because a caller that writes
	// the result somewhere hew does not own — `hew apply -o -`, whose
	// destination is a stream — must not have to stage the apply a second time
	// to get the bytes; two stagings are two answers to one question.
	After []byte
}

// SectionError attributes a staging failure to the file section that raised
// it. A CLI reads several patch files into one list of sections and must be
// able to say WHICH file a diagnostic came from (§10.3's "patch.hew:6"); the
// index is the only part of that this layer knows, because a transform list
// carries no file name of its own. It renders as the wrapped error verbatim,
// so a caller that does not care can print it unchanged.
type SectionError struct {
	Index int
	Err   error
	// More is how many LATER file sections also fail to stage. It exists so a
	// caller can say "and 2 more" instead of leaving the reader to discover
	// them one re-run at a time.
	More int
}

// countFailingSections stages the sections after a failure purely to count the
// ones that also fail. It writes nothing and its errors are discarded: the
// report belongs to the first failure, and this is only its scale.
func countFailingSections(fsys afero.Fs, root string, rest []hew.TransformList, opt WriteOptions) int {
	n := 0
	for _, tl := range rest {
		if _, err := stageOne(fsys, root, tl, opt); err != nil {
			n++
		}
	}
	return n
}

func (e *SectionError) Error() string { return e.Err.Error() }
func (e *SectionError) Unwrap() error { return e.Err }

// ApplyFile applies every file section of a parsed patch, honoring §10.5:
// every section stages in memory, and the commit phase runs only if all
// staged successfully. There is no .rej file, no partial output, and NO
// BACKUP FILE — the write is a temp-and-rename, and a failed apply leaves
// every target byte-identical. (Rename atomicity is the afero backend's; see
// this package's doc comment.)
func ApplyFile(fsys afero.Fs, root string, tls []hew.TransformList, opt WriteOptions) ([]FileResult, error) {
	return applyLists(fsys, root, tls, opt)
}

// ApplyTransforms is the same path for a hand-authored or generated .hewt
// document — the `hew apply --transforms` entry point, and the seam the corpus
// pins as `apply-ir`.
//
// It is deliberately the SAME execution as ApplyFile rather than a parallel
// one: §13.5's round-trip identities require notation and IR to reach the same
// bytes, and two write paths that could drift is exactly how they would stop.
// The two names mark the two entry points; the behaviour behind them is one.
func ApplyTransforms(fsys afero.Fs, root string, tls []hew.TransformList, opt WriteOptions) ([]FileResult, error) {
	return applyLists(fsys, root, tls, opt)
}

// staged is one file section's staged result (§10.5's stage phase): everything
// the commit phase needs, computed before a single byte is written.
type staged struct {
	tl       hew.TransformList
	format   hew.FormatID
	before   []byte
	after    []byte
	reversal []byte // rendered reversal patch, or nil
	revPath  string // where it goes, or ""
}

func (s staged) changed() bool { return !bytes.Equal(s.before, s.after) }

func applyLists(fsys afero.Fs, root string, tls []hew.TransformList, opt WriteOptions) ([]FileResult, error) {
	if opt.ReversalPath != "" && len(tls) > 1 {
		return nil, usageErr("--reversal names one file but this patch has %d targets; leave the name off and hew writes one <target>.undo.hew per target (Appendix B.1)", len(tls))
	}
	if opt.Output != "" && len(tls) != 1 {
		return nil, usageErr("-o/--output requires exactly one file section, got %d (Appendix B.1)", len(tls))
	}

	// STAGE. Read, apply, and render every artifact in memory. Any failure
	// here — a stale target, an unreadable file, a reversal the differ cannot
	// express — aborts with not one byte written.
	stages := make([]staged, 0, len(tls))
	for i, tl := range tls {
		s, err := stageOne(fsys, root, tl, opt)
		if err != nil {
			// Nothing is written either way, so the remaining sections are
			// staged anyway — not to apply them, but to tell the reader
			// whether fixing this one finishes the job. Only the FIRST failure
			// is reported in full; the rest are a count, which is the cheap
			// half of the answer and the half a reader acts on.
			//
			// The count is per SECTION, deliberately. Counting failures WITHIN
			// a section would mean evaluating one `test` outside its hunk, and
			// a transform's meaning depends on its siblings there — a paired
			// write converges an assertion that would fail alone — so a
			// per-transform count could report failures that are not real.
			return nil, &SectionError{Index: i, Err: err, More: countFailingSections(fsys, root, tls[i+1:], opt)}
		}
		stages = append(stages, s)
	}

	// The record is BUILT before the commit and WRITTEN after it. Resolving the
	// executed list can fail where the apply did not — an `? absent` assertion
	// on a key-match that matches nothing is satisfied by the applier and has no
	// RFC 6901 pointer at all — and a `--record` run that cannot produce its
	// record must leave the target untouched (§10.5) rather than edit a file and
	// then report failure.
	var recordBytes []byte
	if opt.RecordPath != "" {
		rec, err := buildRecord(stages, opt)
		if err != nil {
			return nil, err
		}
		recordBytes, err = MarshalRecord(rec)
		if err != nil {
			return nil, err
		}
	}

	results := make([]FileResult, len(stages))
	for i, s := range stages {
		results[i] = FileResult{Target: s.tl.Target, Changed: s.changed(), Ops: s.tl.Transform, After: s.after}
	}
	if opt.DryRun {
		return results, nil
	}

	// COMMIT. Pure writes from here down: no parsing, no matching, no diffing.
	// §10.5's honest residual is that a crash between two renames leaves a
	// prefix, and the mitigation is that this window contains nothing else.
	for i, s := range stages {
		dest, write := commitDest(root, s, opt)
		if write {
			if err := WriteAtomic(fsys, dest, s.after); err != nil {
				return results, err
			}
			results[i].Written = true
		}
		if s.reversal != nil {
			if err := WriteAtomic(fsys, filepath.Join(root, s.revPath), s.reversal); err != nil {
				return results, err
			}
			results[i].Reversal = s.revPath
		}
	}
	if recordBytes != nil {
		if err := WriteAtomic(fsys, filepath.Join(root, opt.RecordPath), recordBytes); err != nil {
			return results, err
		}
	}
	return results, nil
}

// commitDest answers where one staged section's bytes go, and whether they go
// anywhere at all.
//
// Three cases. With no `-o`, the bytes go back to the target — and only if
// they CHANGED: a no-op section writes no file, because there is nothing to
// write (§10.2 as amended by O38). With `-o FILE` they go to FILE
// unconditionally, because the caller asked for a file at that path and "your
// patch changed nothing" is not a reason to leave it missing. With `-o -` they
// go nowhere: the caller takes them from FileResult.After.
func commitDest(root string, s staged, opt WriteOptions) (string, bool) {
	switch {
	case opt.Output == "-":
		return "", false
	case opt.Output != "":
		return filepath.Join(root, opt.Output), true
	}
	return filepath.Join(root, s.tl.Target), s.changed()
}

// stageOne reads one target, applies its section in memory, and renders the
// reversal patch if one was asked for.
func stageOne(fsys afero.Fs, root string, tl hew.TransformList, opt WriteOptions) (staged, error) {
	before, format, err := readTarget(fsys, root, tl, opt.Format)
	if err != nil {
		return staged{}, err
	}
	s := staged{tl: tl, format: format, before: before, after: before}

	// A section with no transforms is the no-op patch `hew diff` emits for two
	// identical inputs (§9.4-R8, §10.2 as amended by O38). It is answered
	// without consulting a binding at all: there is nothing to apply, and
	// round-tripping the file through an applier to prove it would risk
	// reformatting a file the patch said nothing about.
	if len(tl.Transform) > 0 {
		apply, ok := applierFor(format)
		if !ok {
			return staged{}, noBinding(tl.Target, format)
		}
		after, aerr := apply(before, tl)
		if aerr != nil {
			return staged{}, aerr
		}
		s.after = after
	}

	if path := reversalPath(tl.Target, opt); path != "" && s.changed() {
		rev, rerr := ReversalPatch(s.after, s.before, format, tl.Target)
		if rerr != nil {
			return staged{}, rerr
		}
		s.reversal, s.revPath = rev, path
	}
	return s, nil
}

// reversalPath is Appendix B.1's naming rule: an explicit name when the caller
// gave one, "<target>.undo.hew" when the flag carried no value, and "" when the
// reversal was not asked for — it is opt-in always, so no flag means no file.
func reversalPath(target string, opt WriteOptions) string {
	switch {
	case opt.ReversalPath != "":
		return opt.ReversalPath
	case opt.Reversal:
		return target + ".undo.hew"
	}
	return ""
}

// ReversalPatch renders diff(after → before) as a real .hew file (O40): the
// same differ and renderer `hew diff` uses, at §9.4-R2's default context
// radius, with the OLD side — which here is the POST-apply image — as the
// target (§9.4-R7, O39). Applying it is the undo.
//
// It is not a backup and does not weaken §10.5's no-backup rule: a backup is a
// copy of a file, opaque and whole; a reversal patch is a statement of what to
// undo, reviewable in a pull request, and it refuses to apply if the file has
// drifted since — which a backup would happily clobber.
func ReversalPatch(after, before []byte, format hew.FormatID, target string) ([]byte, error) {
	tl, err := hewdiff.Diff(after, before, format, hew.DiffOptions{
		Target:  target,
		Context: hew.ContextDefault,
	})
	if err != nil {
		return nil, err
	}
	return hew.Render(tl, hew.RenderOptions{
		Context:  hew.ContextDefault,
		Fragment: hew.FragmentNative,
	})
}

// WriteAtomic is §10.5's write, kept on hewfs as A.8 spells it. The commit
// itself lives in the core as hew.WriteAtomic and is shared with Doc.Write:
// two file-writing surfaces, one commit rule.
func WriteAtomic(fsys afero.Fs, path string, data []byte) error {
	return hew.WriteAtomic(fsys, path, data)
}

// readTarget reads one file section's target and settles its format (§8.0):
// the caller's override wins, then the section's own declaration, then the
// target's extension.
func readTarget(fsys afero.Fs, root string, tl hew.TransformList, override hew.FormatID) ([]byte, hew.FormatID, error) {
	src, err := afero.ReadFile(fsys, filepath.Join(root, tl.Target))
	if err != nil {
		return nil, "", &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver,
			Target: tl.Target, Detail: err.Error()}
	}
	format := override
	if format == "" {
		format = tl.Format
	}
	if format == "" {
		format, _ = hew.DetectFormat(tl.Target)
	}
	return src, format, nil
}

// buildRecord resolves each staged section against the target as READ. The
// pre-image is the right document to resolve against: it is what the applier
// itself resolved every path through, so `/mcpServers/1` in the record is the
// position the add really took.
func buildRecord(stages []staged, opt WriteOptions) (Record, error) {
	rec := Record{
		Version:   RecordVersion,
		AppliedAt: appliedAt(opt),
		Patch:     opt.Patch,
	}
	for _, s := range stages {
		ops, err := resolveOps(s)
		if err != nil {
			return Record{}, err
		}
		rec.Targets = append(rec.Targets, RecordTarget{
			Target: s.tl.Target, Format: s.format,
			Before: Digest(s.before), After: Digest(s.after),
			Committed: true, Transforms: ops,
		})
	}
	return rec, nil
}

// resolveOps projects one staged section onto §9.2's RFC 6901 form. A no-op
// section resolves to an empty list without opening a document: §10.2's
// amendment says a no-op record carries an empty `transforms` and equal
// digests, "a record that truthfully says nothing happened".
func resolveOps(s staged) ([]hew.ResolvedOp, error) {
	if len(s.tl.Transform) == 0 {
		return nil, nil
	}
	doc, err := documentFor(s.tl.Target, s.format, s.before)
	if err != nil {
		return nil, err
	}
	return hew.Resolve(s.tl, doc)
}

// appliedAt is §9.7's precedence, minus the two environment steps the CLI owns:
// an explicit caller value wins, and otherwise the clock. A library that read
// the environment itself could not implement "the caller wins", because it
// could not tell an unset field from a deliberate one.
func appliedAt(opt WriteOptions) time.Time {
	if !opt.AppliedAt.IsZero() {
		return opt.AppliedAt
	}
	return time.Now()
}

// applierFor is the registry's answer to "can this build apply this format".
// A format with no registered extension and a format whose extension ships no
// applier are the same answer here — not found — because they are the same
// fact for the caller: nothing linked into this binary can do it.
func applierFor(format hew.FormatID) (hew.Applier, bool) {
	b, ok := hew.Lookup(format)
	if !ok || b.Applier == nil {
		return nil, false
	}
	return b.Applier, true
}

// documentFor parses target bytes into the read-only view Resolve projects
// against, through the same registry binding the applier came from.
func documentFor(target string, format hew.FormatID, src []byte) (hew.Document, error) {
	b, ok := hew.Lookup(format)
	if !ok || b.Document == nil {
		return nil, noBinding(target, format)
	}
	doc, err := b.Document(target, src)
	if err != nil {
		if he, ok := hewerr.As(err); ok {
			he.Target = target
		}
		return nil, err
	}
	return doc, nil
}

func noBinding(target string, format hew.FormatID) error {
	if format == "" {
		return &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentApplier, Target: target,
			Detail: "no format declared and none inferred from the target's extension (§8.0)"}
	}
	return &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentApplier, Target: target,
		Detail: fmt.Sprintf("no binding for format %q (P3)", format)}
}

// UsageError is an argument combination this layer refuses before touching a
// file. It is distinct from a hew error code because Appendix B.3 gives usage
// trouble exit 2 without a HEW code to name.
type UsageError struct{ Detail string }

func (e *UsageError) Error() string { return e.Detail }

func usageErr(format string, args ...any) error {
	return &UsageError{Detail: fmt.Sprintf(format, args...)}
}

// IsUsage reports whether err is a UsageError.
func IsUsage(err error) bool {
	var ue *UsageError
	return errors.As(err, &ue)
}

func ioErr(path, format string, args ...any) error {
	return &hewerr.Error{
		Code:      hewerr.CodeTargetPath,
		Component: hewerr.ComponentResolver,
		Target:    path,
		Detail:    fmt.Sprintf(format, args...),
	}
}
