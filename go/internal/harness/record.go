package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// recordArgValue returns the value following flag in argv, or "".
func recordArgValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// RecordFixtures supplies the bytes a record's digests must be recomputed
// from: Pre reads a case fixture as it stood BEFORE the run, Post reads a
// file as it stands after it.
type RecordFixtures struct {
	Pre  func(name string) ([]byte, bool)
	Post func(name string) ([]byte, bool)
}

// CheckRecord compares an emitted application record (§9.7) against a corpus
// fixture and returns the problems found.
//
// Two classes of field cannot be pinned in a fixture, and both are ASSERTED
// rather than ignored — a comparison that merely dropped them would let a
// record with wrong digests and no timestamp pass, which is exactly the
// vacuous-pass this harness exists to refuse:
//
//   - The digests named by record_digest_fields. The runner recomputes each
//     one from the case's own fixture bytes — the patch source and the target
//     the record itself names, both of which the structural comparison pins —
//     and requires the record to have got it right, then drops it from both
//     sides.
//   - applied_at, a wall clock. The runner requires it to be present and RFC
//     3339 UTC, then drops it.
//
// Everything else compares structurally, so a missing or extra field is a
// failure.
func CheckRecord(want, got []byte, digestFields []string, fx RecordFixtures) []string {
	var wantV, gotV any
	if err := yaml.Unmarshal(want, &wantV); err != nil {
		return []string{"expected_record fixture: " + err.Error()}
	}
	if err := yaml.Unmarshal(got, &gotV); err != nil {
		return []string{"record file: " + err.Error()}
	}

	probs := checkAppliedAt(wantV, gotV)
	for _, f := range digestFields {
		probs = append(probs, checkDigestField(f, gotV, fx)...)
		parts := strings.Split(f, ".")
		deletePath(gotV, parts)
		deletePath(wantV, parts)
	}
	if !reflect.DeepEqual(wantV, gotV) {
		probs = append(probs, fmt.Sprintf("record mismatch (digest and timestamp fields excluded):\nwant: %#v\ngot:  %#v", wantV, gotV))
	}
	return probs
}

const appliedAtKey = "applied_at"

// checkAppliedAt asserts the record carries an RFC 3339 UTC timestamp (§9.7)
// and removes it from the comparison. A fixture cannot pin a wall clock, so a
// fixture that tries to is itself the error.
func checkAppliedAt(wantV, gotV any) []string {
	var probs []string
	if _, ok := lookupPath(wantV, []string{appliedAtKey}); ok {
		probs = append(probs, "expected_record fixture pins applied_at, which is a wall clock and cannot be pinned")
		deletePath(wantV, []string{appliedAtKey})
	}
	raw, ok := lookupPath(gotV, []string{appliedAtKey})
	if !ok {
		return append(probs, "record is missing applied_at (§9.7)")
	}
	deletePath(gotV, []string{appliedAtKey})

	// An unquoted RFC 3339 scalar is YAML's own timestamp type and decodes
	// to a time.Time (§9.7's example is spelled that way); a quoted one stays
	// a string. Both are legal.
	var ts time.Time
	switch v := raw.(type) {
	case time.Time:
		ts = v
	case string:
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return append(probs, "record applied_at is not RFC 3339: "+v)
		}
		ts = parsed
	default:
		return append(probs, fmt.Sprintf("record applied_at is %T, want an RFC 3339 timestamp", raw))
	}
	if _, offset := ts.Zone(); offset != 0 {
		probs = append(probs, "record applied_at is not UTC (§9.7): "+ts.Format(time.RFC3339))
	}
	return probs
}

// checkDigestField recomputes one record_digest_fields entry from the
// fixtures and compares it against what the record claims.
func checkDigestField(field string, gotV any, fx RecordFixtures) []string {
	parts := strings.Split(field, ".")
	raw, ok := lookupPath(gotV, parts)
	if !ok {
		return []string{"record is missing digest field " + field + " (§9.7)"}
	}
	src, serr := digestSource(field, parts, gotV, fx)
	if serr != "" {
		return []string{serr}
	}
	claimed, isStr := raw.(string)
	if !isStr {
		return []string{fmt.Sprintf("record field %s is %T, want a \"sha256:\" string", field, raw)}
	}
	sum := sha256.Sum256(src)
	if wantDigest := "sha256:" + hex.EncodeToString(sum[:]); claimed != wantDigest {
		return []string{fmt.Sprintf("record field %s: want %s (recomputed from the fixtures), got %s", field, wantDigest, claimed)}
	}
	return nil
}

// digestSource maps a record_digest_fields entry to the bytes it must digest.
// The file each entry names is read out of the RECORD — patch.source for the
// patch, targets[n].target for a target — and both of those are themselves
// pinned by the structural comparison, so the record cannot pick its own
// convenient input.
func digestSource(field string, parts []string, gotV any, fx RecordFixtures) ([]byte, string) {
	missing := func(name string) string {
		return "no fixture bytes to recompute " + field + " from (" + name + ")"
	}
	if field == "patch.digest" {
		name, ok := lookupString(gotV, "patch", "source")
		if !ok {
			return nil, "record has no patch.source to recompute " + field + " from"
		}
		b, ok := fx.Pre(name)
		if !ok {
			return nil, missing(name)
		}
		return b, ""
	}
	if len(parts) == 3 && parts[0] == "targets" && (parts[2] == "before" || parts[2] == "after") {
		name, ok := lookupString(gotV, "targets", parts[1], "target")
		if !ok {
			return nil, "record has no targets." + parts[1] + ".target to recompute " + field + " from"
		}
		read := fx.Pre
		if parts[2] == "after" {
			read = fx.Post
		}
		b, ok := read(name)
		if !ok {
			return nil, missing(name)
		}
		return b, ""
	}
	return nil, "corpus error: record_digest_fields entry " + field + " names no digest this runner knows how to recompute"
}

func lookupString(v any, parts ...string) (string, bool) {
	raw, ok := lookupPath(v, parts)
	if !ok {
		return "", false
	}
	s, isStr := raw.(string)
	return s, isStr
}

// lookupPath walks a dotted field path over decoded YAML, where a numeric
// component indexes a sequence.
func lookupPath(v any, parts []string) (any, bool) {
	cur := v
	for _, p := range parts {
		child, ok := childOf(cur, p)
		if !ok {
			return nil, false
		}
		cur = child
	}
	return cur, true
}

func childOf(v any, part string) (any, bool) {
	switch n := v.(type) {
	case map[string]any:
		child, ok := n[part]
		return child, ok
	case []any:
		i, err := strconv.Atoi(part)
		if err != nil || i < 0 || i >= len(n) {
			return nil, false
		}
		return n[i], true
	}
	return nil, false
}

// deletePath removes a field from decoded YAML. A sequence element cannot be
// deleted without renumbering its siblings, so one is replaced by a
// placeholder instead; record_digest_fields never names a whole element.
func deletePath(v any, parts []string) {
	if len(parts) == 0 {
		return
	}
	parent := v
	for _, p := range parts[:len(parts)-1] {
		child, ok := childOf(parent, p)
		if !ok {
			return
		}
		parent = child
	}
	last := parts[len(parts)-1]
	switch n := parent.(type) {
	case map[string]any:
		delete(n, last)
	case []any:
		if i, err := strconv.Atoi(last); err == nil && i >= 0 && i < len(n) {
			n[i] = nil
		}
	}
}
