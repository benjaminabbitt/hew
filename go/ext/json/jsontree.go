// Package json is hew's JSON format binding (§8.1): a byte-preserving
// applier over a hand-rolled JSON tree that records each value's source byte
// span, so an edit touches only the bytes it must (§6.3's byte-preservation
// contract) and every other member keeps its exact original bytes,
// indentation, and numeric literal form.
package json

import (
	"fmt"
	"strconv"
)

type jKind int

const (
	jObj jKind = iota
	jArr
	jStr
	jNum
	jBool
	jNull
)

// jNode is one parsed JSON value with its exact source span.
type jNode struct {
	kind       jKind
	start, end int // byte span of the value itself, delimiters included
	members    []jMember
	elems      []jElem
	raw        string // scalar: exact source text (preserves numeric literal form)
}

type jMember struct {
	keyStart, keyEnd int // span of the quoted key
	key              string
	valStart, valEnd int
	value            *jNode
	commaPos         int // position of this member's own trailing comma, or -1
}

type jElem struct {
	valStart, valEnd int
	value            *jNode
	commaPos         int
}

type jsonErr struct {
	pos int
	msg string
}

func (e *jsonErr) Error() string { return fmt.Sprintf("offset %d: %s", e.pos, e.msg) }

// parseJSON parses a complete JSON document. Errors are *jsonErr.
func parseJSON(src []byte) (*jNode, error) {
	n, pos, err := parseValue(src, skipWS(src, 0))
	if err != nil {
		return nil, err
	}
	pos = skipWS(src, pos)
	if pos != len(src) {
		return nil, &jsonErr{pos, "trailing content after top-level value"}
	}
	return n, nil
}

func skipWS(src []byte, pos int) int {
	for pos < len(src) {
		switch src[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			return pos
		}
	}
	return pos
}

func parseValue(src []byte, pos int) (*jNode, int, error) {
	if pos >= len(src) {
		return nil, pos, &jsonErr{pos, "unexpected end of input"}
	}
	switch src[pos] {
	case '{':
		return parseObject(src, pos)
	case '[':
		return parseArray(src, pos)
	case '"':
		end, err := skipString(src, pos)
		if err != nil {
			return nil, pos, err
		}
		return &jNode{kind: jStr, start: pos, end: end, raw: string(src[pos:end])}, end, nil
	case 't':
		return litNode(src, pos, "true", jBool)
	case 'f':
		return litNode(src, pos, "false", jBool)
	case 'n':
		return litNode(src, pos, "null", jNull)
	default:
		if src[pos] == '-' || (src[pos] >= '0' && src[pos] <= '9') {
			end := skipNumber(src, pos)
			if end == pos {
				return nil, pos, &jsonErr{pos, "malformed number"}
			}
			return &jNode{kind: jNum, start: pos, end: end, raw: string(src[pos:end])}, end, nil
		}
		return nil, pos, &jsonErr{pos, fmt.Sprintf("unexpected character %q", src[pos])}
	}
}

func litNode(src []byte, pos int, lit string, kind jKind) (*jNode, int, error) {
	if pos+len(lit) > len(src) || string(src[pos:pos+len(lit)]) != lit {
		return nil, pos, &jsonErr{pos, fmt.Sprintf("expected %q", lit)}
	}
	end := pos + len(lit)
	return &jNode{kind: kind, start: pos, end: end, raw: lit}, end, nil
}

func skipString(src []byte, pos int) (int, error) {
	if src[pos] != '"' {
		return pos, &jsonErr{pos, "expected string"}
	}
	i := pos + 1
	for i < len(src) {
		switch src[i] {
		case '"':
			return i + 1, nil
		case '\\':
			i += 2
		default:
			i++
		}
	}
	return pos, &jsonErr{pos, "unterminated string"}
}

func skipNumber(src []byte, pos int) int {
	i := pos
	if i < len(src) && src[i] == '-' {
		i++
	}
	for i < len(src) && src[i] >= '0' && src[i] <= '9' {
		i++
	}
	if i < len(src) && src[i] == '.' {
		i++
		for i < len(src) && src[i] >= '0' && src[i] <= '9' {
			i++
		}
	}
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		i++
		if i < len(src) && (src[i] == '+' || src[i] == '-') {
			i++
		}
		for i < len(src) && src[i] >= '0' && src[i] <= '9' {
			i++
		}
	}
	return i
}

func decodeString(raw string) (string, error) {
	s, err := strconv.Unquote(raw)
	if err != nil {
		return "", &jsonErr{0, "malformed string " + raw}
	}
	return s, nil
}

func parseObject(src []byte, pos int) (*jNode, int, error) {
	start := pos
	pos++ // '{'
	n := &jNode{kind: jObj, start: start}
	pos = skipWS(src, pos)
	if pos < len(src) && src[pos] == '}' {
		n.end = pos + 1
		return n, pos + 1, nil
	}
	for {
		pos = skipWS(src, pos)
		if pos >= len(src) || src[pos] != '"' {
			return nil, pos, &jsonErr{pos, "expected object key"}
		}
		keyStart := pos
		keyEnd, err := skipString(src, pos)
		if err != nil {
			return nil, pos, err
		}
		key, err := decodeString(string(src[keyStart:keyEnd]))
		if err != nil {
			return nil, keyStart, err
		}
		pos = skipWS(src, keyEnd)
		if pos >= len(src) || src[pos] != ':' {
			return nil, pos, &jsonErr{pos, "expected ':'"}
		}
		pos = skipWS(src, pos+1)
		val, next, err := parseValue(src, pos)
		if err != nil {
			return nil, pos, err
		}
		m := jMember{keyStart: keyStart, keyEnd: keyEnd, key: key, valStart: pos, valEnd: next, value: val, commaPos: -1}
		pos = skipWS(src, next)
		if pos < len(src) && src[pos] == ',' {
			m.commaPos = pos
			pos = skipWS(src, pos+1)
			n.members = append(n.members, m)
			continue
		}
		n.members = append(n.members, m)
		if pos < len(src) && src[pos] == '}' {
			n.end = pos + 1
			return n, pos + 1, nil
		}
		return nil, pos, &jsonErr{pos, "expected ',' or '}'"}
	}
}

func parseArray(src []byte, pos int) (*jNode, int, error) {
	start := pos
	pos++ // '['
	n := &jNode{kind: jArr, start: start}
	pos = skipWS(src, pos)
	if pos < len(src) && src[pos] == ']' {
		n.end = pos + 1
		return n, pos + 1, nil
	}
	for {
		pos = skipWS(src, pos)
		val, next, err := parseValue(src, pos)
		if err != nil {
			return nil, pos, err
		}
		e := jElem{valStart: pos, valEnd: next, value: val, commaPos: -1}
		pos = skipWS(src, next)
		if pos < len(src) && src[pos] == ',' {
			e.commaPos = pos
			pos = skipWS(src, pos+1)
			n.elems = append(n.elems, e)
			continue
		}
		n.elems = append(n.elems, e)
		if pos < len(src) && src[pos] == ']' {
			n.end = pos + 1
			return n, pos + 1, nil
		}
		return nil, pos, &jsonErr{pos, "expected ',' or ']'"}
	}
}

// lineOf is the 1-based line the byte at off sits on, for §10.3's `found`
// line. Zero for an offset outside the source.
func lineOf(src []byte, off int) int {
	if off < 0 || off > len(src) {
		return 0
	}
	n := 1
	for _, c := range src[:off] {
		if c == '\n' {
			n++
		}
	}
	return n
}
