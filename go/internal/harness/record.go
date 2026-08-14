package harness

import (
	"strconv"
	"strings"

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

// recordDigestZeroed decodes a record.hewt-shaped document and replaces
// every dotted field path in fields (e.g. "patch.digest",
// "targets.0.before") with a fixed placeholder, so two records that differ
// only in recomputed sha256 digests compare equal (spec §9.7,
// record_digest_fields).
func recordDigestZeroed(src []byte, fields []string) (any, error) {
	var v any
	if err := yaml.Unmarshal(src, &v); err != nil {
		return nil, err
	}
	for _, f := range fields {
		zeroPath(v, strings.Split(f, "."))
	}
	return v, nil
}

const digestPlaceholder = "<digest>"

func zeroPath(v any, parts []string) {
	if len(parts) == 0 {
		return
	}
	switch n := v.(type) {
	case map[string]any:
		if len(parts) == 1 {
			if _, ok := n[parts[0]]; ok {
				n[parts[0]] = digestPlaceholder
			}
			return
		}
		if child, ok := n[parts[0]]; ok {
			zeroPath(child, parts[1:])
		}
	case []any:
		idx, err := strconv.Atoi(parts[0])
		if err != nil || idx < 0 || idx >= len(n) {
			return
		}
		if len(parts) == 1 {
			n[idx] = digestPlaceholder
			return
		}
		zeroPath(n[idx], parts[1:])
	}
}
