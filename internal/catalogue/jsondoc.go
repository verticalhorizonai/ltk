package catalogue

// Strict JSON documents and RFC 6901 JSON pointers. The catalogue check reads the catalogue as strict JSON and the
// comparator reads two decoded documents leaf by leaf; both read through Parse, which keeps every value's own text and
// every object's key order, so a number is compared as its decimal text and never loses precision.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kind is the JSON kind of a Node.
type Kind int

// The kinds of a JSON value.
const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Node is one JSON value of a document parsed by Parse.
type Node struct {
	// Kind is the value's JSON kind.
	Kind Kind
	// Raw is the value's own text in the document, as written.
	Raw json.RawMessage
	// Text is a scalar's value: a string decoded, a number's literal, "true", "false" or "null".
	Text string
	// Keys are an object's keys in document order; Values[i] is the value of Keys[i].
	Keys   []string
	Values []*Node
	// Elems are an array's elements.
	Elems []*Node
}

// Member returns an object's value for key, or nil.
func (n *Node) Member(key string) *Node {
	if n == nil || n.Kind != KindObject {
		return nil
	}
	for i, k := range n.Keys {
		if k == key {
			return n.Values[i]
		}
	}
	return nil
}

// JSONError is a document that is not strict RFC 8259 JSON: a byte-order mark, bytes that are not UTF-8, invalid
// syntax, an object holding one key twice, or data after the document. Pointer is where the problem is: "" for the
// document as a whole, or the object holding a duplicate key's pointer to that key.
type JSONError struct {
	Pointer string
	Reason  string
}

func (e *JSONError) Error() string { return e.Reason }

// Parse reads b as one strict JSON document: RFC 8259 JSON in UTF-8 without a byte-order mark.
// The error is a *JSONError.
func Parse(b []byte) (*Node, error) {
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		return nil, &JSONError{Reason: "the document starts with a byte-order mark; JSON here is UTF-8 without BOM"}
	}
	if !utf8.Valid(b) {
		return nil, &JSONError{Reason: "the document is not valid UTF-8"}
	}
	p := &parser{src: b, dec: json.NewDecoder(bytes.NewReader(b))}
	p.dec.UseNumber()
	root, err := p.value("")
	if err != nil {
		return nil, err
	}
	if _, err := p.dec.Token(); err != io.EOF {
		return nil, &JSONError{Reason: fmt.Sprintf("data after the JSON document at byte %d", p.dec.InputOffset())}
	}
	return root, nil
}

type parser struct {
	src []byte
	dec *json.Decoder
}

func (p *parser) syntax(err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return &JSONError{Reason: fmt.Sprintf("not valid JSON: %v (at byte %d)", se, se.Offset)}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &JSONError{Reason: "not valid JSON: the document ends early"}
	}
	return &JSONError{Reason: "not valid JSON: " + err.Error()}
}

// value reads the next value, whose pointer is ptr.
func (p *parser) value(ptr string) (*Node, error) {
	start := p.dec.InputOffset()
	tok, err := p.dec.Token()
	if err != nil {
		return nil, p.syntax(err)
	}
	n := &Node{}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n.Kind = KindObject
			n.Keys, n.Values = []string{}, []*Node{}
			seen := map[string]bool{}
			for p.dec.More() {
				kt, err := p.dec.Token()
				if err != nil {
					return nil, p.syntax(err)
				}
				key, _ := kt.(string)
				child := ptr + "/" + EscapeToken(key)
				if seen[key] {
					return nil, &JSONError{Pointer: child, Reason: fmt.Sprintf("the object at %q holds the key %q twice", ptr, key)}
				}
				seen[key] = true
				v, err := p.value(child)
				if err != nil {
					return nil, err
				}
				n.Keys, n.Values = append(n.Keys, key), append(n.Values, v)
			}
		case '[':
			n.Kind = KindArray
			n.Elems = []*Node{}
			for i := 0; p.dec.More(); i++ {
				v, err := p.value(ptr + "/" + strconv.Itoa(i))
				if err != nil {
					return nil, err
				}
				n.Elems = append(n.Elems, v)
			}
		}
		if _, err := p.dec.Token(); err != nil { // the closing delimiter
			return nil, p.syntax(err)
		}
	case string:
		n.Kind, n.Text = KindString, t
	case json.Number:
		n.Kind, n.Text = KindNumber, t.String()
	case bool:
		n.Kind, n.Text = KindBool, strconv.FormatBool(t)
	case nil:
		n.Kind, n.Text = KindNull, "null"
	}
	n.Raw = json.RawMessage(bytes.TrimLeft(p.src[start:p.dec.InputOffset()], " \t\r\n,:"))
	return n, nil
}

// Equal reports whether a and b are the same JSON value: the same kind, strings equal once decoded, numbers equal as
// their decimal text (so a 64-bit value never loses precision), objects with the same keys holding equal
// values in any order, arrays element by element.
func Equal(a, b *Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindArray:
		if len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !Equal(a.Elems[i], b.Elems[i]) {
				return false
			}
		}
		return true
	case KindObject:
		if len(a.Keys) != len(b.Keys) {
			return false
		}
		for i, k := range a.Keys {
			if !Equal(a.Values[i], b.Member(k)) {
				return false
			}
		}
		return true
	}
	return a.Text == b.Text
}

// FirstDifference returns the JSON pointer of the first place, in a's document order, at which a and b are not the same
// JSON value as Equal reads them, and false when they are equal (`ltk check` holds an entry's decoded JSON file to the
// Rust XDR tool's fresh decoding with it). Objects with the same set of keys and arrays of the same length are walked
// into; anywhere else (another kind, another key set, another array length, a scalar that differs) the pointer of that
// value is the difference, as `ltk compare` reports a difference in shape. The root is "".
func FirstDifference(a, b *Node) (string, bool) {
	return firstDifference(a, b, "")
}

func firstDifference(a, b *Node, ptr string) (string, bool) {
	if Equal(a, b) {
		return "", false
	}
	if a == nil || b == nil {
		return ptr, true
	}
	switch {
	case a.Kind == KindObject && b.Kind == KindObject && sameKeys(a, b):
		for i, k := range a.Keys {
			if p, ok := firstDifference(a.Values[i], b.Member(k), ptr+"/"+EscapeToken(k)); ok {
				return p, true
			}
		}
	case a.Kind == KindArray && b.Kind == KindArray && len(a.Elems) == len(b.Elems):
		for i := range a.Elems {
			if p, ok := firstDifference(a.Elems[i], b.Elems[i], ptr+"/"+strconv.Itoa(i)); ok {
				return p, true
			}
		}
	}
	return ptr, true
}

// EscapeToken escapes one reference token of a JSON pointer: "~" as "~0", then "/" as "~1" (RFC 6901).
func EscapeToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// Resolve returns the value the RFC 6901 JSON pointer names in the document whose root is n, and false when it names
// nothing: a token that is not a key of its object, an array index that is not a decimal number without leading zeros
// or is out of range (`-` included), a step into a scalar, or a pointer that does not start with "/". The root "" names
// n itself. A pointer that names a JSON null returns that null node and true, so a value recorded as null is told apart
// from a pointer that resolves to nothing (the expected-values check of `ltk check`).
func (n *Node) Resolve(pointer string) (*Node, bool) {
	if n == nil {
		return nil, false
	}
	if pointer == "" {
		return n, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	cur := n
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch cur.Kind {
		case KindObject:
			next := cur.Member(tok)
			if next == nil {
				return nil, false
			}
			cur = next
		case KindArray:
			i, ok := arrayIndex(tok)
			if !ok || i >= len(cur.Elems) {
				return nil, false
			}
			cur = cur.Elems[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// arrayIndex reads an array-index token of RFC 6901: "0", or decimal digits with no leading zero.
func arrayIndex(tok string) (int, bool) {
	if tok == "" || (len(tok) > 1 && tok[0] == '0') {
		return 0, false
	}
	for _, c := range tok {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	i, err := strconv.Atoi(tok)
	return i, err == nil
}

// IsPointerPrefix reports whether prefix equals pointer or names one of its ancestors: the prefix ends on a segment
// boundary of pointer, so "/a/b" is a prefix of "/a/b/c" and not of "/a/bc" (how `ltk compare` attributes a
// difference to a ledger-dependent field). The root "" is a prefix of every pointer.
func IsPointerPrefix(prefix, pointer string) bool {
	return pointer == prefix || strings.HasPrefix(pointer, prefix+"/")
}
