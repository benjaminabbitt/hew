package conformance

import (
	"strings"
	"testing"

	"github.com/spf13/afero"

	hew "github.com/benjaminabbitt/hew/go"
	_ "github.com/benjaminabbitt/hew/go/ext/all"
)

// CreateIfMissing on the shipped bindings: open -> Set -> Write is one shape
// whether or not the file is there yet.

func TestCreateIfMissingCreatesTheFile(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"json", "/etc/new.json"},
		{"jsonc", "/etc/new.jsonc"},
		{"toml", "/etc/new.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := afero.NewMemMapFs()
			d, err := hew.Open(fsys, tc.path, hew.CreateIfMissing())
			if err != nil {
				t.Fatalf("Open with CreateIfMissing: %v", err)
			}
			d.At("/host").Set("example.com")
			if err := d.Write(); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got, err := afero.ReadFile(fsys, tc.path)
			if err != nil {
				t.Fatalf("the file was not created: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("the created file is empty")
			}
			// It must be a real document of its format: reopening and reading
			// the key back is the check that the bytes are not merely present.
			back, err := hew.Open(fsys, tc.path)
			if err != nil {
				t.Fatalf("the created file does not reopen: %v\n%s", err, got)
			}
			back.At("/host").Assert("example.com")
			if _, err := back.Transforms(); err != nil {
				t.Fatalf("the created file does not hold the value that was set: %v\n%s", err, got)
			}
		})
	}
}

// The option changes what happens when the path is ABSENT and nothing else: an
// existing document is read as usual, not replaced by a blank one.
func TestCreateIfMissingLeavesAnExistingDocumentAlone(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "/etc/config.json", []byte("{ \"host\": \"old\", \"port\": 8080 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := hew.Open(fsys, "/etc/config.json", hew.CreateIfMissing())
	if err != nil {
		t.Fatal(err)
	}
	d.At("/host").Replace("new")
	if err := d.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := afero.ReadFile(fsys, "/etc/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "port") {
		t.Fatalf("the existing document was replaced rather than edited:\n%s", got)
	}
}
