package harness

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyCase copies a case directory into dst (runner obligation 1: every seam
// run works on a scratch copy — in-place CLI cases mutate their target).
func CopyCase(srcDir, dst string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("corpus error: %s is not a regular file", path)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer w.Close()
		_, err = io.Copy(w, in)
		return err
	})
}

// Snapshot reads every regular file directly under dir into memory, keyed by
// base name. Taken before a seam run so unchanged-target obligations can be
// asserted byte-exactly afterwards.
func Snapshot(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	snap := make(map[string][]byte, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			return nil, err
		}
		snap[ent.Name()] = b
	}
	return snap, nil
}
