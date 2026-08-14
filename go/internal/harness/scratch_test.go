package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCopyCaseCopiesContentAndPermissions(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	files := map[string]struct {
		content string
		perm    os.FileMode
	}{
		"case.yaml":   {"name: json/c\n", 0o644},
		"patch.hew":   {"@ config.json\n+ a: 1\n", 0o600},
		"run.sh":      {"#!/bin/sh\n", 0o755},
		"target.json": {"", 0o644},
	}
	for name, f := range files {
		if err := os.WriteFile(filepath.Join(src, name), []byte(f.content), f.perm); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(src, name), f.perm); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(src, "sub", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "deeper", "note.txt"), []byte("deep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyCase(src, dst); err != nil {
		t.Fatalf("CopyCase: %v", err)
	}

	for name, f := range files {
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("reading copy of %s: %v", name, err)
		}
		if string(got) != f.content {
			t.Errorf("%s content = %q, want %q", name, got, f.content)
		}
		st, err := os.Stat(filepath.Join(dst, name))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm() != f.perm {
			t.Errorf("%s perm = %04o, want %04o", name, st.Mode().Perm(), f.perm)
		}
	}
	deep, err := os.ReadFile(filepath.Join(dst, "sub", "deeper", "note.txt"))
	if err != nil || string(deep) != "deep\n" {
		t.Errorf("nested file not copied: %q, %v", deep, err)
	}
}

// TestCopyCaseOverwritesExistingScratchFile: scratch dirs are reused across a
// case's seams in some frontends; a stale longer file must be truncated.
func TestCopyCaseOverwritesExistingScratchFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "target.json"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "target.json"), []byte("much longer stale content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyCase(src, dst); err != nil {
		t.Fatalf("CopyCase: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Errorf("stale content survived: %q", got)
	}
}

func TestCopyCaseRejectsNonRegularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "real.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(src, "real.json"), filepath.Join(src, "link.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := CopyCase(src, dst)
	if err == nil {
		t.Fatal("copying a symlinked corpus file must be a corpus error")
	}
	if !strings.Contains(err.Error(), "corpus error") || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error %q must name the corpus problem", err)
	}
	if !strings.Contains(err.Error(), "link.json") {
		t.Errorf("error %q must name the offending path", err)
	}
}

func TestCopyCaseMissingSource(t *testing.T) {
	err := CopyCase(filepath.Join(t.TempDir(), "nope"), t.TempDir())
	if err == nil {
		t.Fatal("copying a missing directory must fail")
	}
}

// TestCopyCaseUnwritableDestination surfaces the write-side error path.
func TestCopyCaseUnwritableDestination(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dst, 0o755) })
	if err := CopyCase(src, dst); err == nil {
		t.Fatal("writing into a read-only scratch dir must fail")
	}
}

func TestSnapshotTopLevelRegularFilesOnly(t *testing.T) {
	dir := t.TempDir()
	want := map[string]string{
		"case.yaml":   "name: json/c\n",
		"target.json": "{\"a\": 1}\n",
		"empty":       "",
	}
	for name, content := range want {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.json"), []byte("nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) != len(want) {
		t.Errorf("snapshot has %d entries (%v), want %d", len(snap), keysOf(snap), len(want))
	}
	for name, content := range want {
		got, ok := snap[name]
		if !ok {
			t.Errorf("snapshot missing %s", name)
			continue
		}
		if string(got) != content {
			t.Errorf("%s = %q, want %q", name, got, content)
		}
	}
	if _, ok := snap["sub"]; ok {
		t.Error("snapshot must not contain directories")
	}
	if _, ok := snap["nested.json"]; ok {
		t.Error("snapshot must not recurse into subdirectories")
	}
}

func TestSnapshotEmptyDir(t *testing.T) {
	snap, err := Snapshot(t.TempDir())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) != 0 {
		t.Errorf("snapshot = %v, want empty", keysOf(snap))
	}
}

func TestSnapshotMissingDir(t *testing.T) {
	snap, err := Snapshot(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("snapshotting a missing directory must fail")
	}
	if snap != nil {
		t.Errorf("snapshot = %v, want nil on error", keysOf(snap))
	}
}

// TestSnapshotUnreadableFile: a listed but unreadable file is an error, not a
// silently absent key.
func TestSnapshotUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	if _, err := Snapshot(dir); err == nil {
		t.Fatal("an unreadable file must fail the snapshot")
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
