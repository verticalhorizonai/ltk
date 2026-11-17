package check

// The round trip with a codec double in place of Stellar's Rust XDR tool: the tool itself runs in the integration tier
// (cmd/ltk/check_integration_test.go).

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// doubleCodec is a codec double: decode and encode answer from functions; every call is recorded as "decode <type>"
// or "encode <type>".
type doubleCodec struct {
	decode func(raw []byte) ([]byte, error)
	encode func(jsonForm []byte) ([]byte, error)
	calls  []string
}

func (c *doubleCodec) Decode(_ context.Context, xdrType string, raw []byte) ([]byte, error) {
	c.calls = append(c.calls, "decode "+xdrType)
	return c.decode(raw)
}

func (c *doubleCodec) Encode(_ context.Context, xdrType string, jsonForm []byte) ([]byte, error) {
	c.calls = append(c.calls, "encode "+xdrType)
	return c.encode(jsonForm)
}

// stringCodec decodes bytes to a JSON string holding them and encodes it back, changed by alter when set.
func stringCodec(alter func([]byte) []byte) *doubleCodec {
	return &doubleCodec{
		decode: func(raw []byte) ([]byte, error) { return []byte(`"` + string(raw) + `"`), nil },
		encode: func(j []byte) ([]byte, error) {
			b := []byte(strings.Trim(string(j), `"`))
			if alter != nil {
				b = alter(b)
			}
			return b, nil
		},
	}
}

var rtEntry = contract.CatalogueEntry{ID: "cap-x", XdrType: "LedgerCloseMeta", FilePath: "cap-x/entry.xdr"}

// TestRoundTripPasses: the file is decoded then re-encoded as its declared type; equal bytes pass and bytesCompared is
// the file's length; the decoded form is kept for the expected values.
func TestRoundTripPasses(t *testing.T) {
	c := stringCodec(nil)
	out, err := RoundTrip(context.Background(), c, rtEntry, []byte("abcdef"), nil, []byte(`"abcdef"`), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := contract.RoundTripResult{Check: contract.CheckNameRoundTrip, Passed: true, BytesCompared: 6}
	if out.Result != want || out.Reason != "" || string(out.Decoded) != `"abcdef"` {
		t.Errorf("%+v", out)
	}
	if strings.Join(c.calls, ",") != "decode LedgerCloseMeta,encode LedgerCloseMeta" {
		t.Errorf("calls %v: decode then encode, as the entry's xdrType", c.calls)
	}
}

// TestRoundTripByteDifferences: bytes that differ anywhere fail the check, which names the entry; bytesCompared is the
// shorter length; the first differing offset is in the reason only.
func TestRoundTripByteDifferences(t *testing.T) {
	file := []byte("abcdefgh")
	cases := []struct {
		name      string
		alter     func([]byte) []byte
		compared  int
		inMessage string
	}{
		{"one byte flipped", func(b []byte) []byte { b[5] ^= 1; return b }, 8, "at offset 5 (the file is 8 bytes, the re-encoding 8)"},
		{"the last byte flipped", func(b []byte) []byte { b[7] = 'X'; return b }, 8, "at offset 7"},
		{"re-encoding shorter", func(b []byte) []byte { return b[:6] }, 6, "at offset 6 (the file is 8 bytes, the re-encoding 6)"},
		{"re-encoding longer", func(b []byte) []byte { return append(b, 0) }, 8, "at offset 8 (the file is 8 bytes, the re-encoding 9)"},
		{"re-encoding empty", func([]byte) []byte { return nil }, 0, "at offset 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := RoundTrip(context.Background(), stringCodec(c.alter), rtEntry, append([]byte(nil), file...), nil, []byte(`"abcdefgh"`), nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Result.Passed || out.Result.BytesCompared != c.compared || out.Result.Check != contract.CheckNameRoundTrip {
				t.Errorf("result %+v, want failed with bytesCompared %d", out.Result, c.compared)
			}
			if !strings.HasPrefix(out.Reason, "entry cap-x: round_trip: ") || !strings.Contains(out.Reason, c.inMessage) {
				t.Errorf("reason %q, want the entry named and %q", out.Reason, c.inMessage)
			}
		})
	}
}

// TestRoundTripFailureKinds: an unreadable file, a file the tool cannot decode as its type, and a decoded form that
// does not re-encode are each a failed check naming the entry, with bytesCompared 0.
func TestRoundTripFailureKinds(t *testing.T) {
	toolErr := errors.New("stellar-xdr decode --type LedgerCloseMeta failed: error: xdr value invalid")
	decodeFails := &doubleCodec{decode: func([]byte) ([]byte, error) { return nil, toolErr }}
	encodeFails := &doubleCodec{
		decode: func([]byte) ([]byte, error) { return []byte(`{"v":1}`), nil },
		encode: func([]byte) ([]byte, error) { return nil, errors.New("stellar-xdr encode failed") },
	}
	cases := []struct {
		name      string
		codec     *doubleCodec
		readErr   error
		inMessage string
		decoded   bool
		calls     int
	}{
		{"unreadable file", stringCodec(nil), errors.New("no such file"), "cannot be read, so nothing was decoded", false, 0},
		{"not decodable", decodeFails, nil, "is not decodable as LedgerCloseMeta: stellar-xdr decode", false, 1},
		{"does not re-encode", encodeFails, nil, "does not re-encode as LedgerCloseMeta: stellar-xdr encode failed", true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := RoundTrip(context.Background(), c.codec, rtEntry, []byte("abc"), c.readErr, []byte(`{"v":1}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Result.Passed || out.Result.BytesCompared != 0 {
				t.Errorf("result %+v, want failed with bytesCompared 0", out.Result)
			}
			if !strings.HasPrefix(out.Reason, "entry cap-x: round_trip: ") || !strings.Contains(out.Reason, c.inMessage) {
				t.Errorf("reason %q, want %q", out.Reason, c.inMessage)
			}
			if (out.Decoded != nil) != c.decoded || len(c.codec.calls) != c.calls {
				t.Errorf("decoded %q, calls %v", out.Decoded, c.codec.calls)
			}
		})
	}
}

// TestRoundTripStops: an interrupt (the context cancelled) and an unavailable decoder stop the command; they are not an
// entry's failed check.
func TestRoundTripStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cut := &doubleCodec{decode: func([]byte) ([]byte, error) { return nil, errors.New("signal: killed") }}
	if _, err := RoundTrip(ctx, cut, rtEntry, []byte("a"), nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("an interrupted decode: %v, want context.Canceled", err)
	}
	gone := &doubleCodec{decode: func([]byte) ([]byte, error) {
		return nil, &UnavailableError{Err: errors.New("docker run: no space left")}
	}}
	var u *UnavailableError
	if _, err := RoundTrip(context.Background(), gone, rtEntry, []byte("a"), nil, nil, nil); !errors.As(err, &u) || !strings.Contains(err.Error(), "Rust XDR tool is not available") {
		t.Errorf("an unavailable decoder: %v", err)
	}
}

// TestRoundTripDecodedJSON: the tool's fresh decoding of the file must equal the decoded JSON file beside it as JSON
// values (key order and white space aside, numbers as their decimal text). A difference, a missing file or one that is
// not JSON fails the check, naming the entry, the file and, for a difference, its pointer; bytesCompared is the byte
// comparison's, unchanged by this condition.
func TestRoundTripDecodedJSON(t *testing.T) {
	const decoded = `{"a":{"b":1,"c":"x"},"d":[1,2]}`
	jsonCodec := func(alter func([]byte) []byte) *doubleCodec {
		return &doubleCodec{
			decode: func([]byte) ([]byte, error) { return []byte(decoded), nil },
			encode: func([]byte) ([]byte, error) {
				b := []byte("abcdef")
				if alter != nil {
					b = alter(b)
				}
				return b, nil
			},
		}
	}
	const prefix = "entry cap-x: round_trip: "
	cases := []struct {
		name      string
		entryJSON []byte
		jsonErr   error
		alter     func([]byte) []byte
		passed    bool
		inMessage []string
	}{
		{"equal as written", []byte(decoded), nil, nil, true, nil},
		{"reformatted but equal", []byte("{\n  \"d\": [1, 2],\n  \"a\": {\"c\": \"x\", \"b\": 1}\n}\n"), nil, nil, true, nil},
		{"one leaf edited", []byte(`{"a":{"b":1,"c":"y"},"d":[1,2]}`), nil, nil, false,
			[]string{`the decoded JSON file cap-x/entry.json differs from the Rust XDR tool's decoding of cap-x/entry.xdr at "/a/c": the file holds "y", the decoding "x"`}},
		{"a number with other decimal text", []byte(`{"a":{"b":1.0,"c":"x"},"d":[1,2]}`), nil, nil, false, []string{`at "/a/b": the file holds 1.0, the decoding 1`}},
		{"the file missing", nil, fs.ErrNotExist, nil, false, []string{"the decoded JSON file cap-x/entry.json cannot be read: file does not exist"}},
		{"the file not JSON", []byte(`{"a":`), nil, nil, false, []string{"the decoded JSON file cap-x/entry.json is not JSON: "}},
		{"the bytes and the JSON differ", []byte(`{"a":{"b":1,"c":"y"},"d":[1,2]}`), nil, func(b []byte) []byte { b[2] = 'X'; return b }, false,
			[]string{"the re-encoded bytes differ from the file cap-x/entry.xdr at offset 2", `; the decoded JSON file cap-x/entry.json differs from the Rust XDR tool's decoding of cap-x/entry.xdr at "/a/c"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := RoundTrip(context.Background(), jsonCodec(c.alter), rtEntry, []byte("abcdef"), nil, c.entryJSON, c.jsonErr)
			if err != nil {
				t.Fatal(err)
			}
			if out.Result.Passed != c.passed || out.Result.BytesCompared != 6 || out.Result.Check != contract.CheckNameRoundTrip {
				t.Errorf("result %+v, want passed %v with bytesCompared 6", out.Result, c.passed)
			}
			if string(out.Decoded) != decoded {
				t.Errorf("decoded %s: the tool's decoding is kept for the expected values", out.Decoded)
			}
			if c.passed {
				if out.Reason != "" {
					t.Errorf("reason %q, want none", out.Reason)
				}
				return
			}
			if !strings.HasPrefix(out.Reason, prefix) {
				t.Errorf("reason %q does not name the entry and the check", out.Reason)
			}
			for _, m := range c.inMessage {
				if !strings.Contains(out.Reason, m) {
					t.Errorf("reason %q\nwant it to hold %q", out.Reason, m)
				}
			}
		})
	}
}
