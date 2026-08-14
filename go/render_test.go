package hew

import (
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

func TestRenderAgainstCorpusRoundtripFixtures(t *testing.T) {
	families := []string{"json", "jsonc", "yaml"}
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
