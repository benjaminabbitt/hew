// Package hewcli is the hew CLI's implementation (Appendix B), importable so
// the corpus harness's RunCLI hook can drive it in-process instead of
// spawning a subprocess per case.
package hewcli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/afero"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/hewfs"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// This package names no format package. Every format reaches it through the
// registry (Appendix A.6), so which formats the CLI can handle is decided by
// what cmd/hew imports for effect — today `_ ext/all` — and is answerable at
// runtime with hew.Formats(). Before O35 this file held a switch over five
// bindings and a second, JSON-only switch for documentFor; that the two could
// disagree without anyone noticing is the gap a registry closes.

// Run executes argv (without argv0) with relative paths resolved against
// dir, and returns the process exit code (Appendix B.3): 0 applied, 1 did
// not apply (nothing modified), 2 trouble.
//
// env is the process environment, and it is passed as DATA rather than read
// from the process: `hew apply` reads exactly two variables (Appendix B.1),
// both governing the record's applied_at, and a CLI that reached os.Getenv
// directly could not be driven by the corpus harness, whose cases pin those
// two through a manifest `env:` block (§13.4). Nothing else about hew's
// behaviour is reachable through the environment — a patch tool whose EFFECT
// depends on invisible input is the thing this format exists to refuse.
func Run(argv []string, dir string, env map[string]string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		fmt.Fprintln(stderr, usageText)
		return 2
	}
	switch argv[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, usageText)
		return 0
	case "apply":
		return runApply(argv[1:], dir, env, stdin, stdout, stderr)
	case "diff":
		// `hew diff` reads no environment at all: Appendix B.1's two variables
		// govern the application record, and diff writes none.
		return runDiff(argv[1:], dir, stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "hew: unknown command %q\n", argv[0])
		fmt.Fprintln(stderr, usageText)
		return 2
	}
}

// usageText is the whole of hew's help. It lists the flags because two of them
// — --ops and --record — are the troubleshooting affordances, and a reader who
// cannot discover them is left guessing at a failure they could have inspected.
const usageText = `hew: structure-aware patching

usage:
  hew apply [flags] <patch.hew>...   apply a patch
  hew diff  [flags] <old> <new>      write a patch between two documents

apply flags:
  -i, --in-place        write the result over the target (the default when a
                        patch names its target and no -o is given)
  -o, --output FILE     write to FILE instead; "-" writes to stdout
      --transforms      read a .hewt transform list instead of .hew notation
      --ops             print the RESOLVED RFC 6901 op list and write nothing
      --record FILE     write the §9.7 application record to FILE
      --reversal[=FILE] write an undo patch; with no FILE, <target>.undo.hew
      --format FORMAT   override format detection for every target
      --format-out MODE diagnostics on failure: "human" (default) or "json"
  -q, --quiet           suppress progress output

diff flags:
  -o, --output FILE     write the patch to FILE instead of stdout
      --format FORMAT   override format detection
      --context N       context radius (default 1)

exit codes:
  0  applied
  1  did not apply, and NOTHING was written
  2  the patch, the target or the invocation was unusable`

// patchInput is the patch as read, retained because §9.7's record ties itself
// to the exact bytes that produced it (patch.digest).
type patchInput struct {
	source string // the path as given, or "-" for stdin
	bytes  []byte
}

type applyFlags struct {
	target          string
	inPlace         bool
	output          string
	root            string
	transformsFiles []string
	patchFiles      []string
	record          string
	dryRun          bool
	ops             bool
	transformsOut   string
	format          string
	quiet           bool
	// formatOut selects the diagnostic channel on failure (§10.3): "human"
	// (default) or "json", one object per error on stdout.
	formatOut string
	// reversal and reversalPath are `--reversal [FILE]` (O40): the flag is
	// opt-in and its value optional, so "was it given" and "with what name"
	// are two facts.
	reversal     bool
	reversalPath string
}

func parseApplyArgs(args []string) (*applyFlags, error) {
	f := &applyFlags{root: "."}
	need := func(i int, name string) (string, error) {
		if i+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", name)
		}
		return args[i+1], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-t", "--target":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.target = v
			i++
		case "-i", "--in-place":
			f.inPlace = true
		case "-o", "--output":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.output = v
			i++
		case "-R", "--root":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.root = v
			i++
		case "--transforms":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.transformsFiles = append(f.transformsFiles, v)
			i++
		case "--record":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.record = v
			i++
		case "--reversal":
			// `--reversal` takes an OPTIONAL file name (Appendix B.1), which
			// makes "is the next argument my value or the patch?" a real
			// question. The rule is the only one that can read the corpus's own
			// `--reversal target.yaml.undo.hew patch.hew`: the next argument is
			// the value unless there is none or it is a flag. A caller who
			// wants the derived name with a patch following writes
			// `--reversal=` or puts the flag last.
			f.reversal = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				f.reversalPath = args[i+1]
				i++
			}
		case "--dry-run":
			f.dryRun = true
		case "--ops":
			f.ops = true
		case "--transforms-out":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.transformsOut = v
			i++
		case "--format":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.format = v
			i++
		case "--format-out":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			switch v {
			case "human", "json":
				f.formatOut = v
			default:
				return nil, usageErr("--format-out takes %q or %q, got %q", "human", "json", v)
			}
			i++
		case "-q", "--quiet":
			f.quiet = true
		case "-":
			f.patchFiles = append(f.patchFiles, "-")
		default:
			// `--reversal=FILE` is the attached spelling, and `--reversal=` is
			// how a caller asks for the derived name with a patch still to
			// come on the command line.
			if v, ok := strings.CutPrefix(a, "--reversal="); ok {
				f.reversal = true
				f.reversalPath = v
				continue
			}
			if strings.HasPrefix(a, "-") && a != "-" {
				return nil, fmt.Errorf("unknown flag %q", a)
			}
			f.patchFiles = append(f.patchFiles, a)
		}
	}
	return f, nil
}

func runApply(args []string, dir string, env map[string]string, stdin io.Reader, stdout, stderr io.Writer) int {
	f, err := parseApplyArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "hew: usage error: %v\n", err)
		return 2
	}
	// §9.7's pin is read here, at the boundary, and checked BEFORE anything is
	// read or written: a malformed HEW_APPLIED_AT or SOURCE_DATE_EPOCH is exit
	// 2, never a silent fallback to the clock, because a pin that quietly does
	// not pin is worse than no pin — the artifact still looks reproducible.
	appliedAt, err := resolveAppliedAt(env)
	if err != nil {
		fmt.Fprintf(stderr, "hew: usage error: %v\n", err)
		return 2
	}
	if len(f.transformsFiles) > 0 && len(f.patchFiles) > 0 {
		fmt.Fprintln(stderr, "hew: usage error: --transforms is mutually exclusive with positional .hew patch arguments (Appendix B.1)")
		return 2
	}
	if len(f.transformsFiles) == 0 && len(f.patchFiles) == 0 {
		fmt.Fprintln(stderr, "hew: usage error: no patch given (positional .hew file(s), \"-\" for stdin, or --transforms)")
		return 2
	}

	inputs := f.transformsFiles
	fromTransforms := len(inputs) > 0
	if !fromTransforms {
		inputs = f.patchFiles
	}
	// §9.7's record names ONE patch source and one digest of it. Rather than
	// invent a spelling for several, refuse the combination loudly.
	if f.record != "" && len(inputs) > 1 {
		fmt.Fprintln(stderr, "hew: usage error: --record names a single patch source (§9.7); pass one patch per invocation")
		return 2
	}

	var tls []hew.TransformList
	var read []patchInput
	// from[i] names the patch file section i was read from, so a diagnostic
	// can cite "patch.hew:6" rather than a bare line number (§10.3).
	var from []string
	for _, in := range inputs {
		src, rerr := readInput(dir, in, stdin)
		if rerr != nil {
			fmt.Fprintf(stderr, "hew: %s: %v\n", in, rerr)
			return 2
		}
		read = append(read, patchInput{source: in, bytes: src})
		var more []hew.TransformList
		var perr error
		if fromTransforms {
			more, perr = hew.UnmarshalTransformStream(src)
		} else {
			more, perr = hew.Parse(src)
		}
		if perr != nil {
			printDiag(stdout, stderr, f.formatOut, withPatchFile(perr, in))
			return exitFor(perr)
		}
		tls = append(tls, more...)
		for range more {
			from = append(from, in)
		}
	}

	if f.target != "" {
		if len(tls) != 1 {
			fmt.Fprintln(stderr, "hew: usage error: --target requires exactly one file section")
			return 2
		}
		tls[0].Target = f.target
	}
	if f.format != "" {
		for i := range tls {
			tls[i].Format = hew.FormatID(f.format)
		}
	}

	if f.ops {
		return runOps(tls, from, dir, stdout, stderr, f.formatOut)
	}
	if f.transformsOut != "" {
		out, merr := hew.MarshalTransformStream(tls)
		if merr != nil {
			printDiag(stdout, stderr, f.formatOut, merr)
			return exitFor(merr)
		}
		if werr := writeOutput(dir, f.transformsOut, out, stdout); werr != nil {
			fmt.Fprintf(stderr, "hew: %s: %v\n", f.transformsOut, werr)
			return 2
		}
		return 0
	}

	// Everything below the flags is hewfs's (Appendix A.8): staging, §10.5's
	// atomic temp-and-rename commit, the record, and the reversal patch. What
	// stays here is what a filesystem boundary has no business knowing — which
	// patch FILE a section came from (§10.3's provenance line), and how to
	// write to a stream.
	opt := hewfs.WriteOptions{
		DryRun:       f.dryRun,
		Format:       hew.FormatID(f.format),
		RecordPath:   f.record,
		AppliedAt:    appliedAt,
		Patch:        patchProvenance(read),
		Output:       f.output,
		Reversal:     f.reversal,
		ReversalPath: f.reversalPath,
	}
	// The two entry points are the same execution behind two names (Appendix
	// A.8), and the CLI calls the one that describes where its lists came from,
	// so a reader of either side sees the same two words the spec uses.
	apply := hewfs.ApplyFile
	if fromTransforms {
		apply = hewfs.ApplyTransforms
	}
	results, aerr := apply(afero.NewOsFs(), dir, tls, opt)
	if aerr != nil {
		return reportApplyErr(stdout, stderr, f.formatOut, aerr, from)
	}
	if f.output == "-" {
		for _, r := range results {
			stdout.Write(r.After)
		}
	}
	return 0
}

// patchProvenance is §9.7's patch.source / patch.digest: the ONE patch the
// record names, and a digest of the bytes exactly as read. runApply has already
// refused --record with several patch sources, so the first input is the only
// one.
func patchProvenance(read []patchInput) hewfs.RecordPatch {
	if len(read) == 0 {
		return hewfs.RecordPatch{Source: "-"}
	}
	return hewfs.RecordPatch{Source: read[0].source, Digest: hewfs.Digest(read[0].bytes)}
}

// reportApplyErr prints a write-path failure in the §10.3 shape and returns its
// exit code. A staging failure knows which file section raised it, and `from`
// turns that index into the patch file's name — the difference between
// "patch.hew:6" and a bare line number.
func reportApplyErr(stdout, stderr io.Writer, mode string, err error, from []string) int {
	if hewfs.IsUsage(err) {
		fmt.Fprintf(stderr, "hew: usage error: %v\n", err)
		return 2
	}
	var se *hewfs.SectionError
	more := 0
	if errors.As(err, &se) {
		if se.Index < len(from) {
			err = withPatchFile(err, from[se.Index])
		}
		more = se.More
	}
	printDiag(stdout, stderr, mode, err)
	// The scale of the failure, after the failure itself: a reader who fixes
	// this one should know whether that finishes the job. It goes to stderr
	// even in json mode, because it is about the report rather than in it.
	if more > 0 {
		fmt.Fprintf(stderr, "hew: and %d more file section(s) failed\n", more)
	}
	return exitFor(err)
}

// resolveAppliedAt is §9.7's precedence at the boundary that owns it: the two
// environment spellings, in order, and otherwise the zero time, which hewfs
// reads as "the clock". The library takes a VALUE — a library that read the
// environment itself could not implement the rule that an explicit caller wins.
func resolveAppliedAt(env map[string]string) (time.Time, error) {
	if v := strings.TrimSpace(env["HEW_APPLIED_AT"]); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, fmt.Errorf("HEW_APPLIED_AT %q is not an RFC 3339 timestamp (§9.7)", v)
		}
		return ts.UTC(), nil
	}
	if v := strings.TrimSpace(env["SOURCE_DATE_EPOCH"]); v != "" {
		secs, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("SOURCE_DATE_EPOCH %q is not an integer number of seconds since the Unix epoch (§9.7)", v)
		}
		return time.Unix(secs, 0).UTC(), nil
	}
	return time.Time{}, nil
}

// runOps implements `--ops`: print the RESOLVED RFC 6901 op list (§9.2) for
// every file section and write no target. It reads each target because
// resolution is only meaningful against a concrete document, and it evaluates
// nothing — a stale target still prints, because `--ops` reports addresses,
// not an apply.
// from names the patch file each transform list came from, positionally, so a
// resolve failure can say "named.hew:6" instead of a bare "patch:6" that no
// caller with more than one patch can act on.
func runOps(tls []hew.TransformList, from []string, dir string, stdout, stderr io.Writer, mode string) int {
	var out bytes.Buffer
	patchName := func(i int) string {
		if i < len(from) {
			return from[i]
		}
		return ""
	}
	for i, tl := range tls {
		target, format, rerr := readTarget(dir, tl)
		if rerr != nil {
			printDiag(stdout, stderr, mode, withPatchFile(rerr, patchName(i)))
			return exitFor(rerr)
		}
		doc, derr := documentFor(tl.Target, format, target)
		if derr != nil {
			printDiag(stdout, stderr, mode, withPatchFile(derr, patchName(i)))
			return exitFor(derr)
		}
		ops, oerr := hew.Resolve(tl, doc)
		if oerr != nil {
			printDiag(stdout, stderr, mode, withPatchFile(oerr, patchName(i)))
			return exitFor(oerr)
		}
		out.Write(hew.MarshalResolvedOps(ops))
	}
	stdout.Write(out.Bytes())
	return 0
}

// readTarget reads one file section's target and settles its format (§8.0).
func readTarget(dir string, tl hew.TransformList) ([]byte, hew.FormatID, error) {
	src, err := os.ReadFile(filepath.Join(dir, tl.Target))
	if err != nil {
		return nil, "", &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver,
			Target: tl.Target, Detail: err.Error()}
	}
	format := tl.Format
	if format == "" {
		format = detectFormat(tl.Target)
	}
	return src, format, nil
}

// documentFor parses target bytes into the read-only view Resolve projects
// against. It asks the registry for the same binding the applier came from, so
// a format hew cannot apply cannot silently produce an op list either — which
// is what the two hand-rolled switches could not guarantee.
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

func readInput(dir, path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(filepath.Join(dir, path))
}

func writeOutput(dir, path string, data []byte, stdout io.Writer) error {
	if path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	return os.WriteFile(filepath.Join(dir, path), data, 0o644)
}

// detectFormat is §8.0 over whatever extensions this build linked. The table
// it used to hold moved to the extensions themselves (O48): a copy of it here
// was a second place for a format's own filename claims to be stated, and the
// two could drift.
func detectFormat(path string) hew.FormatID {
	f, _ := hew.DetectFormat(path)
	return f
}

// withPatchFile attributes an error to the patch file it came from, so the
// provenance line reads "patch.hew:6" (§10.3). The library layer cannot fill
// this in: it is handed bytes, not a path.
func withPatchFile(err error, patchFile string) error {
	if he, ok := hewerr.As(err); ok && he.PatchFile == "" {
		he.PatchFile = patchFile
	}
	return err
}

// usageErr is an invocation the CLI refuses before doing any work.
func usageErr(format string, args ...any) error {
	return &hewerr.Error{Code: hewerr.CodeParse, Component: hewerr.ComponentParser,
		Detail: fmt.Sprintf(format, args...)}
}

// printErr writes a hew error to stderr in the §10.3 human diagnostic
// shape.
func printErr(w io.Writer, err error) { printDiag(nil, w, "", err) }

// printDiag writes one diagnostic on the channel the caller asked for: the
// §10.3 human shape on stderr, or one JSON object per error on stdout. The
// two are the SAME facts — json is not a richer mode, it is the same report
// without the prose a consumer would have to parse back out.
func printDiag(stdout, stderr io.Writer, mode string, err error) {
	if mode == "json" && stdout != nil {
		if he, ok := hewerr.As(err); ok {
			if b, jerr := he.JSON(); jerr == nil {
				fmt.Fprintln(stdout, string(b))
				return
			}
		}
	}
	fmt.Fprintln(stderr, "hew: "+strings.TrimPrefix(err.Error(), "hew: "))
}

// exitFor classifies an error into Appendix B.3's exit code: HEW010-HEW041
// mean "did not apply" (1); everything else — parse/target/format/IO
// trouble — means exit 2.
func exitFor(err error) int {
	he, ok := hewerr.As(err)
	if !ok {
		return 2
	}
	switch he.Code {
	case hewerr.CodeStaleTarget, hewerr.CodeAssertionFailed, hewerr.CodeAmbiguousMatch, hewerr.CodeNoMatch,
		hewerr.CodeAlreadyExists, hewerr.CodeInexpressible, hewerr.CodeConflict,
		hewerr.CodeAnchorAmbiguity, hewerr.CodeSurfaceAmbiguity:
		return 1
	default: // HEW001, HEW002, HEW003, HEW021
		return 2
	}
}
