package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// An add whose path does NOT resolve and whose PARENT is a sequence must be
// REFUSED, identically in every format (cupped-trowel).
//
// yaml and jsonc used to append into the parent instead: the same abstract
// transform, against structurally equivalent documents, either silently grew a
// sequence or failed loud, decided only by the target's format. hew's premise
// is that a transform means the same thing across formats, so that split was a
// defect wherever it fell.
//
// Refusing is the side that was chosen because the path did not match anything.
// Creating on a failed match is opted into through the ANNOTATION vocabulary —
// `! default` (OP-04) and `! upsert` (OP-03) — and nothing in a bare path
// authorises it. Growing a sequence at an address that resolved to nothing is
// the failure class hew exists to prevent.
//
// This is NOT the sequence-style append, which stays legal in every binding:
// there the path resolves TO the sequence, so the container is what the
// transform addressed. TestSequenceStyleAppendStillWorks below pins that, so a
// future reader cannot mistake one for the other.
func TestAddUnderASequenceParentIsRefusedEverywhere(t *testing.T) {
	for _, c := range []struct{ name, format, doc string }{
		{"yaml", "yaml", "items:\n  - 1\n  - 2\n"},
		{"json", "json", "{\n  \"items\": [1, 2]\n}\n"},
		{"jsonc", "jsonc", "{\n  \"items\": [1, 2]\n}\n"},
		{"toml", "toml", "items = [1, 2]\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, ok := hew.Lookup(hew.FormatID(c.format))
			if !ok {
				t.Fatalf("no binding for %s", c.format)
			}
			v, err := hew.ValueOf(9)
			if err != nil {
				t.Fatal(err)
			}
			out, aerr := b.Applier([]byte(c.doc), hew.TransformList{
				Target: "t", Format: hew.FormatID(c.format),
				Transform: []hew.Transform{{
					Op:    hew.OpAdd,
					Path:  hew.MustParsePath("/items/nope"),
					Value: v,
				}},
			})
			if aerr == nil {
				t.Fatalf("the add was accepted and grew the sequence: %q", string(out))
			}
			if out != nil {
				t.Fatalf("all-or-nothing violated: bytes returned alongside %v", aerr)
			}
			he, ok := hewerr.As(aerr)
			if !ok {
				t.Fatalf("not a *hewerr.Error: %v", aerr)
			}
			if he.Code != hewerr.CodeInexpressible {
				t.Fatalf("code: want HEW020, got %s (%v)", he.Code, he)
			}
			if he.Path != "/items/nope" {
				t.Fatalf("path: want /items/nope, got %s", he.Path)
			}
		})
	}
}

// The branch that is CORRECT and must not be broken by the refusal above: a
// path that resolves TO a sequence addresses the container, and a plain add
// appends to it (OP-11, §9.1 step 5).
func TestSequenceStyleAppendStillWorks(t *testing.T) {
	for _, c := range []struct{ name, format, doc, want string }{
		{"yaml", "yaml", "items:\n  - 1\n", "items:\n  - 1\n  - 9\n"},
		{"json", "json", "{\n  \"items\": [1]\n}\n", "{\n  \"items\": [1, 9]\n}\n"},
		{"jsonc", "jsonc", "{\n  \"items\": [1]\n}\n", "{\n  \"items\": [1, 9]\n}\n"},
		{"toml", "toml", "items = [1]\n", "items = [1, 9]\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, _ := hew.Lookup(hew.FormatID(c.format))
			v, err := hew.ValueOf(9)
			if err != nil {
				t.Fatal(err)
			}
			out, aerr := b.Applier([]byte(c.doc), hew.TransformList{
				Target: "t", Format: hew.FormatID(c.format),
				Transform: []hew.Transform{{
					Op: hew.OpAdd, Path: hew.MustParsePath("/items"), Value: v,
				}},
			})
			if aerr != nil {
				t.Fatalf("the sequence-style append must still work: %v", aerr)
			}
			if got := string(out); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
