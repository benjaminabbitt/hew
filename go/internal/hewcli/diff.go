package hewcli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/hewdiff"
	"github.com/benjaminabbitt/hew/go/internal/hewsource"
)

type diffFlags struct {
	context       int
	format        string
	keyFields     []string
	transformsOut string
	output        string
	sources       []string
}

// contextHelp is the sentence Appendix B.2 requires the help text to carry:
// context lines compile into assertions (§9.0), so the radius is a strictness
// dial, not a verbosity dial, and a user reaching for -U0 to make a patch
// smaller is quietly disabling drift detection.
const contextHelp = "-U/--context sets the sibling context radius (default 1, or `all`). " +
	"It is a STRICTNESS dial, not a verbosity dial: context lines compile into assertions, " +
	"so a smaller radius makes the patch weaker at detecting drift."

func parseDiffArgs(args []string) (*diffFlags, error) {
	f := &diffFlags{context: hew.ContextDefault}
	need := func(i int, name string) (string, error) {
		if i+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", name)
		}
		return args[i+1], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-U", "--context":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			n, err := parseContext(v)
			if err != nil {
				return nil, err
			}
			f.context = n
			i++
		case "--format":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.format = v
			i++
		case "--key-fields":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.keyFields = splitFields(v)
			if len(f.keyFields) == 0 {
				return nil, fmt.Errorf("--key-fields needs at least one field name")
			}
			i++
		case "--transforms-out":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.transformsOut = v
			i++
		case "-o", "--output":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			f.output = v
			i++
		case "-":
			f.sources = append(f.sources, "-")
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				return nil, fmt.Errorf("unknown flag %q", a)
			}
			f.sources = append(f.sources, a)
		}
	}
	if len(f.sources) != 2 {
		return nil, fmt.Errorf("diff takes exactly two source descriptors (<old> <new>), got %d", len(f.sources))
	}
	return f, nil
}

// parseContext reads -U's argument. "all" is its own spelling because "every
// sibling" is not a number, and 0 maps to the named ContextNone rather than to
// the zero value, which means "unset, use the default".
func parseContext(v string) (int, error) {
	if v == "all" {
		return hew.ContextAll, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("--context takes a non-negative integer or \"all\", got %q", v)
	}
	if n == 0 {
		return hew.ContextNone, nil
	}
	return n, nil
}

func splitFields(v string) []string {
	var out []string
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// runDiff implements `hew diff <old> <new>` (Appendix B.2). Its exit contract
// is two-valued: 0 when a patch was produced (an empty one included, because
// "these two documents are the same" is an answer, not a failure) and 2 for
// trouble. There is no exit 1 here — nothing was going to be applied.
func runDiff(args []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int {
	f, err := parseDiffArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "hew: usage error: %v\n", err)
		fmt.Fprintln(stderr, "hew: "+contextHelp)
		return 2
	}

	resolver := hewsource.NewResolver(dir, stdin)
	oldBytes, oldLabel, err := resolver.Resolve(f.sources[0])
	if err != nil {
		return diffSourceErr(stderr, f.sources[0], err)
	}
	newBytes, newLabel, err := resolver.Resolve(f.sources[1])
	if err != nil {
		return diffSourceErr(stderr, f.sources[1], err)
	}

	// §9.4-R7 / Appendix B.2.1, ruling O39: the `--- ` line names the OLD
	// side. The patch is a function FROM old — RT1 says so in symbols,
	// apply(parse(render(diff(old, new))), old) == new — so naming the new side
	// would stamp a file the applier never opens. The labels arrive from the
	// resolver, which is also where B.2.1's git-anchor corollary is already
	// satisfied: `HEAD:config.yaml` resolves with the label `config.yaml`,
	// because a `--- ` line is a target path and a revision is not one.
	//
	// A stdin old side has no name to give, so the new side's label stands in.
	// Both sides stdin never reaches here: the resolver refuses the second "-"
	// as a usage error (§9.5).
	target := oldLabel
	if target == "-" {
		target = newLabel
	}
	format := hew.FormatID(f.format)
	if format == "" {
		format = detectFormat(target)
	}
	if format == "" {
		format = detectFormat(newLabel)
	}

	var notes []string
	tl, err := hewdiff.Diff(oldBytes, newBytes, format, hew.DiffOptions{
		KeyFields: f.keyFields,
		Context:   f.context,
		Target:    target,
		Note:      func(s string) { notes = append(notes, s) },
	})
	if err != nil {
		printErr(stderr, err)
		return 2
	}

	// Two identical inputs produce a PREAMBLE-ONLY patch, not zero bytes
	// (§9.4-R8, Appendix B.2.2, ruling O38): `hew: 1`, the `--- ` line, and no
	// hunks. This code used to return here without writing anything, which made
	// `hew diff a b > p.hew && hew apply p.hew` an error the day the answer was
	// "no change" — zero bytes is a file apply refuses as HEW001. Nothing
	// special happens below: the renderer writes a hunkless list as its
	// preamble and target line, which is exactly the artifact wanted.
	var out []byte
	dest := f.output
	if f.transformsOut != "" {
		out, err = hew.MarshalTransformStream([]hew.TransformList{tl})
		dest = f.transformsOut
	} else {
		out, err = hew.Render(tl, hew.RenderOptions{
			Context:  f.context,
			Comment:  strings.Join(notes, "\n"),
			Fragment: hew.FragmentNative,
		})
	}
	if err != nil {
		printErr(stderr, err)
		return 2
	}
	if dest == "" {
		dest = "-"
	}
	if werr := writeOutput(dir, dest, out, stdout); werr != nil {
		fmt.Fprintf(stderr, "hew: %s: %v\n", dest, werr)
		return 2
	}
	return 0
}

func diffSourceErr(stderr io.Writer, descriptor string, err error) int {
	var ue *hewsource.UsageError
	if errors.As(err, &ue) {
		fmt.Fprintf(stderr, "hew: usage error: %v\n", ue)
		return 2
	}
	fmt.Fprintf(stderr, "hew: %s: %v\n", descriptor, err)
	return 2
}
