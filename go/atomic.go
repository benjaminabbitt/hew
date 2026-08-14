package hew

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/spf13/afero"
)

// defaultFileMode is the mode a target created by hew gets. An EXISTING
// target keeps its own mode: a patch tool that silently widened permissions on
// every apply would be a security regression disguised as a write.
const defaultFileMode fs.FileMode = 0o644

// WriteAtomic is §10.5's commit: stage a sibling temporary file, then rename it
// over the target, so a reader sees either the old bytes or the new ones and
// never a half-written file. No backup file is left behind, and a failed write
// leaves the target byte-identical.
//
// The temporary file is a SIBLING of the target deliberately — a rename across
// filesystems is not atomic anywhere, and a temp directory is a different
// filesystem often enough that using one would silently degrade the guarantee.
// An existing target keeps its own mode; a new one gets 0644.
//
// It is exported, and it is the ONLY implementation: Doc.Write and hewfs's
// ApplyFile must commit identically, so a change to the commit rule cannot land
// in one and not the other. hewfs.WriteAtomic delegates here rather than
// keeping a parallel copy.
//
// A Chmod failure is deliberately NOT fatal. The bytes are staged and the
// rename still carries them onto the target; a backend with no permission model
// (afero's MemMapFs, object-store shims) would otherwise turn a completed write
// into a spurious error.
func WriteAtomic(fsys afero.Fs, path string, data []byte) error {
	mode := defaultFileMode
	if st, err := fsys.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := afero.TempFile(fsys, filepath.Dir(path), "."+filepath.Base(path)+".hew*")
	if err != nil {
		return writeErr(path, fmt.Errorf("creating a temporary file beside the target: %w", err))
	}
	staged := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = fsys.Remove(staged)
		return writeErr(path, fmt.Errorf("writing the temporary file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = fsys.Remove(staged)
		return writeErr(path, fmt.Errorf("closing the temporary file: %w", err))
	}
	_ = fsys.Chmod(staged, mode)
	if err := fsys.Rename(staged, path); err != nil {
		_ = fsys.Remove(staged)
		return writeErr(path, fmt.Errorf("renaming the temporary file over the target: %w", err))
	}
	return nil
}
