package hew

import (
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// Rule 1 (A.0): format appears at the open boundary and nowhere else, is
// detected from the NAME (§8.0), and is overridable only by hew.As.
//
// Every test here runs on afero.MemMapFs (O49/O50): a tmpdir-based test where
// an in-memory filesystem serves is a defect.

const toySrc = "port: 8080\nhost: localhost\n"

func memfs(t *testing.T, name, src string) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, name, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return fsys
}

func wantCode(t *testing.T, err error, code hewerr.Code) *hewerr.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got no error", code)
	}
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a hew error %s, got %T: %v", code, err, err)
	}
	if he.Code != code {
		t.Fatalf("want %s, got %s: %v", code, he.Code, err)
	}
	return he
}

func TestOpenDetectsFormatFromPath(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "/etc/config.toy", toySrc)

	doc, err := Open(fsys, "/etc/config.toy")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format() != formatToy {
		t.Fatalf("Format() = %q, want %q", doc.Format(), formatToy)
	}
	if doc.Name() != "/etc/config.toy" {
		t.Fatalf("Name() = %q", doc.Name())
	}
}

func TestOpenDetectsWellKnownName(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "toyrc", toySrc)

	doc, err := Open(fsys, "toyrc")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format() != formatToy {
		t.Fatalf("Format() = %q, want %q", doc.Format(), formatToy)
	}
}

func TestOpenUndetectableNameIsHEW021(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "config.unknown", toySrc)

	_, err := Open(fsys, "config.unknown")
	he := wantCode(t, err, hewerr.CodeUnsupportedFormat)
	if he.Target != "config.unknown" {
		t.Fatalf("error does not name the target: %+v", he)
	}
}

func TestOpenAmbiguousNameIsHEW021(t *testing.T) {
	toyOnly(t)
	// A second format claiming the same extension: §8.0 never guesses between
	// two claimants, and the caller's cue is hew.As.
	Register("toy2", Binding{Detect: DetectRule{Extensions: []string{".toy"}}})
	fsys := memfs(t, "config.toy", toySrc)

	_, err := Open(fsys, "config.toy")
	wantCode(t, err, hewerr.CodeUnsupportedFormat)

	if _, err := Open(fsys, "config.toy", As(formatToy)); err != nil {
		t.Fatalf("As did not resolve the ambiguity: %v", err)
	}
}

func TestOpenMissingFileIsHEW003(t *testing.T) {
	toyOnly(t)
	_, err := Open(afero.NewMemMapFs(), "absent.toy")
	wantCode(t, err, hewerr.CodeTargetPath)
}

func TestOpenReadsContent(t *testing.T) {
	toyOnly(t)
	doc, err := Open(memfs(t, "config.toy", toySrc), "config.toy")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(doc.src); got != toySrc {
		t.Fatalf("Source() = %q, want %q", got, toySrc)
	}
}

func TestOpenBytesDetectsFromName(t *testing.T) {
	toyOnly(t)
	doc, err := OpenBytes("config.toy", []byte(toySrc))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format() != formatToy || doc.Name() != "config.toy" {
		t.Fatalf("OpenBytes: name=%q format=%q", doc.Name(), doc.Format())
	}
}

func TestOpenBytesUndetectableIsHEW021(t *testing.T) {
	toyOnly(t)
	_, err := OpenBytes("config", []byte(toySrc))
	wantCode(t, err, hewerr.CodeUnsupportedFormat)
}

func TestOpenBytesWithAs(t *testing.T) {
	toyOnly(t)
	doc, err := OpenBytes("config", []byte(toySrc), As(formatToy))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format() != formatToy {
		t.Fatalf("As did not take: %q", doc.Format())
	}
}

func TestAsOnUnregisteredFormatIsHEW021(t *testing.T) {
	toyOnly(t)
	_, err := OpenBytes("config", []byte(toySrc), As("nosuch"))
	he := wantCode(t, err, hewerr.CodeUnsupportedFormat)
	if !strings.Contains(he.Detail, "nosuch") {
		t.Fatalf("error does not name the format asked for: %q", he.Detail)
	}
}

func TestOpenFileDetectsFromHandleName(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "config.toy", toySrc)
	f, err := fsys.OpenFile("config.toy", 0o2 /*O_RDWR*/, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := OpenFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format() != formatToy || doc.Name() != "config.toy" {
		t.Fatalf("OpenFile: name=%q format=%q", doc.Name(), doc.Format())
	}
	if got := string(doc.src); got != toySrc {
		t.Fatalf("OpenFile read %q", got)
	}
	// The caller owns the handle: hew never closes one it did not open, so
	// the caller's own Close is the FIRST close and must succeed.
	if err := f.Close(); err != nil {
		t.Fatalf("hew closed a handle it did not open: %v", err)
	}
}

func TestOpenFileUndetectableNameNeedsAs(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "config", toySrc)
	f, err := fsys.Open("config")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, err := OpenFile(f); err == nil {
		t.Fatal("a handle with no usable name was accepted without hew.As")
	} else {
		wantCode(t, err, hewerr.CodeUnsupportedFormat)
	}
	if _, err := OpenFile(f, As(formatToy)); err != nil {
		t.Fatalf("As on a nameless handle: %v", err)
	}
}

func TestOpenFileRereadsFromTheStart(t *testing.T) {
	toyOnly(t)
	fsys := memfs(t, "config.toy", toySrc)
	f, err := fsys.Open("config.toy")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// A careful config writer has already read and validated the file, so the
	// handle's offset is at EOF. Adopting it must still see the content.
	if _, err := afero.ReadAll(f); err != nil {
		t.Fatal(err)
	}
	doc, err := OpenFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(doc.src); got != toySrc {
		t.Fatalf("OpenFile after the caller read to EOF: %q", got)
	}
}

func TestFormatNeverSniffsContent(t *testing.T) {
	toyOnly(t)
	// Content that is unmistakably the toy's own syntax, under a name no
	// binding claims: §8.0 forbids reading it, so this is HEW021 and not a
	// lucky guess.
	_, err := OpenBytes("config.unknown", []byte(toySrc))
	wantCode(t, err, hewerr.CodeUnsupportedFormat)
}
