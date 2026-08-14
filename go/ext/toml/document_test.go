package toml

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

func mustDoc(t *testing.T, src string) hew.Document {
	t.Helper()
	d, err := Document("config.toml", []byte(src))
	if err != nil {
		t.Fatalf("Document(%q): %v", src, err)
	}
	return d
}

func TestDocumentRejectsUnparseableTarget(t *testing.T) {
	_, err := Document("config.toml", []byte("[unclosed\n"))
	if err == nil {
		t.Fatal("an unparseable target must be an error, not an empty document")
	}
	if !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("the error must name the target it could not parse; got %q", err)
	}
	if !strings.Contains(err.Error(), "TOML") {
		t.Errorf("the error must say which format failed to parse; got %q", err)
	}
}

func TestDocumentNodeKinds(t *testing.T) {
	root := mustDoc(t, "scalar = 1\nseq = [1, 2]\n[table]\nk = \"v\"\n").Root()
	if got := root.Kind(); got != hew.KindMap {
		t.Errorf("root Kind() = %q, want %q", got, hew.KindMap)
	}
	for _, c := range []struct {
		member string
		want   hew.NodeKind
	}{
		{"scalar", hew.KindScalar},
		{"seq", hew.KindSeq},
		{"table", hew.KindMap},
	} {
		n, ok := root.Member(c.member)
		if !ok {
			t.Fatalf("Member(%q) missing", c.member)
		}
		if got := n.Kind(); got != c.want {
			t.Errorf("%q Kind() = %q, want %q", c.member, got, c.want)
		}
	}
}

// The projection is over the node's KIND, not the surface it was written in.
// §8.4's dotted-key / header / inline-table duality is a spelling difference the
// applier's tree already collapses, so the same address must resolve against all
// three spellings — that is the reason to expose the applier's own tree rather
// than parse a second time.
func TestDocumentIsIndifferentToTheSurfaceATableWasWrittenIn(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"table header", "[a.b]\nc = 30\n"},
		{"dotted key", "a.b.c = 30\n"},
		{"inline table", "a = {b = {c = 30}}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, ok := mustDoc(t, c.src).Root().Member("a")
			if !ok {
				t.Fatal(`Member("a") missing`)
			}
			b, ok := a.Member("b")
			if !ok {
				t.Fatal(`Member("b") missing`)
			}
			cn, ok := b.Member("c")
			if !ok {
				t.Fatal(`Member("c") missing`)
			}
			var got int
			if err := cn.Value().Decode(&got); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got != 30 {
				t.Errorf("c = %d, want 30", got)
			}
		})
	}
}

// Member must read `entries` (members), never `lines` (assignments physically in
// this table's body). They are deliberately different sets: a dotted key
// registers its member on the table the dots name while the line lives in the
// body it was written in. Reading lines would miss the member entirely here.
func TestDocumentMemberReadsMembersNotPhysicalLines(t *testing.T) {
	root := mustDoc(t, "[owner]\nname = \"ben\"\ndog.name = \"rex\"\n").Root()
	owner, ok := root.Member("owner")
	if !ok {
		t.Fatal(`Member("owner") missing`)
	}
	dog, ok := owner.Member("dog")
	if !ok {
		t.Fatal(`the dotted key's intermediate "dog" must be a MEMBER of owner`)
	}
	if got := dog.Kind(); got != hew.KindMap {
		t.Fatalf("dog Kind() = %q, want %q", got, hew.KindMap)
	}
	if _, ok := dog.Member("name"); !ok {
		t.Error(`"dog" must hold member "name" from the dotted assignment`)
	}
	// owner has exactly two members, name and dog — not three, and not one.
	if got := owner.Len(); got != 2 {
		t.Errorf("owner Len() = %d, want 2 (name, dog)", got)
	}
}

func TestDocumentLenAndElemBounds(t *testing.T) {
	root := mustDoc(t, "seq = [10, 20, 30]\nscalar = 1\n").Root()
	seq, _ := root.Member("seq")
	if got := seq.Len(); got != 3 {
		t.Fatalf("seq Len() = %d, want 3", got)
	}
	for _, i := range []int{-1, 3, 99} {
		if _, ok := seq.Elem(i); ok {
			t.Errorf("Elem(%d) must be out of bounds for a 3-element sequence", i)
		}
	}
	e, ok := seq.Elem(1)
	if !ok {
		t.Fatal("Elem(1) missing")
	}
	var got int
	if err := e.Value().Decode(&got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != 20 {
		t.Errorf("Elem(1) = %d, want 20", got)
	}

	// A scalar has no children, and indexing it is not a panic.
	scalar, _ := root.Member("scalar")
	if got := scalar.Len(); got != 0 {
		t.Errorf("scalar Len() = %d, want 0", got)
	}
	if _, ok := scalar.Elem(0); ok {
		t.Error("a scalar has no element 0")
	}
	if _, ok := scalar.Member("anything"); ok {
		t.Error("a scalar has no members")
	}
}

// Value carries the DECODED scalar (tag + canonical text), which is what makes
// §6.1's format-native comparison work: three spellings of thirty compare as the
// integer they denote, not as three different strings.
func TestDocumentValueDecodesNativeTypes(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"decimal", "n = 30\n"},
		{"hex", "n = 0x1e\n"},
		{"underscored", "n = 3_0\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, ok := mustDoc(t, c.src).Root().Member("n")
			if !ok {
				t.Fatal(`Member("n") missing`)
			}
			var got int
			if err := n.Value().Decode(&got); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got != 30 {
				t.Errorf("n = %d, want 30 (all three spellings denote thirty)", got)
			}
		})
	}
}

func TestDocumentNonScalarIsTheAbsentValue(t *testing.T) {
	root := mustDoc(t, "[table]\nk = 1\n").Root()
	tbl, _ := root.Member("table")
	if !tbl.Value().IsZero() {
		t.Error("a table has no scalar value, so Value() must be the absent Value")
	}
	if !root.Value().IsZero() {
		t.Error("the root has no scalar value either")
	}
}

// The reader's whole purpose: Resolve must be able to project a transform list
// against a TOML target. Before this existed Resolve had no document and §9.7
// records could not be produced for TOML at all.
func TestDocumentDrivesResolve(t *testing.T) {
	doc := mustDoc(t, "[agent_servers.other]\ncommand = \"other\"\n")
	tl := hew.TransformList{
		Target: "config.toml",
		Format: hew.FormatTOML,
		Transform: []hew.Transform{{
			Op: hew.OpRemove,
			Path: hew.RootPath().Append(hew.Segment{Kind: hew.SegKey, Name: "agent_servers"}).
				Append(hew.Segment{Kind: hew.SegKey, Name: "other"}),
		}},
	}
	ops, err := hew.Resolve(tl, doc)
	if err != nil {
		t.Fatalf("Resolve against a TOML document: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("Resolve returned %d ops, want 1", len(ops))
	}
	if got := ops[0].Path; got != "/agent_servers/other" {
		t.Errorf("resolved path = %q, want /agent_servers/other", got)
	}
}

// Resolve must REFUSE an address the target does not hold. Without this, the
// test above would pass against a reader that resolved everything blindly.
func TestDocumentResolveRefusesAnAbsentAddress(t *testing.T) {
	doc := mustDoc(t, "[agent_servers.other]\ncommand = \"other\"\n")
	tl := hew.TransformList{
		Target: "config.toml",
		Format: hew.FormatTOML,
		Transform: []hew.Transform{{
			Op:   hew.OpRemove,
			Path: hew.RootPath().Append(hew.Segment{Kind: hew.SegKey, Name: "nope"}),
		}},
	}
	if _, err := hew.Resolve(tl, doc); err == nil {
		t.Fatal("resolving an address the document does not hold must fail")
	}
}
