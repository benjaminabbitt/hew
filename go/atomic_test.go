package hew

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// The happy paths and the temp-file-creation failure are pinned in hewfs, whose
// WriteAtomic is this one. What is pinned HERE is the rest of §10.5's promise:
// a failure AFTER the temporary file exists still leaves the target identical
// and still removes the staging file, on every one of the three branches that
// can reach that state.

var errInjected = errors.New("injected")

// renameFailFs stages fine and then refuses the rename — the last step, and the
// only one where the target is moments from being replaced.
type renameFailFs struct{ afero.Fs }

func (f renameFailFs) Rename(_, _ string) error { return errInjected }

// writeFailFs hands back a file that accepts no bytes.
type writeFailFs struct{ afero.Fs }

func (f writeFailFs) OpenFile(name string, flag int, perm fs.FileMode) (afero.File, error) {
	fh, err := f.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return writeFailFile{fh}, nil
}

type writeFailFile struct{ afero.File }

func (writeFailFile) Write([]byte) (int, error) { return 0, errInjected }

func TestWriteAtomicRenameFailureLeavesNoTrace(t *testing.T) {
	base := afero.NewMemMapFs()
	if err := afero.WriteFile(base, "/w/f.txt", []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteAtomic(renameFailFs{base}, "/w/f.txt", []byte("replacement"))
	if err == nil {
		t.Fatal("a failed rename must be reported")
	}
	if he, ok := hewerr.As(err); !ok || he.Code != hewerr.CodeTargetPath {
		t.Fatalf("want HEW003, got %v", err)
	}
	if !strings.Contains(err.Error(), "renaming") {
		t.Errorf("error = %q, want it to name the failed step", err.Error())
	}
	if got := readFs(t, base, "/w/f.txt"); got != "original" {
		t.Errorf("target = %q, want the original bytes", got)
	}
	if left := stagedFiles(t, base, "/w"); len(left) != 0 {
		t.Errorf("staged files left behind: %v", left)
	}
}

func TestWriteAtomicWriteFailureLeavesNoTrace(t *testing.T) {
	base := afero.NewMemMapFs()
	if err := afero.WriteFile(base, "/w/f.txt", []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteAtomic(writeFailFs{base}, "/w/f.txt", []byte("replacement"))
	if err == nil {
		t.Fatal("a failed write must be reported")
	}
	if !strings.Contains(err.Error(), "writing") {
		t.Errorf("error = %q, want it to name the failed step", err.Error())
	}
	if got := readFs(t, base, "/w/f.txt"); got != "original" {
		t.Errorf("target = %q, want the original bytes", got)
	}
	if left := stagedFiles(t, base, "/w"); len(left) != 0 {
		t.Errorf("staged files left behind: %v", left)
	}
}

func readFs(t *testing.T, fsys afero.Fs, path string) string {
	t.Helper()
	b, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stagedFiles reports the leftover ".<name>.hew*" staging files in dir.
func stagedFiles(t *testing.T, fsys afero.Fs, dir string) []string {
	t.Helper()
	ents, err := afero.ReadDir(fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".hew") {
			out = append(out, e.Name())
		}
	}
	return out
}
