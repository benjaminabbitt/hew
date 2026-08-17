package hew

import (
	"testing"

	"github.com/spf13/afero"
)

// CreateIfMissing binds a missing path to an EMPTY document of the target's
// format, so a caller writing a config file need not special-case the first
// write — the case B3's ctxloom exercise hit. The empty document is the
// BINDING's: only the extension knows what an empty one of its format is, and
// core never guesses.
//
// The behaviour that needs a real binding is pinned in conformance, where the
// shipped formats are registered.

func TestOpenOfAMissingPathIsStillAnErrorByDefault(t *testing.T) {
	toyOnly(t)
	fsys := afero.NewMemMapFs()
	if _, err := Open(fsys, "/etc/absent.toy"); err == nil {
		t.Fatal("Open of a missing path succeeded without CreateIfMissing")
	}
}

// A format whose binding declares no empty document cannot be created from
// nothing, and says so rather than inventing one.
func TestCreateIfMissingRefusesAFormatWithNoEmptyDocument(t *testing.T) {
	isolate(t)
	b := toyBinding()
	b.EmptyDocument = nil
	Register(formatToy, b)
	fsys := afero.NewMemMapFs()
	_, err := Open(fsys, "/etc/new.toy", CreateIfMissing())
	if err == nil {
		t.Fatal("a binding with no empty document created one anyway")
	}
}
