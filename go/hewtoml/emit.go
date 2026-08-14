package hewtoml

import (
	"errors"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file renders a transform's value as TOML source text. Nothing here
// re-emits anything the target already contains: an emitted string is only
// ever spliced into the source at the one position the plan chose, so a
// document's existing quoting, spacing and surface choices are untouched
// (§6.3).

// errInexpressible is what a value with no TOML spelling raises; the caller
// turns it into HEW020 against the transform's own path and line. errNotATable
// is the narrower complaint a `[a.b]` block makes about its own shape, kept
// distinct so the two get different diagnostics.
var (
	errInexpressible = errors.New("value has no TOML spelling")
	errNotATable     = errors.New("value is not a table")
)

// tomlValue renders a value on one line: a scalar literal, an inline array, or
// an inline table. TOML has no null, so a null value is inexpressible rather
// than silently written as something else.
func tomlValue(v *yaml.Node) (string, error) {
	if v == nil {
		return "", errInexpressible
	}
	switch v.Kind {
	case yaml.ScalarNode:
		return tomlScalar(v)
	case yaml.SequenceNode:
		parts := make([]string, 0, len(v.Content))
		for _, c := range v.Content {
			s, err := tomlValue(c)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case yaml.MappingNode:
		parts := make([]string, 0, len(v.Content)/2)
		for i := 0; i+1 < len(v.Content); i += 2 {
			s, err := tomlValue(v.Content[i+1])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKey(v.Content[i].Value)+" = "+s)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", errInexpressible
}

func tomlScalar(v *yaml.Node) (string, error) {
	switch v.ShortTag() {
	case "!!null":
		return "", errInexpressible
	case "!!bool":
		return strings.ToLower(v.Value), nil
	case "!!int", "!!float", "!!timestamp":
		return v.Value, nil
	case "!!str":
		return tomlString(v.Value), nil
	}
	return "", errInexpressible
}

// tomlPairLines renders a mapping as the body of a table: one `key = value`
// line per member. A nested container is written inline, because promoting it
// to its own `[a.b]` header would choose a surface the patch did not ask for
// (§8.4 rule 4).
func tomlPairLines(v *yaml.Node) ([]string, error) {
	if v == nil || v.Kind != yaml.MappingNode {
		return nil, errNotATable
	}
	out := make([]string, 0, len(v.Content)/2)
	for i := 0; i+1 < len(v.Content); i += 2 {
		s, err := tomlValue(v.Content[i+1])
		if err != nil {
			return nil, err
		}
		out = append(out, tomlKey(v.Content[i].Value)+" = "+s)
	}
	return out, nil
}

// tomlKey renders a key bare where TOML permits it and quoted otherwise.
func tomlKey(k string) string {
	if k == "" {
		return `""`
	}
	for i := 0; i < len(k); i++ {
		if !isBareKeyByte(k[i]) {
			return tomlString(k)
		}
	}
	return k
}

// dottedKey renders a key path as the dotted spelling an assignment uses.
func dottedKey(keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = tomlKey(k)
	}
	return strings.Join(parts, ".")
}

// tomlString renders a Go string as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteString(`\u`)
				b.WriteString(strings.ToUpper(pad4(strconv.FormatInt(int64(r), 16))))
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func pad4(s string) string {
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
