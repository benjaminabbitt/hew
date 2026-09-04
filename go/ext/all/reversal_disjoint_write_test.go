package all

import (
	"encoding/json"
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// A REVERSAL IS PATH-ADDRESSED, so a write at a DISJOINT path must not change
// what it restores (docs/hew-spec.md §9.4, §10.5). This is the whole reason a
// reversal is a structural patch and not a line diff: an independent writer
// touching an unrelated key is not drift on the reversal's own addresses.
//
// The consumer shape this protects is a config-patch store: it applies a
// change, keeps Invert(before, after) rendered as .hew text, and later replays
// that text against whatever the file has become in order to hand the user
// their own bytes back. If a disjoint sibling key can corrupt that replay, the
// store silently clobbers a foreign config file instead of restoring it.
//
// Asserted on the RESTORED CONTENT, never on err == nil: the failure this pins
// produced VALID JSON with duplicated entries wrapped in spurious nested
// arrays, so a bare error check passes against the broken library and proves
// nothing.

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookMatcher struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

type settingsDoc struct {
	Hooks      map[string][]hookMatcher `json:"hooks"`
	StatusLine map[string]any           `json:"statusLine"`
}

func preToolUse(cmd string) []any {
	return []any{
		map[string]any{
			"matcher": "Bash",
			"hooks": []any{
				map[string]any{"type": "command", "command": cmd},
			},
		},
	}
}

// setAt opens src, sets one path, and returns the produced bytes.
func setAt(t *testing.T, name string, format hew.FormatID, src []byte, path string, val any) []byte {
	t.Helper()
	doc, err := hew.OpenBytes(name, src, hew.As(format))
	if err != nil {
		t.Fatalf("OpenBytes(%s): %v", path, err)
	}
	doc.AtPath(hew.MustParsePath(path)).Set(val)
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("Bytes after Set(%s): %v", path, err)
	}
	return out
}

func TestReversalUnaffectedByDisjointSiblingWrite(t *testing.T) {
	const name = "settings.json"
	const seed = `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo seed"}]}
    ]
  }
}`
	format := hew.FormatJSON
	binding, ok := hew.Lookup(format)
	if !ok {
		t.Fatalf("no binding for %s", format)
	}

	// 1. A tracked write, and the reversal derived from its two images.
	after := setAt(t, name, format, []byte(seed), "/hooks/PreToolUse", preToolUse("echo first"))
	tl, err := hew.Invert(format, []byte(seed), after, hew.DiffOptions{Target: name})
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	reversal, err := hew.Render(tl, hew.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	t.Logf("reversal patch:\n%s", reversal)

	// 2. An INDEPENDENT writer touches a disjoint sibling path. Nothing the
	// reversal addresses is involved.
	drifted := setAt(t, name, format, after, "/statusLine",
		map[string]any{"type": "command", "command": "echo status"})
	t.Logf("drifted:\n%s", drifted)

	// 3. Replay the reversal against what the file has since become.
	replay, err := hew.ParseSingle(reversal)
	if err != nil {
		t.Fatalf("ParseSingle the rendered reversal: %v\npatch=%s", err, reversal)
	}
	restored, err := binding.Applier(drifted, replay)
	t.Logf("restored err: %v", err)
	t.Logf("restored:\n%s", restored)
	if err != nil {
		t.Fatalf("applying the reversal over a DISJOINT sibling write must succeed; "+
			"a new /statusLine key is not drift on /hooks: %v\npatch=%s\ndrifted=%s", err, reversal, drifted)
	}

	// 4. The restored content is the assertion. Not that Applier returned nil.
	if !json.Valid(restored) {
		t.Fatalf("restored document is not valid JSON:\n%s", restored)
	}
	var got settingsDoc
	if err := json.Unmarshal(restored, &got); err != nil {
		t.Fatalf("restored document does not decode into the real shape "+
			"(nested arrays where hook entries belong): %v\n%s", err, restored)
	}
	entries := got.Hooks["PreToolUse"]
	if len(entries) != 1 {
		t.Fatalf("PreToolUse must hold exactly one matcher entry, got %d:\n%s", len(entries), restored)
	}
	if len(entries[0].Hooks) != 1 {
		t.Fatalf("the matcher's hooks must hold exactly one entry, got %d:\n%s", len(entries[0].Hooks), restored)
	}
	if entries[0].Hooks[0].Command != "echo seed" {
		t.Fatalf("the reversal must restore the pre-application command %q, got %q:\n%s",
			"echo seed", entries[0].Hooks[0].Command, restored)
	}

	// The independent writer's key must survive the reversal untouched: a
	// reversal restores what it wrote, and nothing else.
	if got.StatusLine["command"] != "echo status" {
		t.Fatalf("the disjoint writer's /statusLine must survive the reversal, got %v:\n%s",
			got.StatusLine, restored)
	}
}

// The reduced cause, in every format that ships a differ and an applier.
//
// hew's emitters write a Go map's keys in sorted order, so setting a container
// whose document spelled its keys in some other order REORDERS them. Diffing
// back the other way then sees a member that MOVED, which the differ expresses
// as a remove plus a re-add of the same key — the one shape the renderer used
// to collapse. Nothing about it is JSON-specific: the renderer is shared, so
// every format carried it.
//
// The two assertions are the two halves of "it reversed": the post-application
// value is GONE, and the pre-application value is back exactly ONCE. The
// corruption satisfied neither — it left the new value in place and appended
// the old one twice.
func TestReversalRestoresWhenMappingKeysWereReordered(t *testing.T) {
	for _, c := range []struct {
		format hew.FormatID
		name   string
		// seed spells /server's keys in non-sorted order, so that setting
		// /server reorders them.
		seed string
	}{
		{hew.FormatJSON, "config.json", "{\n  \"server\": {\"timeout\": 30, \"host\": \"localhost\"}\n}\n"},
		{hew.FormatJSONC, ".mcp.json", "{\n  \"server\": {\"timeout\": 30, \"host\": \"localhost\"}\n}\n"},
		{hew.FormatYAML, "config.yaml", "server:\n  timeout: 30\n  host: localhost\n"},
		{hew.FormatTOML, "config.toml", "server = {timeout = 30, host = \"localhost\"}\n"},
	} {
		t.Run(string(c.format), func(t *testing.T) {
			binding, ok := hew.Lookup(c.format)
			if !ok {
				t.Fatalf("no binding for %s", c.format)
			}
			after := setAt(t, c.name, c.format, []byte(c.seed), "/server",
				map[string]any{"host": "localhost", "timeout": 60})
			tl, err := hew.Invert(c.format, []byte(c.seed), after, hew.DiffOptions{Target: c.name})
			if err != nil {
				t.Fatalf("Invert: %v", err)
			}
			reversal, err := hew.Render(tl, hew.RenderOptions{})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			replay, err := hew.ParseSingle(reversal)
			if err != nil {
				t.Fatalf("ParseSingle: %v\npatch=%s", err, reversal)
			}
			restored, err := binding.Applier(after, replay)
			if err != nil {
				t.Fatalf("applying the reversal: %v\npatch=%s\nafter=%s", err, reversal, after)
			}
			got := string(restored)
			if strings.Contains(got, "60") {
				t.Fatalf("the reversal left the post-application value in place:\npatch=%s\nrestored=%s", reversal, got)
			}
			if n := strings.Count(got, "30"); n != 1 {
				t.Fatalf("the pre-application value must come back exactly once, got %d:\npatch=%s\nrestored=%s", n, reversal, got)
			}
			// And it is readable at the path it belongs to, not merely present
			// somewhere in the bytes.
			check, err := hew.OpenBytes(c.name, restored, hew.As(c.format))
			if err != nil {
				t.Fatalf("re-open restored: %v\n%s", err, got)
			}
			check.AtPath(hew.MustParsePath("/server/timeout")).Assert(30)
			if _, err := check.Bytes(); err != nil {
				t.Fatalf("/server/timeout did not come back as 30: %v\nrestored=%s", err, got)
			}
		})
	}
}
