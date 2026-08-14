package hew

import "testing"

// §8.4's surface duality is the TOML behaviour most exposed to changes in the
// mirror grammar: a table header, a dotted key and an array-of-tables all
// reach the lowering through mirrorEntry paths that HCL's block entries also
// used. These pin what the lowering produces for each spelling, so a change
// that removes the block half cannot quietly take a table branch with it.
//
// They assert the LOWERED transform — path, op, surface and value — because
// that is the contract §9.1 states and the only thing a later stage sees.

func lowerOne(t *testing.T, patch string) TransformList {
	t.Helper()
	tls, err := Parse([]byte(patch))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("got %d file sections, want 1", len(tls))
	}
	return tls[0]
}

func wantTransform(t *testing.T, tl TransformList, i int, op OpKind, path string, surface Surface) Transform {
	t.Helper()
	if len(tl.Transform) <= i {
		t.Fatalf("want at least %d transforms, got %d", i+1, len(tl.Transform))
	}
	got := tl.Transform[i]
	if got.Op != op {
		t.Errorf("transform %d op = %v, want %v", i, got.Op, op)
	}
	if got.Path.String() != path {
		t.Errorf("transform %d path = %s, want %s", i, got.Path, path)
	}
	if got.Surface != surface {
		t.Errorf("transform %d surface = %v, want %v", i, got.Surface, surface)
	}
	return got
}

// A nested table header names a nested NODE: `[a.b]` under an `/a` anchor is
// /a/b, one segment per dotted component, not one segment spelled "a.b".
func TestTOMLTableHeaderLowersToNestedSegments(t *testing.T) {
	tl := lowerOne(t, "hew: 1\n\n--- t.toml format=toml\n\n@@ / @@\n  [a]\n  k = 1\n+ [a.b]\n+ m = 2\n")
	wantTransform(t, tl, 0, OpTest, "/a/k", "")
	got := wantTransform(t, tl, 1, OpAdd, "/a/b", "")
	if got.Value.String() != "{m: 2}" {
		t.Errorf("added value = %s, want the table's own members", got.Value.String())
	}
}

// `! surface table` rides the ADD as a qualifier and does not change the
// address: the node is the same one a dotted spelling would name (§8.4, §9.6).
func TestTOMLSurfaceDirectiveQualifiesWithoutChangingTheAddress(t *testing.T) {
	tl := lowerOne(t, "hew: 1\n\n--- t.toml format=toml\n\n@@ /mcp_servers @@\n! surface table\n+ [mcp_servers.taskloom]\n+ command = \"taskloom\"\n")
	got := wantTransform(t, tl, 0, OpAdd, "/mcp_servers/taskloom", SurfaceTable)
	if got.Value.String() != "{command: taskloom}" {
		t.Errorf("added value = %s", got.Value.String())
	}
}

// An array-of-tables element is POSITIONAL: `[[plugin]]` adds AT the container
// rather than at a key of its own, which is what positional() decides.
func TestTOMLArrayOfTablesAddsAtTheContainer(t *testing.T) {
	tl := lowerOne(t, "hew: 1\n\n--- t.toml format=toml\n\n@@ /plugin @@\n  [[plugin]]\n  name = \"beta\"\n+ [[plugin]]\n+ name = \"gamma\"\n")
	// The context element is asserted by its identity, not its index.
	wantTransform(t, tl, 0, OpTest, "/plugin/name=beta/name", "")
	got := wantTransform(t, tl, 1, OpAdd, "/plugin", "")
	if got.Value.String() != "{name: gamma}" {
		t.Errorf("added value = %s", got.Value.String())
	}
}

// A dotted key written as an ordinary member line stays the member it was
// written as. This is the counterpart to the table-header case above: the two
// spellings are NOT normalised into each other at parse time — §8.4 keeps the
// surface the document's, and the applier decides.
func TestTOMLDottedMemberLineKeepsItsWrittenForm(t *testing.T) {
	tl := lowerOne(t, "hew: 1\n\n--- t.toml format=toml\n\n@@ /tool @@\n  name = \"x\"\n+ opts.verbose = true\n")
	wantTransform(t, tl, 0, OpTest, "/tool/name", "")
	wantTransform(t, tl, 1, OpAdd, "/tool/opts.verbose", "")
}
