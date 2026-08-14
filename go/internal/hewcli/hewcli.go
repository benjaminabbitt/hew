// Package hewcli is the hew CLI's implementation (Appendix B), importable so
// the corpus harness's RunCLI hook can drive it in-process instead of
// spawning a subprocess per case.
package hewcli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/hewjson"
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

	var tls []hew.TransformList
	if len(f.transformsFiles) > 0 {
		for _, tf := range f.transformsFiles {
			src, rerr := readInput(dir, tf, stdin)
			if rerr != nil {
				fmt.Fprintf(stderr, "hew: %s: %v\n", tf, rerr)
				return 2
			}
			more, perr := hew.UnmarshalTransformStream(src)
			if perr != nil {
				printErr(stderr, perr)
				return exitFor(perr)
			}
			tls = append(tls, more...)
		}
	} else {
		for _, pf := range f.patchFiles {
			src, rerr := readInput(dir, pf, stdin)
			if rerr != nil {
				fmt.Fprintf(stderr, "hew: %s: %v\n", pf, rerr)
				return 2
			}
			more, perr := hew.Parse(src)
			if perr != nil {
				printErr(stderr, perr)
				return exitFor(perr)
			}
			tls = append(tls, more...)
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
		// Appendix A.1's Resolve (abstract IR -> concrete RFC 6901 form) is
		// not implemented in this slice: flagged as deferred in the P2
		// report. --ops needs exactly that projection.
		fmt.Fprintln(stderr, "hew: --ops: resolved op-list projection not implemented (§9.2, deferred to a later slice)")
		return 2
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
	for _, tl := range tls {
		before, rerr := os.ReadFile(filepath.Join(dir, tl.Target))
		if rerr != nil {
			e := &hewerr.Error{Code: hewerr.CodeTargetPath, Component: hewerr.ComponentResolver, Target: tl.Target, Detail: rerr.Error()}
			printErr(stderr, e)
			return 2
		}
		format := tl.Format
		if format == "" {
			format = detectFormat(tl.Target)
		}
		var after []byte
		var aerr error
		switch format {
		case hew.FormatJSON:
			after, aerr = hewjson.Apply(before, tl)
		case "":
			aerr = &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentApplier, Target: tl.Target,
				Detail: "no format declared and none inferred from the target's extension (§8.0)"}
		default:
			aerr = &hewerr.Error{Code: hewerr.CodeUnsupportedFormat, Component: hewerr.ComponentApplier, Target: tl.Target,
				Detail: fmt.Sprintf("no binding for format %q (P3)", format)}
		}
		if aerr != nil {
			printErr(stderr, aerr)
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
		rec := buildRecord(f.patchFiles, results)
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
