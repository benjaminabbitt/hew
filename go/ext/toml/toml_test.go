package toml

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew/go"
	"github.com/benjaminabbitt/hew/go/internal/hewerr"
)

// hewtDoc wraps transform records in a .hewt document for the "t.toml" target.
func hewtDoc(records string) string {
	return "hew-transforms: 1\ntarget: t.toml\nformat: toml\ntransforms:\n" + records
}

func applyIR(t *testing.T, target, records string) ([]byte, error) {
	t.Helper()
	tl, err := hew.UnmarshalTransforms([]byte(hewtDoc(records)))
	if err != nil {
		t.Fatalf("test fixture is not valid .hewt: %v", err)
	}
	return Apply([]byte(target), tl)
}

// mustApply applies and asserts the exact output bytes.
func mustApply(t *testing.T, target, records, want string) {
	t.Helper()
	got, err := applyIR(t, target, records)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != want {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// mustFail applies and asserts the error's code and path; it also asserts the
// all-or-nothing contract (§10.5) that no bytes come back with an error.
func mustFail(t *testing.T, target, records string, code hewerr.Code, path string) *hewerr.Error {
	t.Helper()
	got, err := applyIR(t, target, records)
	if err == nil {
		t.Fatalf("expected %s, got success:\n%s", code, got)
	}
	if got != nil {
		t.Errorf("non-nil bytes alongside an error (all-or-nothing violated)")
	}
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("error is not a *hewerr.Error: %v", err)
	}
	if he.Code != code {
		t.Errorf("code: want %s, got %s (%v)", code, he.Code, err)
	}
	if path != "" && he.Path != path {
		t.Errorf("path: want %s, got %s", path, he.Path)
	}
	if he.Component != hewerr.ComponentApplier {
		t.Errorf("component: want applier, got %s", he.Component)
	}
	return he
}

func mustContain(t *testing.T, he *hewerr.Error, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(he.Error(), s) {
			t.Errorf("message %q does not contain %q", he.Error(), s)
		}
	}
}

func mustParse(t *testing.T, src string) *doc {
	t.Helper()
	d, err := parseDoc([]byte(src))
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return d
}

// scalarAt walks a dotted path to a scalar node, for reader assertions.
func scalarAt(t *testing.T, d *doc, path ...string) *tnode {
	t.Helper()
	n := d.root
	for _, k := range path {
		e := n.lookup(k)
		if e == nil {
			t.Fatalf("no key %q under %v", k, n.path)
		}
		n = e.val
	}
	return n
}

// --- the surface duality, §8.4 ---------------------------------------------

// An edit to an existing path lands at whichever surface the target uses; hew
// never adds a second surface for it (§8.4 rule 1). The same transform is
// applied to the same tree written three ways.
func TestEditAdoptsTheSurfaceTheTargetAlreadyUses(t *testing.T) {
	set := "  - op: replace\n    path: /tool/ctxloom/timeout\n    value: 60\n"
	for _, tc := range []struct{ name, target, want string }{
		{"dotted", "tool.ctxloom.timeout = 30\n", "tool.ctxloom.timeout = 60\n"},
		{"header", "[tool.ctxloom]\ntimeout = 30\n", "[tool.ctxloom]\ntimeout = 60\n"},
		{"split", "[tool]\nctxloom.timeout = 30\n", "[tool]\nctxloom.timeout = 60\n"},
		{"inline", "tool = {ctxloom = {timeout = 30}}\n", "tool = {ctxloom = {timeout = 60}}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { mustApply(t, tc.target, set, tc.want) })
	}
}

// §8.4 rule 2: a path defined at two surfaces is refused, not resolved. The
// refusal names the LEAF, which is the node that genuinely has two values —
// the table above it has only one definition site.
func TestTwoSurfacesForOnePathIsSurfaceAmbiguity(t *testing.T) {
	target := "tool.ctxloom.timeout = 30\n\n[tool.ctxloom]\ntimeout = 45\n"
	he := mustFail(t, target, "  - op: replace\n    path: /tool/ctxloom/timeout\n    value: 60\n",
		hewerr.CodeSurfaceAmbiguity, "/tool/ctxloom/timeout")
	mustContain(t, he, "surface-ambiguity", "tool.ctxloom.timeout", "§8.4 rule 2", "2 surfaces")
}

// The ambiguity is raised the moment the path resolves THROUGH the doubly
// defined node, not only when it ends there: here /a/b is written both as an
// inline table and as a table header.
func TestSurfaceAmbiguityOnAnIntermediateTable(t *testing.T) {
	target := "a.b = {x = 1}\n\n[a.b]\ny = 2\n"
	he := mustFail(t, target, "  - op: test\n    path: /a/b/x\n    value: 1\n",
		hewerr.CodeSurfaceAmbiguity, "/a/b")
	mustContain(t, he, "surface-ambiguity", "a.b")
}

// A doubly defined path poisons only itself: an edit elsewhere in the same
// document still lands on the right line, because each definition keeps its
// own bytes even though they share one node.
func TestASecondDefinitionDoesNotStealTheFirstsBytes(t *testing.T) {
	mustApply(t, "a.b = 1\n\n[a]\nb = 2\n", "  - op: add\n    path: /z\n    value: 9\n",
		"a.b = 1\nz = 9\n\n[a]\nb = 2\n")
}

// An `? absent` assert cannot swallow a surface ambiguity: the refusal is
// final, so it outranks the assertion's own tolerance.
func TestSurfaceAmbiguityIsFinalUnderAbsent(t *testing.T) {
	target := "a.b = 1\n\n[a]\nb = 2\n"
	mustFail(t, target, "  - op: test\n    path: /a/b\n    absent: true\n",
		hewerr.CodeSurfaceAmbiguity, "/a/b")
}

// §8.4 rule 3: a creation adopts the surface of its nearest existing ancestor.
func TestCreationAdoptsTheNearestAncestorsSurface(t *testing.T) {
	add := "  - op: add\n    path: /tool/ctxloom/retries\n    value: 3\n"
	t.Run("dotted ancestor keeps the dotted spelling", func(t *testing.T) {
		mustApply(t, "title = \"c\"\ntool.ctxloom.timeout = 30\n", add,
			"title = \"c\"\ntool.ctxloom.timeout = 30\ntool.ctxloom.retries = 3\n")
	})
	t.Run("table ancestor gains a body line", func(t *testing.T) {
		mustApply(t, "[tool.ctxloom]\ntimeout = 30\n", add,
			"[tool.ctxloom]\ntimeout = 30\nretries = 3\n")
	})
	t.Run("partial header keeps the residual dotted", func(t *testing.T) {
		mustApply(t, "[tool]\nctxloom.timeout = 30\n", add,
			"[tool]\nctxloom.timeout = 30\nctxloom.retries = 3\n")
	})
	t.Run("inline ancestor is edited in place", func(t *testing.T) {
		mustApply(t, "tool = {ctxloom = {timeout = 30}}\n", add,
			"tool = {ctxloom = {timeout = 30, retries = 3}}\n")
	})
	t.Run("empty inline ancestor gains its first member", func(t *testing.T) {
		mustApply(t, "tool = {ctxloom = {}}\n", add, "tool = {ctxloom = {retries = 3}}\n")
	})
}

// Rule 3's last clause: where nothing exists, the creation writes a table
// header at the end of the document.
func TestCreationWithNoAncestorWritesATableHeader(t *testing.T) {
	mustApply(t, "title = \"c\"\n", "  - op: add\n    path: /a/b/c\n    value: 5\n",
		"title = \"c\"\n\n[a.b]\nc = 5\n")
}

// A table that exists only as a PREFIX of some other header has no body of its
// own, so a child creation gives it one rather than inventing a dotted key
// that TOML would reject next to the existing sub-table.
func TestCreationUnderAHeaderPrefixTableWritesItsOwnHeader(t *testing.T) {
	mustApply(t, "[a.b]\nx = 1\n", "  - op: add\n    path: /a/y\n    value: 2\n",
		"[a.b]\nx = 1\n\n[a]\ny = 2\n")
}

// §8.4 rule 4: `! surface table` overrides rule 3 for a creation, and the
// header belongs to the ADDED CHILD (§2.3's exception).
func TestSurfaceTableDirectiveWritesTheChildsOwnHeader(t *testing.T) {
	mustApply(t, "[mcp_servers]\n\n[mcp_servers.ctxloom]\ncommand = \"ctxloom\"\n",
		"  - op: add\n    path: /mcp_servers/taskloom\n    surface: table\n"+
			"    value:\n      command: taskloom\n      args: [mcp]\n",
		"[mcp_servers]\n\n[mcp_servers.ctxloom]\ncommand = \"ctxloom\"\n\n"+
			"[mcp_servers.taskloom]\ncommand = \"taskloom\"\nargs = [\"mcp\"]\n")
}

// `! surface dotted` is the other half of rule 4: it forces the assignment
// spelling where rule 3 would have chosen a header.
func TestSurfaceDottedDirectiveForcesAnAssignment(t *testing.T) {
	mustApply(t, "title = \"c\"\n",
		"  - op: add\n    path: /a/b/c\n    surface: dotted\n    value: 5\n",
		"title = \"c\"\na.b.c = 5\n")
}

func TestSurfaceTableDirectiveNeedsATableValue(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1\n", "  - op: add\n    path: /a/b\n    surface: table\n    value: 5\n",
		hewerr.CodeInexpressible, "/a/b")
	mustContain(t, he, "[a.b]", "§8.4 rule 4")
}

func TestSurfaceDottedDirectiveNeedsAnAncestorWithABody(t *testing.T) {
	he := mustFail(t, "[a.b]\nx = 1\n", "  - op: add\n    path: /a/y\n    surface: dotted\n    value: 2\n",
		hewerr.CodeInexpressible, "/a/y")
	mustContain(t, he, "surface dotted", "§8.4 rule 3")
}

// `! surface` chooses where a CREATION goes. Over a path that already exists,
// an upsert would have to migrate the surface to honour it, so it refuses
// rather than writing at the old surface and saying nothing (§9.3).
func TestSurfaceDirectiveOverAnExistingPathIsRefused(t *testing.T) {
	he := mustFail(t, "tool.ctxloom.timeout = 30\n",
		"  - op: add\n    path: /tool/ctxloom\n    surface: table\n    on_conflict: replace\n"+
			"    value:\n      timeout: 60\n", hewerr.CodeInexpressible, "/tool/ctxloom")
	mustContain(t, he, "§8.4 rule 4", "already exists")
	// `! default` writes nothing at all, so the directive stays moot.
	mustApply(t, "tool.ctxloom.timeout = 30\n",
		"  - op: add\n    path: /tool/ctxloom\n    surface: table\n    on_conflict: keep\n"+
			"    value:\n      timeout: 60\n", "tool.ctxloom.timeout = 30\n")
}

// Surface migration is not a v0 operation (§8.4 rule 4, O10): a table written
// as a header has no value text a write could overwrite.
func TestWritingOverAHeaderTableIsRefused(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1\n", "  - op: replace\n    path: /a\n    value:\n      y: 2\n",
		hewerr.CodeInexpressible, "/a")
	mustContain(t, he, "§8.4 rule 4", "no value text")
}

// --- array-of-tables, §8.4 / OP-16 -----------------------------------------

func TestArrayOfTablesElementIsAddressedByKeyMatch(t *testing.T) {
	target := "[[plugin]]\nname = \"alpha\"\n\n[[plugin]]\nname = \"beta\"\n"
	mustApply(t, target, "  - op: replace\n    path: /plugin/name=beta/name\n    value: gamma\n",
		"[[plugin]]\nname = \"alpha\"\n\n[[plugin]]\nname = \"gamma\"\n")
}

func TestArrayOfTablesAppendsAfterTheLastElement(t *testing.T) {
	target := "[[plugin]]\nname = \"alpha\"\n\n[other]\nx = 1\n"
	mustApply(t, target, "  - op: add\n    path: /plugin\n    value:\n      name: beta\n",
		"[[plugin]]\nname = \"alpha\"\n\n[[plugin]]\nname = \"beta\"\n\n[other]\nx = 1\n")
}

func TestArrayOfTablesRespectsBeforePlacement(t *testing.T) {
	target := "[[plugin]]\nname = \"alpha\"\n\n[[plugin]]\nname = \"beta\"\n"
	mustApply(t, target,
		"  - op: add\n    path: /plugin\n    before: /plugin/name=beta\n    value:\n      name: mid\n",
		"[[plugin]]\nname = \"alpha\"\n\n[[plugin]]\nname = \"mid\"\n\n[[plugin]]\nname = \"beta\"\n")
}

func TestArrayOfTablesElementMustBeATable(t *testing.T) {
	he := mustFail(t, "[[plugin]]\nname = \"a\"\n", "  - op: add\n    path: /plugin\n    value: 5\n",
		hewerr.CodeInexpressible, "/plugin")
	mustContain(t, he, "array-of-tables element is a table")
}

func TestArrayOfTablesElementIsRemovedWholesale(t *testing.T) {
	target := "[[plugin]]\nname = \"alpha\"\n\n[[plugin]]\nname = \"beta\"\n"
	mustApply(t, target, "  - op: remove\n    path: /plugin/name=beta\n",
		"[[plugin]]\nname = \"alpha\"\n\n")
}

func TestKeyMatchMatchingTwoElementsIsAmbiguous(t *testing.T) {
	target := "[[p]]\nname = \"x\"\n\n[[p]]\nname = \"x\"\n"
	he := mustFail(t, target, "  - op: replace\n    path: /p/name=x/name\n    value: y\n",
		hewerr.CodeAmbiguousMatch, "/p/name=x")
	mustContain(t, he, "2 elements match", "§6.4.2")
}

func TestKeyMatchMatchingNothingIsNoMatch(t *testing.T) {
	mustFail(t, "[[p]]\nname = \"x\"\n", "  - op: replace\n    path: /p/name=q/name\n    value: y\n",
		hewerr.CodeNoMatch, "/p/name=q")
}

func TestBareValueKeyMatchAddressesAnArrayItem(t *testing.T) {
	mustApply(t, "tags = [\"a\", \"b\"]\n", "  - op: remove\n    path: /tags/=b\n", "tags = [\"a\"]\n")
}

// --- inline collections ------------------------------------------------------

func TestInlineArrayItemsAreAddedAndRemoved(t *testing.T) {
	t.Run("append", func(t *testing.T) {
		mustApply(t, "tags = [\"a\"]\n", "  - op: add\n    path: /tags\n    value: b\n",
			"tags = [\"a\", \"b\"]\n")
	})
	t.Run("into an empty array", func(t *testing.T) {
		mustApply(t, "tags = []\n", "  - op: add\n    path: /tags\n    value: a\n", "tags = [\"a\"]\n")
	})
	t.Run("before the first item", func(t *testing.T) {
		mustApply(t, "tags = [\"a\", \"b\"]\n",
			"  - op: add\n    path: /tags\n    before: /tags/0\n    value: z\n",
			"tags = [\"z\", \"a\", \"b\"]\n")
	})
	t.Run("after a named item", func(t *testing.T) {
		mustApply(t, "tags = [\"a\", \"b\"]\n",
			"  - op: add\n    path: /tags\n    after: /tags/0\n    value: z\n",
			"tags = [\"a\", \"z\", \"b\"]\n")
	})
	t.Run("remove the first of many", func(t *testing.T) {
		mustApply(t, "tags = [\"a\", \"b\"]\n", "  - op: remove\n    path: /tags/0\n", "tags = [\"b\"]\n")
	})
	t.Run("remove the only one", func(t *testing.T) {
		mustApply(t, "tags = [\"a\"]\n", "  - op: remove\n    path: /tags/0\n", "tags = []\n")
	})
}

func TestInlineTableMembersAreRemovedWithTheirSeparator(t *testing.T) {
	t.Run("last", func(t *testing.T) {
		mustApply(t, "x = {a = 1, b = 2}\n", "  - op: remove\n    path: /x/b\n", "x = {a = 1}\n")
	})
	t.Run("first", func(t *testing.T) {
		mustApply(t, "x = {a = 1, b = 2}\n", "  - op: remove\n    path: /x/a\n", "x = {b = 2}\n")
	})
	t.Run("only", func(t *testing.T) {
		mustApply(t, "x = {a = 1}\n", "  - op: remove\n    path: /x/a\n", "x = {}\n")
	})
}

func TestInlineTableCannotGainAnIntermediateTable(t *testing.T) {
	he := mustFail(t, "x = {}\n", "  - op: add\n    path: /x/a/b\n    value: 1\n",
		hewerr.CodeInexpressible, "/x/a/b")
	mustContain(t, he, "inline table cannot gain an intermediate table")
}

func TestInlineTableWithDottedKeysParses(t *testing.T) {
	mustApply(t, "x = {a.b = 1}\n", "  - op: replace\n    path: /x/a/b\n    value: 2\n", "x = {a.b = 2}\n")
}

func TestMultiLineArrayKeepsItsLayout(t *testing.T) {
	target := "tags = [\n  # first\n  \"a\",\n  \"b\",\n]\n"
	mustApply(t, target, "  - op: replace\n    path: /tags/1\n    value: z\n",
		"tags = [\n  # first\n  \"a\",\n  \"z\",\n]\n")
}

// --- placement and byte preservation ----------------------------------------

func TestAddPlacesItselfRelativeToContext(t *testing.T) {
	target := "[server]\n# ports below 1024 need CAP_NET_BIND_SERVICE\nport = 8080\ntimeout = 30\n"
	t.Run("after", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/tls\n    after: /server/port\n    value: true\n",
			"[server]\n# ports below 1024 need CAP_NET_BIND_SERVICE\nport = 8080\ntls = true\ntimeout = 30\n")
	})
	t.Run("before, above the leading comment", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/tls\n    before: /server/port\n    value: true\n",
			"[server]\ntls = true\n# ports below 1024 need CAP_NET_BIND_SERVICE\nport = 8080\ntimeout = 30\n")
	})
	t.Run("no placement appends", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/tls\n    value: true\n",
			"[server]\n# ports below 1024 need CAP_NET_BIND_SERVICE\nport = 8080\ntimeout = 30\ntls = true\n")
	})
}

func TestAddIntoAnEmptyTableUsesItsRegion(t *testing.T) {
	mustApply(t, "[a]\n\n[b]\nx = 1\n", "  - op: add\n    path: /a/y\n    value: 2\n",
		"[a]\ny = 2\n\n[b]\nx = 1\n")
	// An empty table has no sibling to place against, so a placement that
	// names one is refused rather than quietly dropped (§9.3).
	he := mustFail(t, "[a]\n\n[b]\nx = 1\n", "  - op: add\n    path: /a/y\n    after: /b/x\n    value: 2\n",
		hewerr.CodeNoMatch, "/b/x")
	mustContain(t, he, "not a child of the container")
}

func TestPlacementSiblingInAnotherTableIsRefused(t *testing.T) {
	target := "[a]\nx = 1\n\n[b]\ny = 2\n"
	he := mustFail(t, target, "  - op: add\n    path: /a/z\n    after: /b/y\n    value: 3\n",
		hewerr.CodeNoMatch, "/b/y")
	mustContain(t, he, "placement sibling is not a child")
}

func TestBlockPlacementSiblingWithNoBlockIsRefused(t *testing.T) {
	target := "x = {a = 1}\n"
	he := mustFail(t, target,
		"  - op: add\n    path: /p\n    surface: table\n    after: /x/a\n    value:\n      k: 1\n",
		hewerr.CodeNoMatch, "/x/a")
	mustContain(t, he, "occupies no block of its own")
}

func TestBlockPlacementBeforeATable(t *testing.T) {
	target := "[a]\nx = 1\n\n[c]\nz = 3\n"
	mustApply(t, target,
		"  - op: add\n    path: /b\n    surface: table\n    before: /c\n    value:\n      y: 2\n",
		"[a]\nx = 1\n\n[b]\ny = 2\n\n[c]\nz = 3\n")
}

func TestATargetWithNoTrailingNewlineGainsOne(t *testing.T) {
	mustApply(t, "[a]\nx = 1", "  - op: add\n    path: /a/y\n    value: 2\n", "[a]\nx = 1\ny = 2\n")
	mustApply(t, "x = 1", "  - op: add\n    path: /b\n    surface: table\n    value:\n      y: 2\n",
		"x = 1\n\n[b]\ny = 2\n")
}

func TestAddingATableToAnEmptyDocumentAddsNoLeadingBlankLine(t *testing.T) {
	mustApply(t, "", "  - op: add\n    path: /a\n    surface: table\n    value:\n      x: 1\n",
		"[a]\nx = 1\n")
}

// Everything the patch did not name keeps its exact bytes: the comment, the
// alignment, the literal-string quoting, the underscore in the integer, and
// the multi-line basic string (§6.3).
func TestUntouchedBytesSurvive(t *testing.T) {
	target := "# top matter\ntitle   =   'my app'   # trailing\n\n[server]\nport = 8_080\n" +
		"motd = \"\"\"\nhello\n\"\"\"\n"
	want := "# top matter\ntitle   =   'my app'   # trailing\n\n[server]\nport = 9090\n" +
		"motd = \"\"\"\nhello\n\"\"\"\n"
	mustApply(t, target, "  - op: test\n    path: /server/port\n    value: 8080\n"+
		"  - op: test\n    path: /server/motd\n    value: \"hello\\n\"\n"+
		"  - op: replace\n    path: /server/port\n    value: 9090\n", want)
}

// A CHANGED scalar adopts the patch's rendering, which is the other half of
// §6.3 — the multi-line string becomes the basic string the value denotes.
func TestAChangedScalarAdoptsThePatchsRendering(t *testing.T) {
	mustApply(t, "motd = \"\"\"\nhello\n\"\"\"\n",
		"  - op: replace\n    path: /motd\n    value: \"goodbye\\n\"\n", "motd = \"goodbye\\n\"\n")
}

// --- comments, §4.5b / OP-30 ------------------------------------------------

func TestCommentLineIsAddedAtItsAddress(t *testing.T) {
	mustApply(t, "[hooks]\non_start = \"ctxloom apply\"\n",
		"  - op: add\n    path: /hooks/#0\n    before: /hooks/on_start\n    value:\n      comment: managed\n",
		"[hooks]\n# managed\non_start = \"ctxloom apply\"\n")
}

func TestStandaloneCommentIsTestedReplacedAndRemoved(t *testing.T) {
	target := "[a]\n# one\n\nx = 1\n"
	mustApply(t, target, "  - op: test\n    path: /a/#0\n    value:\n      comment: one\n"+
		"  - op: replace\n    path: /a/#0\n    value:\n      comment: two\n", "[a]\n# two\n\nx = 1\n")
	mustApply(t, target, "  - op: remove\n    path: /a/#0\n", "[a]\n\nx = 1\n")
}

// A missing node under a `test` is drift (HEW010), the same as any other
// before-image assert that cannot be satisfied; under a write it is HEW013.
func TestCommentAddressOutOfRangeIsMissing(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1\n", "  - op: test\n    path: /a/#0\n    value:\n      comment: q\n",
		hewerr.CodeStaleTarget, "/a/#0")
	mustContain(t, he, "no comment #0")
	he = mustFail(t, "[a]\nx = 1\n", "  - op: remove\n    path: /a/#0\n", hewerr.CodeNoMatch, "/a/#0")
	mustContain(t, he, "remove: node does not exist")
}

func TestCommentTextDrifted(t *testing.T) {
	he := mustFail(t, "[a]\n# one\n\nx = 1\n",
		"  - op: test\n    path: /a/#0\n    value:\n      comment: two\n",
		hewerr.CodeStaleTarget, "/a/#0")
	mustContain(t, he, "comment text differs", "expected two", "found one")
}

func TestTrailingCommentIsAddressed(t *testing.T) {
	mustApply(t, "[a] # header note\nx = 1 # value note\n",
		"  - op: replace\n    path: /a/x/#t\n    value:\n      comment: changed\n"+
			"  - op: replace\n    path: /a/#t\n    value:\n      comment: retitled\n",
		"[a] # retitled\nx = 1 # changed\n")
}

func TestTrailingCommentAbsentOrImpossible(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1\n", "  - op: test\n    path: /a/x/#t\n    value:\n      comment: q\n",
		hewerr.CodeStaleTarget, "/a/x/#t")
	mustContain(t, he, "no trailing comment here")

	he = mustFail(t, "x = 1\n", "  - op: test\n    path: /#t\n    value:\n      comment: q\n",
		hewerr.CodeStaleTarget, "/#t")
	mustContain(t, he, "no line of its own")
}

func TestCommentAddressNeedsACommentValue(t *testing.T) {
	target := "[a]\n# one\n\nx = 1\n"
	he := mustFail(t, target, "  - op: test\n    path: /a/#0\n    value: 5\n",
		hewerr.CodeInexpressible, "/a/#0")
	mustContain(t, he, "§4.5b")
	he = mustFail(t, target, "  - op: replace\n    path: /a/#0\n    value: 5\n",
		hewerr.CodeInexpressible, "/a/#0")
	mustContain(t, he, "§4.5b")
	he = mustFail(t, "[a]\nx = 1\n", "  - op: add\n    path: /a/#0\n    value: 5\n",
		hewerr.CodeInexpressible, "/a/#0")
	mustContain(t, he, "§4.5b")
}

func TestCommentCannotBeAddedToATableWithNoBody(t *testing.T) {
	he := mustFail(t, "x = {a = 1}\n", "  - op: add\n    path: /x/#0\n    value:\n      comment: q\n",
		hewerr.CodeInexpressible, "/x/#0")
	mustContain(t, he, "table with a body of its own")
}

func TestDescendingIntoACommentIsRefused(t *testing.T) {
	he := mustFail(t, "[a]\n# one\n\nx = 1\n", "  - op: test\n    path: /a/#0/x\n    value: 1\n",
		hewerr.CodeStaleTarget, "/a/#0/x")
	mustContain(t, he, "cannot descend into a comment node")
}

// A comment glued to the member below it is that member's leading comment, not
// a free comment of the container (§8.2's rule, which §8.4 inherits); only the
// one with a blank line under it is addressable as /#0.
func TestCommentAtTheRootIsAddressable(t *testing.T) {
	mustApply(t, "# hello\n\nx = 1\n", "  - op: replace\n    path: /#0\n    value:\n      comment: bye\n",
		"# bye\n\nx = 1\n")
	mustFail(t, "# hello\nx = 1\n", "  - op: remove\n    path: /#0\n", hewerr.CodeNoMatch, "/#0")
}

// --- tests, §7.1 -------------------------------------------------------------

func TestValueAssertTolerance(t *testing.T) {
	target := "[server]\nhost = \"localhost\"\nport = 8080\ntags = [\"a\", \"b\", \"c\"]\n"
	// A table asserts as a SUBSET and an array as an ORDERED SUBSEQUENCE.
	mustApply(t, target, "  - op: test\n    path: /server\n    value:\n      port: 8080\n"+
		"  - op: test\n    path: /server/tags\n    value: [a, c]\n"+
		"  - op: replace\n    path: /server/port\n    value: 9090\n",
		"[server]\nhost = \"localhost\"\nport = 9090\ntags = [\"a\", \"b\", \"c\"]\n")
}

func TestValueAssertFailsLoudly(t *testing.T) {
	he := mustFail(t, "[server]\nport = 8080\n", "  - op: test\n    path: /server/port\n    value: 9090\n",
		hewerr.CodeStaleTarget, "/server/port")
	mustContain(t, he, "expected 9090", "found 8080")
}

func TestSubsetAndSubsequenceCanStillFail(t *testing.T) {
	target := "[server]\ntags = [\"a\", \"b\"]\n"
	mustFail(t, target, "  - op: test\n    path: /server/tags\n    value: [b, a]\n",
		hewerr.CodeStaleTarget, "/server/tags")
	mustFail(t, target, "  - op: test\n    path: /server\n    value:\n      port: 1\n",
		hewerr.CodeStaleTarget, "/server")
	mustFail(t, target, "  - op: test\n    path: /server/tags\n    value:\n      a: 1\n",
		hewerr.CodeStaleTarget, "/server/tags")
	mustFail(t, target, "  - op: test\n    path: /server\n    value: [1]\n",
		hewerr.CodeStaleTarget, "/server")
	mustFail(t, target, "  - op: test\n    path: /server\n    value: 1\n",
		hewerr.CodeStaleTarget, "/server")
}

func TestAbsentAssert(t *testing.T) {
	mustApply(t, "[a]\nx = 1\n", "  - op: test\n    path: /a/y\n    absent: true\n"+
		"  - op: add\n    path: /a/y\n    value: 2\n", "[a]\nx = 1\ny = 2\n")
	he := mustFail(t, "[a]\nx = 1\n", "  - op: test\n    path: /a/x\n    absent: true\n",
		hewerr.CodeAssertionFailed, "/a/x")
	mustContain(t, he, "expected absent")
}

func TestCountAndExhaustiveAsserts(t *testing.T) {
	target := "[a]\nx = 1\ny = 2\n"
	mustApply(t, target, "  - op: test\n    path: /a\n    count: 2\n"+
		"  - op: replace\n    path: /a/x\n    value: 3\n", "[a]\nx = 3\ny = 2\n")
	he := mustFail(t, target, "  - op: test\n    path: /a\n    count: 3\n",
		hewerr.CodeAssertionFailed, "/a")
	mustContain(t, he, "? count: mismatch", "expected 3", "found 2")
	he = mustFail(t, target, "  - op: test\n    path: /a\n    count: 1\n    exhaustive: true\n",
		hewerr.CodeAssertionFailed, "/a")
	mustContain(t, he, "exhaustive")
	he = mustFail(t, target, "  - op: test\n    path: /a/x\n    count: 0\n",
		hewerr.CodeAssertionFailed, "/a/x")
	mustContain(t, he, "not a container")
	mustApply(t, "tags = [\"a\"]\n", "  - op: test\n    path: /tags\n    count: 1\n"+
		"  - op: replace\n    path: /tags/0\n    value: b\n", "tags = [\"b\"]\n")
	mustFail(t, target, "  - op: test\n    path: /a/z\n    count: 1\n", hewerr.CodeNoMatch, "/a/z")
}

func TestKindAssert(t *testing.T) {
	target := "[a]\nx = 1\ntags = []\n"
	mustApply(t, target, "  - op: test\n    path: /a\n    kind: map\n"+
		"  - op: test\n    path: /a/x\n    kind: scalar\n"+
		"  - op: test\n    path: /a/tags\n    kind: seq\n"+
		"  - op: replace\n    path: /a/x\n    value: 2\n", "[a]\nx = 2\ntags = []\n")
	he := mustFail(t, target, "  - op: test\n    path: /a/x\n    kind: map\n",
		hewerr.CodeAssertionFailed, "/a/x")
	mustContain(t, he, "? kind: mismatch", "expected map", "found scalar")
	mustFail(t, target, "  - op: test\n    path: /a/q\n    kind: map\n", hewerr.CodeNoMatch, "/a/q")
}

func TestEveryTestIsEvaluatedBeforeAnyMutation(t *testing.T) {
	// The replace would succeed on its own; the later failing test must still
	// leave the target untouched (§9.0, §10.5).
	mustFail(t, "[a]\nx = 1\ny = 2\n",
		"  - op: replace\n    path: /a/x\n    value: 9\n"+
			"  - op: test\n    path: /a/y\n    value: 3\n",
		hewerr.CodeStaleTarget, "/a/y")
}

// --- add semantics, §7.7 -----------------------------------------------------

func TestAddSemanticsVariants(t *testing.T) {
	target := "[mcp_servers.taskloom]\ncommand = \"taskloom-old\"\n"
	add := "  - op: add\n    path: /mcp_servers/taskloom/command\n"
	t.Run("upsert overwrites", func(t *testing.T) {
		mustApply(t, target, add+"    on_conflict: replace\n    value: taskloom\n",
			"[mcp_servers.taskloom]\ncommand = \"taskloom\"\n")
	})
	t.Run("default keeps", func(t *testing.T) {
		mustApply(t, target, add+"    on_conflict: keep\n    value: taskloom\n", target)
	})
	t.Run("plain add refuses", func(t *testing.T) {
		he := mustFail(t, target, add+"    value: taskloom\n", hewerr.CodeAlreadyExists,
			"/mcp_servers/taskloom/command")
		mustContain(t, he, "already exists", "! upsert")
	})
	t.Run("idempotent add over an equal value is a no-op", func(t *testing.T) {
		mustApply(t, target, add+"    idempotent: true\n    value: taskloom-old\n", target)
	})
}

func TestReplaceOverAnEqualValueIsANoOp(t *testing.T) {
	mustApply(t, "[a]\nx = 1\n", "  - op: replace\n    path: /a/x\n    value: 1\n", "[a]\nx = 1\n")
}

func TestReplaceRequiresTheNodeToExist(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1\n", "  - op: replace\n    path: /a/y\n    value: 1\n",
		hewerr.CodeNoMatch, "/a/y")
	mustContain(t, he, "replace requires the node to exist")
}

// --- tolerance flags, §7.5 / §7.6 -------------------------------------------

func TestAlreadyAppliedIsDistinguishedFromDrift(t *testing.T) {
	target := "[a]\nx = 60\n"
	// The before-image assert fails, but the after-image holds: §10.6.
	he := mustFail(t, target, "  - op: test\n    path: /a/x\n    value: 30\n"+
		"  - op: replace\n    path: /a/x\n    value: 60\n", hewerr.CodeAssertionFailed, "/a/x")
	mustContain(t, he, "already applied", "! idempotent")
	// With the tolerance, it converges silently.
	mustApply(t, target, "  - op: test\n    path: /a/x\n    idempotent: true\n    value: 30\n"+
		"  - op: replace\n    path: /a/x\n    idempotent: true\n    value: 60\n", target)
}

func TestAlreadyRemovedIsAlreadyApplied(t *testing.T) {
	target := "[a]\nx = 1\n"
	he := mustFail(t, target, "  - op: test\n    path: /a/y\n    value: 2\n"+
		"  - op: remove\n    path: /a/y\n", hewerr.CodeAssertionFailed, "/a/y")
	mustContain(t, he, "already applied")
	mustApply(t, target, "  - op: test\n    path: /a/y\n    idempotent: true\n    value: 2\n"+
		"  - op: remove\n    path: /a/y\n    idempotent: true\n", target)
}

// A tolerated assert makes its paired STRICT write report the refusal against
// its own line (§7.5's pragma-vs-hunk split).
func TestATolerantAssertDoesNotSilenceAStrictWrite(t *testing.T) {
	target := "[a]\nx = 60\n"
	he := mustFail(t, target, "  - op: test\n    path: /a/x\n    idempotent: true\n    value: 30\n"+
		"  - op: replace\n    path: /a/x\n    value: 60\n", hewerr.CodeAssertionFailed, "/a/x")
	mustContain(t, he, "already applied")
	he = mustFail(t, target, "  - op: test\n    path: /a/x\n    idempotent: true\n    value: 30\n"+
		"  - op: add\n    path: /a/x\n    value: 60\n", hewerr.CodeAssertionFailed, "/a/x")
	mustContain(t, he, "already applied")
}

func TestOptionalAndIdempotentTolerateAMissingNode(t *testing.T) {
	target := "[a]\nx = 1\n"
	mustApply(t, target, "  - op: test\n    path: /a/y\n    optional: true\n    value: 2\n", target)
	mustApply(t, target, "  - op: remove\n    path: /a/y\n    optional: true\n", target)
	mustApply(t, target, "  - op: remove\n    path: /a/y\n    idempotent: true\n", target)
	he := mustFail(t, target, "  - op: remove\n    path: /a/y\n", hewerr.CodeNoMatch, "/a/y")
	mustContain(t, he, "remove: node does not exist")
}

func TestOptionalAndIdempotentDoNotSwallowAFinalError(t *testing.T) {
	target := "a.b = 1\n\n[a]\nb = 2\n"
	mustFail(t, target, "  - op: test\n    path: /a/b\n    optional: true\n    value: 1\n",
		hewerr.CodeSurfaceAmbiguity, "/a/b")
	mustFail(t, target, "  - op: remove\n    path: /a/b\n    optional: true\n",
		hewerr.CodeSurfaceAmbiguity, "/a/b")
	mustFail(t, target, "  - op: add\n    path: /a/b\n    value: 1\n",
		hewerr.CodeSurfaceAmbiguity, "/a/b")
	he := mustFail(t, target, "  - op: replace\n    path: /a/b\n    value: 1\n",
		hewerr.CodeSurfaceAmbiguity, "/a/b")
	// The refusal keeps its own diagnostic; replace does not overwrite it with
	// its "the node must exist" complaint.
	mustContain(t, he, "defined at 2 surfaces")
}

// §10.6's after-image check, in each of the three shapes it takes: a node that
// is already gone, a node that already holds the new scalar, and a container
// that already equals the new container exactly.
func TestAfterImageIsCheckedBeforeTheCodeIsChosen(t *testing.T) {
	// Missing node paired with an ADD is not "already applied" — the add has
	// not happened, the node is simply absent, and that is drift.
	he := mustFail(t, "[a]\nx = 1\n", "  - op: test\n    path: /a/y\n    value: 2\n"+
		"  - op: add\n    path: /a/y\n    value: 2\n", hewerr.CodeStaleTarget, "/a/y")
	mustContain(t, he, "expected 2")

	// A value that is neither the before-image nor the after-image is drift.
	he = mustFail(t, "[a]\nx = 45\n", "  - op: test\n    path: /a/x\n    value: 30\n"+
		"  - op: replace\n    path: /a/x\n    value: 60\n", hewerr.CodeStaleTarget, "/a/x")
	mustContain(t, he, "expected 30", "found 45")

	// A container that already equals the after-image exactly IS "already
	// applied", for both table and array values.
	he = mustFail(t, "x = {a = 1}\n", "  - op: test\n    path: /x\n    value:\n      a: 2\n"+
		"  - op: replace\n    path: /x\n    value:\n      a: 1\n", hewerr.CodeAssertionFailed, "/x")
	mustContain(t, he, "already applied")
	he = mustFail(t, "tags = [1]\n", "  - op: test\n    path: /tags\n    value: [2]\n"+
		"  - op: replace\n    path: /tags\n    value: [1]\n", hewerr.CodeAssertionFailed, "/tags")
	mustContain(t, he, "already applied")
}

// §7.5's tolerance rides the HUNK, so an `! idempotent` on the write alone
// still tolerates the before-image assert that write belongs to.
func TestIdempotenceOnTheWriteAloneToleratesTheAssert(t *testing.T) {
	mustApply(t, "[a]\nx = 1\n", "  - op: test\n    path: /a/y\n    value: 2\n"+
		"  - op: remove\n    path: /a/y\n    idempotent: true\n", "[a]\nx = 1\n")
}

// --- remove ------------------------------------------------------------------

func TestRemoveTakesTheLeadingCommentWithIt(t *testing.T) {
	mustApply(t, "[a]\n# why x exists\nx = 1\ny = 2\n", "  - op: remove\n    path: /a/x\n",
		"[a]\ny = 2\n")
}

func TestRemoveATableRemovesItsHeaderAndBody(t *testing.T) {
	mustApply(t, "[a]\nx = 1\n\n[b]\ny = 2\n", "  - op: remove\n    path: /b\n",
		"[a]\nx = 1\n\n")
}

func TestRemoveTheRootIsRefused(t *testing.T) {
	he := mustFail(t, "x = 1\n", "  - op: remove\n    path: /\n", hewerr.CodeInexpressible, "/")
	mustContain(t, he, "cannot remove the document root")
}

// A table that only the dots of other keys imply has no region of its own, so
// removing it would mean rewriting every line that mentions it.
func TestRemoveAnImpliedTableIsRefused(t *testing.T) {
	he := mustFail(t, "tool.ctxloom.timeout = 30\n", "  - op: remove\n    path: /tool/ctxloom\n",
		hewerr.CodeInexpressible, "/tool/ctxloom")
	mustContain(t, he, "only implied by the keys around it")
}

// --- copy, §9.6 / Appendix C.1 ----------------------------------------------

func TestCopyPreservesTheSourceBytesAndItsComment(t *testing.T) {
	target := "[a]\n# keep me\nold   =   'value'\n"
	mustApply(t, target, "  - op: copy\n    path: /a/new\n    from: /a/old\n"+
		"  - op: remove\n    path: /a/old\n", "[a]\n# keep me\nnew   =   'value'\n")
}

func TestCopyRefusesWhatItCannotSplice(t *testing.T) {
	he := mustFail(t, "x = {a = 1}\n", "  - op: copy\n    path: /y\n    from: /x/a\n",
		hewerr.CodeInexpressible, "/x/a")
	mustContain(t, he, "key/value assignment")

	he = mustFail(t, "[a]\nx = 1\n", "  - op: copy\n    path: /a/x/deeper/still\n    from: /a/x\n",
		hewerr.CodeInexpressible, "/a/x/deeper/still")
	mustContain(t, he, "new key in a table")

	// Each half of the destination contract on its own: a table that does not
	// exist yet, and a destination addressed by something other than a key.
	he = mustFail(t, "x = 1\n", "  - op: copy\n    path: /newa/newb\n    from: /x\n",
		hewerr.CodeInexpressible, "/newa/newb")
	mustContain(t, he, "new key in a table")
	he = mustFail(t, "[a]\nx = 1\n", "  - op: copy\n    path: /a/n=1\n    from: /a/x\n",
		hewerr.CodeInexpressible, "/a/n=1")
	mustContain(t, he, "new key in a table")

	mustFail(t, "[a]\nx = 1\n", "  - op: copy\n    path: /a/y\n    from: /a/q\n",
		hewerr.CodeNoMatch, "/a/q")
}

// --- refusals ----------------------------------------------------------------

func TestUnsupportedQualifiersAreRefusedNotIgnored(t *testing.T) {
	he := mustFail(t, "x = 1\n", "  - op: replace\n    path: /x\n    anchor: rewrite\n    value: 2\n",
		hewerr.CodeInexpressible, "/x")
	mustContain(t, he, "anchor is a YAML alias directive", "§8.3")

	he = mustFail(t, "[[p]]\nx = 1\n", "  - op: replace\n    path: /p[0]/x\n    value: 2\n",
		hewerr.CodeInexpressible, "/p[0]/x")
	mustContain(t, he, "ordinal selectors are not implemented")

	he = mustFail(t, "[[p]]\nx = 1\n", "  - op: copy\n    path: /y\n    from: /p[0]/x\n",
		hewerr.CodeInexpressible, "/y")
	mustContain(t, he, "ordinal selectors are not implemented")
}

func TestNullHasNoTomlSpelling(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1\n", "  - op: replace\n    path: /a/x\n    value: null\n",
		hewerr.CodeInexpressible, "/a/x")
	mustContain(t, he, "no TOML spelling")

	he = mustFail(t, "[a]\nx = 1\n", "  - op: add\n    path: /a/y\n    value: null\n",
		hewerr.CodeInexpressible, "/a/y")
	mustContain(t, he, "no TOML spelling")

	he = mustFail(t, "x = {}\n", "  - op: add\n    path: /x/y\n    value: null\n",
		hewerr.CodeInexpressible, "/x/y")
	mustContain(t, he, "no TOML spelling")

	he = mustFail(t, "tags = []\n", "  - op: add\n    path: /tags\n    value: null\n",
		hewerr.CodeInexpressible, "/tags")
	mustContain(t, he, "no TOML spelling")

	he = mustFail(t, "x = 1\n", "  - op: add\n    path: /a/b\n    value: null\n",
		hewerr.CodeInexpressible, "/a/b")
	mustContain(t, he, "no TOML spelling")
}

func TestNonKeyAddressesInACreationAreRefused(t *testing.T) {
	he := mustFail(t, "tags = [\"a\"]\n", "  - op: add\n    path: /tags/name=x/y\n    value: 1\n",
		hewerr.CodeInexpressible, "/tags/name=x/y")
	mustContain(t, he, "is not a key")
}

func TestCreatingUnderANonTableIsRefused(t *testing.T) {
	he := mustFail(t, "tags = [\"a\"]\n", "  - op: add\n    path: /tags/y\n    value: 1\n",
		hewerr.CodeInexpressible, "/tags/y")
	mustContain(t, he, "nearest existing ancestor is not a table")
}

func TestDescendingIntoAScalarOrArrayByKeyIsRefused(t *testing.T) {
	for _, tc := range []struct{ target, path, msg string }{
		{"x = 1\n", "/x/y", `"y": not a table`},
		{"x = 1\n", "/x/0", "not an array"},
		{"x = 1\n", "/x/n=1", "not an array"},
		{"tags = []\n", "/tags/0", "index 0 out of range"},
		{"tags = [\"a\"]\n", "/tags/-", "no TOML representation"},
	} {
		he := mustFail(t, tc.target, "  - op: test\n    path: "+tc.path+"\n    value: 1\n",
			hewerr.CodeStaleTarget, tc.path)
		mustContain(t, he, tc.msg)
	}
}

func TestOverlappingEditsAreAConflict(t *testing.T) {
	_, err := applyIR(t, "x = {a = 1, b = 2}\n",
		"  - op: remove\n    path: /x/a\n  - op: remove\n    path: /x/b\n")
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeConflict {
		t.Fatalf("want HEW030, got %v", err)
	}
	mustContain(t, he, "overlapping regions")
}

func TestATargetThatIsNotTomlIsATargetParseError(t *testing.T) {
	for _, bad := range []string{
		"x = \n", "x =", "x 1\n", "= 1\n", "[", "[a\n", "[.]\n", "[[a]\n",
		"x = \"open\n", "x = \"\"\"abc", "x = [1\n", "x = [1,\n", "x = [@]\n",
		"x = {a = 1\n", "x = {a = 1,\n",
		"x = @\n", "x = 1.2.3\n", "x = -05-27\n", "x = \"\\q\"\n", "x = \"\\u00\"\n", "x = \"\\u00", "x = \"\\", "x = \"\\\n",
		"x = [\"a\" \"b\"]\n", "x = {a = 1 b = 2}\n", "x = 1\ny = {,}\n", "x = {a 1}\n", "x = {@ = 1}\n", "x = {a = @}\n", "x = {a = 1, a.b = 2}\n",
		"[a]\n[a.b]\nx = 1\n[[a.b]]\ny = 2\n", "a = 1\na.b = 2\n", "[a]\n[[a]]\n",
		"a = 1\n[a]\nx = 2\n", "a = 1\n[a.b]\nx = 2\n",
	} {
		got, err := applyIR(t, bad, "  - op: replace\n    path: /x\n    value: 2\n")
		he, ok := hewerr.As(err)
		if !ok || he.Code != hewerr.CodeTargetParse {
			t.Errorf("%q: want HEW002, got %v (bytes %q)", bad, err, got)
			continue
		}
		if he.Component != hewerr.ComponentApplier {
			t.Errorf("%q: component: want applier, got %s", bad, he.Component)
		}
	}
}

// A reader complaint names the line it is about, so a malformed target is
// findable rather than merely rejected.
func TestATargetParseErrorNamesItsLine(t *testing.T) {
	_, err := applyIR(t, "x = 1\n\n[a]\ny = @\n", "  - op: replace\n    path: /x\n    value: 2\n")
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("error is not a *hewerr.Error: %v", err)
	}
	mustContain(t, he, "line 4")
}

// --- the reader --------------------------------------------------------------

func TestScalarsDecodeFormatNatively(t *testing.T) {
	for _, tc := range []struct{ src, tag, text string }{
		{"x = 30", "!!int", "30"},
		{"x = 0x1e", "!!int", "30"},
		{"x = 0o17", "!!int", "15"},
		{"x = 0b101", "!!int", "5"},
		{"x = 1_000", "!!int", "1000"},
		{"x = +7", "!!int", "7"},
		{"x = -7", "!!int", "-7"},
		{"x = 1.5", "!!float", "1.5"},
		{"x = 1e3", "!!float", "1e3"},
		{"x = inf", "!!float", "inf"},
		{"x = -nan", "!!float", "-nan"},
		{"x = 1_0.5", "!!float", "10.5"},
		{"x = true", "!!bool", "true"},
		{"x = false", "!!bool", "false"},
		{"x = 1979-05-27", "!!timestamp", "1979-05-27"},
		{"x = 07:32:00", "!!timestamp", "07:32:00"},
		{"x = \"a\\tb\"", "!!str", "a\tb"},
		{"x = \"a\\bb\"", "!!str", "a\bb"},
		{"x = \"a\\nb\"", "!!str", "a\nb"},
		{"x = \"a\\fb\"", "!!str", "a\fb"},
		{"x = \"a\\rb\"", "!!str", "a\rb"},
		{"x = \"a\\\"b\"", "!!str", `a"b`},
		{"x = \"a\\\\b\"", "!!str", `a\b`},
		{"x = \"\\u00e9\"", "!!str", "é"},
		{"x = \"\\U0001F600\"", "!!str", "\U0001F600"},
		{"x = 'a\\tb'", "!!str", `a\tb`},
		{"x = \"\"\"\nline\n\"\"\"", "!!str", "line\n"},
		{"x = \"\"\"\r\nline\r\n\"\"\"", "!!str", "line\r\n"},
		{"x = \"\"\"a\\\n   b\"\"\"", "!!str", "ab"},
		{"x = '''\nraw\n'''", "!!str", "raw\n"},
		{"x = \"\"", "!!str", ""},
	} {
		n := scalarAt(t, mustParse(t, tc.src), "x")
		if n.tag != tc.tag || n.text != tc.text {
			t.Errorf("%q: got (%s, %q), want (%s, %q)", tc.src, n.tag, n.text, tc.tag, tc.text)
		}
	}
}

func TestQuotedKeysAreDecoded(t *testing.T) {
	d := mustParse(t, "\"a.b\".'c d' = 1\n")
	n := scalarAt(t, d, "a.b", "c d")
	if n.text != "1" {
		t.Errorf("got %q", n.text)
	}
}

func TestDefinitionCountingIgnoresImplicitAncestors(t *testing.T) {
	d := mustParse(t, "a.b.c = 1\na.b.d = 2\n\n[e.f]\ng = 3\n")
	for _, tc := range []struct {
		path []string
		want int
	}{
		{[]string{"a"}, 0},
		{[]string{"a", "b"}, 0},
		{[]string{"a", "b", "c"}, 1},
		{[]string{"e"}, 0},
		{[]string{"e", "f"}, 1},
		{[]string{"e", "f", "g"}, 1},
	} {
		if got := scalarAt(t, d, tc.path...).defs; got != tc.want {
			t.Errorf("%v: defs %d, want %d", tc.path, got, tc.want)
		}
	}
}

func TestRepeatedArrayOfTablesHeadersDefineTheArrayOnce(t *testing.T) {
	d := mustParse(t, "[[p]]\nx = 1\n\n[[p]]\nx = 2\n")
	seq := scalarAt(t, d, "p")
	if seq.defs != 1 || len(seq.elems) != 2 {
		t.Errorf("defs %d, elems %d; want 1 and 2", seq.defs, len(seq.elems))
	}
}

func TestCommentChildrenSkipMemberOwnedComments(t *testing.T) {
	d := mustParse(t, "[a]\n# free\n\n# leading\nx = 1\n# tail\n")
	got := d.commentChildren(d.root.lookup("a").val)
	if len(got) != 2 || got[0].text != "free" || got[1].text != "tail" {
		t.Fatalf("got %d comments: %+v", len(got), got)
	}
	if d.commentChildren(scalarAt(t, d, "a", "x")) != nil {
		t.Error("a scalar has no comment children")
	}
	if d.commentChildren(nil) != nil {
		t.Error("no node has no comment children")
	}
}

// A table's comments stop where the next table begins: a free comment under
// [b] is not addressable as /a/#1.
func TestCommentChildrenStopAtTheNextTable(t *testing.T) {
	target := "[a]\n# free\n\nx = 1\n\n[b]\n# other\n\ny = 2\n"
	mustApply(t, target, "  - op: test\n    path: /a/#0\n    value:\n      comment: free\n"+
		"  - op: test\n    path: /b/#0\n    value:\n      comment: other\n"+
		"  - op: replace\n    path: /a/x\n    value: 2\n",
		"[a]\n# free\n\nx = 2\n\n[b]\n# other\n\ny = 2\n")
	mustFail(t, target, "  - op: remove\n    path: /a/#1\n", hewerr.CodeNoMatch, "/a/#1")
}

// The comment scan walks to the end of the file, including a last line that is
// nothing but blanks and never terminated.
func TestCommentScanSurvivesATrailingBlankLine(t *testing.T) {
	mustApply(t, "# c\n\nx = 1\n\n   ", "  - op: replace\n    path: /#0\n    value:\n      comment: d\n",
		"# d\n\nx = 1\n\n   ")
}

func TestTrailingCommentScanStopsAtEndOfFile(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1", "  - op: test\n    path: /a/x/#t\n    value:\n      comment: q\n",
		hewerr.CodeStaleTarget, "/a/x/#t")
	mustContain(t, he, "no trailing comment here")
}

func TestNodeKindAndChildCountVocabulary(t *testing.T) {
	d := mustParse(t, "[a]\nx = 1\ntags = [1, 2]\n")
	if got := nodeKind(nil); got != "" {
		t.Errorf("nil kind: %q", got)
	}
	if got := nodeKind(scalarAt(t, d, "a")); got != hew.KindMap {
		t.Errorf("table kind: %q", got)
	}
	if got := nodeKind(scalarAt(t, d, "a", "tags")); got != hew.KindSeq {
		t.Errorf("array kind: %q", got)
	}
	if _, ok := childCount(nil); ok {
		t.Error("nil is not a container")
	}
	if n, ok := childCount(scalarAt(t, d, "a", "tags")); !ok || n != 2 {
		t.Errorf("array count %d %v", n, ok)
	}
	if d.describe(nil) != "" {
		t.Error("nil describes as empty")
	}
	if got := d.describe(scalarAt(t, d, "a", "tags")); got != "array" {
		t.Errorf("describe array: %q", got)
	}
	if got := d.describe(scalarAt(t, d, "a")); got != "table" {
		t.Errorf("describe table: %q", got)
	}
	if got := nkind(9).String(); got != "scalar" {
		t.Errorf("fallback kind name: %q", got)
	}
}

func TestScalarEqualityIsTypedAndNormalized(t *testing.T) {
	mustApply(t, "x = 0x1e\n", "  - op: test\n    path: /x\n    value: 30\n"+
		"  - op: replace\n    path: /x\n    value: 31\n", "x = 31\n")
	mustApply(t, "x = 1e0\n", "  - op: test\n    path: /x\n    value: 1.0\n"+
		"  - op: replace\n    path: /x\n    value: 2.0\n", "x = 2.0\n")
	mustApply(t, "x = true\n", "  - op: test\n    path: /x\n    value: True\n"+
		"  - op: replace\n    path: /x\n    value: false\n", "x = false\n")
	// A string is never a number, however it is spelled (§6.1).
	mustFail(t, "x = \"8080\"\n", "  - op: test\n    path: /x\n    value: 8080\n",
		hewerr.CodeStaleTarget, "/x")
	mustFail(t, "x = 8080\n", "  - op: test\n    path: /x\n    value: \"8080\"\n",
		hewerr.CodeStaleTarget, "/x")
	// nan is never equal to itself, so the float path falls through honestly.
	mustFail(t, "x = 1.5\n", "  - op: test\n    path: /x\n    value: .nan\n",
		hewerr.CodeStaleTarget, "/x")
}

func TestKeyMatchValuesAreTypedToo(t *testing.T) {
	mustApply(t, "a = [1, \"1\", true]\n", "  - op: remove\n    path: /a/=1\n", "a = [\"1\", true]\n")
	mustApply(t, "a = [1, \"1\", true]\n", "  - op: remove\n    path: /a/=\"1\"\n", "a = [1, true]\n")
	mustApply(t, "a = [1, \"1\", true]\n", "  - op: remove\n    path: /a/=true\n", "a = [1, \"1\"]\n")
	mustFail(t, "a = [1]\n", "  - op: remove\n    path: /a/=null\n", hewerr.CodeNoMatch, "/a/=null")
	mustApply(t, "a = [1.5]\n", "  - op: remove\n    path: /a/=1.5\n", "a = []\n")
	mustFail(t, "a = [{k = 1}]\n", "  - op: remove\n    path: /a/=1\n", hewerr.CodeNoMatch, "/a/=1")
	mustFail(t, "a = [1]\n", "  - op: remove\n    path: /a/k=1\n", hewerr.CodeNoMatch, "/a/k=1")
}

// --- the emitter -------------------------------------------------------------

func TestValuesAreRenderedAsToml(t *testing.T) {
	mustApply(t, "[a]\nk = 0\n", "  - op: replace\n    path: /a/k\n"+
		"    value:\n      s: hi\n      n: 8080\n      f: 1.5\n      b: true\n      l: [1, two]\n"+
		"      m: {x: 1}\n",
		"[a]\nk = {s = \"hi\", n = 8080, f = 1.5, b = true, l = [1, \"two\"], m = {x = 1}}\n")
}

func TestStringsAreEscapedAndKeysQuotedOnlyWhenNeeded(t *testing.T) {
	mustApply(t, "[a]\nk = 0\n",
		"  - op: replace\n    path: /a/k\n    value: \"q\\\"b\\\\s\\ttab\\nnl\\u0001end\"\n",
		"[a]\nk = \"q\\\"b\\\\s\\ttab\\nnl\\u0001end\"\n")
	mustApply(t, "[a]\nk = 0\n",
		"  - op: replace\n    path: /a/k\n    value:\n      \"needs quotes\": 1\n      \"\": 2\n      ok-1_A: 3\n",
		"[a]\nk = {\"needs quotes\" = 1, \"\" = 2, ok-1_A = 3}\n")
}

func TestCarriageReturnAndFormFeedEscape(t *testing.T) {
	mustApply(t, "[a]\nk = 0\n", "  - op: replace\n    path: /a/k\n    value: \"\\r\\f\\b\"\n",
		"[a]\nk = \"\\r\\f\\b\"\n")
}

func TestDottedKeysAreQuotedWhereNeeded(t *testing.T) {
	mustApply(t, "x = 1\n", "  - op: add\n    path: /a/b~1c/d\n    value: 5\n",
		"x = 1\n\n[a.\"b/c\"]\nd = 5\n")
}

func TestTrailingBlanksAndBareCommentsParse(t *testing.T) {
	mustApply(t, "x = 1\n\n   ", "  - op: replace\n    path: /x\n    value: 2\n", "x = 2\n\n   ")
	// An indented free comment, and a "#" with nothing after it at end of file.
	mustApply(t, "[a]\n   # spaced\n\nx = 1\n#",
		"  - op: test\n    path: /a/#0\n    value:\n      comment: spaced\n"+
			"  - op: replace\n    path: /a/#1\n    value:\n      comment: tail\n",
		"[a]\n   # spaced\n\nx = 1\n#tail")
}

// The `equals` used by §10.6's after-image check compares CONTAINERS exactly,
// not as the subset a before-image assert tolerates.
func TestAfterImageEqualityOverContainers(t *testing.T) {
	mustApply(t, "x = {a = 1}\n", "  - op: replace\n    path: /x\n    value:\n      a: 1\n", "x = {a = 1}\n")
	mustApply(t, "tags = [1, 2]\n", "  - op: replace\n    path: /tags\n    value: [1, 2]\n", "tags = [1, 2]\n")
	// One key more, one element fewer, one value different, one key renamed,
	// one kind swapped: none of them is the after-image, so each is written.
	mustApply(t, "x = {a = 1, b = 2}\n", "  - op: replace\n    path: /x\n    value:\n      a: 1\n",
		"x = {a = 1}\n")
	mustApply(t, "tags = [1, 2]\n", "  - op: replace\n    path: /tags\n    value: [1]\n", "tags = [1]\n")
	mustApply(t, "tags = [1, 2]\n", "  - op: replace\n    path: /tags\n    value: [1, 3]\n",
		"tags = [1, 3]\n")
	mustApply(t, "x = {a = 1}\n", "  - op: replace\n    path: /x\n    value:\n      b: 1\n", "x = {b = 1}\n")
	mustApply(t, "x = {a = 1}\n", "  - op: replace\n    path: /x\n    value: [1]\n", "x = [1]\n")
	mustApply(t, "tags = [1]\n", "  - op: replace\n    path: /tags\n    value:\n      a: 1\n",
		"tags = {a = 1}\n")
	mustApply(t, "x = 1\n", "  - op: replace\n    path: /x\n    value: [1]\n", "x = [1]\n")
}

// A YAML alias in the transform's value denotes no TOML node, so it neither
// matches nor renders — it is refused rather than silently resolved.
func TestAnAliasValueMatchesNothingAndCannotBeWritten(t *testing.T) {
	mustFail(t, "x = 1\n", "  - op: test\n    path: /x\n    value: &v 1\n"+
		"  - op: test\n    path: /x\n    value: *v\n", hewerr.CodeStaleTarget, "/x")
	he := mustFail(t, "x = 1\ny = 2\n", "  - op: replace\n    path: /x\n    value: &v 1\n"+
		"  - op: replace\n    path: /y\n    value: *v\n", hewerr.CodeInexpressible, "/y")
	mustContain(t, he, "no TOML spelling")
}

func TestAnUnknownScalarTagHasNoTomlSpelling(t *testing.T) {
	he := mustFail(t, "x = 1\n", "  - op: replace\n    path: /x\n    value: !!binary aGk=\n",
		hewerr.CodeInexpressible, "/x")
	mustContain(t, he, "no TOML spelling")
}

// A null nested inside a container is refused wherever the container is
// rendered: inline, as an array item, and as a table body line.
func TestANestedNullIsRefusedEverywhereItIsRendered(t *testing.T) {
	for _, tc := range []struct{ target, records, path string }{
		{"x = 1\n", "  - op: replace\n    path: /x\n    value: [null]\n", "/x"},
		{"x = 1\n", "  - op: replace\n    path: /x\n    value:\n      k: null\n", "/x"},
		{"[a]\nx = 1\n", "  - op: add\n    path: /a/b\n    surface: table\n    value:\n      k: null\n", "/a/b"},
		{"[a]\nx = 1\n", "  - op: add\n    path: /a/b\n    surface: table\n    value:\n      k: [null]\n", "/a/b"},
		{"[[p]]\nname = \"a\"\n", "  - op: add\n    path: /p\n    value:\n      k: null\n", "/p"},
	} {
		he := mustFail(t, tc.target, tc.records, hewerr.CodeInexpressible, tc.path)
		mustContain(t, he, "no TOML spelling")
	}
}

func TestPlacementPathsAreResolvedNotAssumed(t *testing.T) {
	// A line placement, an inline-array placement, and a block placement each
	// report the placement path that did not resolve.
	mustFail(t, "[a]\nx = 1\n", "  - op: add\n    path: /a/y\n    after: /a/nope\n    value: 2\n",
		hewerr.CodeNoMatch, "/a/nope")
	mustFail(t, "tags = [1]\n", "  - op: add\n    path: /tags\n    after: /tags/9\n    value: 2\n",
		hewerr.CodeNoMatch, "/tags/9")
	mustFail(t, "[a]\nx = 1\n",
		"  - op: add\n    path: /b\n    surface: table\n    after: /nope\n    value:\n      k: 1\n",
		hewerr.CodeNoMatch, "/nope")
	// An inline-array placement naming something outside the array.
	he := mustFail(t, "[a]\ntags = [1]\nz = 2\n",
		"  - op: add\n    path: /a/tags\n    after: /a/z\n    value: 3\n", hewerr.CodeNoMatch, "/a/z")
	mustContain(t, he, "not a child of the container")
}

func TestBlockPlacementAfterANamedTable(t *testing.T) {
	mustApply(t, "[a]\nx = 1\n\n[c]\nz = 3\n",
		"  - op: add\n    path: /b\n    surface: table\n    after: /a\n    value:\n      y: 2\n",
		"[a]\nx = 1\n\n[b]\ny = 2\n\n[c]\nz = 3\n")
	// The separator is only added on the side that lacks one: placing AFTER a
	// table that butts straight up against the next leaves that join alone.
	mustApply(t, "[a]\nx = 1\n[c]\nz = 3\n",
		"  - op: add\n    path: /b\n    surface: table\n    after: /a\n    value:\n      y: 2\n",
		"[a]\nx = 1\n\n[b]\ny = 2\n[c]\nz = 3\n")
}

// A placement names a sibling anywhere in the container, not just the first
// one the search happens to look at.
func TestPlacementFindsASiblingPastTheFirst(t *testing.T) {
	mustApply(t, "[server]\nhost = \"h\"\nport = 8080\ntimeout = 30\n",
		"  - op: add\n    path: /server/tls\n    before: /server/timeout\n    value: true\n",
		"[server]\nhost = \"h\"\nport = 8080\ntls = true\ntimeout = 30\n")
}

// A root-level key added to a file whose body starts with a table header goes
// ABOVE the header, which is the only place TOML lets it live.
func TestRootKeyAddedAboveTheFirstTableHeader(t *testing.T) {
	mustApply(t, "[a]\nx = 1\n", "  - op: add\n    path: /y\n    value: 2\n", "y = 2\n[a]\nx = 1\n")
}

func TestCommentAddressUnderAMissingTable(t *testing.T) {
	he := mustFail(t, "[a]\nx = 1\n", "  - op: add\n    path: /nope/#0\n    value:\n      comment: q\n",
		hewerr.CodeNoMatch, "/nope")
	mustContain(t, he, "no key")
}

func TestACommentAddressCannotBeChainedOntoAComment(t *testing.T) {
	he := mustFail(t, "[a]\n# one\n\nx = 1\n",
		"  - op: test\n    path: /a/#0/#0\n    value:\n      comment: q\n",
		hewerr.CodeStaleTarget, "/a/#0/#0")
	mustContain(t, he, "no node to attach a comment address to")
}

func TestCopyRefusesADestinationUnderAnAmbiguousSurface(t *testing.T) {
	target := "a.b = {x = 1}\n\n[a.b]\ny = 2\n\n[dest]\nk = 1\n"
	mustFail(t, target, "  - op: copy\n    path: /a/b/z\n    from: /dest/k\n",
		hewerr.CodeSurfaceAmbiguity, "/a/b")
}
