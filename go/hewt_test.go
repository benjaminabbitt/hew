package hew

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctxloom/hew/internal/hewerr"
)

// corpusHewtFiles returns every .hewt transform list in the conformance
// corpus. corpus/cli/apply-record ships an application record (spec §9.7),
// which shares the extension and the document model but is a different
// schema — it is identified by its own version key and excluded here.
func corpusHewtFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("../corpus/*/*/*.hewt")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .hewt fixtures found; is the corpus present?")
	}
	var out []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.HasPrefix(src, []byte("hew-record:")) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// TestCorpusHewtRoundTrip is the codec's acceptance test: every transform
// list the corpus pins must read, and re-writing what was read must be
// value-preserving and byte-stable.
func TestCorpusHewtRoundTrip(t *testing.T) {
	files := corpusHewtFiles(t)
	if len(files) < 13 {
		t.Fatalf("only %d transform-list fixtures found; the corpus should have at least 13", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(filepath.Dir(f)), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			// (a) it reads.
			tl, err := UnmarshalTransforms(src)
			if err != nil {
				t.Fatalf("UnmarshalTransforms: %v", err)
			}
			if tl.Target == "" || len(tl.Transform) == 0 {
				t.Fatalf("decoded an empty list: %+v", tl)
			}
			// No emitted list ever carries "move" (§9.6).
			for i, tr := range tl.Transform {
				if !tr.Op.Valid() {
					t.Errorf("transform %d has non-core op %q", i, tr.Op)
				}
			}
			// (b) marshal→unmarshal preserves the value.
			out, err := MarshalTransforms(tl)
			if err != nil {
				t.Fatalf("MarshalTransforms: %v", err)
			}
			back, err := UnmarshalTransforms(out)
			if err != nil {
				t.Fatalf("re-reading canonical output: %v\n%s", err, out)
			}
			if !tl.Equal(back) {
				t.Errorf("marshal→unmarshal is not value-preserving\n--- canonical ---\n%s", out)
			}
			// (c) marshal is byte-idempotent.
			again, err := MarshalTransforms(back)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, again) {
				t.Errorf("marshal is not byte-idempotent\n--- first ---\n%s--- second ---\n%s", out, again)
			}
			// Determinism: the same input always produces the same bytes.
			for i := 0; i < 3; i++ {
				repeat, err := MarshalTransforms(tl)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out, repeat) {
					t.Fatalf("marshal is not deterministic across calls")
				}
			}
		})
	}
}

// TestCorpusHewtCanonicalBytes pins the stronger property the fixtures
// actually have: the corpus is already written in canonical form, so
// canonicalizing it is a no-op — except for the one fixture that authors the
// `move` sugar, which normalizes.
func TestCorpusHewtCanonicalBytes(t *testing.T) {
	const sugarCase = "ir-move-normalized"
	sawSugar := false
	for _, f := range corpusHewtFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := CanonicalizeTransforms(src)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if filepath.Base(filepath.Dir(f)) == sugarCase {
			sawSugar = true
			if bytes.Equal(src, got) {
				t.Errorf("%s: `op: move` survived canonicalization; §9.6 says it never appears in emitted output", f)
			}
			if bytes.Contains(got, []byte("op: move")) {
				t.Errorf("%s: canonical output still mentions move:\n%s", f, got)
			}
			continue
		}
		if !bytes.Equal(src, got) {
			t.Errorf("%s is not already canonical:\n--- fixture ---\n%s--- canonical ---\n%s", f, src, got)
		}
	}
	if !sawSugar {
		t.Errorf("corpus/json/%s is missing; the move-sugar normalization is unpinned", sugarCase)
	}
}

func TestUnmarshalMoveNormalization(t *testing.T) {
	src := []byte(`hew-transforms: 1
target: config.yaml
format: yaml
transforms:
  - op: move
    from: /server/host
    path: /network/host
`)
	tl, err := UnmarshalTransforms(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Transform) != 2 {
		t.Fatalf("move expanded to %d records, want 2", len(tl.Transform))
	}
	cp, rm := tl.Transform[0], tl.Transform[1]
	if cp.Op != OpCopy {
		t.Errorf("first record is %s, want copy", cp.Op)
	}
	if cp.From.String() != "/server/host" || cp.Path.String() != "/network/host" {
		t.Errorf("copy = from %s path %s", cp.From, cp.Path)
	}
	if rm.Op != OpRemove {
		t.Errorf("second record is %s, want remove", rm.Op)
	}
	if rm.Path.String() != "/server/host" {
		t.Errorf("remove path = %s, want the move's source", rm.Path)
	}
	// Placement rides the copy half, not the remove half.
	withPlacement, err := UnmarshalTransforms([]byte(`hew-transforms: 1
target: c.yaml
transforms:
  - op: move
    from: /a
    path: /b
    before: /c
`))
	if err != nil {
		t.Fatal(err)
	}
	if withPlacement.Transform[0].Before.String() != "/c" {
		t.Errorf("copy lost the placement")
	}
	if !withPlacement.Transform[1].Before.IsZero() {
		t.Errorf("remove picked up a placement")
	}
}

func TestMarshalCanonicalShape(t *testing.T) {
	tl := TransformList{
		Target: "config.yaml",
		Format: FormatYAML,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/timeout"), Value: mustValue(t, 30)},
			{Op: OpReplace, Path: MustParsePath("/server/timeout"), Value: mustValue(t, 60)},
		},
	}
	got, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatal(err)
	}
	want := `hew-transforms: 1
target: config.yaml
format: yaml
transforms:
  - op: test
    path: /server/timeout
    value: 30
  - op: replace
    path: /server/timeout
    value: 60
`
	if string(got) != want {
		t.Errorf("canonical output:\n%s\nwant:\n%s", got, want)
	}
	// hew-transforms MUST be the first key (§9.6).
	if !bytes.HasPrefix(got, []byte("hew-transforms: 1\n")) {
		t.Error("hew-transforms is not the first key")
	}
	// format is optional and omitted when unset.
	tl.Format = ""
	got, err = MarshalTransforms(tl)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("format:")) {
		t.Errorf("an unset format was emitted:\n%s", got)
	}
	if !bytes.HasPrefix(got, []byte("hew-transforms: 1\ntarget: config.yaml\ntransforms:\n")) {
		t.Errorf("document key order changed when format was omitted:\n%s", got)
	}
}

// TestMarshalFieldOrder pins the per-record key order across every optional
// field at once — the order the corpus fixtures use, extended to the
// qualifiers no fixture happens to exercise.
func TestMarshalFieldOrder(t *testing.T) {
	kind := KindMap
	n := 3
	tl := TransformList{
		Target: "t.json",
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/a"), Absent: true, Optional: true},
			{Op: OpTest, Path: MustParsePath("/b"), Count: &n},
			{Op: OpTest, Path: MustParsePath("/c"), NodeKind: &kind},
			{
				Op: OpAdd, Path: MustParsePath("/d"), Value: mustValue(t, "v"),
				OnConflict: ConflictReplace, Anchor: AnchorFork, Surface: SurfaceTable,
				Idempotent: true, After: MustParsePath("/e"),
			},
			{Op: OpCopy, From: MustParsePath("/f"), Path: MustParsePath("/g"), Before: MustParsePath("/h")},
		},
	}
	got, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatal(err)
	}
	want := `hew-transforms: 1
target: t.json
transforms:
  - op: test
    path: /a
    absent: true
    optional: true
  - op: test
    path: /b
    count: 3
  - op: test
    path: /c
    kind: map
  - op: add
    path: /d
    on_conflict: replace
    anchor: fork
    surface: table
    idempotent: true
    after: /e
    value: v
  - op: copy
    from: /f
    path: /g
    before: /h
`
	if string(got) != want {
		t.Errorf("canonical output:\n%s\nwant:\n%s", got, want)
	}
	back, err := UnmarshalTransforms(got)
	if err != nil {
		t.Fatalf("canonical output does not read back: %v", err)
	}
	if !tl.Equal(back) {
		t.Error("full-field round trip lost information")
	}
}

func TestMarshalStream(t *testing.T) {
	tls := []TransformList{
		{Target: "a.json", Format: FormatJSON, Transform: []Transform{
			{Op: OpRemove, Path: MustParsePath("/x")},
		}},
		{Target: "b.yaml", Format: FormatYAML, Transform: []Transform{
			{Op: OpRemove, Path: MustParsePath("/y")},
		}},
	}
	got, err := MarshalTransformStream(tls)
	if err != nil {
		t.Fatal(err)
	}
	want := `hew-transforms: 1
target: a.json
format: json
transforms:
  - op: remove
    path: /x
---
hew-transforms: 1
target: b.yaml
format: yaml
transforms:
  - op: remove
    path: /y
`
	if string(got) != want {
		t.Errorf("stream output:\n%s\nwant:\n%s", got, want)
	}
	back, err := UnmarshalTransformStream(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || !back[0].Equal(tls[0]) || !back[1].Equal(tls[1]) {
		t.Errorf("stream round trip = %+v", back)
	}
	// The single-document reader refuses a stream rather than silently
	// dropping the tail.
	if _, err := UnmarshalTransforms(got); err == nil {
		t.Error("UnmarshalTransforms accepted a two-document stream")
	}
	if _, err := MarshalTransformStream(nil); err == nil {
		t.Error("MarshalTransformStream accepted an empty stream")
	}
}

func TestUnmarshalErrors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		detail string
	}{
		{"empty", "", "no .hewt document"},
		{"comment only", "# nothing here\n", "no .hewt document"},
		{"not yaml", "hew-transforms: 1\n\ttab: bad\n", "malformed .hewt YAML"},
		{"not a mapping", "- a\n- b\n", "must be a mapping"},
		{"scalar document", "hello\n", "must be a mapping"},
		{"version not first", "target: t.json\nhew-transforms: 1\ntransforms: [{op: remove, path: /a}]\n",
			`"hew-transforms" must be the first key`},
		{"version missing", "transforms: [{op: remove, path: /a}]\ntarget: t.json\n",
			`"hew-transforms" must be the first key`},
		{"version wrong", "hew-transforms: 2\ntarget: t.json\ntransforms: [{op: remove, path: /a}]\n",
			"unsupported hew-transforms version 2"},
		{"version not an int", "hew-transforms: one\ntarget: t.json\ntransforms: [{op: remove, path: /a}]\n",
			"expected an integer"},
		{"no target", "hew-transforms: 1\ntransforms: [{op: remove, path: /a}]\n", "no target"},
		{"empty target", "hew-transforms: 1\ntarget: \"\"\ntransforms: [{op: remove, path: /a}]\n", "no target"},
		{"target not a string", "hew-transforms: 1\ntarget: 7\ntransforms: [{op: remove, path: /a}]\n",
			"expected a string"},
		{"no transforms", "hew-transforms: 1\ntarget: t.json\n", `missing "transforms"`},
		{"empty transforms", "hew-transforms: 1\ntarget: t.json\ntransforms: []\n", "transform list is empty"},
		{"transforms not a sequence", "hew-transforms: 1\ntarget: t.json\ntransforms: {}\n",
			"transforms must be a sequence"},
		{"transform not a mapping", "hew-transforms: 1\ntarget: t.json\ntransforms: [hello]\n",
			"a transform must be a mapping"},
		{"unknown document key", "hew-transforms: 1\ntarget: t.json\nnope: 1\ntransforms: [{op: remove, path: /a}]\n",
			`unknown document key "nope"`},
		{"duplicate document key", "hew-transforms: 1\ntarget: t.json\ntarget: u.json\ntransforms: [{op: remove, path: /a}]\n",
			"duplicate document key"},
		{"unknown transform field", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove, path: /a, nope: 1}]\n",
			`unknown transform field "nope"`},
		{"duplicate transform field", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove, path: /a, path: /b}]\n",
			"duplicate transform field"},
		{"unknown op", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: frobnicate, path: /a}]\n",
			`unknown op "frobnicate"`},
		{"missing op", "hew-transforms: 1\ntarget: t.json\ntransforms: [{path: /a}]\n", "missing op"},
		{"missing path", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove}]\n", "missing path"},
		{"bad path", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove, path: nope}]\n",
			`must begin with "/" or "."`},
		{"bad from", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: copy, from: nope, path: /a}]\n",
			`must begin with "/" or "."`},
		{"op not a string", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: 1, path: /a}]\n",
			"expected a string"},
		{"absent not a bool", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: test, path: /a, absent: yes please}]\n",
			"expected a boolean"},
		{"absent false", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: test, path: /a, absent: false}]\n",
			"false is meaningless"},
		{"optional false", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove, path: /a, optional: false}]\n",
			"false is meaningless"},
		{"idempotent false", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove, path: /a, idempotent: false}]\n",
			"false is meaningless"},
		{"count not an int", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: test, path: /a, count: many}]\n",
			"expected an integer"},
		{"line not an int", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove, path: /a, line: soon}]\n",
			"expected an integer"},
		{"kind not a string", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: test, path: /a, kind: 3}]\n",
			"expected a string"},
		{"unknown format", "hew-transforms: 1\ntarget: t.xml\nformat: xml\ntransforms: [{op: remove, path: /a}]\n",
			`unknown format "xml"`},
		{"non-scalar key", "hew-transforms: 1\ntarget: t.json\ntransforms: [{? [a]: b}]\n", "non-scalar mapping key"},

		// move-sugar rejections
		{"move without from", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, path: /a}]\n",
			"op move: missing from"},
		{"move without path", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a}]\n",
			"op move: missing path"},
		{"move with value", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, value: 1}]\n",
			"op move: value is not valid"},
		{"move with absent", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, absent: true}]\n",
			"op move: absent is not valid"},
		{"move with count", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, count: 1}]\n",
			"op move: count is not valid"},
		{"move with kind", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, kind: map}]\n",
			"op move: kind is not valid"},
		{"move with on_conflict", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, on_conflict: keep}]\n",
			"op move: on_conflict is not valid"},
		{"move with surface", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, surface: table}]\n",
			"op move: surface is not valid"},
		{"move with optional", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, optional: true}]\n",
			"op move: optional is not valid"},
		{"move with idempotent", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, idempotent: true}]\n",
			"op move: idempotent is not valid"},
		{"move with both placements", "hew-transforms: 1\ntarget: t.json\ntransforms: [{op: move, from: /a, path: /b, before: /c, after: /d}]\n",
			"mutually exclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl, err := UnmarshalTransforms([]byte(tc.src))
			if err == nil {
				t.Fatalf("accepted malformed input: %+v", tl)
			}
			he, ok := hewerr.As(err)
			if !ok {
				t.Fatalf("error is not a *hewerr.Error: %T %v", err, err)
			}
			wantCode := hewerr.CodeParse
			if strings.Contains(tc.detail, "unknown format") {
				wantCode = hewerr.CodeUnsupportedFormat
			}
			if he.Code != wantCode {
				t.Errorf("code = %s, want %s", he.Code, wantCode)
			}
			if he.Component != hewerr.ComponentParser {
				t.Errorf("component = %s, want parser", he.Component)
			}
			if !strings.Contains(he.Detail, tc.detail) {
				t.Errorf("detail %q does not contain %q", he.Detail, tc.detail)
			}
		})
	}
}

func TestUnmarshalIgnoresLine(t *testing.T) {
	// §9.6: `line` is provenance, ignored on input — accepting and dropping
	// it keeps unmarshal∘marshal the identity on the IR.
	src := []byte("hew-transforms: 1\ntarget: t.json\ntransforms:\n  - op: remove\n    path: /a\n    line: 12\n")
	tl, err := UnmarshalTransforms(src)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Transform[0].PatchLine != 0 {
		t.Errorf("line was carried into PatchLine (%d)", tl.Transform[0].PatchLine)
	}
	out, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("line:")) {
		t.Errorf("line was emitted:\n%s", out)
	}
}

func TestMarshalDropsPatchLine(t *testing.T) {
	tl := TransformList{Target: "t.json", Transform: []Transform{
		{Op: OpRemove, Path: MustParsePath("/a"), PatchLine: 9},
	}}
	out, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("line")) {
		t.Errorf("PatchLine reached the wire:\n%s", out)
	}
}

func TestUnmarshalReportsLineNumbers(t *testing.T) {
	src := "hew-transforms: 1\ntarget: t.json\ntransforms:\n  - op: remove\n    path: /a\n  - op: nope\n    path: /b\n"
	_, err := UnmarshalTransforms([]byte(src))
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want a *hewerr.Error, got %v", err)
	}
	if he.PatchLine != 6 {
		t.Errorf("PatchLine = %d, want 6 (the failing record's op line)", he.PatchLine)
	}
}

func TestUnmarshalPreservesValueShapes(t *testing.T) {
	src := []byte(`hew-transforms: 1
target: t.toml
format: toml
transforms:
  - op: add
    path: /a
    value:
      command: taskloom
      args: [mcp]
      count: 3
      enabled: true
      nothing: null
      quoted: "8080"
  - op: test
    path: /b/#0
    value:
      comment: ports below 1024 need CAP_NET_BIND_SERVICE
`)
	tl, err := UnmarshalTransforms(src)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Command string   `yaml:"command"`
		Args    []string `yaml:"args"`
		Count   int      `yaml:"count"`
		Enabled bool     `yaml:"enabled"`
		Nothing *string  `yaml:"nothing"`
		Quoted  string   `yaml:"quoted"`
	}
	if err := tl.Transform[0].Value.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Command != "taskloom" || len(got.Args) != 1 || got.Args[0] != "mcp" ||
		got.Count != 3 || !got.Enabled || got.Nothing != nil || got.Quoted != "8080" {
		t.Errorf("decoded value = %+v", got)
	}
	// Key order and flow style survive, so a canonical rewrite is a no-op.
	out, err := MarshalTransforms(tl)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, out) {
		t.Errorf("value shapes were not preserved:\n--- want ---\n%s--- got ---\n%s", src, out)
	}
	// A comment-node value is an ordinary mapping (§11.10 reduction 3).
	var comment struct {
		Comment string `yaml:"comment"`
	}
	if err := tl.Transform[1].Value.Decode(&comment); err != nil {
		t.Fatal(err)
	}
	if comment.Comment != "ports below 1024 need CAP_NET_BIND_SERVICE" {
		t.Errorf("comment value = %q", comment.Comment)
	}
}

func TestUnmarshalExplicitNullValue(t *testing.T) {
	// An explicit `value: null` is a value; a missing `value:` is not.
	tl, err := UnmarshalTransforms([]byte("hew-transforms: 1\ntarget: t.json\ntransforms: [{op: replace, path: /a, value: null}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if tl.Transform[0].Value.IsZero() {
		t.Error("an explicit null decoded as the absent value")
	}
	if _, err := UnmarshalTransforms([]byte("hew-transforms: 1\ntarget: t.json\ntransforms: [{op: replace, path: /a}]\n")); err == nil {
		t.Error("a replace with no value was accepted")
	}
}

func TestCanonicalizeTransformsPropagatesErrors(t *testing.T) {
	if _, err := CanonicalizeTransforms([]byte("nope")); err == nil {
		t.Error("CanonicalizeTransforms accepted garbage")
	}
	out, err := CanonicalizeTransforms([]byte("hew-transforms: 1\ntarget: t.json\ntransforms: [{op: remove, path: /a}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hew-transforms: 1\ntarget: t.json\ntransforms:\n  - op: remove\n    path: /a\n" {
		t.Errorf("canonicalized flow input to:\n%s", out)
	}
}

func mustValue(t *testing.T, x any) Value {
	t.Helper()
	v, err := ValueOf(x)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
