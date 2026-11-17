package catalogue

// Strict JSON and JSON pointers, with their edge inputs.

import (
	"errors"
	"strings"
	"testing"
)

func TestParseStrict(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		pointer string
		reason  string
	}{
		{"a byte-order mark", "\xEF\xBB\xBF{}", "", "byte-order mark"},
		{"not UTF-8", "{\"a\":\"\xff\"}", "", "not valid UTF-8"},
		{"invalid syntax", `{"a":}`, "", "not valid JSON"},
		{"a trailing comma", `{"a":1,}`, "", "not valid JSON"},
		{"ends early", `{"a":[1,2`, "", "ends early"},
		{"trailing data", `{"a":1} {"b":2}`, "", "data after the JSON document"},
		{"trailing garbage", `{"a":1}x`, "", ""},
		{"a duplicate key", `{"entries":[{"id":"x","id":"y"}]}`, "/entries/0/id", `holds the key "id" twice`},
		{"a duplicate escaped key", `{"a/b":1,"a\/b":2}`, "/a~1b", "twice"},
		{"empty input", ``, "", "not valid JSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.in))
			var je *JSONError
			if !errors.As(err, &je) {
				t.Fatalf("Parse(%q) = %v, want a *JSONError", c.in, err)
			}
			if je.Pointer != c.pointer {
				t.Errorf("pointer %q, want %q", je.Pointer, c.pointer)
			}
			if !strings.Contains(je.Reason, c.reason) {
				t.Errorf("reason %q does not contain %q", je.Reason, c.reason)
			}
		})
	}
	for _, ok := range []string{`{}`, `[]`, `null`, `0`, " \n{\"a\": [1, {\"b\": null}]}\n ", `"x"`} {
		if _, err := Parse([]byte(ok)); err != nil {
			t.Errorf("Parse(%q): %v", ok, err)
		}
	}
}

func TestParseKeepsOrderAndText(t *testing.T) {
	n, err := Parse([]byte(`{"z": 1, "a": {"y": 18446744073709551615, "b": 1.50}, "m": ["A", true]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(n.Keys, ","); got != "z,a,m" {
		t.Errorf("key order %q", got)
	}
	a := n.Member("a")
	if a.Kind != KindObject || string(a.Raw) != `{"y": 18446744073709551615, "b": 1.50}` {
		t.Errorf("raw text of /a: %q", a.Raw)
	}
	if y := a.Member("y"); y.Kind != KindNumber || y.Text != "18446744073709551615" {
		t.Errorf("a 64-bit number keeps its decimal text: %+v", y)
	}
	if b := a.Member("b"); b.Text != "1.50" {
		t.Errorf("a number keeps its literal: %q", b.Text)
	}
	if m := n.Member("m"); m.Elems[0].Text != "A" || m.Elems[1].Kind != KindBool || string(m.Elems[0].Raw) != `"A"` {
		t.Errorf("array: %+v", m.Elems)
	}
	if n.Member("nope") != nil || a.Member("y").Member("x") != nil {
		t.Error("Member of an absent key or of a scalar must be nil")
	}
}

func TestEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`"A"`, `"A"`, true},
		{`9223372036854775807`, `9223372036854775807`, true},
		{`9223372036854775807`, `9223372036854775806`, false}, // equal as float64; different as decimal text
		{`1`, `1.0`, false},
		{`"1"`, `1`, false},
		{`null`, `null`, true},
		{`null`, `false`, false},
		{`{"a":1,"b":[1,2]}`, `{"b":[1,2],"a":1}`, true},
		{`{"a":1}`, `{"a":1,"b":2}`, false},
		{`[1,2]`, `[2,1]`, false},
		{`[]`, `{}`, false},
		{`{}`, `{}`, true},
	}
	for _, c := range cases {
		a, _ := Parse([]byte(c.a))
		b, _ := Parse([]byte(c.b))
		if got := Equal(a, b); got != c.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if !Equal(nil, nil) || Equal(nil, &Node{}) {
		t.Error("Equal with nil")
	}
}

func TestPointers(t *testing.T) {
	if got := EscapeToken("a/b~c"); got != "a~1b~0c" {
		t.Errorf("EscapeToken: %q", got)
	}
	if got := EscapeToken("~1"); got != "~01" {
		t.Errorf("EscapeToken(~1): %q, want ~01 (the ~ is escaped first)", got)
	}
	cases := []struct {
		prefix, pointer string
		want            bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/bc", false},
		{"/a/b/c", "/a/b", false},
		{"", "/a", true},
		{"", "", true},
		{"/a~1b", "/a~1b/0", true},
		{"/a", "/a~1b", false},
		{"/0", "/01", false},
	}
	for _, c := range cases {
		if got := IsPointerPrefix(c.prefix, c.pointer); got != c.want {
			t.Errorf("IsPointerPrefix(%q, %q) = %v, want %v", c.prefix, c.pointer, got, c.want)
		}
	}
}

// TestResolve (RFC 6901; the expected-values check): a pointer names a value or nothing; a JSON null that is there is
// told apart from a pointer that resolves to nothing; "~1" and "~0" are unescaped; array indexes are decimal with no
// leading zero, "-" names nothing.
func TestResolve(t *testing.T) {
	doc, err := Parse([]byte(`{"a":{"b":[10,{"c":null}],"x/y":1,"m~n":2,"":3},"s":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		pointer string
		ok      bool
		text    string
	}{
		{"", true, ""},
		{"/a/b/0", true, "10"},
		{"/a/b/1/c", true, "null"},
		{"/a/x~1y", true, "1"},
		{"/a/m~0n", true, "2"},
		{"/a/", true, "3"},
		{"/s", true, "t"},
		{"/a/b/2", false, ""},
		{"/a/b/-", false, ""},
		{"/a/b/01", false, ""},
		{"/a/b/+1", false, ""},
		{"/a/b/1/d", false, ""},
		{"/s/0", false, ""},
		{"/missing", false, ""},
		{"a", false, ""},
	}
	for _, c := range cases {
		n, ok := doc.Resolve(c.pointer)
		if ok != c.ok {
			t.Errorf("%q: resolved %v, want %v", c.pointer, ok, c.ok)
			continue
		}
		if ok && c.pointer != "" && n.Text != c.text {
			t.Errorf("%q: %q, want %q", c.pointer, n.Text, c.text)
		}
	}
	if n, ok := doc.Resolve("/a/b/1/c"); !ok || n.Kind != KindNull {
		t.Error("a null that is there resolves, as a null")
	}
	var none *Node
	if _, ok := none.Resolve(""); ok {
		t.Error("no document resolves nothing")
	}
}

// TestFirstDifference: the pointer of the first place two documents differ as JSON values, in the first document's
// order; a difference in shape is reported at its own pointer; key order and white space are no difference, and numbers
// compare as their decimal text.
func TestFirstDifference(t *testing.T) {
	const base = `{"a":{"b":1,"c":"x"},"d":[1,{"e/f":true}],"g":null}`
	cases := []struct {
		name, other, want string
		differs           bool
	}{
		{"equal", base, "", false},
		{"reordered and re-spaced", "{\n  \"g\": null,\n  \"d\": [1, {\"e/f\": true}],\n  \"a\": {\"c\": \"x\", \"b\": 1}\n}", "", false},
		{"a leaf edited", `{"a":{"b":1,"c":"y"},"d":[1,{"e/f":true}],"g":null}`, "/a/c", true},
		{"a number with other decimal text", `{"a":{"b":1.0,"c":"x"},"d":[1,{"e/f":true}],"g":null}`, "/a/b", true},
		{"an escaped key's value", `{"a":{"b":1,"c":"x"},"d":[1,{"e/f":false}],"g":null}`, "/d/1/e~1f", true},
		{"an array of another length", `{"a":{"b":1,"c":"x"},"d":[1],"g":null}`, "/d", true},
		{"another key set", `{"a":{"b":1},"d":[1,{"e/f":true}],"g":null}`, "/a", true},
		{"another kind", `{"a":{"b":1,"c":"x"},"d":[1,{"e/f":true}],"g":0}`, "/g", true},
		{"the root", `[]`, "", true},
	}
	a, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := Parse([]byte(c.other))
			if err != nil {
				t.Fatal(err)
			}
			got, differs := FirstDifference(a, b)
			if got != c.want || differs != c.differs {
				t.Errorf("FirstDifference = %q %v, want %q %v", got, differs, c.want, c.differs)
			}
		})
	}
}
