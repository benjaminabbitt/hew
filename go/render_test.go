package hew

import (
	"errors"
	"os"
	"testing"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return b
}

// rt2 asserts parse(render(canon(tl))) == canon(tl) — the corpus's RT2
// identity (§13.5), exercised directly against a hand-built TransformList.
func rt2(t *testing.T, tl TransformList) {
	t.Helper()
	if err := tl.Validate(); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	out, err := Render(tl, RenderOptions{Preamble: true, Context: 1})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	tls, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse of rendered notation failed: %v\n--- rendered ---\n%s", err, out)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d:\n%s", len(tls), out)
	}
	if !tl.Equal(tls[0]) {
		t.Fatalf("RT2 violated: parse(render(ir)) != ir\nrendered:\n%s\nwant:\n%+v\ngot:\n%+v", out, tl, tls[0])
	}
}

func TestRenderRoundTripPlainFields(t *testing.T) {
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/host"), Value: mustValNoT("localhost")},
			{Op: OpTest, Path: MustParsePath("/server/port"), Value: mustValNoT(8080)},
			{Op: OpAdd, Path: MustParsePath("/server/tls"), After: MustParsePath("/server/host"), Value: mustValNoT(true)},
		},
	}
	rt2(t, tl)
}

func TestRenderRoundTripReplace(t *testing.T) {
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/timeout"), Value: mustValNoT(30)},
			{Op: OpReplace, Path: MustParsePath("/server/timeout"), Value: mustValNoT(60)},
		},
	}
	rt2(t, tl)
}

func TestRenderRoundTripRemove(t *testing.T) {
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/host"), Value: mustValNoT("localhost")},
			{Op: OpRemove, Path: MustParsePath("/server/host")},
		},
	}
	rt2(t, tl)
}

func TestRenderRoundTripScalarSeqElement(t *testing.T) {
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/tags/=beta"), Value: mustValNoT("beta")},
			{Op: OpRemove, Path: MustParsePath("/tags/=beta")},
		},
	}
	rt2(t, tl)
}

func TestRenderRoundTripKeyedElementAdd(t *testing.T) {
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/servers/name=github/name"), Value: mustValNoT("github")},
			{Op: OpAdd, Path: MustParsePath("/servers"), After: MustParsePath("/servers/name=github"),
				Value: mustValNoT(map[string]any{"name": "ctxloom", "command": "ctxloom"})},
		},
	}
	rt2(t, tl)
}

func TestRenderRoundTripExhaustive(t *testing.T) {
	n := 2
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/permissions/allow"), Value: mustValNoT([]any{"a"})},
			{Op: OpTest, Path: MustParsePath("/permissions/deny"), Value: mustValNoT([]any{"b"})},
			{Op: OpTest, Path: MustParsePath("/permissions"), Exhaustive: true, Count: &n},
		},
	}
	rt2(t, tl)
}

func TestRenderRoundTripFreeAssertions(t *testing.T) {
	n := 3
	k := KindMap
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/env/ANTHROPIC_API_KEY"), Absent: true},
			{Op: OpTest, Path: MustParsePath("/permissions"), NodeKind: &k},
			{Op: OpTest, Path: MustParsePath("/tags"), Count: &n},
		},
	}
	rt2(t, tl)
}

func TestRenderCopyIsInexpressible(t *testing.T) {
	tl := TransformList{
		Target: "target.json", Format: FormatJSON,
		Transform: []Transform{
			{Op: OpCopy, From: MustParsePath("/defaults"), Path: MustParsePath("/service_b")},
		},
	}
	_, err := Render(tl, RenderOptions{Preamble: true})
	if err == nil {
		t.Fatal("copy must be ErrInexpressible (Appendix C.2)")
	}
}

// TestRenderRoundTripCorpusIRFixtures runs RT2 over every corpus
// transforms.hewt that belongs to a parse-seam case: those fixtures ARE the
// pinned IR, so anything the parser produces the renderer must be able to
// write back — comments (§4.5b), TOML surfaces (§8.4) and HCL ordinals
// (§7.2) included.
func TestRenderRoundTripCorpusIRFixtures(t *testing.T) {
	cases := []string{
		"json/add-key",
		"json/set-scalar",
		"json/array-remove-element",
		"json/keyed-array-add",
		"jsonc/add-with-leading-comment",
		"yaml/set-scalar",
		"toml/surface-directive-table",
		"hcl/repeated-label-ordinal",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, fixture, _ := corpusCase(t, c)
			if fixture == nil {
				t.Fatalf("%s has no transforms.hewt fixture", c)
			}
			tl, err := UnmarshalTransforms(fixture)
			if err != nil {
				t.Fatalf("UnmarshalTransforms: %v", err)
			}
			rt2(t, tl)
		})
	}
}

// TestRenderRoundTripQualifiers pins that every `!` directive the parser
// lowers onto a transform comes back out as the directive line that produced
// it (§9.1 step 6, inverted).
func TestRenderRoundTripQualifiers(t *testing.T) {
	cases := []struct {
		name string
		tl   TransformList
	}{{
		name: "optional rides the test and the removal",
		tl: TransformList{Target: "t.yaml", Format: FormatYAML, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/legacy"), Value: mustValNoT(true), Optional: true},
			{Op: OpRemove, Path: MustParsePath("/server/legacy"), Optional: true},
		}},
	}, {
		name: "idempotent rides the replace, not the test it pairs with",
		tl: TransformList{Target: "t.yaml", Format: FormatYAML, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/timeout"), Value: mustValNoT(30)},
			{Op: OpReplace, Path: MustParsePath("/server/timeout"), Value: mustValNoT(60), Idempotent: true},
		}},
	}, {
		name: "upsert and default are the two add-semantics variants",
		tl: TransformList{Target: "t.yaml", Format: FormatYAML, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/port"), Value: mustValNoT(8080)},
			{Op: OpAdd, Path: MustParsePath("/server/host"), After: MustParsePath("/server/port"),
				Value: mustValNoT("localhost"), OnConflict: ConflictReplace},
			{Op: OpAdd, Path: MustParsePath("/server/tls"), After: MustParsePath("/server/host"),
				Value: mustValNoT(true), OnConflict: ConflictKeep},
		}},
	}, {
		name: "anchor policy rides both halves of a replace",
		tl: TransformList{Target: "t.yaml", Format: FormatYAML, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/service_a/timeout"), Value: mustValNoT(30), Anchor: AnchorFork},
			{Op: OpReplace, Path: MustParsePath("/service_a/timeout"), Value: mustValNoT(60), Anchor: AnchorFork},
		}},
	}, {
		name: "an idempotent add keeps its directive",
		tl: TransformList{Target: "t.yaml", Format: FormatYAML, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/port"), Value: mustValNoT(8080)},
			{Op: OpAdd, Path: MustParsePath("/server/tls"), After: MustParsePath("/server/port"),
				Value: mustValNoT(true), Idempotent: true},
		}},
	}, {
		name: "a free assertion keeps the body position it was written in",
		tl: TransformList{Target: "t.json", Format: FormatJSON, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/server/port"), Value: mustValNoT(8080)},
			{Op: OpTest, Path: MustParsePath("/env/KEY"), Absent: true},
			{Op: OpTest, Path: MustParsePath("/server/host"), Value: mustValNoT("localhost")},
		}},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { rt2(t, tc.tl) })
	}
}

// TestRenderInexpressible pins the shapes the mirror grammar cannot write,
// which Render must refuse rather than approximate (Appendix C).
func TestRenderInexpressible(t *testing.T) {
	tbl := mustValNoT(map[string]any{"command": "x"})
	cases := []struct {
		name string
		tl   TransformList
	}{{
		name: "adds under one anchor disagreeing about surface",
		tl: TransformList{Target: "t.toml", Format: FormatTOML, Transform: []Transform{
			{Op: OpAdd, Path: MustParsePath("/servers/a"), Value: tbl, Surface: SurfaceTable},
			{Op: OpAdd, Path: MustParsePath("/servers/b"), Value: tbl, Surface: SurfaceDotted},
		}},
	}, {
		name: "an assertion under an ordinal-selected block",
		tl: TransformList{Target: "t.tf", Format: FormatHCL, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath(`/provider/"aws"[1]/region`), Absent: true},
		}},
	}, {
		name: "an ordinal-addressed assertion hosted in another hunk",
		tl: TransformList{Target: "t.tf", Format: FormatHCL, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath("/terraform/required_version"), Value: mustValNoT(">= 1.6")},
			{Op: OpTest, Path: MustParsePath(`/provider/"aws"[1]`), Absent: true},
		}},
	}, {
		name: "an ordinal that is not the anchor's last segment",
		tl: TransformList{Target: "t.tf", Format: FormatHCL, Transform: []Transform{
			{Op: OpTest, Path: MustParsePath(`/provider/"aws"[1]/settings/region`), Value: mustValNoT("us-east-1")},
		}},
	}, {
		name: "a remove with no test to supply the removed value",
		tl: TransformList{Target: "t.json", Format: FormatJSON, Transform: []Transform{
			{Op: OpRemove, Path: MustParsePath("/server/host")},
		}},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Render(tc.tl, RenderOptions{Preamble: true}); !errors.Is(err, ErrInexpressible) {
				t.Fatalf("want ErrInexpressible, got %v", err)
			}
		})
	}
}

func TestRenderAgainstCorpusRoundtripFixtures(t *testing.T) {
	families := []string{"json", "jsonc", "yaml", "toml", "hcl"}
	for _, fam := range families {
		t.Run(fam, func(t *testing.T) {
			dir := corpusDir(t, fam+"/roundtrip-basic")
			expectedHew := readFile(t, dir+"/expected.hew")
			tls, err := Parse(expectedHew)
			if err != nil {
				t.Fatalf("Parse(expected.hew): %v", err)
			}
			for _, tl := range tls {
				rt2(t, tl)
			}
		})
	}
}
