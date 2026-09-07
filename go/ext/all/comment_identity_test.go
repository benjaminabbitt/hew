package all

import (
	"strings"
	"testing"

	hew "github.com/benjaminabbitt/hew/go"
)

// Comments were the one thing hew addressed by ORDINAL, in a spec whose §4 says
// "there is no ordinal segment; a path is always a statement about identity,
// never about position in the file". The ordinal was assigned by the comment's
// position in the PATCH BODY, not in the target, so a patch naming one comment
// deleted a different one — silently, exit 0.
//
// A comment is a keyless member, exactly like a set element, so it locates the
// way one does: by the digest of its content.
func TestCommentAddressedByContentNotOrdinal(t *testing.T) {
	for _, c := range []struct {
		name          string
		format        hew.FormatID
		fname, target string
		marker        string
	}{
		{"jsonc", hew.FormatJSONC, "c.jsonc", "{\n  // AAA first comment\n  // BBB second comment\n  \"timeout\": 30\n}\n", "//"},
		{"yaml", hew.FormatYAML, "c.yaml", "# AAA first comment\n# BBB second comment\ntimeout: 30\n", "#"},
		// TOML models a comment immediately above an entry as that entry's
		// LEADING comment, not as a child of the container — correct for TOML,
		// and a difference from YAML and JSONC rather than a defect. The blank
		// line is what makes these standalone container comments, which is the
		// shape a container-addressed comment op is about.
		{"toml", hew.FormatTOML, "c.toml", "# AAA first comment\n# BBB second comment\n\ntimeout = 30\n", "#"},
	} {
		t.Run(c.name, func(t *testing.T) {
			binding, ok := hew.Lookup(c.format)
			if !ok {
				t.Fatalf("no binding for %s", c.format)
			}
			// The patch names the SECOND comment, by its full text. It is the
			// only comment in the patch body, so an ordinal derived from patch
			// order calls it #0 — while in the document it is #1.
			body := "  \"timeout\": 30"
			if c.format != hew.FormatJSONC {
				body = "  timeout: 30"
			}
			if c.format == hew.FormatTOML {
				body = "  timeout = 30"
			}
			patch := "hew: 1\n\n--- " + c.fname + " format=" + string(c.format) +
				"\n\n@@ / @@\n- " + c.marker + " BBB second comment\n" + body + "\n"
			tl, err := hew.ParseSingle([]byte(patch))
			if err != nil {
				t.Fatalf("ParseSingle: %v\n%s", err, patch)
			}
			got, err := binding.Applier([]byte(c.target), tl)
			if err != nil {
				t.Fatalf("removing a comment named by its own text must succeed: %v\n%s", err, patch)
			}
			out := string(got)
			// The named comment goes; the OTHER one survives. Getting this
			// backwards is the data loss.
			if strings.Contains(out, "BBB second comment") {
				t.Fatalf("the comment the patch named is still there:\n%s", out)
			}
			if !strings.Contains(out, "AAA first comment") {
				t.Fatalf("DATA LOSS: deleted a comment the patch never named:\n%s", out)
			}
		})
	}
}
