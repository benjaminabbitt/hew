// Package hewsource resolves §9.5's source descriptors to bytes. It is a CLI
// boundary — nothing in the library core imports it, and the core differ has
// zero git awareness (§9.4, Appendix A.7). Keeping resolution here is what
// keeps the library embeddable: an embedder gets a differ over two byte
// slices, not a differ that might shell out.
package hewsource

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// UsageError is a descriptor the CLI must reject with exit 2 (Appendix B.3):
// a malformed descriptor, a second use of stdin, or a git anchor on a machine
// with no git. It is deliberately distinct from an I/O error so the CLI can
// print it as a usage error rather than as a file problem.
type UsageError struct{ Detail string }

func (e *UsageError) Error() string { return e.Detail }

// Resolver turns a descriptor into bytes. The interface is deliberately tiny
// so the core never grows a dependency on it and a test can substitute a map.
type Resolver interface {
	// Resolve accepts "path/to/file", "-" (stdin), or "REV:path" (a git
	// anchor, git's own <tree-ish>:<path> convention). The label is the name
	// the produced patch should carry as its target.
	Resolve(descriptor string) (content []byte, label string, err error)
}

// gitResolver resolves against a working directory, invoking git plumbing as a
// SUBPROCESS for anchors. It never links a git library: the resolver is a
// hundred lines in any language, the subprocess boundary is identical in Go
// and Rust, and linking a git library into a patch tool to read one blob is
// the dependency §9.4's core was designed to avoid.
type gitResolver struct {
	dir       string
	stdin     io.Reader
	stdinUsed bool
	// lookPath and run are indirected so a test can drive both the
	// git-is-missing and the git-is-present paths without a repository.
	lookPath func(string) (string, error)
	run      func(dir string, args ...string) ([]byte, error)
}

// NewResolver builds the resolver the CLI uses. dir is the working directory
// relative paths and git invocations resolve against; stdin backs the "-"
// descriptor, which §9.5 allows at most once per invocation.
func NewResolver(dir string, stdin io.Reader) Resolver {
	return &gitResolver{dir: dir, stdin: stdin, lookPath: exec.LookPath, run: runGit}
}

func (r *gitResolver) Resolve(descriptor string) ([]byte, string, error) {
	switch {
	case descriptor == "":
		return nil, "", &UsageError{"empty source descriptor"}
	case descriptor == "-":
		if r.stdinUsed {
			return nil, "", &UsageError{`"-" (standard input) is legal at most once per invocation (§9.5)`}
		}
		r.stdinUsed = true
		b, err := io.ReadAll(r.stdin)
		if err != nil {
			return nil, "", fmt.Errorf("reading standard input: %w", err)
		}
		return b, "-", nil
	}
	rev, path, isAnchor := splitAnchor(descriptor)
	if !isAnchor {
		b, err := os.ReadFile(filepath.Join(r.dir, descriptor))
		if err != nil {
			return nil, "", err
		}
		return b, descriptor, nil
	}
	// git absent is a usage error, never a silent fallback to treating the
	// descriptor as a filename — a fallback would read a file the user never
	// named, or report "no such file" for a revision that exists (§9.5).
	if _, err := r.lookPath("git"); err != nil {
		return nil, "", &UsageError{fmt.Sprintf(
			"%q is a git anchor (<rev>:<path>) but git is not on PATH; write ./%s to name a literal file with a colon in it (§9.5)",
			descriptor, descriptor)}
	}
	b, err := r.run(r.dir, "cat-file", "blob", rev+":"+path)
	if err != nil {
		return nil, "", fmt.Errorf("resolving git anchor %q: %w", descriptor, err)
	}
	return b, path, nil
}

// splitAnchor decides whether a descriptor is a git anchor, following git's
// own rule: a literal path containing ":" is disambiguated by a leading "./"
// (or "../", or an absolute path — none of which git reads as a tree-ish).
func splitAnchor(descriptor string) (rev, path string, ok bool) {
	if strings.HasPrefix(descriptor, "./") || strings.HasPrefix(descriptor, "../") ||
		strings.HasPrefix(descriptor, "/") {
		return "", "", false
	}
	i := strings.IndexByte(descriptor, ':')
	if i <= 0 || i == len(descriptor)-1 {
		return "", "", false
	}
	return descriptor[:i], descriptor[i+1:], true
}

func runGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return out, nil
}
