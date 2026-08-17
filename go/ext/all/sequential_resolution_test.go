package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// SEQUENTIAL RESOLUTION (ruled by the human): each transform resolves against
// the document AS MODIFIED BY the transforms before it, not against a single
// frozen pre-image (docs/hew-spec.md §9.2, §9.3).
//
// The motivating case: a container created by one op must be visible to the
// next op that writes into it. `CreateIfMissing` (opening a MISSING FILE as
// an empty document) is a different concern and does not satisfy this — the
// document exists throughout; what is missing is the /mcpServers KEY inside
// it, created mid-list by the first transform.

// TestSequentialResolutionCreatedContainerThenChildWrite is the brief's
// motivating case, across every format that ships an applier:
//
//	doc.AtPath("/mcpServers").Default(map[string]any{})   // creates the container
//	doc.AtPath("/mcpServers/ctxloom").Set(...)             // must see it
//
// Before sequential resolution this failed HEW013 on the second op: it
// resolved against the ORIGINAL image, where /mcpServers does not exist yet.
func TestSequentialResolutionCreatedContainerThenChildWrite(t *testing.T) {
	for _, c := range []struct {
		format hew.FormatID
		name   string
		src    string
	}{
		{hew.FormatJSON, "config.json", "{\n  \"port\": 8080\n}\n"},
		{hew.FormatJSONC, ".mcp.json", "{\n  \"port\": 8080\n}\n"},
		{hew.FormatYAML, "config.yaml", "port: 8080\n"},
		{hew.FormatTOML, "config.toml", "port = 8080\n"},
	} {
		t.Run(string(c.format), func(t *testing.T) {
			doc, err := hew.OpenBytes(c.name, []byte(c.src), hew.As(c.format))
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			doc.AtPath(hew.MustParsePath("/mcpServers")).Default(map[string]any{})
			doc.AtPath(hew.MustParsePath("/mcpServers/ctxloom")).Set(map[string]any{"command": "ctxloom"})
			out, err := doc.Bytes()
			if err != nil {
				t.Fatalf("Bytes: %v (the second op must resolve against what the first op created, §9.2/§9.3)", err)
			}

			// Confirm the write really landed, by reopening the produced bytes
			// and reading /mcpServers/ctxloom/command back.
			checkDoc, err := hew.OpenBytes(c.name, out, hew.As(c.format))
			if err != nil {
				t.Fatalf("re-open written bytes: %v\nout=%s", err, out)
			}
			checkDoc.AtPath(hew.MustParsePath("/mcpServers/ctxloom/command")).Assert("ctxloom")
			if _, err := checkDoc.Bytes(); err != nil {
				t.Fatalf("/mcpServers/ctxloom/command not present after the sequence: %v\nout=%s", err, out)
			}
		})
	}
}

// TestSequentialResolutionAddThenAddSiblingIntoNewContainer is the same
// shape one level further: two children written into a container the first
// op creates, addressed independently rather than chained by relative
// placement (the pendingAdd sibling-chaining mechanism already handled
// PLACEMENT; this exercises ordinary key addressing into a brand-new node).
//
// The starting documents carry one pre-existing top-level key rather than
// being fully empty: an empty YAML/JSON root has its own pre-existing,
// sequential-resolution-UNRELATED limitation (a root written in flow style
// with nothing in it, "{}\n", has no owning entry for the binding to expand
// — the same rule TestSequentialResolutionAddThenAddSiblingIntoNewContainer
// would otherwise trip over one level higher than the case it means to
// exercise). A non-empty root sidesteps that and keeps this test aimed at
// the one thing it is for.
func TestSequentialResolutionAddThenAddSiblingIntoNewContainer(t *testing.T) {
	for _, c := range []struct {
		format hew.FormatID
		name   string
		src    string
	}{
		{hew.FormatJSON, "config.json", "{\n  \"port\": 8080\n}\n"},
		{hew.FormatJSONC, ".mcp.json", "{\n  \"port\": 8080\n}\n"},
		{hew.FormatYAML, "config.yaml", "port: 8080\n"},
		{hew.FormatTOML, "config.toml", "port = 8080\n"},
	} {
		t.Run(string(c.format), func(t *testing.T) {
			doc, err := hew.OpenBytes(c.name, []byte(c.src), hew.As(c.format))
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			doc.AtPath(hew.MustParsePath("/mcpServers")).Default(map[string]any{})
			doc.AtPath(hew.MustParsePath("/mcpServers/a")).Add("first")
			doc.AtPath(hew.MustParsePath("/mcpServers/b")).Add("second")
			out, err := doc.Bytes()
			if err != nil {
				t.Fatalf("Bytes: %v", err)
			}
			checkDoc, err := hew.OpenBytes(c.name, out, hew.As(c.format))
			if err != nil {
				t.Fatalf("re-open written bytes: %v\nout=%s", err, out)
			}
			checkDoc.AtPath(hew.MustParsePath("/mcpServers/a")).Assert("first")
			checkDoc.AtPath(hew.MustParsePath("/mcpServers/b")).Assert("second")
			if _, err := checkDoc.Bytes(); err != nil {
				t.Fatalf("both children not present after the sequence: %v\nout=%s", err, out)
			}
		})
	}
}

// TestSequentialResolutionTestSeesEarlierWrite settles the brief's HARD PART
// 1: under sequential resolution a `test` transform resolves and evaluates
// against the document AS MODIFIED by the transforms before it — the same
// rule as every other op, uniformly, replacing the old "evaluate every test
// before any mutation" two-phase model (§9.3). A `? expect` asserting a value
// an EARLIER op in the same list just wrote must see that write, not the
// original pre-image.
func TestSequentialResolutionTestSeesEarlierWrite(t *testing.T) {
	for _, c := range []struct {
		format hew.FormatID
		name   string
		src    string
	}{
		{hew.FormatJSON, "config.json", "{\n  \"port\": 8080\n}\n"},
		{hew.FormatJSONC, ".mcp.json", "{\n  \"port\": 8080\n}\n"},
		{hew.FormatYAML, "config.yaml", "port: 8080\n"},
		{hew.FormatTOML, "config.toml", "port = 8080\n"},
	} {
		t.Run(string(c.format), func(t *testing.T) {
			b, ok := hew.Lookup(c.format)
			if !ok || b.Applier == nil {
				t.Fatalf("%q has no applier", c.format)
			}
			// add /mode "on", then test /mode expects "on" — the value only
			// the FIRST transform, not the original document, holds.
			tl := hew.TransformList{
				Target: c.name, Format: c.format,
				Transform: []hew.Transform{
					{Op: hew.OpAdd, Path: hew.MustParsePath("/mode"), Value: strVal(t, "on"), OnConflict: hew.ConflictReplace},
					{Op: hew.OpTest, Path: hew.MustParsePath("/mode"), Value: strVal(t, "on")},
				},
			}
			out, err := b.Applier([]byte(c.src), tl)
			if err != nil {
				t.Fatalf("Applier: %v (a test after its own list's earlier write must see that write, §9.3)", err)
			}
			if len(out) == 0 {
				t.Fatal("empty output")
			}
		})
	}
}

// strVal builds a hew.Value carrying a plain string, for a test transform's
// asserted value.
func strVal(t *testing.T, s string) hew.Value {
	t.Helper()
	v, err := hew.ValueOf(s)
	if err != nil {
		t.Fatalf("ValueOf(%q): %v", s, err)
	}
	return v
}

