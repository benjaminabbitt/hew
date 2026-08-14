package hew

import "testing"

// parseOne parses a single-section patch, with the preamble lines after
// "hew: 1" and the hunk body supplied separately.
func parseOneDirective(t *testing.T, preamble, body string) TransformList {
	t.Helper()
	src := []byte("hew: 1\n" + preamble + "\n--- t.yaml format=yaml\n\n" + body)
	tls, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, src)
	}
	if len(tls) != 1 {
		t.Fatalf("want 1 file section, got %d", len(tls))
	}
	return tls[0]
}

func parseFails(t *testing.T, body string) {
	t.Helper()
	src := []byte("hew: 1\n\n--- t.yaml format=yaml\n\n" + body)
	if _, err := Parse(src); err == nil {
		t.Fatalf("expected HEW001 for:\n%s", body)
	}
}

func opAt(t *testing.T, tl TransformList, op OpKind) Transform {
	t.Helper()
	for _, tr := range tl.Transform {
		if tr.Op == op {
			return tr
		}
	}
	t.Fatalf("no %s transform in %v", op, tl.Transform)
	return Transform{}
}

// TestParseDirectivesRideTheirTransforms pins §9.1 step 6: a `!` line emits no
// transform of its own, it qualifies the transforms its body lines produce.
func TestParseDirectivesRideTheirTransforms(t *testing.T) {
	t.Run("anchor", func(t *testing.T) {
		for _, mode := range []AnchorMode{AnchorFork, AnchorRewrite} {
			tl := parseOneDirective(t, "", "@@ /s @@\n! anchor "+string(mode)+"\n- timeout: 30\n+ timeout: 60\n")
			for _, tr := range tl.Transform {
				if tr.Anchor != mode {
					t.Errorf("%s: anchor want %s, got %q", tr.Op, mode, tr.Anchor)
				}
			}
		}
	})
	t.Run("anchor argument is checked", func(t *testing.T) {
		parseFails(t, "@@ /s @@\n! anchor sideways\n- a: 1\n")
		parseFails(t, "@@ /s @@\n! anchor\n- a: 1\n")
	})
	t.Run("optional", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n! optional\n- gone: true\n")
		for _, tr := range tl.Transform {
			if !tr.Optional {
				t.Errorf("%s: optional not carried", tr.Op)
			}
		}
	})
	t.Run("default and upsert", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n! default\n+ timeout: 30\n")
		if got := opAt(t, tl, OpAdd).OnConflict; got != ConflictKeep {
			t.Errorf("! default: on_conflict want keep, got %q", got)
		}
		tl = parseOneDirective(t, "", "@@ /s @@\n! upsert\n+ timeout: 30\n")
		if got := opAt(t, tl, OpAdd).OnConflict; got != ConflictReplace {
			t.Errorf("! upsert: on_conflict want replace, got %q", got)
		}
	})
	t.Run("idempotent rides both the assert and the write", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n! idempotent\n- timeout: 30\n+ timeout: 60\n")
		if !opAt(t, tl, OpTest).Idempotent || !opAt(t, tl, OpReplace).Idempotent {
			t.Errorf("! idempotent must qualify both the test and the replace: %v", tl.Transform)
		}
	})
	t.Run("strict opts the write out of the file pragma", func(t *testing.T) {
		tl := parseOneDirective(t, "idempotent: true\n", "@@ /s @@\n! strict\n- timeout: 30\n+ timeout: 60\n")
		if !opAt(t, tl, OpTest).Idempotent {
			t.Error("the pragma's tolerance still applies to the assert (§7.5)")
		}
		if opAt(t, tl, OpReplace).Idempotent {
			t.Error("! strict must make the write strict")
		}
	})
	t.Run("file pragma alone", func(t *testing.T) {
		tl := parseOneDirective(t, "idempotent: true\n", "@@ /s @@\n- timeout: 30\n+ timeout: 60\n")
		if !opAt(t, tl, OpTest).Idempotent || !opAt(t, tl, OpReplace).Idempotent {
			t.Errorf("the pragma applies to every hunk: %v", tl.Transform)
		}
	})
	t.Run("no pragma, no directive", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n- timeout: 30\n+ timeout: 60\n")
		if opAt(t, tl, OpTest).Idempotent || opAt(t, tl, OpReplace).Idempotent {
			t.Error("strict is the default (§7.5)")
		}
	})
	t.Run("line-scoped attaches to the next line only", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n+ a: 1\n! default\n+ b: 2\n")
		var adds []Transform
		for _, tr := range tl.Transform {
			if tr.Op == OpAdd {
				adds = append(adds, tr)
			}
		}
		if len(adds) != 2 {
			t.Fatalf("want 2 adds, got %d", len(adds))
		}
		if adds[0].OnConflict != "" {
			t.Errorf("the first add precedes the directive: %q", adds[0].OnConflict)
		}
		if adds[1].OnConflict != ConflictKeep {
			t.Errorf("the second add carries it: %q", adds[1].OnConflict)
		}
	})
	t.Run("unattached line-scoped directive", func(t *testing.T) {
		parseFails(t, "@@ /s @@\n+ a: 1\n! default\n")
	})
	t.Run("unknown directive", func(t *testing.T) {
		parseFails(t, "@@ /s @@\n! frobnicate\n+ a: 1\n")
	})
}

// TestParseGroupsIndentedBodyLines pins that a member's continuation lines are
// part of that member, not members of their own.
func TestParseGroupsIndentedBodyLines(t *testing.T) {
	t.Run("nested mapping value", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n  cmd: npx\n+ env:\n+   K: v\n")
		add := opAt(t, tl, OpAdd)
		if add.Path.String() != "/s/env" {
			t.Fatalf("path: want /s/env, got %s", add.Path)
		}
		var got map[string]string
		if err := add.Value.Decode(&got); err != nil || got["K"] != "v" {
			t.Fatalf("value: want {K: v}, got %v (%v)", got, err)
		}
		if n := len(tl.Transform); n != 2 {
			t.Fatalf("want 1 test + 1 add, got %d transforms", n)
		}
	})
	t.Run("two-line sequence element", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n  - name: github\n+ - name: ctxloom\n+   command: ctxloom\n")
		add := opAt(t, tl, OpAdd)
		if add.Path.String() != "/s" {
			t.Fatalf("an element add addresses the container, got %s", add.Path)
		}
		if add.After.String() != "/s/name=github" {
			t.Fatalf("placement: want after /s/name=github, got %q", add.After)
		}
		var got map[string]string
		if err := add.Value.Decode(&got); err != nil || got["name"] != "ctxloom" || got["command"] != "ctxloom" {
			t.Fatalf("value: %v (%v)", got, err)
		}
	})
	t.Run("a same-indent line is its own member", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n+ a: 1\n+ b: 2\n")
		n := 0
		for _, tr := range tl.Transform {
			if tr.Op == OpAdd {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("want 2 adds, got %d", n)
		}
	})
	t.Run("two dash lines are two elements", func(t *testing.T) {
		tl := parseOneDirective(t, "", "@@ /s @@\n+ - a\n+ - b\n")
		if n := len(tl.Transform); n != 2 {
			t.Fatalf("want 2 adds, got %d transforms: %v", n, tl.Transform)
		}
	})
}

// TestParseCommentOrdinalsArePerImage pins §4.5b's ordinals against §5's two
// images: a removed comment and the comment replacing it are both #0.
func TestParseCommentOrdinalsArePerImage(t *testing.T) {
	tl := parseOneDirective(t, "", "@@ /s @@\n- # old\n+ # new\n  timeout: 30\n")
	rep := opAt(t, tl, OpReplace)
	if rep.Path.String() != "/s/#0" {
		t.Fatalf("want a replace at /s/#0, got %s", rep.Path)
	}
	if text, ok := CommentText(rep.Value); !ok || text != "new" {
		t.Fatalf("comment value: got %q (%v)", text, ok)
	}
	if text, ok := CommentText(opAt(t, tl, OpTest).Value); !ok || text != "old" {
		t.Fatalf("before-image comment: got %q (%v)", text, ok)
	}

	// Two added comments number 0 and 1 in the after-image.
	tl = parseOneDirective(t, "", "@@ /s @@\n+ # one\n+ # two\n")
	var paths []string
	for _, tr := range tl.Transform {
		paths = append(paths, tr.Path.String())
	}
	if len(paths) != 2 || paths[0] != "/s/#0" || paths[1] != "/s/#1" {
		t.Fatalf("comment ordinals: %v", paths)
	}

	// A context comment is in both images, so the next removed comment is #1.
	tl = parseOneDirective(t, "", "@@ /s @@\n  # kept\n- # dropped\n")
	if rm := opAt(t, tl, OpRemove); rm.Path.String() != "/s/#1" {
		t.Fatalf("want a remove at /s/#1, got %s", rm.Path)
	}
}

// TestCommentValueRoundTrip pins the {comment: "…"} shape the corpus fixtures
// carry at a comment address (§4.5b, §11.10 reduction 3).
func TestCommentValueRoundTripDirectives(t *testing.T) {
	v := CommentValue("hello")
	got, ok := CommentText(v)
	if !ok || got != "hello" {
		t.Fatalf("round trip: %q (%v)", got, ok)
	}
	if _, ok := CommentText(Value{}); ok {
		t.Error("the absent value is not a comment value")
	}
	plain, err := ValueOf("hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := CommentText(plain); ok {
		t.Error("a plain scalar is not a comment value")
	}
	notComment, err := ValueOf(map[string]string{"other": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := CommentText(notComment); ok {
		t.Error("a one-key mapping that is not {comment: …} is not a comment value")
	}
}
