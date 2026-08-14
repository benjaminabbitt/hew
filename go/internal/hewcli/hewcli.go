// Package hewcli is the hew CLI's implementation (Appendix B), importable so
// the corpus harness's RunCLI hook can drive it in-process instead of
// spawning a subprocess per case.
package hewcli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/hewjson"
	"github.com/hew-format/hew/hewjsonc"
	"github.com/hew-format/hew/hewtoml"
	"github.com/hew-format/hew/hewyaml"
	"github.com/hew-format/hew/internal/hewerr"
)

// Run executes argv (without argv0) with relative paths resolved against
// dir, and returns the process exit code (Appendix B.3): 0 applied, 1 did
// not apply (nothing modified), 2 trouble.
func Run(argv []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		fmt.Fprintln(stderr, "hew: usage: hew <apply|diff> [flags] ...")
		return 2
	}
	switch argv[0] {
	case "apply":
		return runApply(argv[1:], dir, stdin, stdout, stderr)
	case "diff":
		// P4: the differ is not implemented in this slice. A silent
		// success here would be exactly the failure mode the rest of hew
		// exists to refuse, so this fails loud rather than printing an
		// empty patch.
		fmt.Fprintln(stderr, "hew: diff: not implemented (P4)")
		return 2
	default:
		fmt.Fprintf(stderr, "hew: unknown command %q\n", argv[0])
		return 2
	}
}

// staged is one file section's staged result (§10.5's stage phase).
type staged struct {
	tl     hew.TransformList
	format hew.FormatID
	before []byte
	after  []byte
}

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
			_, err := need(i, a)
			if err != nil {
				return nil, err
			}
			i++ // parsed, not implemented: human-readable stderr only in this slice
		case "-q", "--quiet":
			f.quiet = true
		case "-":
			f.patchFiles = append(f.patchFiles, "-")
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				return nil, fmt.Errorf("unknown flag %q", a)
			}
			f.patchFiles = append(f.patchFiles, a)
		}
	}
	return f, nil
}

func runApply(args []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int {
	f, err := parseApplyArgs(args)
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
			printErr(stderr, withPatchFile(perr, in))
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
		return runOps(tls, dir, stdout, stderr)
	}
	if f.transformsOut != "" {
		out, merr := hew.MarshalTransformStream(tls)
		if merr != nil {
			printErr(stderr, merr)
			return exitFor(merr)
		}
		if werr := writeOutput(dir, f.transformsOut, out, stdout); werr != nil {
			fmt.Fprintf(stderr, "hew: %s: %v\n", f.transformsOut, werr)
			return 2
		}
		return 0
	}

	var results []staged
	for i, tl := range tls {
		before, format, rerr := readTarget(dir, tl)
		if rerr != nil {
			printErr(stderr, rerr)
			return exitFor(rerr)
		}
		var after []byte
		var aerr error
		switch format {
		case hew.FormatJSON:
			after, aerr = hewjson.Apply(before, tl)
		case hew.FormatJSONC:
			after, aerr = hewjsonc.Apply(before, tl)
		case hew.FormatYAML:
			after, aerr = hewyaml.Apply(before, tl)
		case hew.FormatTOML:
			after, aerr = hewtoml.Apply(before, tl)
		case "":
			aerr = &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentApplier, Target: tl.Target,
				Detail: "no format declared and none inferred from the target's extension (§8.0)"}
		default:
			aerr = noBinding(tl.Target, format)
		}
		if aerr != nil {
			printErr(stderr, withPatchFile(aerr, from[i]))
			return exitFor(aerr)
		}
		results = append(results, staged{tl: tl, format: format, before: before, after: after})
	}

	if f.dryRun {
		return 0
	}

	if f.output != "" && len(results) != 1 {
		fmt.Fprintln(stderr, "hew: usage error: -o/--output requires exactly one file section")
		return 2
	}

	// The record is BUILT before the commit and WRITTEN after it. Resolving
	// the executed list can fail where the apply did not — an `? absent`
	// assertion on a key-match that matches nothing is satisfied by the
	// applier and has no RFC 6901 pointer at all — and a `--record` run that
	// cannot produce its record must leave the target untouched (§10.5)
	// rather than edit a file and then report failure.
	var rec applicationRecord
	if f.record != "" {
		var berr error
		rec, berr = buildRecord(read, results)
		if berr != nil {
			printErr(stderr, berr)
			return exitFor(berr)
		}
	}

	// Commit: every section staged successfully above, so every write here
	// happens (§10.5's all-or-nothing already held at the staging boundary).
	for _, r := range results {
		switch {
		case f.output == "-":
			stdout.Write(r.after)
		case f.output != "":
			if werr := os.WriteFile(filepath.Join(dir, f.output), r.after, 0o644); werr != nil {
				fmt.Fprintf(stderr, "hew: %s: %v\n", f.output, werr)
				return 2
			}
		default:
			if werr := os.WriteFile(filepath.Join(dir, r.tl.Target), r.after, 0o644); werr != nil {
				fmt.Fprintf(stderr, "hew: %s: %v\n", r.tl.Target, werr)
				return 2
			}
		}
	}

	if f.record != "" {
		out, merr := marshalRecord(rec)
		if merr != nil {
			fmt.Fprintf(stderr, "hew: --record: %v\n", merr)
			return 2
		}
		if werr := os.WriteFile(filepath.Join(dir, f.record), out, 0o644); werr != nil {
			fmt.Fprintf(stderr, "hew: %s: %v\n", f.record, werr)
			return 2
		}
	}

	return 0
}

// runOps implements `--ops`: print the RESOLVED RFC 6901 op list (§9.2) for
// every file section and write no target. It reads each target because
// resolution is only meaningful against a concrete document, and it evaluates
// nothing — a stale target still prints, because `--ops` reports addresses,
// not an apply.
func runOps(tls []hew.TransformList, dir string, stdout, stderr io.Writer) int {
	var out bytes.Buffer
	for _, tl := range tls {
		target, format, rerr := readTarget(dir, tl)
		if rerr != nil {
			printErr(stderr, rerr)
			return exitFor(rerr)
		}
		doc, derr := documentFor(tl.Target, format, target)
		if derr != nil {
			printErr(stderr, derr)
			return exitFor(derr)
		}
		ops, oerr := hew.Resolve(tl, doc)
		if oerr != nil {
			printErr(stderr, oerr)
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
// against. It dispatches over exactly the formats that have a binding, so a
// format hew cannot apply cannot silently produce an op list either.
func documentFor(target string, format hew.FormatID, src []byte) (hew.Document, error) {
	switch format {
	case hew.FormatJSON:
		doc, err := hewjson.Document(src)
		if err != nil {
			if he, ok := hewerr.As(err); ok {
				he.Target = target
			}
			return nil, err
		}
		return doc, nil
	default:
		return nil, noBinding(target, format)
	}
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

func detectFormat(path string) hew.FormatID {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return hew.FormatJSON
	case ".jsonc":
		return hew.FormatJSONC
	case ".yaml", ".yml":
		return hew.FormatYAML
	case ".toml":
		return hew.FormatTOML
	case ".tf", ".hcl":
		return hew.FormatHCL
	case ".md", ".markdown":
		return hew.FormatMarkdown
	default:
		return ""
	}
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

// printErr writes a hew error to stderr in the §10.3 human diagnostic
// shape.
func printErr(w io.Writer, err error) {
	fmt.Fprintln(w, "hew: "+strings.TrimPrefix(err.Error(), "hew: "))
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
