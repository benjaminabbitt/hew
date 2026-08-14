package hewjson

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/benjaminabbitt/hew"
	"github.com/benjaminabbitt/hew/internal/hewerr"
	"gopkg.in/yaml.v3"
)

// --- test ---------------------------------------------------------------

// evalTest evaluates one OpTest, raising the code the failing MODE maps to.
//
// Scope note flagged in the P2 report: §10's error table gives two
// overlapping readings for "an addressed node is missing" — HEW010's own row
// claims it for "a context or '-' line", while HEW013's row separately
// claims it for "a '-' line's node". Appendix A.1's Transform does not
// retain which mirror-grammar spelling produced a given OpTest (a context
// line and a `? expect` both lower to the same record), so this
// implementation treats every plain-value OpTest failure as HEW010 — the
// row that names "context" lines explicitly, and the one json/stale-context
// pins — reserving HEW013 for remove/replace/copy's own missing-target
// checks, where the taxonomy is unambiguous.
func (d *doc) evalTest(target string, t hew.Transform) error {
	switch {
	case t.Absent:
		n, err := d.resolveFull(target, t.Path, t.PatchLine)
		if err != nil {
			if he, ok := err.(*hewerr.Error); ok && he.Code == hewerr.CodeAmbiguousMatch {
				return err
			}
			return nil // genuinely absent: satisfied
		}
		_ = n
		return appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, "expected absent (? absent), but the node exists")
	case t.Exhaustive:
		n, err := d.resolveFull(target, t.Path, t.PatchLine)
		if err != nil {
			return err
		}
		got, kerr := containerCount(n)
		if kerr != nil {
			return appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, "? exhaustive: "+kerr.Error())
		}
		if got != *t.Count {
			e := appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine,
				"? exhaustive: the listed children are not the complete child set")
			e.Want, e.Got = strconv.Itoa(*t.Count), strconv.Itoa(got)
			return e
		}
		return nil
	case t.Count != nil:
		n, err := d.resolveFull(target, t.Path, t.PatchLine)
		if err != nil {
			return err
		}
		got, kerr := containerCount(n)
		if kerr != nil {
			return appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, "? count: "+kerr.Error())
		}
		if got != *t.Count {
			e := appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, "? count: mismatch")
			e.Want, e.Got = strconv.Itoa(*t.Count), strconv.Itoa(got)
			return e
		}
		return nil
	case t.NodeKind != nil:
		n, err := d.resolveFull(target, t.Path, t.PatchLine)
		if err != nil {
			return err
		}
		got := nodeKindOf(n)
		if got != *t.NodeKind {
			e := appErr(hewerr.CodeAssertionFailed, target, t.Path.String(), t.PatchLine, "? kind: mismatch")
			e.Want, e.Got = string(*t.NodeKind), string(got)
			return e
		}
		return nil
	default: // plain value test: a context/"-" line, or "? expect"
		n, err := d.resolveFull(target, t.Path, t.PatchLine)
		if err != nil {
			if he, ok := err.(*hewerr.Error); ok {
				if he.Code == hewerr.CodeAmbiguousMatch {
					return he
				}
				he.Code = hewerr.CodeStaleTarget
				he.Want = t.Value.String()
			}
			return err
		}
		got, verr := d.nodeValue(n)
		if verr != nil {
			return appErr(hewerr.CodeTargetParse, target, t.Path.String(), t.PatchLine, verr.Error())
		}
		if !got.Equal(t.Value) {
			e := appErr(hewerr.CodeStaleTarget, target, t.Path.String(), t.PatchLine, "")
			e.Want, e.Got = t.Value.String(), got.String()
			return e
		}
		return nil
	}
}

func containerCount(n *jNode) (int, error) {
	switch n.kind {
	case jObj:
		return len(n.members), nil
	case jArr:
		return len(n.elems), nil
	default:
		return 0, fmt.Errorf("not a container")
	}
}

func nodeKindOf(n *jNode) hew.NodeKind {
	switch n.kind {
	case jObj:
		return hew.KindMap
	case jArr:
		return hew.KindSeq
	default:
		return hew.KindScalar
	}
}

// --- add ------------------------------------------------------------------

// planAdd computes the edit for one OpAdd, or nil if the add is satisfied
// with zero edits (! default with an existing key, or ! idempotent with a
// matching existing value — §7.5, §7.7).
func (d *doc) planAdd(target string, t hew.Transform) (*edit, error) {
	return d.planInsert(target, t.Path, t.Before, t.After, t.Value, jsonEncode(t.Value), t.OnConflict, t.Idempotent, t.PatchLine)
}

// planCopy resolves From and inserts its RAW source bytes at Path — copy
// takes its value by reference (§9.6), and the JSON binding preserves the
// referenced subtree's exact formatting rather than re-encoding it.
func (d *doc) planCopy(target string, t hew.Transform) (*edit, error) {
	from, err := d.resolveFull(target, t.From, t.PatchLine)
	if err != nil {
		return nil, err
	}
	raw := string(d.src[from.start:from.end])
	v, verr := d.nodeValue(from)
	if verr != nil {
		return nil, appErr(hewerr.CodeTargetParse, target, t.From.String(), t.PatchLine, verr.Error())
	}
	return d.planInsert(target, t.Path, t.Before, t.After, v, raw, "", false, t.PatchLine)
}

// planInsert computes the edit for one add (or a copy's insertion half).
//
// Disambiguating map-style (path = container+newKey) from sequence-style
// (path = the container itself) can't be done from the segment kind alone —
// both a keyed field ("/server/tls") and a sequence container
// ("/servers", itself a key under root) end in a SegKey. The real signal is
// whether path resolves directly to an existing ARRAY (sequence-style: the
// container must already exist) versus anything else. When path DOES
// resolve to something that is NOT an array, that is not an error — it is a
// map-style add whose key already exists, the ordinary OnConflict/idempotent
// case json/reapply-add-exists pins.
func (d *doc) planInsert(target string, path, before, after hew.Path, value hew.Value, text string, onConflict hew.OnConflict, idempotent bool, line int) (*edit, error) {
	if existing, err := d.resolveFull(target, path, line); err == nil {
		if existing.kind == jArr {
			return d.insertArrayElement(existing, before, after, value, text, line)
		}
		return d.handleExistingConflict(target, path, existing, value, text, onConflict, idempotent, line)
	}

	parentPath, ok := path.Parent()
	if !ok {
		return nil, appErr(hewerr.CodeNoMatch, target, path.String(), line, "add: no container to insert into")
	}
	parent, err := d.resolveFull(target, parentPath, line)
	if err != nil {
		return nil, err
	}
	if parent.kind != jObj {
		return nil, appErr(hewerr.CodeInexpressible, target, path.String(), line, "add: parent is not an object")
	}
	last := path.Segment(path.Len() - 1)
	if last.Kind == hew.SegComment {
		return nil, appErr(hewerr.CodeInexpressible, target, parentPath.String(), line,
			"add: JSON has no comment nodes; cannot add a comment (§8.1, §9.3)")
	}
	if last.Kind != hew.SegKey {
		return nil, appErr(hewerr.CodeInexpressible, target, path.String(), line, "add: unsupported address shape")
	}
	return d.insertObjectMember(parent, last.Name, before, after, value, text, line)
}

// handleExistingConflict implements the add-semantics variants (OP-02/03/04,
// §7.7) for a node that already exists at path.
func (d *doc) handleExistingConflict(target string, path hew.Path, existing *jNode, value hew.Value, text string, onConflict hew.OnConflict, idempotent bool, line int) (*edit, error) {
	existingVal, verr := d.nodeValue(existing)
	if verr != nil {
		return nil, appErr(hewerr.CodeTargetParse, target, path.String(), line, verr.Error())
	}
	switch onConflict {
	case hew.ConflictKeep:
		return nil, nil
	case hew.ConflictReplace:
		return &edit{start: existing.start, end: existing.end, text: text}, nil
	}
	if idempotent && existingVal.Equal(value) {
		return nil, nil
	}
	return nil, appErr(hewerr.CodeAlreadyExists, target, path.String(), line,
		"add: node already exists; use \"! idempotent\" if re-applying is expected")
}

// insertObjectMember computes the edit to add a new "key": value member.
func (d *doc) insertObjectMember(obj *jNode, key string, before, after hew.Path, value hew.Value, valueText string, line int) (*edit, error) {
	afterIdx, beforeIdx, err := d.siblingIndicesObj(obj, before, after, line)
	if err != nil {
		return nil, err
	}
	newMember := jsonQuote(key) + ": " + valueText
	d.note(obj, hew.Segment{Kind: hew.SegKey, Name: key}, value, afterIdx)
	return insertIntoObj(d.src, obj, afterIdx, beforeIdx, newMember), nil
}

// pendingAdd is one insertion this Apply has already planned, kept because a
// later placement may name it: §9.1 step 5 chains a run of `+` lines, each
// placed after the one above it (corpus jsonc/add-with-leading-comment pins
// the shape), and a node this patch is adding is not in the parsed document
// for resolveFull to find. Landing at the SAME child index puts this add
// immediately after it, because equal-offset edits keep their list order.
type pendingAdd struct {
	container *jNode
	seg       hew.Segment // the added member's key, for a map
	value     hew.Value
	idx       int
}

func (d *doc) note(container *jNode, seg hew.Segment, value hew.Value, idx int) {
	d.pending = append(d.pending, pendingAdd{container: container, seg: seg, value: value, idx: idx})
}

// pendingIndex reports the child index a placement path names among this
// Apply's own pending inserts. The most recent match wins, so a chain of three
// `+` lines walks forward rather than collapsing onto the first.
func (d *doc) pendingIndex(container *jNode, p hew.Path) (int, bool) {
	seg := p.Segment(p.Len() - 1)
	for i := len(d.pending) - 1; i >= 0; i-- {
		pa := d.pending[i]
		if pa.container != container {
			continue
		}
		if (seg.Kind == hew.SegKey && pa.seg.Kind == hew.SegKey && seg.Name == pa.seg.Name) ||
			segNamesValue(seg, pa.value) {
			return pa.idx, true
		}
	}
	return 0, false
}

// segNamesValue applies §4.2's key-match comparison to a value in hand, for a
// node that is not in the document yet and so has no parsed node to compare.
func segNamesValue(seg hew.Segment, v hew.Value) bool {
	n := v.Node()
	if n == nil || seg.Kind != hew.SegMatch {
		return false
	}
	want := scalarToValue(seg.Value)
	if seg.Name == "" {
		return hew.NodeValue(n).Equal(want)
	}
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == seg.Name {
			return hew.NodeValue(n.Content[i+1]).Equal(want)
		}
	}
	return false
}

func (d *doc) siblingIndicesObj(obj *jNode, before, after hew.Path, line int) (afterIdx, beforeIdx int, err error) {
	afterIdx, beforeIdx = -1, -1
	if !after.IsZero() {
		n, rerr := d.resolveFull("", after, line)
		if rerr != nil {
			if i, ok := d.pendingIndex(obj, after); ok {
				return i, -1, nil
			}
			return 0, 0, rerr
		}
		for i, m := range obj.members {
			if m.value == n || (m.valStart == n.start && m.valEnd == n.end) {
				afterIdx = i
			}
		}
	}
	if afterIdx < 0 && !before.IsZero() {
		n, rerr := d.resolveFull("", before, line)
		if rerr != nil {
			return 0, 0, rerr
		}
		for i, m := range obj.members {
			if m.value == n || (m.valStart == n.start && m.valEnd == n.end) {
				beforeIdx = i
			}
		}
	}
	return afterIdx, beforeIdx, nil
}

func (d *doc) insertArrayElement(arr *jNode, before, after hew.Path, value hew.Value, valueText string, line int) (*edit, error) {
	afterIdx, beforeIdx := -1, -1
	if !after.IsZero() {
		n, rerr := d.resolveFull("", after, line)
		switch {
		case rerr != nil:
			i, ok := d.pendingIndex(arr, after)
			if !ok {
				return nil, rerr
			}
			afterIdx = i
		default:
			for i, e := range arr.elems {
				if e.value == n {
					afterIdx = i
				}
			}
		}
	}
	if afterIdx < 0 && !before.IsZero() {
		n, rerr := d.resolveFull("", before, line)
		if rerr != nil {
			return nil, rerr
		}
		for i, e := range arr.elems {
			if e.value == n {
				beforeIdx = i
			}
		}
	}
	d.note(arr, hew.Segment{}, value, afterIdx)
	return insertIntoArr(d.src, arr, afterIdx, beforeIdx, valueText), nil
}

// --- remove -----------------------------------------------------------------

func (d *doc) planRemove(target string, t hew.Transform) (*edit, error) {
	last := t.Path.Segment(t.Path.Len() - 1)
	parentPath, ok := t.Path.Parent()
	if !ok {
		return nil, appErr(hewerr.CodeInexpressible, target, t.Path.String(), t.PatchLine, "remove: cannot remove the document root")
	}
	parent, err := d.resolveFull(target, parentPath, t.PatchLine)
	if err != nil {
		if t.Optional {
			return nil, nil
		}
		return nil, err
	}
	switch {
	case parent.kind == jObj && last.Kind == hew.SegKey:
		for i, m := range parent.members {
			if m.key == last.Name {
				return removeObjMember(d.src, parent, i), nil
			}
		}
	case parent.kind == jArr && (last.Kind == hew.SegMatch || last.Kind == hew.SegIndex):
		for i, e := range parent.elems {
			if last.Kind == hew.SegIndex && i == last.Index {
				return removeArrElem(d.src, parent, i), nil
			}
			if last.Kind == hew.SegMatch && d.matchesSegMatch(e.value, last) {
				return removeArrElem(d.src, parent, i), nil
			}
		}
	}
	if t.Optional {
		return nil, nil
	}
	return nil, appErr(hewerr.CodeNoMatch, target, t.Path.String(), t.PatchLine, "remove: node does not exist")
}

// --- replace ----------------------------------------------------------------

func (d *doc) planReplace(target string, t hew.Transform) (*edit, error) {
	n, err := d.resolveFull(target, t.Path, t.PatchLine)
	if err != nil {
		if he, ok := err.(*hewerr.Error); ok && he.Code == hewerr.CodeNoMatch {
			he.Detail = "replace requires the node to exist (O26); use add for an unconditional write"
		}
		return nil, err
	}
	return &edit{start: n.start, end: n.end, text: jsonEncode(t.Value)}, nil
}

// --- JSON value encoding ----------------------------------------------------

// jsonEncode renders a hew.Value as JSON text in the corpus's compact house
// style: objects as "{ k: v, k2: v2 }" (spaced), arrays as "[v, v2]"
// (unspaced), matching json/keyed-array-add and json/array-append.
func jsonEncode(v hew.Value) string {
	n := v.Node()
	if n == nil {
		return "null"
	}
	return jsonEncodeNode(n)
}

func jsonEncodeNode(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return jsonScalarLiteral(n)
	case yaml.MappingNode:
		var parts []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			parts = append(parts, jsonQuote(n.Content[i].Value)+": "+jsonEncodeNode(n.Content[i+1]))
		}
		if len(parts) == 0 {
			return "{}"
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case yaml.SequenceNode:
		var parts []string
		for _, c := range n.Content {
			parts = append(parts, jsonEncodeNode(c))
		}
		if len(parts) == 0 {
			return "[]"
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return "null"
	}
}

func jsonScalarLiteral(n *yaml.Node) string {
	switch n.ShortTag() {
	case "!!str":
		return jsonQuote(n.Value)
	case "!!bool", "!!int", "!!float":
		return n.Value
	case "!!null":
		return "null"
	default:
		return jsonQuote(n.Value)
	}
}

func jsonQuote(s string) string { return strconv.Quote(s) }
