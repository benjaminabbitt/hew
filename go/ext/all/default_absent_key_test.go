package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// TestDefaultCreatesAbsentTopLevelKey pins OP-04's `! default` (Sel.Default)
// against the shape a caller like ctxloom's .mcp.json writer hits: a key
// directly under the document ROOT — always present — that may or may not
// exist yet ("mcpServers").
//
// This file used to exercise each address twice, with and without a trailing
// `?`, and measured that the two were FULLY REDUNDANT: `Sel.Default` lowers to
// Op:Add, OnConflict:ConflictKeep (OP-04), and every binding's add-planning
// (ext/{json,jsonc,yaml,toml}'s planAdd / planInsert) treats a last-segment
// SegKey whose immediate parent already resolves — root always does — as
// creatable UNCONDITIONALLY: on_conflict is consulted only in the branch
// reached when the node ALREADY exists, never in the "not found" branch that
// runs here. The `?` changed nothing observable, because the op's own semantics
// already provide the "create if absent" it promised.
//
// That measurement is why the optional segment was retired rather than wired
// up (§4.7). `! default` is the spelling, and it is the only one: `?` no
// longer means anything in a path at all, which
// TestOptionalSegmentAddressIsAnOrdinaryKey below pins.
func TestDefaultCreatesAbsentTopLevelKey(t *testing.T) {
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
		for _, addr := range []string{"/mcpServers"} {
			t.Run(string(c.format)+"/"+addr, func(t *testing.T) {
				doc, err := hew.OpenBytes(c.name, []byte(c.src), hew.As(c.format))
				if err != nil {
					t.Fatalf("OpenBytes: %v", err)
				}
				p, err := hew.ParsePathIn(c.format, addr)
				if err != nil {
					t.Fatalf("ParsePathIn(%q): %v", addr, err)
				}
				doc.AtPath(p).Default(map[string]any{})
				out, err := doc.Bytes()
				if err != nil {
					t.Fatalf("Bytes: %v (addr=%q)", err, addr)
				}
				np, err := hew.ParsePathIn(c.format, "/mcpServers")
				if err != nil {
					t.Fatalf("ParsePathIn(check): %v", err)
				}
				checkDoc, err := hew.OpenBytes(c.name, out, hew.As(c.format))
				if err != nil {
					t.Fatalf("re-open written bytes: %v\nout=%s", err, out)
				}
				checkDoc.AtPath(np).Assert(map[string]any{})
				if _, err := checkDoc.Bytes(); err != nil {
					t.Fatalf("mcpServers not present as an empty map after Default: %v\nout=%s", err, out)
				}
			})
		}
	}
}

// TestOptionalSegmentAddressIsAnOrdinaryKey is the other side of the ruling.
// `! default` is the spelling for "create if absent", and there is no second
// one — but the way that is enforced is by `?` MEANING NOTHING, not by hew
// reserving it. `/mcpServers?` therefore addresses a key whose name really is
// `mcpServers?`, in every format, which is what any other punctuation in a key
// already does.
//
// The refusal that stood here was migration scaffolding: it named the
// replacement for a stale patch, and charged for that by keeping `?` reserved
// in a grammar where it means nothing. What stops a second spelling coming
// back is that `?` is not a flag, not that it is refused.
func TestOptionalSegmentAddressIsAnOrdinaryKey(t *testing.T) {
	for _, f := range []hew.FormatID{hew.FormatJSON, hew.FormatJSONC, hew.FormatYAML, hew.FormatTOML} {
		t.Run(string(f), func(t *testing.T) {
			p, err := hew.ParsePathIn(f, "/mcpServers?")
			if err != nil {
				t.Fatalf(`ParsePathIn("/mcpServers?"): %v`, err)
			}
			if seg := p.Segment(0); seg.Kind != hew.SegKey || seg.Name != "mcpServers?" {
				t.Fatalf("segment = %v/%q, want the key mcpServers?", seg.Kind, seg.Name)
			}
		})
	}
}

// TestDefaultLeavesPresentKeyAlone is OP-04's non-clobbering half: a key that
// already exists is left exactly as the user wrote it.
func TestDefaultLeavesPresentKeyAlone(t *testing.T) {
	for _, c := range []struct {
		format hew.FormatID
		name   string
		src    string
	}{
		{hew.FormatJSON, "config.json", "{\n  \"mcpServers\": {\n    \"mine\": {}\n  }\n}\n"},
		{hew.FormatYAML, "config.yaml", "mcpServers:\n  mine: {}\n"},
	} {
		for _, addr := range []string{"/mcpServers"} {
			t.Run(string(c.format)+"/"+addr, func(t *testing.T) {
				doc, err := hew.OpenBytes(c.name, []byte(c.src), hew.As(c.format))
				if err != nil {
					t.Fatalf("OpenBytes: %v", err)
				}
				p, err := hew.ParsePathIn(c.format, addr)
				if err != nil {
					t.Fatalf("ParsePathIn(%q): %v", addr, err)
				}
				doc.AtPath(p).Default(map[string]any{"clobbered": true})
				out, err := doc.Bytes()
				if err != nil {
					t.Fatalf("Bytes: %v (addr=%q)", err, addr)
				}
				if string(out) != c.src {
					t.Fatalf("Default on a PRESENT key must be a no-op (§7.7); got %q, want unchanged %q", out, c.src)
				}
			})
		}
	}
}
