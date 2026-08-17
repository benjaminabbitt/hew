package all

import (
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// TestDefaultCreatesAbsentTopLevelKey pins OP-04's `! default` (Sel.Default)
// against the shape a caller like ctxloom's .mcp.json writer hits: a key
// directly under the document ROOT — always present — that may or may not
// exist yet ("mcpServers"). §4.4's trailing `?` on the address is documented
// as a second, independent way to say "match it, or create it" for the same
// last-segment-absent shape.
//
// Both are exercised here, addressed with and without the trailing "?", and
// both create the key. Measured: they are FULLY REDUNDANT for this shape.
// Sel.Default already lowers to Op:Add, OnConflict:ConflictKeep (OP-04), and
// every binding's add-planning (ext/{json,jsonc,yaml,toml}'s planAdd /
// planInsert) treats a last-segment SegKey whose immediate parent already
// resolves — root always does — as creatable UNCONDITIONALLY: on_conflict is
// consulted only in the branch reached when the node ALREADY exists, never in
// the "not found" branch that runs here. So a trailing "?" on the address
// changes nothing observable for `add`/`! default`: the op's own semantics
// already provide the "create if absent" `?` promises. Segment.Optional (the
// parsed "?" flag) is consulted nowhere in the resolution pipeline — see
// go/resolve.go's resolver.walk/step/createPointer and each ext package's own
// private resolver — which is consistent with §4.4 restricting "?" to a hunk
// ANCHOR: a fluent-API Sel whose own address IS the anchor, with no children
// written beneath it, is exactly the case where the anchor's own op already
// decides "absent" for itself.
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
		for _, addr := range []string{"/mcpServers", "/mcpServers?"} {
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

// TestDefaultLeavesPresentKeyAlone is OP-04's non-clobbering half: a key that
// already exists is left exactly as the user wrote it, `?` or no `?`.
func TestDefaultLeavesPresentKeyAlone(t *testing.T) {
	for _, c := range []struct {
		format hew.FormatID
		name   string
		src    string
	}{
		{hew.FormatJSON, "config.json", "{\n  \"mcpServers\": {\n    \"mine\": {}\n  }\n}\n"},
		{hew.FormatYAML, "config.yaml", "mcpServers:\n  mine: {}\n"},
	} {
		for _, addr := range []string{"/mcpServers", "/mcpServers?"} {
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
