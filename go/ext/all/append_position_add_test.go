package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"

	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

var appendPositionCases = []struct{ name, format, doc, want string }{
	{"yaml", "yaml", "items:\n  - 1\n", "items:\n  - 1\n  - 9\n"},
	{"json", "json", "{\n  \"items\": [1]\n}\n", "{\n  \"items\": [1, 9]\n}\n"},
	{"jsonc", "jsonc", "{\n  \"items\": [1]\n}\n", "{\n  \"items\": [1, 9]\n}\n"},
	{"toml", "toml", "items = [1]\n", "items = [1, 9]\n"},
}

func applyOne(t *testing.T, format, doc string, tr hew.Transform) ([]byte, error) {
	t.Helper()
	b, ok := hew.Lookup(hew.FormatID(format))
	if !ok {
		t.Fatalf("no binding for %s", format)
	}
	return b.Applier([]byte(doc), hew.TransformList{Target: "t", Format: hew.FormatID(format), Transform: []hew.Transform{tr}})
}

// §4.1's "-" is the APPEND POSITION, and an add there appends: it is RFC
// 6902's own spelling of OP-11, which the resolver already lowers to the index
// one past the end (TestResolveAddAtAppendPosition) and the document API
// already accepts (Sel.containerPath). It is not the refused shape of
// TestAddUnderASequenceParentIsRefusedEverywhere: that path names a member that
// does not exist; "-" names the one position that never holds a member.
//
// The on-conflict policy cannot change the result: nothing exists at the append
// position, so there is nothing for "! upsert" to replace or "! default" to keep.
func TestAddAtTheAppendPositionAppends(t *testing.T) {
	for _, c := range appendPositionCases {
		for _, oc := range []hew.OnConflict{"", hew.ConflictFail, hew.ConflictReplace, hew.ConflictKeep} {
			t.Run(c.name+"/"+string(oc), func(t *testing.T) {
				v, err := hew.ValueOf(9)
				if err != nil {
					t.Fatal(err)
				}
				out, aerr := applyOne(t, c.format, c.doc, hew.Transform{
					Op: hew.OpAdd, Path: hew.MustParsePath("/items/-"), Value: v, OnConflict: oc,
				})
				if aerr != nil {
					t.Fatalf("an add at the append position must append: %v", aerr)
				}
				if got := string(out); got != c.want {
					t.Fatalf("got %q, want %q", got, c.want)
				}
			})
		}
	}
}

// "-" under a node that is not a sequence addresses nothing: HEW013, the code
// the resolver gives the same path (TestResolveAppendUnderNonSequenceParent).
func TestAddAtTheAppendPositionOfANonSequenceIsNoMatch(t *testing.T) {
	for _, c := range []struct{ name, format, doc string }{
		{"yaml", "yaml", "items:\n  a: 1\n"},
		{"json", "json", "{\n  \"items\": {\"a\": 1}\n}\n"},
		{"jsonc", "jsonc", "{\n  \"items\": {\"a\": 1}\n}\n"},
		{"toml", "toml", "[items]\na = 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := hew.ValueOf(9)
			if err != nil {
				t.Fatal(err)
			}
			out, aerr := applyOne(t, c.format, c.doc, hew.Transform{Op: hew.OpAdd, Path: hew.MustParsePath("/items/-"), Value: v})
			if aerr == nil {
				t.Fatalf("accepted: %q", string(out))
			}
			he, ok := hewerr.As(aerr)
			if !ok || he.Code != hewerr.CodeNoMatch {
				t.Fatalf("want HEW013, got %v", aerr)
			}
		})
	}
}

// The document API spells the same append with Append(): the bytes, not only
// the recorded transform, must carry it.
func TestDocAppendWritesTheElement(t *testing.T) {
	for _, c := range appendPositionCases {
		t.Run(c.name, func(t *testing.T) {
			d, err := hew.OpenBytes("t", []byte(c.doc), hew.As(hew.FormatID(c.format)))
			if err != nil {
				t.Fatal(err)
			}
			d.At("/items/{}", hew.Append()).Add(9)
			out, err := d.Bytes()
			if err != nil {
				t.Fatalf("Bytes: %v", err)
			}
			if got := string(out); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
