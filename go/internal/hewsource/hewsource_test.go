package hewsource

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitAnchor(t *testing.T) {
	cases := []struct {
		in        string
		rev, path string
		ok        bool
		why       string
	}{
		{"HEAD:config.yaml", "HEAD", "config.yaml", true, "the canonical anchor"},
		{"main:sub/dir/c.yaml", "main", "sub/dir/c.yaml", true, "a path with slashes"},
		{"HEAD~3:.mcp.json", "HEAD~3", ".mcp.json", true, "a revision expression"},
		{"abc1234:a:b.yaml", "abc1234", "a:b.yaml", true, "split at the FIRST colon, as git does"},
		{"config.yaml", "", "", false, "a plain path"},
		{"./weird:name.yaml", "", "", false, `git's own "./" disambiguation`},
		{"../up:name.yaml", "", "", false, "a parent-relative literal path"},
		{"/abs:name.yaml", "", "", false, "an absolute literal path"},
		{":leading", "", "", false, "no revision"},
		{"trailing:", "", "", false, "no path"},
	}
	for _, c := range cases {
		rev, path, ok := splitAnchor(c.in)
		if ok != c.ok || rev != c.rev || path != c.path {
			t.Fatalf("%s: splitAnchor(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.why, c.in, rev, path, ok, c.rev, c.path, c.ok)
		}
	}
}

func TestResolvePlainPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, label, err := NewResolver(dir, strings.NewReader("")).Resolve("config.yaml")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != "a: 1\n" || label != "config.yaml" {
		t.Fatalf("got %q / %q", got, label)
	}
}

func TestResolveMissingPath(t *testing.T) {
	_, _, err := NewResolver(t.TempDir(), strings.NewReader("")).Resolve("nope.yaml")
	if err == nil {
		t.Fatal("want an error")
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		t.Fatalf("a missing file is not a usage error: %v", err)
	}
}

func TestResolveStdinOnlyOnce(t *testing.T) {
	r := NewResolver(t.TempDir(), strings.NewReader("body"))
	got, label, err := r.Resolve("-")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != "body" || label != "-" {
		t.Fatalf("got %q / %q", got, label)
	}
	_, _, err = r.Resolve("-")
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("a second stdin must be a usage error, got %v", err)
	}
	if !strings.Contains(ue.Error(), "at most once") {
		t.Fatalf("message: %q", ue)
	}
}

func TestResolveEmptyDescriptor(t *testing.T) {
	_, _, err := NewResolver(t.TempDir(), strings.NewReader("")).Resolve("")
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("want a usage error, got %v", err)
	}
}

// A literal path with a colon is reached through "./", and it must NOT be
// handed to git — the file is right there.
func TestResolveColonPathViaDotSlash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "weird:name.yaml"), []byte("x: 1\n"), 0o644); err != nil {
		t.Skipf("filesystem rejects a colon in a filename: %v", err)
	}
	r := &gitResolver{dir: dir, stdin: strings.NewReader(""),
		lookPath: func(string) (string, error) { return "", errors.New("no git") },
		run:      func(string, ...string) ([]byte, error) { t.Fatal("git must not be invoked"); return nil, nil },
	}
	got, label, err := r.Resolve("./weird:name.yaml")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != "x: 1\n" || label != "./weird:name.yaml" {
		t.Fatalf("got %q / %q", got, label)
	}
}

// §9.5: git absent plus a ":" descriptor is a usage error, never a silent
// fallback to treating the descriptor as a filename.
func TestResolveGitAnchorWithoutGitIsAUsageError(t *testing.T) {
	r := &gitResolver{dir: t.TempDir(), stdin: strings.NewReader(""),
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		run:      func(string, ...string) ([]byte, error) { t.Fatal("git must not be invoked"); return nil, nil },
	}
	_, _, err := r.Resolve("HEAD:config.yaml")
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("want a usage error, got %v", err)
	}
	for _, want := range []string{"git anchor", "not on PATH", "./HEAD:config.yaml"} {
		if !strings.Contains(ue.Error(), want) {
			t.Fatalf("message %q lacks %q", ue, want)
		}
	}
}

func TestResolveGitAnchorInvokesPlumbing(t *testing.T) {
	var got []string
	var gotDir string
	r := &gitResolver{dir: "/work", stdin: strings.NewReader(""),
		lookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		run: func(dir string, args ...string) ([]byte, error) {
			gotDir, got = dir, args
			return []byte("blob\n"), nil
		},
	}
	content, label, err := r.Resolve("HEAD:sub/config.yaml")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(content) != "blob\n" {
		t.Fatalf("content = %q", content)
	}
	// The label is the PATH half: a patch produced from HEAD:config.yaml
	// still targets config.yaml.
	if label != "sub/config.yaml" {
		t.Fatalf("label = %q", label)
	}
	if gotDir != "/work" {
		t.Fatalf("git ran in %q", gotDir)
	}
	if strings.Join(got, " ") != "cat-file blob HEAD:sub/config.yaml" {
		t.Fatalf("git args = %q", got)
	}
}

func TestResolveGitFailureIsReported(t *testing.T) {
	r := &gitResolver{dir: t.TempDir(), stdin: strings.NewReader(""),
		lookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		run:      func(string, ...string) ([]byte, error) { return nil, fmt.Errorf("bad object") },
	}
	_, _, err := r.Resolve("HEAD:missing.yaml")
	if err == nil || !strings.Contains(err.Error(), "bad object") {
		t.Fatalf("got %v", err)
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		t.Fatalf("a git failure is not a usage error: %v", err)
	}
}

// The real subprocess path, exercised against a throwaway repository — the
// mocked tests above cannot catch a wrong plumbing verb.
func TestRunGitAgainstARealRepository(t *testing.T) {
	dir := t.TempDir()
	// Pin identity and mask host git config so the fixture is hermetic.
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull,
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@invalid",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@invalid",
	} {
		t.Setenv(k, v)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.yaml"), []byte("committed: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "."}, {"add", "c.yaml"}, {"commit", "-q", "-m", "base"}} {
		if _, err := runGit(dir, args...); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "c.yaml"), []byte("worktree: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, label, err := NewResolver(dir, strings.NewReader("")).Resolve("HEAD:c.yaml")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != "committed: 1\n" {
		t.Fatalf("the anchor must read the COMMITTED blob, got %q", got)
	}
	if label != "c.yaml" {
		t.Fatalf("label = %q", label)
	}
	if _, _, err := NewResolver(dir, strings.NewReader("")).Resolve("HEAD:absent.yaml"); err == nil {
		t.Fatal("a missing blob must be an error")
	}
}
