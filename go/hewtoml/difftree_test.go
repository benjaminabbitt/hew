package hewtoml

import (
	"strings"
	"testing"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
)

// tree renders a DiffNode as a compact, total shape string, so a test can pin
// child ORDER and scalar tags in one comparison.
func tree(n *hew.DiffNode) string {
	var b strings.Builder
	write(&b, n)
	return b.String()
}

func write(b *strings.Builder, n *hew.DiffNode) {
	switch n.Kind {
	case hew.KindMap, hew.KindSeq:
		open, close := "{", "}"
		if n.Kind == hew.KindSeq {
			open, close = "[", "]"
		}
		b.WriteString(open)
		for i, c := range n.Children {
			if i > 0 {
				b.WriteString(" ")
			}
			switch {
			case c.Comment:
				b.WriteString("#" + c.Text)
			default:
				b.WriteString(c.Key + ":")
				write(b, c.Node)
			}
		}
		b.WriteString(close)
	default:
		node := n.Value.Node()
		b.WriteString(node.ShortTag() + "|" + node.Value)
	}
}

func mustTree(t *testing.T, src string) *hew.DiffNode {
	t.Helper()
	n, err := DiffTree([]byte(src))
	if err != nil {
		t.Fatalf("DiffTree: %v", err)
	}
	return n
}

// The tree is the document's own spelling: a dotted key nests, a header table
// nests, and neither is rewritten into the other (§8.4 rule 1).
func TestDiffTreeKeepsBothSurfacesAsWritten(t *testing.T) {
	got := tree(mustTree(t, "title = \"config\"\ntool.ctxloom.timeout = 30\n\n[server]\nport = 8080\n"))
	want := `{title:!!str|config tool:{ctxloom:{timeout:!!int|30}} server:{port:!!int|8080}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestDiffTreeDecodesScalarsUnderTheirTags(t *testing.T) {
	got := tree(mustTree(t, "a = 0x1e\nb = true\nc = 1.5\nd = 1979-05-27\ne = 'raw'\n"))
	want := `{a:!!int|30 b:!!bool|true c:!!float|1.5 d:!!timestamp|1979-05-27 e:!!str|raw}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestDiffTreeReadsArraysAndInlineTables(t *testing.T) {
	got := tree(mustTree(t, "args = [\"a\", \"b\"]\nmeta = { k = 1 }\n\n[[plugin]]\nname = \"beta\"\n"))
	want := `{args:[:!!str|a :!!str|b] meta:{k:!!int|1} plugin:[:{name:!!str|beta}]}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A standalone comment is a positional child, in source order — which is what
// its /table/#n address says it is (§4.5b).
func TestDiffTreeInterleavesStandaloneComments(t *testing.T) {
	got := tree(mustTree(t, "[server]\nport = 8080\n\n# a note\n\ntls = true\n"))
	want := `{server:{port:!!int|8080 #a note tls:!!bool|true}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A table opened by a header and a table that exists only as a dotted key's
// prefix both order against the first line that defines them, so a comment
// written between them lands between them.
func TestDiffTreeOrdersImplicitTablesByTheirFirstLine(t *testing.T) {
	got := tree(mustTree(t, "# lead\n\nz = 1\n\n# mid\n\ntool.a = 2\n"))
	want := `{#lead z:!!int|1 #mid tool:{a:!!int|2}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A comment written directly above a member is that member's LEADING comment
// (§8.2's anchoring rule), and this binding folds it into the member's own
// line region: commentChildren does not enumerate it, so neither does the diff
// tree. The differ shares the applier's enumeration on purpose — a comment the
// differ numbered #0 has to be the comment /table/#0 resolves to — so the two
// halves agree, at the cost of a comment-only edit to a leading comment being
// invisible to the differ. Pinned here so the limitation is a decision on
// record rather than a surprise.
func TestDiffTreeFoldsALeadingCommentIntoItsMember(t *testing.T) {
	got := tree(mustTree(t, "[hooks]\n# ctxloom-managed\non_start = \"x\"\n"))
	want := `{hooks:{on_start:!!str|x}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestDiffTreeRejectsUnparseableSource(t *testing.T) {
	_, err := DiffTree([]byte("a = \n"))
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeTargetParse || he.Component != hewerr.ComponentDiffer {
		t.Fatalf("want HEW002 from the differ, got %v", err)
	}
	if !strings.Contains(he.Error(), "does not parse as TOML") {
		t.Fatalf("diagnostic does not name the format: %v", he)
	}
}

// §9.1 step 5 chains a run of `+` lines: the second add is placed after the
// first, which is in no parse of the target.
func TestChainedAddsPlaceAgainstEachOther(t *testing.T) {
	mustApply(t, "[server]\nport = 8080\n", `
- op: add
  path: /server/tls
  after: /server/port
  value: true
- op: add
  path: /server/timeout
  after: /server/tls
  value: 30
`, "[server]\nport = 8080\ntls = true\ntimeout = 30\n")
}

func TestChainedItemAddsPlaceAgainstEachOther(t *testing.T) {
	mustApply(t, "args = [\"a\"]\n", `
- op: add
  path: /args
  after: /args/=a
  value: b
- op: add
  path: /args
  after: /args/=b
  value: c
`, "args = [\"a\", \"b\", \"c\"]\n")
}

// A key-match placement names the pending element by its identity field, the
// same §4.2 comparison the applier makes against a parsed element.
func TestChainedKeyedItemAddsPlaceAgainstEachOther(t *testing.T) {
	mustApply(t, "args = [{cmd = \"x\", name = \"a\"}]\n", `
- op: add
  path: /args
  after: /args/name=a
  value: {cmd: y, name: b}
- op: add
  path: /args
  after: /args/name=b
  value: {cmd: z, name: c}
`, "args = [{cmd = \"x\", name = \"a\"}, {cmd = \"y\", name = \"b\"}, {cmd = \"z\", name = \"c\"}]\n")
}

// An array-of-tables element is a whole block, and a chain of them places the
// same way.
func TestChainedBlockAddsPlaceAgainstEachOther(t *testing.T) {
	mustApply(t, "[[plugin]]\nname = \"beta\"\n", `
- op: add
  path: /plugin
  after: /plugin/name=beta
  value: {name: gamma}
- op: add
  path: /plugin
  after: /plugin/name=gamma
  value: {name: delta}
`, "[[plugin]]\nname = \"beta\"\n\n[[plugin]]\nname = \"gamma\"\n\n[[plugin]]\nname = \"delta\"\n")
}

// The pending-add fallback widens nothing: a placement naming a sibling that
// is in neither the target nor this run is still a no-match, even with an
// unrelated add of this run's already pending in the same table.
func TestAnUnknownPlacementSiblingIsStillANoMatch(t *testing.T) {
	mustFail(t, "[server]\nport = 8080\n", `
- op: add
  path: /server/tls
  after: /server/port
  value: true
- op: add
  path: /server/timeout
  after: /server/ghost
  value: 30
`, hewerr.CodeNoMatch, "/server/ghost")
}

// Nor does a key-match placement match a pending element that simply does not
// carry the identity field.
func TestAKeyMatchPlacementNeedsThePendingElementsField(t *testing.T) {
	mustFail(t, "args = [{name = \"a\"}]\n", `
- op: add
  path: /args
  after: /args/name=a
  value: {cmd: y, arg: z}
- op: add
  path: /args
  after: /args/name=b
  value: {name: c}
`, hewerr.CodeNoMatch, "/args/name=b")
}
