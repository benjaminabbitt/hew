package hewyaml

import (
	"strings"
	"testing"

	"github.com/hew-format/hew"
	"github.com/hew-format/hew/internal/hewerr"
)

// hewt wraps transform records in a .hewt document for the "t.yaml" target.
func hewtDoc(records string) string {
	return "hew-transforms: 1\ntarget: t.yaml\nformat: yaml\ntransforms:\n" + records
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

// --- scalars and byte preservation -----------------------------------------

func TestReplaceScalarTouchesOnlyItsOwnBytes(t *testing.T) {
	target := "name: myapp\n" +
		"server:\n" +
		"  # ports below 1024 need CAP_NET_BIND_SERVICE\n" +
		"  port:   8080\n" +
		"  timeout: 30 # seconds\n" +
		"\n" +
		"other: 'quoted value'\n"
	want := "name: myapp\n" +
		"server:\n" +
		"  # ports below 1024 need CAP_NET_BIND_SERVICE\n" +
		"  port:   8080\n" +
		"  timeout: 60 # seconds\n" +
		"\n" +
		"other: 'quoted value'\n"
	mustApply(t, target, "  - op: replace\n    path: /server/timeout\n    value: 60\n", want)
}

func TestReplaceScalarKinds(t *testing.T) {
	target := "a: 1\nb: two\nc: true\nd: 'x'\ne: \"y\"\n"
	records := "" +
		"  - op: replace\n    path: /a\n    value: 2.5\n" +
		"  - op: replace\n    path: /b\n    value: \"true\"\n" +
		"  - op: replace\n    path: /c\n    value: false\n" +
		"  - op: replace\n    path: /d\n    value: null\n" +
		"  - op: replace\n    path: /e\n    value: \"a: colon\"\n"
	mustApply(t, target, records, "a: 2.5\nb: \"true\"\nc: false\nd: null\ne: \"a: colon\"\n")
}

func TestReplaceQuotesWhenPlainWouldChangeType(t *testing.T) {
	target := "a: 1\nb: x\n"
	records := "" +
		"  - op: replace\n    path: /a\n    value: \"8080\"\n" +
		"  - op: replace\n    path: /b\n    value: \"- leading dash\"\n"
	mustApply(t, target, records, "a: \"8080\"\nb: \"- leading dash\"\n")
}

func TestReplaceNullValuedKeyWritesAfterTheColon(t *testing.T) {
	mustApply(t, "a:\nb: 1\n", "  - op: replace\n    path: /a\n    value: filled\n", "a: filled\nb: 1\n")
}

func TestReplaceWithBlockCollectionStartsOnItsOwnLine(t *testing.T) {
	target := "server:\n  tls: off\n"
	want := "server:\n  tls:\n    enabled: true\n    ca: /etc/ca.pem\n"
	records := "  - op: replace\n    path: /server/tls\n    value:\n      enabled: true\n      ca: /etc/ca.pem\n"
	mustApply(t, target, records, want)
}

func TestBlockScalarAndMultilinePlainAreSpannedWhole(t *testing.T) {
	target := "" +
		"script: |\n  line one\n  line two\n" +
		"folded: >-\n  wrapped\n  text\n" +
		"plain: some words\n  continued here\n" +
		"last: 1\n"
	want := strings.Replace(target, "last: 1", "last: 2", 1)
	mustApply(t, target, "  - op: replace\n    path: /last\n    value: 2\n", want)

	// The member's own line region ends with the block scalar, so an add
	// lands after it rather than inside it.
	want2 := "" +
		"script: |\n  line one\n  line two\n" +
		"folded: >-\n  wrapped\n  text\n" +
		"plain: some words\n  continued here\n" +
		"last: 1\nadded: yes\n"
	mustApply(t, target, "  - op: add\n    path: /added\n    value: \"yes\"\n", want2)
}

// --- adds -------------------------------------------------------------------

func TestAddMemberPlacement(t *testing.T) {
	target := "server:\n  host: localhost\n  port: 8080\n"
	t.Run("append", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/tls\n    value: true\n",
			"server:\n  host: localhost\n  port: 8080\n  tls: true\n")
	})
	t.Run("after", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/tls\n    after: /server/host\n    value: true\n",
			"server:\n  host: localhost\n  tls: true\n  port: 8080\n")
	})
	t.Run("before", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/tls\n    before: /server/host\n    value: true\n",
			"server:\n  tls: true\n  host: localhost\n  port: 8080\n")
	})
	t.Run("nested block value", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/env\n    value:\n      A: 1\n      B: 2\n",
			"server:\n  host: localhost\n  port: 8080\n  env:\n    A: 1\n    B: 2\n")
	})
	t.Run("sequence value", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/tags\n    value: [a, b]\n",
			"server:\n  host: localhost\n  port: 8080\n  tags:\n    - a\n    - b\n")
	})
	t.Run("empty collection value stays inline", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/env\n    value: {}\n",
			"server:\n  host: localhost\n  port: 8080\n  env: {}\n")
	})
}

func TestAddAdoptsTheDocumentIndentStep(t *testing.T) {
	target := "server:\n    host: localhost\n"
	mustApply(t, target, "  - op: add\n    path: /server/env\n    value:\n      A: 1\n",
		"server:\n    host: localhost\n    env:\n        A: 1\n")
}

func TestAddElementPlacement(t *testing.T) {
	target := "tags:\n  - alpha\n  - beta\n"
	t.Run("append", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /tags\n    value: gamma\n",
			"tags:\n  - alpha\n  - beta\n  - gamma\n")
	})
	t.Run("prepend", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /tags\n    before: /tags/=alpha\n    value: aa\n",
			"tags:\n  - aa\n  - alpha\n  - beta\n")
	})
	t.Run("middle", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /tags\n    after: /tags/=alpha\n    value: ab\n",
			"tags:\n  - alpha\n  - ab\n  - beta\n")
	})
	t.Run("mapping element", func(t *testing.T) {
		mustApply(t, "s:\n  - name: a\n    cmd: x\n",
			"  - op: add\n    path: /s\n    value:\n      name: b\n      cmd: y\n",
			"s:\n  - name: a\n    cmd: x\n  - name: b\n    cmd: y\n")
	})
	t.Run("index address", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /tags\n    after: /tags/0\n    value: ab\n",
			"tags:\n  - alpha\n  - ab\n  - beta\n")
	})
}

func TestAddIntoEmptyFlowCollectionAdoptsBlockStyle(t *testing.T) {
	t.Run("mapping", func(t *testing.T) {
		mustApply(t, "network: {}\nafter: 1\n", "  - op: add\n    path: /network/host\n    value: localhost\n",
			"network:\n  host: localhost\nafter: 1\n")
	})
	t.Run("sequence", func(t *testing.T) {
		mustApply(t, "tags: []\n", "  - op: add\n    path: /tags\n    value: first\n",
			"tags:\n  - first\n")
	})
}

func TestAddIntoPopulatedFlowCollectionStaysFlow(t *testing.T) {
	mustApply(t, "server: {host: localhost}\n", "  - op: add\n    path: /server/port\n    value: 8080\n",
		"server: {host: localhost, port: 8080}\n")
	mustApply(t, "tags: [a]\n", "  - op: add\n    path: /tags\n    value: b\n", "tags: [a, b]\n")
}

func TestAddOnConflict(t *testing.T) {
	target := "server:\n  timeout: 90\n"
	t.Run("default keeps the existing value", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/timeout\n    on_conflict: keep\n    value: 30\n", target)
	})
	t.Run("upsert overwrites", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/timeout\n    on_conflict: replace\n    value: 30\n",
			"server:\n  timeout: 30\n")
	})
	t.Run("strict refuses", func(t *testing.T) {
		he := mustFail(t, target, "  - op: add\n    path: /server/timeout\n    value: 30\n",
			hewerr.CodeAlreadyExists, "/server/timeout")
		mustContain(t, he, "already exists", "! idempotent")
	})
	t.Run("idempotent tolerates an equal value", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/timeout\n    idempotent: true\n    value: 90\n", target)
	})
	t.Run("idempotent still refuses a different value", func(t *testing.T) {
		mustFail(t, target, "  - op: add\n    path: /server/timeout\n    idempotent: true\n    value: 30\n",
			hewerr.CodeAlreadyExists, "/server/timeout")
	})
}

func TestAddUnsupportedShapes(t *testing.T) {
	mustFail(t, "a: 1\n", "  - op: add\n    path: /\n    value: 1\n", hewerr.CodeAlreadyExists, "/")
	mustFail(t, "a: 1\n", "  - op: add\n    path: /a/b/c\n    value: 1\n", hewerr.CodeNoMatch, "/a/b")
	mustFail(t, "a: hello\n", "  - op: add\n    path: /a/0\n    value: 1\n", hewerr.CodeInexpressible, "/a/0")
}

// --- removes ----------------------------------------------------------------

func TestRemoveMemberTakesItsLeadingComment(t *testing.T) {
	target := "" +
		"server:\n" +
		"  # free comment\n" +
		"\n" +
		"  # the old flag\n" +
		"  deprecated: true\n" +
		"  port: 8080\n"
	want := "" +
		"server:\n" +
		"  # free comment\n" +
		"\n" +
		"  port: 8080\n"
	mustApply(t, target, "  - op: remove\n    path: /server/deprecated\n", want)
}

func TestRemoveElement(t *testing.T) {
	target := "tags:\n  - alpha\n  - beta\n  - gamma\n"
	mustApply(t, target, "  - op: remove\n    path: /tags/=beta\n", "tags:\n  - alpha\n  - gamma\n")
	mustApply(t, target, "  - op: remove\n    path: /tags/2\n", "tags:\n  - alpha\n  - beta\n")
}

func TestRemoveMissingNode(t *testing.T) {
	target := "server:\n  port: 8080\n"
	he := mustFail(t, target, "  - op: remove\n    path: /server/gone\n", hewerr.CodeNoMatch, "/server/gone")
	mustContain(t, he, "does not exist")
	t.Run("optional", func(t *testing.T) {
		mustApply(t, target, "  - op: remove\n    path: /server/gone\n    optional: true\n", target)
	})
	t.Run("idempotent", func(t *testing.T) {
		mustApply(t, target, "  - op: remove\n    path: /server/gone\n    idempotent: true\n", target)
	})
}

func TestRemoveRootIsRefused(t *testing.T) {
	mustFail(t, "a: 1\n", "  - op: remove\n    path: /\n", hewerr.CodeInexpressible, "/")
}

// --- comments ---------------------------------------------------------------

func TestCommentAddressing(t *testing.T) {
	target := "" +
		"server:\n" +
		"  # first\n" +
		"  port: 8080 # trailing\n" +
		"  # second\n" +
		"  timeout: 30\n"
	t.Run("test by ordinal", func(t *testing.T) {
		records := "" +
			"  - op: test\n    path: /server/#0\n    value:\n      comment: first\n" +
			"  - op: test\n    path: /server/#1\n    value:\n      comment: second\n" +
			"  - op: test\n    path: /server/port/#t\n    value:\n      comment: trailing\n"
		mustApply(t, target, records, target)
	})
	t.Run("stale comment text", func(t *testing.T) {
		he := mustFail(t, target, "  - op: test\n    path: /server/#0\n    value:\n      comment: nope\n",
			hewerr.CodeStaleTarget, "/server/#0")
		mustContain(t, he, "first")
	})
	t.Run("replace", func(t *testing.T) {
		mustApply(t, target, "  - op: replace\n    path: /server/#1\n    value:\n      comment: rewritten\n",
			strings.Replace(target, "# second", "# rewritten", 1))
	})
	t.Run("replace trailing", func(t *testing.T) {
		mustApply(t, target, "  - op: replace\n    path: /server/port/#t\n    value:\n      comment: tail\n",
			strings.Replace(target, "# trailing", "# tail", 1))
	})
	t.Run("remove", func(t *testing.T) {
		mustApply(t, target, "  - op: remove\n    path: /server/#0\n",
			strings.Replace(target, "  # first\n", "", 1))
	})
	t.Run("add", func(t *testing.T) {
		mustApply(t, target, "  - op: add\n    path: /server/#2\n    after: /server/port\n    value:\n      comment: added\n",
			strings.Replace(target, "  # second", "  # added\n  # second", 1))
	})
	t.Run("out of range", func(t *testing.T) {
		// #2 is one past the last comment: the boundary, not merely far away.
		mustFail(t, target, "  - op: replace\n    path: /server/#2\n    value:\n      comment: x\n",
			hewerr.CodeNoMatch, "/server/#2")
		mustFail(t, target, "  - op: replace\n    path: /server/#9\n    value:\n      comment: x\n",
			hewerr.CodeNoMatch, "/server/#9")
	})
	t.Run("no trailing comment", func(t *testing.T) {
		mustFail(t, target, "  - op: replace\n    path: /server/timeout/#t\n    value:\n      comment: x\n",
			hewerr.CodeNoMatch, "/server/timeout/#t")
	})
	t.Run("a comment address needs a comment value", func(t *testing.T) {
		mustFail(t, target, "  - op: replace\n    path: /server/#0\n    value: plain\n",
			hewerr.CodeInexpressible, "/server/#0")
		mustFail(t, target, "  - op: test\n    path: /server/#0\n    value: plain\n",
			hewerr.CodeInexpressible, "/server/#0")
		mustFail(t, target, "  - op: add\n    path: /server/#3\n    value: plain\n",
			hewerr.CodeInexpressible, "/server/#3")
	})
	t.Run("a comment is neither a container nor a kind", func(t *testing.T) {
		mustFail(t, target, "  - op: test\n    path: /server/#0\n    count: 0\n",
			hewerr.CodeAssertionFailed, "/server/#0")
		mustFail(t, target, "  - op: test\n    path: /server/#0\n    kind: scalar\n",
			hewerr.CodeAssertionFailed, "/server/#0")
		mustApply(t, target, "  - op: test\n    path: /server/#9\n    absent: true\n", target)
	})
	t.Run("cannot descend through a comment", func(t *testing.T) {
		mustFail(t, target, "  - op: test\n    path: /server/#0/x\n    value: 1\n",
			hewerr.CodeStaleTarget, "/server/#0/x")
		mustFail(t, target, "  - op: remove\n    path: /server/#0/x\n",
			hewerr.CodeNoMatch, "/server/#0/x")
	})
}

// --- anchors, aliases, merge keys (§8.3) ------------------------------------

const aliasTarget = "" +
	"defaults: &defaults\n" +
	"  timeout: 30\n" +
	"  retries: 3\n" +
	"service_a:\n" +
	"  <<: *defaults\n" +
	"  port: 8080\n" +
	"service_b:\n" +
	"  <<: *defaults\n" +
	"  port: 8081\n"

func TestAliasAmbiguityWithoutADirective(t *testing.T) {
	he := mustFail(t, aliasTarget, "  - op: test\n    path: /service_a/timeout\n    value: 30\n",
		hewerr.CodeAnchorAmbiguity, "/service_a/timeout")
	mustContain(t, he, "anchor-ambiguity", "defaults", "! anchor rewrite", "! anchor fork")
}

func TestAnchorRewriteEditsTheDefinition(t *testing.T) {
	records := "" +
		"  - op: test\n    path: /service_a/timeout\n    anchor: rewrite\n    value: 30\n" +
		"  - op: replace\n    path: /service_a/timeout\n    anchor: rewrite\n    value: 60\n"
	mustApply(t, aliasTarget, records, strings.Replace(aliasTarget, "timeout: 30", "timeout: 60", 1))
}

func TestAnchorForkShadowsAtThisSite(t *testing.T) {
	records := "" +
		"  - op: test\n    path: /service_a/timeout\n    anchor: fork\n    value: 30\n" +
		"  - op: replace\n    path: /service_a/timeout\n    anchor: fork\n    value: 60\n"
	want := strings.Replace(aliasTarget, "  <<: *defaults\n  port: 8080\n", "  <<: *defaults\n  port: 8080\n  timeout: 60\n", 1)
	mustApply(t, aliasTarget, records, want)
}

func TestForkCannotRemoveAnInheritedKey(t *testing.T) {
	mustFail(t, aliasTarget, "  - op: remove\n    path: /service_a/timeout\n    anchor: fork\n",
		hewerr.CodeInexpressible, "/service_a/timeout")
}

func TestMergeInheritedKeyWithASingleSiteIsNoMatch(t *testing.T) {
	target := "defaults: &defaults\n  retries: 3\nservice_a:\n  <<: *defaults\n  port: 8080\n"
	he := mustFail(t, target, "  - op: test\n    path: /service_a/retries\n    value: 3\n",
		hewerr.CodeNoMatch, "/service_a/retries")
	mustContain(t, he, "no-match", "defaults", "merge key")
	// The merge rule's own diagnostic survives ops that have a message of
	// their own for a missing node.
	he = mustFail(t, target, "  - op: replace\n    path: /service_a/retries\n    value: 4\n",
		hewerr.CodeNoMatch, "/service_a/retries")
	mustContain(t, he, "merge key")
	if strings.Contains(he.Error(), "replace requires the node to exist") {
		t.Errorf("an inherited key is not simply missing: %q", he.Error())
	}
}

func TestNestedMergeChain(t *testing.T) {
	target := "" +
		"base: &base\n  a: 1\n" +
		"mid: &mid\n  <<: *base\n  b: 2\n" +
		"leaf:\n  <<: *mid\n  c: 3\n"
	mustApply(t, target, "  - op: replace\n    path: /leaf/a\n    anchor: rewrite\n    value: 9\n",
		strings.Replace(target, "a: 1", "a: 9", 1))
}

func TestMergeFromASequenceOfAliases(t *testing.T) {
	target := "" +
		"one: &one\n  a: 1\n" +
		"two: &two\n  b: 2\n" +
		"m:\n  <<: [*one, *two]\n  c: 3\n"
	mustApply(t, target, "  - op: replace\n    path: /m/b\n    anchor: rewrite\n    value: 22\n",
		strings.Replace(target, "b: 2", "b: 22", 1))
}

func TestPathThroughAWholeAlias(t *testing.T) {
	target := "defaults: &defaults\n  timeout: 30\nservice: *defaults\nother: *defaults\n"
	t.Run("no directive", func(t *testing.T) {
		he := mustFail(t, target, "  - op: test\n    path: /service/timeout\n    value: 30\n",
			hewerr.CodeAnchorAmbiguity, "/service/timeout")
		mustContain(t, he, "defaults")
	})
	t.Run("rewrite follows the alias", func(t *testing.T) {
		mustApply(t, target, "  - op: replace\n    path: /service/timeout\n    anchor: rewrite\n    value: 60\n",
			strings.Replace(target, "timeout: 30", "timeout: 60", 1))
	})
	t.Run("fork is not expressible", func(t *testing.T) {
		he := mustFail(t, target, "  - op: replace\n    path: /service/timeout\n    anchor: fork\n    value: 60\n",
			hewerr.CodeInexpressible, "/service/timeout")
		mustContain(t, he, "fork")
	})
	t.Run("an alias with no anchor is a target-parse error", func(t *testing.T) {
		if _, err := applyIR(t, "service: *nope\n", "  - op: test\n    path: /service/x\n    value: 1\n"); err == nil {
			t.Fatal("expected yaml.v3 to reject the dangling alias")
		}
	})
}

func TestKindOfAnAliasFollowsIt(t *testing.T) {
	target := "defaults: &defaults\n  timeout: 30\nservice: *defaults\n"
	mustApply(t, target, "  - op: test\n    path: /service\n    kind: map\n", target)
}

// --- assertions (§7.1, §6.1) -------------------------------------------------

func TestAssertAbsent(t *testing.T) {
	target := "server:\n  port: 8080\n"
	mustApply(t, target, "  - op: test\n    path: /env/KEY\n    absent: true\n", target)
	he := mustFail(t, target, "  - op: test\n    path: /server/port\n    absent: true\n",
		hewerr.CodeAssertionFailed, "/server/port")
	mustContain(t, he, "absent")
}

func TestAssertAbsentPropagatesAFinalError(t *testing.T) {
	target := "tags:\n  - beta\n  - beta\n"
	mustFail(t, target, "  - op: test\n    path: /tags/=beta\n    absent: true\n",
		hewerr.CodeAmbiguousMatch, "/tags/=beta")
}

func TestAssertCountKindExhaustive(t *testing.T) {
	target := "s:\n  - name: a\n  - name: b\nm:\n  x: 1\n"
	mustApply(t, target, "  - op: test\n    path: /s\n    count: 2\n", target)
	mustApply(t, target, "  - op: test\n    path: /m\n    kind: map\n", target)
	mustApply(t, target, "  - op: test\n    path: /m/x\n    kind: scalar\n", target)
	mustApply(t, target, "  - op: test\n    path: /s\n    kind: seq\n", target)
	mustApply(t, target, "  - op: test\n    path: /m\n    exhaustive: true\n    count: 1\n", target)

	he := mustFail(t, target, "  - op: test\n    path: /s\n    count: 3\n", hewerr.CodeAssertionFailed, "/s")
	mustContain(t, he, "count", "expected 3", "found 2")
	mustFail(t, target, "  - op: test\n    path: /m\n    exhaustive: true\n    count: 2\n",
		hewerr.CodeAssertionFailed, "/m")
	mustFail(t, target, "  - op: test\n    path: /m/x\n    count: 1\n", hewerr.CodeAssertionFailed, "/m/x")
	he = mustFail(t, target, "  - op: test\n    path: /m\n    kind: seq\n", hewerr.CodeAssertionFailed, "/m")
	mustContain(t, he, "kind")
	mustFail(t, target, "  - op: test\n    path: /nope\n    count: 1\n", hewerr.CodeNoMatch, "/nope")
	mustFail(t, target, "  - op: test\n    path: /nope\n    kind: map\n", hewerr.CodeNoMatch, "/nope")
}

func TestValueTestsAreSubsetAndSubsequence(t *testing.T) {
	target := "" +
		"m:\n  a: 1\n  b: 2\n  c: 3\n" +
		"s:\n  - name: x\n    cmd: p\n  - name: y\n  - name: z\n"
	mustApply(t, target, "  - op: test\n    path: /m\n    value:\n      b: 2\n", target)
	mustApply(t, target, "  - op: test\n    path: /s\n    value:\n      - name: x\n      - name: z\n", target)
	// Order within the subsequence is part of the assertion.
	mustFail(t, target, "  - op: test\n    path: /s\n    value:\n      - name: z\n      - name: x\n",
		hewerr.CodeStaleTarget, "/s")
	mustFail(t, target, "  - op: test\n    path: /m\n    value:\n      b: 9\n", hewerr.CodeStaleTarget, "/m")
	mustFail(t, target, "  - op: test\n    path: /m\n    value:\n      zz: 1\n", hewerr.CodeStaleTarget, "/m")
	mustFail(t, target, "  - op: test\n    path: /m\n    value: 1\n", hewerr.CodeStaleTarget, "/m")
	mustFail(t, target, "  - op: test\n    path: /s\n    value:\n      a: 1\n", hewerr.CodeStaleTarget, "/s")
}

func TestScalarEqualityIsFormatNative(t *testing.T) {
	target := "port: 8080\nname: \"8080\"\nflag: true\n"
	mustApply(t, target, "  - op: test\n    path: /port\n    value: 8080\n", target)
	mustApply(t, target, "  - op: test\n    path: /name\n    value: \"8080\"\n", target)
	he := mustFail(t, target, "  - op: test\n    path: /port\n    value: \"8080\"\n", hewerr.CodeStaleTarget, "/port")
	mustContain(t, he, "found 8080")
	mustFail(t, target, "  - op: test\n    path: /name\n    value: 8080\n", hewerr.CodeStaleTarget, "/name")
	mustFail(t, target, "  - op: test\n    path: /flag\n    value: \"true\"\n", hewerr.CodeStaleTarget, "/flag")
}

func TestKeyMatchAddressing(t *testing.T) {
	target := "s:\n  - name: a\n    port: 1\n  - name: b\n    port: 2\n"
	mustApply(t, target, "  - op: replace\n    path: /s/name=b/port\n    value: 22\n",
		strings.Replace(target, "port: 2", "port: 22", 1))
	mustFail(t, target, "  - op: test\n    path: /s/name=zz\n    value: 1\n", hewerr.CodeStaleTarget, "/s/name=zz")
	mustFail(t, target, "  - op: test\n    path: /s/9\n    value: 1\n", hewerr.CodeStaleTarget, "/s/9")
	mustFail(t, target, "  - op: test\n    path: /s/name=a/nope/deep\n    value: 1\n",
		hewerr.CodeStaleTarget, "/s/name=a/nope")
}

func TestSequenceIndexOutOfRange(t *testing.T) {
	target := "tags:\n  - alpha\n  - beta\n"
	// Index 2 is one past the end: the boundary the resolver must refuse.
	mustFail(t, target, "  - op: remove\n    path: /tags/2\n", hewerr.CodeNoMatch, "/tags/2")
	mustApply(t, target, "  - op: test\n    path: /tags/1\n    value: beta\n", target)
}

func TestEveryTransformIsExecutedInOrder(t *testing.T) {
	target := "a: 1\nb: 2\n"
	// A test AFTER a write must still be evaluated (pass 1 covers the whole
	// list, not a prefix of it).
	records := "" +
		"  - op: replace\n    path: /a\n    value: 9\n" +
		"  - op: test\n    path: /b\n    value: 99\n"
	mustFail(t, target, records, hewerr.CodeStaleTarget, "/b")
	// ...and a write AFTER a test must still be planned.
	records = "" +
		"  - op: add\n    path: /c\n    value: 3\n" +
		"  - op: test\n    path: /b\n    value: 2\n" +
		"  - op: add\n    path: /d\n    value: 4\n"
	mustApply(t, target, records, "a: 1\nb: 2\nc: 3\nd: 4\n")
}

func TestTwoAddsAtTheSamePositionKeepListOrder(t *testing.T) {
	mustApply(t, "m:\n  k: 1\n  z: 9\n",
		"  - op: add\n    path: /m/x\n    after: /m/k\n    value: 1\n"+
			"  - op: add\n    path: /m/y\n    after: /m/k\n    value: 2\n",
		"m:\n  k: 1\n  x: 1\n  y: 2\n  z: 9\n")
}

func TestAmbiguousMatchIsRefused(t *testing.T) {
	target := "tags:\n  - beta\n  - beta\n"
	he := mustFail(t, target, "  - op: remove\n    path: /tags/=beta\n", hewerr.CodeAmbiguousMatch, "/tags/=beta")
	mustContain(t, he, "ambiguous-match", "beta")
	// Ambiguity is final: `! optional` does not silence it.
	mustFail(t, target, "  - op: remove\n    path: /tags/=beta\n    optional: true\n",
		hewerr.CodeAmbiguousMatch, "/tags/=beta")
}

func TestKeyMatchAgainstNonMappingElements(t *testing.T) {
	target := "s:\n  - alpha\n  - name: b\n"
	mustApply(t, target, "  - op: test\n    path: /s/name=b\n    value:\n      name: b\n", target)
	mustApply(t, target, "  - op: test\n    path: /s/=alpha\n    value: alpha\n", target)
	mustFail(t, target, "  - op: test\n    path: /s/=missing\n    value: x\n", hewerr.CodeStaleTarget, "/s/=missing")
}

// --- convergence (§7.5, §10.6) ----------------------------------------------

func TestAlreadyAppliedIsAssertionFailedNotStale(t *testing.T) {
	target := "server:\n  timeout: 60\n"
	records := "" +
		"  - op: test\n    path: /server/timeout\n    value: 30\n" +
		"  - op: replace\n    path: /server/timeout\n    value: 60\n"
	he := mustFail(t, target, records, hewerr.CodeAssertionFailed, "/server/timeout")
	mustContain(t, he, "already applied", "! idempotent", "strict")
}

func TestIdempotentToleratesAnAppliedPatch(t *testing.T) {
	target := "server:\n  timeout: 60\n"
	records := "" +
		"  - op: test\n    path: /server/timeout\n    idempotent: true\n    value: 30\n" +
		"  - op: replace\n    path: /server/timeout\n    idempotent: true\n    value: 60\n"
	mustApply(t, target, records, target)
}

func TestToleratedAssertWithAStrictWriteFailsAtTheWrite(t *testing.T) {
	target := "server:\n  timeout: 60\n"
	records := "" +
		"  - op: test\n    path: /server/timeout\n    idempotent: true\n    value: 30\n" +
		"  - op: replace\n    path: /server/timeout\n    value: 60\n"
	he := mustFail(t, target, records, hewerr.CodeAssertionFailed, "/server/timeout")
	if he.PatchLine != 0 {
		t.Errorf("patch line should come from the write's own record, got %d", he.PatchLine)
	}
	mustContain(t, he, "already applied")
}

func TestToleratedAssertWithAStrictAddFailsAtTheAdd(t *testing.T) {
	target := "server:\n  tls: true\n"
	records := "" +
		"  - op: test\n    path: /server/tls\n    idempotent: true\n    absent: true\n" +
		"  - op: test\n    path: /server/tls\n    idempotent: true\n    value: false\n" +
		"  - op: add\n    path: /server/tls\n    value: true\n"
	mustFail(t, target, records, hewerr.CodeAssertionFailed, "/server/tls")
}

func TestRemovedNodeCountsAsTheAfterImage(t *testing.T) {
	target := "server:\n  port: 8080\n"
	records := "" +
		"  - op: test\n    path: /server/gone\n    value: 1\n" +
		"  - op: remove\n    path: /server/gone\n"
	he := mustFail(t, target, records, hewerr.CodeAssertionFailed, "/server/gone")
	mustContain(t, he, "already applied")
	t.Run("idempotent", func(t *testing.T) {
		records := "" +
			"  - op: test\n    path: /server/gone\n    idempotent: true\n    value: 1\n" +
			"  - op: remove\n    path: /server/gone\n    idempotent: true\n"
		mustApply(t, target, records, target)
	})
}

func TestStaleTargetWhenNeitherImageHolds(t *testing.T) {
	target := "server:\n  timeout: 45\n"
	records := "" +
		"  - op: test\n    path: /server/timeout\n    value: 30\n" +
		"  - op: replace\n    path: /server/timeout\n    value: 60\n"
	he := mustFail(t, target, records, hewerr.CodeStaleTarget, "/server/timeout")
	mustContain(t, he, "stale-target", "expected 30", "found 45")
}

func TestOptionalTestOnAMissingNode(t *testing.T) {
	target := "server:\n  port: 8080\n"
	mustApply(t, target, "  - op: test\n    path: /server/gone\n    optional: true\n    value: 1\n", target)
}

func TestReplaceRequiresTheNodeToExist(t *testing.T) {
	he := mustFail(t, "a: 1\n", "  - op: replace\n    path: /b\n    value: 2\n", hewerr.CodeNoMatch, "/b")
	mustContain(t, he, "replace requires the node to exist")
}

func TestReplaceWithTheSameValueIsANoOp(t *testing.T) {
	target := "a: 1\n"
	mustApply(t, target, "  - op: replace\n    path: /a\n    value: 1\n", target)
}

// --- copy (§9.6, Appendix C.1) ----------------------------------------------

func TestCopyCarriesTheAttachedComment(t *testing.T) {
	target := "server:\n  # how long to wait upstream\n  timeout: 30\n  port: 8080\n"
	records := "" +
		"  - op: copy\n    from: /server/timeout\n    path: /server/timeout_seconds\n    before: /server/port\n" +
		"  - op: remove\n    path: /server/timeout\n"
	mustApply(t, target, records, "server:\n  # how long to wait upstream\n  timeout_seconds: 30\n  port: 8080\n")
}

func TestCopyPreservesTheSourceBytes(t *testing.T) {
	target := "a:\n  x:   '  spaced  '\nb:\n  keep: 1\n"
	records := "  - op: copy\n    from: /a/x\n    path: /b/y\n"
	mustApply(t, target, records, "a:\n  x:   '  spaced  '\nb:\n  keep: 1\n  y:   '  spaced  '\n")
}

func TestCopyErrors(t *testing.T) {
	target := "a:\n  x: 1\ns:\n  - one\n"
	mustFail(t, target, "  - op: copy\n    from: /a/nope\n    path: /a/y\n", hewerr.CodeNoMatch, "/a/nope")
	mustFail(t, target, "  - op: copy\n    from: /a/x\n    path: /nope/y\n", hewerr.CodeNoMatch, "/nope")
	mustFail(t, target, "  - op: copy\n    from: /a/x\n    path: /s/y\n", hewerr.CodeInexpressible, "/s/y")
	mustFail(t, target, "  - op: copy\n    from: /s/=one\n    path: /a/y\n", hewerr.CodeInexpressible, "/s/=one")
	mustFail(t, target, "  - op: copy\n    from: /a/x\n    path: /\n", hewerr.CodeNoMatch, "/")
}

// --- whole-document behaviour ------------------------------------------------

func TestTargetParseErrors(t *testing.T) {
	for name, src := range map[string]string{
		"malformed":      "a: [1,\n",
		"empty":          "",
		"multi-document": "a: 1\n---\nb: 2\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := applyIR(t, src, "  - op: test\n    path: /a\n    value: 1\n")
			if err == nil {
				t.Fatalf("expected HEW002, got %q", got)
			}
			he, _ := hewerr.As(err)
			if he == nil || he.Code != hewerr.CodeTargetParse {
				t.Fatalf("want HEW002, got %v", err)
			}
		})
	}
}

func TestScalarDocumentHasNoMembers(t *testing.T) {
	mustFail(t, "just a scalar\n", "  - op: test\n    path: /a\n    value: 1\n", hewerr.CodeStaleTarget, "/a")
	mustFail(t, "just a scalar\n", "  - op: test\n    path: /0\n    value: 1\n", hewerr.CodeStaleTarget, "/0")
}

func TestOverlappingEditsAreAConflict(t *testing.T) {
	target := "a:\n  b: 1\n  c: 2\n"
	records := "" +
		"  - op: replace\n    path: /a/b\n    value: 9\n" +
		"  - op: remove\n    path: /a\n"
	got, err := applyIR(t, target, records)
	if err == nil {
		t.Fatalf("expected HEW030, got %q", got)
	}
	he, _ := hewerr.As(err)
	if he == nil || he.Code != hewerr.CodeConflict {
		t.Fatalf("want HEW030, got %v", err)
	}
}

func TestFileWithoutATrailingNewline(t *testing.T) {
	mustApply(t, "a: 1\nb: 2", "  - op: add\n    path: /c\n    value: 3\n", "a: 1\nb: 2\nc: 3")
	mustApply(t, "a: 1\nb: 2", "  - op: replace\n    path: /b\n    value: 9\n", "a: 1\nb: 9")
	mustApply(t, "a: 1\nb: 2", "  - op: remove\n    path: /b\n", "a: 1\n")
}

func TestUnimplementedQualifiersAreRefused(t *testing.T) {
	target := "s:\n  - name: a\n  - name: a\n"
	mustFail(t, target, "  - op: add\n    path: /s/x\n    surface: table\n    value: 1\n",
		hewerr.CodeInexpressible, "/s/x")
	mustFail(t, target, "  - op: replace\n    path: /s/name=a[1]/name\n    value: b\n",
		hewerr.CodeInexpressible, "/s/name=a[1]/name")
	mustFail(t, target, "  - op: copy\n    from: /s/name=a[0]\n    path: /other\n",
		hewerr.CodeInexpressible, "/other")
}

func TestUnsupportedOpIsInexpressible(t *testing.T) {
	tl := hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: []hew.Transform{
		{Op: hew.OpKind("frobnicate"), Path: hew.MustParsePath("/a")},
	}}
	if _, err := Apply([]byte("a: 1\n"), tl); err == nil {
		t.Fatal("expected an error for an unknown op")
	}
}

// --- transform lists built in Go --------------------------------------------
//
// The .hewt codec drops `line` (§9.6) and refuses a valueless write, so the
// cases that turn on a diagnostic's patch line, or on a value the notation
// cannot produce, build their transform list directly.

func p(s string) hew.Path { return hew.MustParsePath(s) }

func val(t *testing.T, x any) hew.Value {
	t.Helper()
	v, err := hew.ValueOf(x)
	if err != nil {
		t.Fatalf("ValueOf(%v): %v", x, err)
	}
	return v
}

func applyTL(target string, ts ...hew.Transform) ([]byte, error) {
	return Apply([]byte(target), hew.TransformList{Target: "t.yaml", Format: hew.FormatYAML, Transform: ts})
}

func TestAlreadyAppliedIsReportedAgainstTheStrictRecord(t *testing.T) {
	target := "server:\n  timeout: 60\n"
	// A file pragma tolerated the assert; the hunk's "! strict" did not
	// tolerate the write, so the diagnostic belongs to the write's line.
	_, err := applyTL(target,
		hew.Transform{Op: hew.OpTest, Path: p("/server/timeout"), Value: val(t, 30), PatchLine: 6, Idempotent: true},
		hew.Transform{Op: hew.OpReplace, Path: p("/server/timeout"), Value: val(t, 60), PatchLine: 9},
	)
	he, ok := hewerr.As(err)
	if !ok {
		t.Fatalf("want HEW011, got %v", err)
	}
	if he.Code != hewerr.CodeAssertionFailed || he.PatchLine != 9 {
		t.Fatalf("want HEW011 at patch line 9, got %s at %d", he.Code, he.PatchLine)
	}
}

func TestAConvergentWriteToleratesAStrictAssert(t *testing.T) {
	target := "server:\n  timeout: 60\n"
	got, err := applyTL(target,
		hew.Transform{Op: hew.OpTest, Path: p("/server/timeout"), Value: val(t, 30), PatchLine: 6},
		hew.Transform{Op: hew.OpReplace, Path: p("/server/timeout"), Value: val(t, 60), PatchLine: 7, Idempotent: true},
	)
	if err != nil {
		t.Fatalf("an idempotent write tolerates its own assert: %v", err)
	}
	if string(got) != target {
		t.Errorf("output: %q", got)
	}
}

func TestValuelessTransforms(t *testing.T) {
	target := "a: 1\n"
	// A test with no value at all asserts nothing that can hold.
	if _, err := applyTL(target, hew.Transform{Op: hew.OpTest, Path: p("/a")}); err == nil {
		t.Error("a test with no value cannot pass")
	}
	// A valueless add writes a null, rather than panicking on the nil node.
	got, err := applyTL(target, hew.Transform{Op: hew.OpAdd, Path: p("/b")})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != "a: 1\nb: null\n" {
		t.Errorf("output: %q", got)
	}
	got, err = applyTL("s:\n  - one\n", hew.Transform{Op: hew.OpAdd, Path: p("/s")})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != "s:\n  - one\n  - null\n" {
		t.Errorf("output: %q", got)
	}
}

// --- the after-image machinery ----------------------------------------------

func TestAfterImageOnlyLooksAtTheWriteForThisPath(t *testing.T) {
	target := "a: 1\nb: 2\n"
	// The write at /b sits between the failing assert and its own write; the
	// after-image check must skip it and find the one at /a.
	records := "" +
		"  - op: test\n    path: /a\n    value: 0\n" +
		"  - op: replace\n    path: /b\n    value: 22\n" +
		"  - op: replace\n    path: /a\n    value: 1\n"
	he := mustFail(t, target, records, hewerr.CodeAssertionFailed, "/a")
	mustContain(t, he, "already applied")
}

func TestAfterImageDistinguishesRemovalFromWriting(t *testing.T) {
	// A missing node whose paired write is an ADD has not been applied: the
	// add would have created it, so this is drift.
	records := "" +
		"  - op: test\n    path: /gone\n    value: 1\n" +
		"  - op: add\n    path: /gone\n    value: 1\n"
	mustFail(t, "a: 1\n", records, hewerr.CodeStaleTarget, "/gone")

	// A present-but-different node whose paired write is a REMOVE has not been
	// applied either.
	records = "" +
		"  - op: test\n    path: /a\n    value: 1\n" +
		"  - op: remove\n    path: /a\n"
	mustFail(t, "a: 2\n", records, hewerr.CodeStaleTarget, "/a")
}

func TestAfterImageOfAWholeMapping(t *testing.T) {
	// The map is exactly what the replace would write, so the patch has
	// already been applied — the container case of §10.6, where the
	// after-image is a whole mapping rather than a scalar.
	target := "m:\n  a: 1\n  b: 9\n"
	records := "" +
		"  - op: test\n    path: /m\n    value:\n      b: 2\n" +
		"  - op: replace\n    path: /m\n    value:\n      a: 1\n      b: 9\n"
	he := mustFail(t, target, records, hewerr.CodeAssertionFailed, "/m")
	mustContain(t, he, "already applied")
}

func TestAfterImageOfAValuelessWrite(t *testing.T) {
	// A write with no value at all has no after-image to hold, so the failing
	// assert stays drift.
	_, err := applyTL("a: 1\n",
		hew.Transform{Op: hew.OpTest, Path: p("/a"), Value: val(t, 2)},
		hew.Transform{Op: hew.OpAdd, Path: p("/a")},
	)
	he, ok := hewerr.As(err)
	if !ok || he.Code != hewerr.CodeStaleTarget {
		t.Fatalf("want HEW010, got %v", err)
	}
}

func TestAfterImageNeedsTheWholeValue(t *testing.T) {
	// The map has the written key AND another one, so the after-image does not
	// hold: subset matching is for asserts, not for "already applied".
	target := "m:\n  a: 1\n  b: 9\n"
	records := "" +
		"  - op: test\n    path: /m\n    value:\n      b: 2\n" +
		"  - op: replace\n    path: /m\n    value:\n      a: 1\n"
	mustFail(t, target, records, hewerr.CodeStaleTarget, "/m")
}

// --- more shapes ------------------------------------------------------------

func TestReplaceKeepsTheSpacingAfterTheColon(t *testing.T) {
	mustApply(t, "a:    30\nb: 1\n", "  - op: replace\n    path: /a\n    value: 60\n", "a:    60\nb: 1\n")
}

func TestAddBeforeTheFirstChildOfTheDocument(t *testing.T) {
	mustApply(t, "a: 1\nb: 2\n", "  - op: add\n    path: /z\n    before: /a\n    value: 0\n", "z: 0\na: 1\nb: 2\n")
}

func TestFinalErrorsKeepTheirOwnDiagnostic(t *testing.T) {
	target := "tags:\n  - beta\n  - beta\n"
	he := mustFail(t, target, "  - op: replace\n    path: /tags/=beta\n    value: x\n",
		hewerr.CodeAmbiguousMatch, "/tags/=beta")
	if strings.Contains(he.Error(), "replace requires the node to exist") {
		t.Errorf("an ambiguity is not a missing node: %q", he.Error())
	}
}

func TestForkMaterializesEvenWhenTheValueIsUnchanged(t *testing.T) {
	// Forking is a statement about WHERE the value lives, so it writes the
	// shadowing member even when the value it writes is what was inherited.
	records := "  - op: replace\n    path: /service_a/timeout\n    anchor: fork\n    value: 30\n"
	want := strings.Replace(aliasTarget, "  <<: *defaults\n  port: 8080\n", "  <<: *defaults\n  port: 8080\n  timeout: 30\n", 1)
	mustApply(t, aliasTarget, records, want)
}

func TestFlowContainerTakesAFlowValue(t *testing.T) {
	mustApply(t, "m: {a: 1}\n", "  - op: add\n    path: /m/b\n    value:\n      k: v\n",
		"m: {a: 1, b: {k: v}}\n")
	mustApply(t, "s: [1]\n", "  - op: add\n    path: /s\n    value: [2, 3]\n", "s: [1, [2, 3]]\n")
}

func TestEmptySequenceValueStaysInline(t *testing.T) {
	mustApply(t, "m:\n  a: 1\n", "  - op: add\n    path: /m/tags\n    value: []\n", "m:\n  a: 1\n  tags: []\n")
}

func TestQuotingRules(t *testing.T) {
	target := "m:\n  a: 1\n"
	for _, tc := range []struct{ value, want string }{
		{`""`, `k: ""`},
		{`" x "`, `k: " x "`},
		{`"a #b"`, `k: "a #b"`},
		{`"["`, `k: "["`},
		{`"@at"`, `k: "@at"`},
		{`"plain-ok"`, "k: plain-ok"},
		{`"3.5"`, `k: "3.5"`},
	} {
		got, err := applyIR(t, target, "  - op: add\n    path: /m/k\n    value: "+tc.value+"\n")
		if err != nil {
			t.Fatalf("value %s: %v", tc.value, err)
		}
		if want := "m:\n  a: 1\n  " + tc.want + "\n"; string(got) != want {
			t.Errorf("value %s: got %q, want %q", tc.value, got, want)
		}
	}
}

func TestKeyMatchAgainstANonScalarField(t *testing.T) {
	target := "s:\n  - other: 1\n  - name:\n      first: a\n  - name: b\n"
	// name=b matches the scalar-valued element only; the map-valued one is not
	// a candidate at all.
	mustApply(t, target, "  - op: test\n    path: /s/name=b\n    value:\n      name: b\n", target)
	mustFail(t, target, "  - op: test\n    path: /s/name=a\n    value: 1\n", hewerr.CodeStaleTarget, "/s/name=a")
}

func TestCopyABlockScalarMember(t *testing.T) {
	target := "" +
		"a:\n" +
		"  script: |\n" +
		"    one\n" +
		"\n" +
		"    two\n" +
		"b:\n" +
		"  keep: 1\n"
	want := "" +
		"a:\n" +
		"  script: |\n" +
		"    one\n" +
		"\n" +
		"    two\n" +
		"b:\n" +
		"  keep: 1\n" +
		"  script2: |\n" +
		"    one\n" +
		"\n" +
		"    two\n"
	mustApply(t, target, "  - op: copy\n    from: /a/script\n    path: /b/script2\n", want)
}

// --- the mirror grammar, end to end -----------------------------------------

func applyPatch(t *testing.T, target, patch string) ([]byte, error) {
	t.Helper()
	tls, err := hew.Parse([]byte(patch))
	if err != nil {
		t.Fatalf("patch does not parse: %v", err)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	return Apply([]byte(target), tls[0])
}

func TestMirrorGrammarNestedBodyLines(t *testing.T) {
	target := "s:\n  - name: a\n    cmd: x\n"
	patch := "hew: 1\n\n--- t.yaml format=yaml\n\n@@ /s @@\n  - name: a\n+ - name: b\n+   env:\n+     K: v\n"
	got, err := applyPatch(t, target, patch)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "s:\n  - name: a\n    cmd: x\n  - name: b\n    env:\n      K: v\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMirrorGrammarCommentReplace(t *testing.T) {
	target := "server:\n  # old\n  timeout: 30\n"
	patch := "hew: 1\n\n--- t.yaml format=yaml\n\n@@ /server @@\n- # old\n+ # new\n  timeout: 30\n"
	got, err := applyPatch(t, target, patch)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if string(got) != "server:\n  # new\n  timeout: 30\n" {
		t.Errorf("got:\n%s", got)
	}
}

func TestMirrorGrammarAnchorFork(t *testing.T) {
	patch := "hew: 1\n\n--- t.yaml format=yaml\n\n@@ /service_a @@\n! anchor fork\n- timeout: 30\n+ timeout: 60\n"
	got, err := applyPatch(t, aliasTarget, patch)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(string(got), "  port: 8080\n  timeout: 60\n") {
		t.Errorf("fork did not shadow at the site:\n%s", got)
	}
}
