package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

func sha(b string) string {
	sum := sha256.Sum256([]byte(b))
	return "sha256:" + hex.EncodeToString(sum[:])
}

const (
	patchBytes  = "hew: 1\n"
	beforeBytes = "{\"a\": 1}\n"
	afterBytes  = "{\"a\": 2}\n"
)

func fixtures() RecordFixtures {
	pre := map[string]string{"patch.hew": patchBytes, "target.json": beforeBytes}
	post := map[string]string{"target.json": afterBytes}
	return RecordFixtures{
		Pre:  func(name string) ([]byte, bool) { b, ok := pre[name]; return []byte(b), ok },
		Post: func(name string) ([]byte, bool) { b, ok := post[name]; return []byte(b), ok },
	}
}

var digestFields = []string{"patch.digest", "targets.0.before", "targets.0.after"}

const wantRecord = `hew-record: 1
patch:
  source: patch.hew
targets:
  - target: target.json
    format: json
    committed: true
    transforms:
      - op: replace
        path: /a
        value: 2
`

func goodRecord() string {
	return `hew-record: 1
applied_at: 2026-08-14T09:31:07Z
patch:
  source: patch.hew
  digest: ` + sha(patchBytes) + `
targets:
  - target: target.json
    format: json
    before: ` + sha(beforeBytes) + `
    after: ` + sha(afterBytes) + `
    committed: true
    transforms:
      - op: replace
        path: /a
        value: 2
`
}

func check(t *testing.T, got string) []string {
	t.Helper()
	return CheckRecord([]byte(wantRecord), []byte(got), digestFields, fixtures(), "")
}

func wantNoProbs(t *testing.T, probs []string) {
	t.Helper()
	if len(probs) != 0 {
		t.Fatalf("unexpected problems: %v", probs)
	}
}

func wantProb(t *testing.T, probs []string, substr string) {
	t.Helper()
	for _, p := range probs {
		if strings.Contains(p, substr) {
			return
		}
	}
	t.Fatalf("no problem mentioning %q in %v", substr, probs)
}

func TestCheckRecordAccepts(t *testing.T) {
	wantNoProbs(t, check(t, goodRecord()))
}

func TestCheckRecordCatchesAWrongDigest(t *testing.T) {
	got := strings.Replace(goodRecord(), sha(beforeBytes), sha("something else"), 1)
	probs := check(t, got)
	wantProb(t, probs, "targets.0.before")
	wantProb(t, probs, "recomputed from the fixtures")
}

func TestCheckRecordCatchesAWrongPatchDigest(t *testing.T) {
	got := strings.Replace(goodRecord(), sha(patchBytes), sha("other patch"), 1)
	wantProb(t, check(t, got), "patch.digest")
}

func TestCheckRecordCatchesAWrongAfterDigest(t *testing.T) {
	// A record claiming the file still holds its pre-image is exactly the
	// lie the after digest exists to catch.
	got := strings.Replace(goodRecord(), "after: "+sha(afterBytes), "after: "+sha(beforeBytes), 1)
	wantProb(t, check(t, got), "targets.0.after")
}

func TestCheckRecordCatchesAMissingDigestField(t *testing.T) {
	got := strings.Replace(goodRecord(), "  digest: "+sha(patchBytes)+"\n", "", 1)
	wantProb(t, check(t, got), "missing digest field patch.digest")
}

func TestCheckRecordCatchesANonStringDigest(t *testing.T) {
	got := strings.Replace(goodRecord(), "digest: "+sha(patchBytes), "digest: 7", 1)
	wantProb(t, check(t, got), "want a \"sha256:\" string")
}

func TestCheckRecordCatchesAStructuralDifference(t *testing.T) {
	got := strings.Replace(goodRecord(), "format: json", "format: jsonc", 1)
	wantProb(t, check(t, got), "record mismatch")
}

func TestCheckRecordCatchesAnAbstractTransformList(t *testing.T) {
	got := strings.Replace(goodRecord(), "path: /a", "path: /list/name=a", 1)
	wantProb(t, check(t, got), "record mismatch")
}

func TestCheckRecordRequiresAppliedAt(t *testing.T) {
	got := strings.Replace(goodRecord(), "applied_at: 2026-08-14T09:31:07Z\n", "", 1)
	wantProb(t, check(t, got), "missing applied_at")
}

func TestCheckRecordRequiresAppliedAtToBeRFC3339(t *testing.T) {
	got := strings.Replace(goodRecord(), "2026-08-14T09:31:07Z", "yesterday", 1)
	wantProb(t, check(t, got), "not RFC 3339")
}

func TestCheckRecordRequiresAppliedAtToBeUTC(t *testing.T) {
	got := strings.Replace(goodRecord(), "2026-08-14T09:31:07Z", "2026-08-14T09:31:07+02:00", 1)
	wantProb(t, check(t, got), "not UTC")
}

func TestCheckRecordRejectsANonStringAppliedAt(t *testing.T) {
	got := strings.Replace(goodRecord(), "applied_at: 2026-08-14T09:31:07Z", "applied_at: 7", 1)
	wantProb(t, check(t, got), "want an RFC 3339 timestamp")
}

func TestCheckRecordRefusesAFixtureThatPinsAppliedAt(t *testing.T) {
	want := "applied_at: 2026-08-14T09:31:07Z\n" + wantRecord
	probs := CheckRecord([]byte(want), []byte(goodRecord()), digestFields, fixtures(), "")
	wantProb(t, probs, "cannot be pinned")
	// Pinning it must not ALSO produce a structural mismatch: the field is
	// dropped from both sides once reported.
	for _, p := range probs {
		if strings.Contains(p, "record mismatch") {
			t.Fatalf("applied_at should be dropped from both sides: %v", probs)
		}
	}
}

func TestCheckRecordReportsUnreadableDocuments(t *testing.T) {
	probs := CheckRecord([]byte("\tnot: yaml"), []byte(goodRecord()), digestFields, fixtures(), "")
	wantProb(t, probs, "expected_record fixture")
	probs = CheckRecord([]byte(wantRecord), []byte("\tnot: yaml"), digestFields, fixtures(), "")
	wantProb(t, probs, "record file")
}

func TestCheckRecordNeedsTheSourceItMustDigest(t *testing.T) {
	got := strings.Replace(goodRecord(), "source: patch.hew", "source: elsewhere.hew", 1)
	wantProb(t, check(t, got), "no fixture bytes to recompute patch.digest")

	got = strings.Replace(goodRecord(), "  source: patch.hew\n", "", 1)
	wantProb(t, check(t, got), "no patch.source")
}

func TestCheckRecordNeedsTheTargetItMustDigest(t *testing.T) {
	got := strings.Replace(goodRecord(), "target: target.json", "target: nowhere.json", 1)
	probs := check(t, got)
	wantProb(t, probs, "no fixture bytes to recompute targets.0.before")
	wantProb(t, probs, "no fixture bytes to recompute targets.0.after")

	got = strings.Replace(goodRecord(), "  - target: target.json", "  - other: target.json", 1)
	wantProb(t, check(t, got), "no targets.0.target")
}

func TestCheckRecordRejectsAnUnknownDigestField(t *testing.T) {
	probs := CheckRecord([]byte(wantRecord), []byte(goodRecord()), []string{"patch.source"}, fixtures(), "")
	wantProb(t, probs, "names no digest this runner knows how to recompute")

	probs = CheckRecord([]byte(wantRecord), []byte(goodRecord()), []string{"targets.0.committed"}, fixtures(), "")
	wantProb(t, probs, "names no digest this runner knows how to recompute")
}

func TestCheckRecordOutOfRangeTargetIndex(t *testing.T) {
	probs := CheckRecord([]byte(wantRecord), []byte(goodRecord()), []string{"targets.9.before"}, fixtures(), "")
	wantProb(t, probs, "missing digest field targets.9.before")
}

// The pinned-clock inversion (ruling O37). goodRecord()'s applied_at is
// 2026-08-14T09:31:07Z, so a fixture pinning the same instant is now REQUIRED
// rather than refused.
const pin = "2026-08-14T09:31:07Z"

func pinnedFixture(appliedAt string) []byte {
	return []byte("applied_at: \"" + appliedAt + "\"\n" + wantRecord)
}

func checkPinned(t *testing.T, want []byte, got string) []string {
	t.Helper()
	return CheckRecord(want, []byte(got), digestFields, fixtures(), pin)
}

func TestCheckRecordAcceptsAPinnedAppliedAt(t *testing.T) {
	wantNoProbs(t, checkPinned(t, pinnedFixture(pin), goodRecord()))
}

// Both YAML spellings §9.7 allows are the same instant: a bare timestamp
// decodes to time.Time, a quoted one stays a string, and the comparison is on
// the normalized RFC 3339 rendering so it is about the instant, not the
// quoting.
func TestCheckRecordPinnedAppliedAtIgnoresYAMLSpelling(t *testing.T) {
	bare := []byte("applied_at: " + pin + "\n" + wantRecord)
	wantNoProbs(t, checkPinned(t, bare, goodRecord()))

	quoted := strings.Replace(goodRecord(), "applied_at: "+pin, `applied_at: "`+pin+`"`, 1)
	wantNoProbs(t, checkPinned(t, pinnedFixture(pin), quoted))
}

// A pinned run whose record carries a different instant is the failure the
// whole case exists to detect.
func TestCheckRecordCatchesAnUnpinnedRecord(t *testing.T) {
	got := strings.Replace(goodRecord(), "applied_at: "+pin, "applied_at: 2001-01-01T00:00:00Z", 1)
	wantProb(t, checkPinned(t, pinnedFixture(pin), got), "but the run pinned "+pin)
}

// A case that pins the clock and then leaves the fixture's applied_at unpinned
// asserts nothing at all, which is a corpus error rather than a pass.
func TestCheckRecordPinnedRunRequiresAPinnedFixture(t *testing.T) {
	wantProb(t, checkPinned(t, []byte(wantRecord), goodRecord()), "expected_record MUST pin it too")
}

// A fixture pinning a different instant than the env means the case
// contradicts itself.
func TestCheckRecordPinnedFixtureMustAgreeWithTheEnv(t *testing.T) {
	probs := checkPinned(t, pinnedFixture("2001-01-01T00:00:00Z"), goodRecord())
	wantProb(t, probs, "but env pins "+pin)
}

func TestCheckRecordPinnedRejectsNonTimestamps(t *testing.T) {
	probs := CheckRecord(pinnedFixture(pin), []byte(goodRecord()), digestFields, fixtures(), "yesterday")
	wantProb(t, probs, "which is not RFC 3339")

	probs = checkPinned(t, []byte("applied_at: 7\n"+wantRecord), goodRecord())
	wantProb(t, probs, "expected_record's applied_at is 7")

	got := strings.Replace(goodRecord(), "applied_at: "+pin, "applied_at: 7", 1)
	wantProb(t, checkPinned(t, pinnedFixture(pin), got), "want an RFC 3339 timestamp")

	got = strings.Replace(goodRecord(), "applied_at: "+pin+"\n", "", 1)
	wantProb(t, checkPinned(t, pinnedFixture(pin), got), "record is missing applied_at")
}

// Whichever branch ran, applied_at must be gone from both sides afterwards, or
// the structural comparison reports it a second time as a mismatch.
func TestCheckRecordPinnedDropsAppliedAtFromTheComparison(t *testing.T) {
	for _, probs := range [][]string{
		checkPinned(t, pinnedFixture(pin), goodRecord()),
		checkPinned(t, []byte(wantRecord), goodRecord()),
	} {
		for _, p := range probs {
			if strings.Contains(p, "record mismatch") {
				t.Fatalf("applied_at should be dropped from both sides: %v", probs)
			}
		}
	}
}

func TestRecordArgValue(t *testing.T) {
	argv := []string{"apply", "-i", "--record", "out.hewt", "patch.hew"}
	if got := recordArgValue(argv, "--record"); got != "out.hewt" {
		t.Fatalf("got %q", got)
	}
	if got := recordArgValue(argv, "--ops"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := recordArgValue([]string{"apply", "--record"}, "--record"); got != "" {
		t.Fatalf("a trailing flag has no value: got %q", got)
	}
}

func TestLookupPathAndChildOf(t *testing.T) {
	doc := map[string]any{
		"a":    map[string]any{"b": "v"},
		"list": []any{"zero", "one"},
		"s":    "scalar",
	}
	if v, ok := lookupPath(doc, []string{"a", "b"}); !ok || v != "v" {
		t.Fatalf("got %v %v", v, ok)
	}
	if v, ok := lookupPath(doc, []string{"list", "1"}); !ok || v != "one" {
		t.Fatalf("got %v %v", v, ok)
	}
	if _, ok := lookupPath(doc, []string{"list", "9"}); ok {
		t.Fatal("index past the end resolved")
	}
	if _, ok := lookupPath(doc, []string{"list", "2"}); ok {
		t.Fatal("index == len resolved")
	}
	if _, ok := lookupPath(doc, []string{"list", "x"}); ok {
		t.Fatal("non-numeric sequence index resolved")
	}
	if _, ok := lookupPath(doc, []string{"list", "-1"}); ok {
		t.Fatal("negative index resolved")
	}
	if _, ok := lookupPath(doc, []string{"s", "b"}); ok {
		t.Fatal("a scalar has no children")
	}
	if _, ok := lookupPath(doc, []string{"nope"}); ok {
		t.Fatal("missing key resolved")
	}
	if v, ok := lookupPath(doc, nil); !ok || !reflect.DeepEqual(v, doc) {
		t.Fatal("the empty path is the document itself")
	}
	if _, ok := lookupString(doc, "a"); ok {
		t.Fatal("a map is not a string")
	}
	if _, ok := lookupString(doc, "nope"); ok {
		t.Fatal("a missing field is not a string")
	}
}

func TestDeletePath(t *testing.T) {
	doc := map[string]any{
		"a":    map[string]any{"b": "v", "c": "w"},
		"list": []any{"zero", "one"},
	}
	deletePath(doc, nil)
	deletePath(doc, []string{"nope", "b"})
	deletePath(doc, []string{"a", "b"})
	if _, ok := doc["a"].(map[string]any)["b"]; ok {
		t.Fatal("a.b not deleted")
	}
	if _, ok := doc["a"].(map[string]any)["c"]; !ok {
		t.Fatal("a.c wrongly deleted")
	}
	deletePath(doc, []string{"list", "0"})
	if doc["list"].([]any)[0] != nil {
		t.Fatal("a sequence element is blanked, not removed")
	}
	doc["list"] = []any{"zero", "one"}
	for _, bad := range []string{"9", "2", "-1", "x"} {
		deletePath(doc, []string{"list", bad})
		if !reflect.DeepEqual(doc["list"], []any{"zero", "one"}) {
			t.Fatalf("index %q must be a no-op, got %v", bad, doc["list"])
		}
	}
	deletePath("scalar", []string{"x"})
}
