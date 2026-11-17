package check

// The expected values: both groups, the reproduced values and the ledger-dependent ones, read again from the decoded
// form and compared with the catalogue.

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// evEntry is an entry recording the given values: reproduced pointer=value pairs, then ledger-dependent ones.
func evEntry(reproduced, ledger map[string]string) contract.CatalogueEntry {
	e := contract.CatalogueEntry{ID: "cap-x"}
	e.ExpectedDecodedValues.ReproducedByEveryRun = []contract.ReproducedValue{}
	e.ExpectedDecodedValues.LedgerDependent = []contract.LedgerDependentValue{}
	for _, p := range sortedKeys(reproduced) {
		e.ExpectedDecodedValues.ReproducedByEveryRun = append(e.ExpectedDecodedValues.ReproducedByEveryRun,
			contract.ReproducedValue{Pointer: p, Value: json.RawMessage(reproduced[p])})
	}
	for _, p := range sortedKeys(ledger) {
		e.ExpectedDecodedValues.LedgerDependent = append(e.ExpectedDecodedValues.LedgerDependent,
			contract.LedgerDependentValue{Field: contract.LedgerDependentFieldLedgerNumber, Pointer: p, Value: json.RawMessage(ledger[p])})
	}
	return e
}

func sortedKeys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }

const evDoc = `{"v2":{"header":{"ledger_seq":17,"close_time":"1794821726","big":18446744073709551615,"fee":1.50},
"op":{"result":"success","flags":null,"signer":{"key":"PA","weight":1},"list":[1,"a",{"x":true}]}}}`

// TestExpectedValuesMatch: values of both groups read again from the decoded form; equal by JSON value equality (object
// keys in any order, numbers by their decimal text, a recorded null matched by a null that is there).
func TestExpectedValuesMatch(t *testing.T) {
	e := evEntry(map[string]string{
		"/v2/op/result": `"success"`,
		"/v2/op/signer": `{ "weight": 1, "key": "PA" }`,
		"/v2/op/list":   `[1, "a", {"x": true}]`,
		"/v2/op/flags":  `null`,
	}, map[string]string{
		"/v2/header/ledger_seq": `17`,
		"/v2/header/close_time": `"1794821726"`,
		"/v2/header/big":        `18446744073709551615`,
		"/v2/header/fee":        `1.50`,
	})
	r, reason := ExpectedValues(e, []byte(evDoc))
	if !r.Passed || len(r.Mismatches) != 0 || r.Mismatches == nil || reason != "" || r.Check != contract.CheckNameExpectedValues {
		t.Errorf("%+v %q", r, reason)
	}
}

// TestExpectedValuesMismatches: an unequal value in either group is a ValueMismatch with its pointer, the recorded
// value and the decoded value; the check fails and names the entry.
func TestExpectedValuesMismatches(t *testing.T) {
	cases := []struct {
		name              string
		reproduced        map[string]string
		ledger            map[string]string
		pointer, recorded string
		decoded           string
		inReason          string
	}{
		{"a reproduced value", map[string]string{"/v2/op/result": `"malformed"`}, nil,
			"/v2/op/result", `"malformed"`, `"success"`, `at /v2/op/result the decoded form holds "success"; the catalogue records "malformed"`},
		{"a ledger-dependent value", nil, map[string]string{"/v2/header/ledger_seq": `18`},
			"/v2/header/ledger_seq", `18`, `17`, "1 of 1 expected values differ"},
		{"a 64-bit number one apart (decimal text)", nil, map[string]string{"/v2/header/big": `18446744073709551614`},
			"/v2/header/big", `18446744073709551614`, `18446744073709551615`, ""},
		{"the same number written another way", nil, map[string]string{"/v2/header/fee": `1.5`},
			"/v2/header/fee", `1.5`, `1.50`, ""},
		{"a string recorded for a number", nil, map[string]string{"/v2/header/ledger_seq": `"17"`},
			"/v2/header/ledger_seq", `"17"`, `17`, ""},
		{"an object with a key missing", map[string]string{"/v2/op/signer": `{"key":"PA"}`}, nil,
			"/v2/op/signer", `{"key":"PA"}`, `{"key":"PA","weight":1}`, ""},
		{"a pointer that resolves to nothing", map[string]string{"/v2/op/gone": `"x"`}, nil,
			"/v2/op/gone", `"x"`, `null`, "at /v2/op/gone the pointer resolves to nothing in the decoded form"},
		{"a recorded null at a pointer that resolves to nothing", map[string]string{"/v2/op/gone": `null`}, nil,
			"/v2/op/gone", `null`, `null`, "resolves to nothing"},
		{"an array index out of range", map[string]string{"/v2/op/list/3": `1`}, nil,
			"/v2/op/list/3", `1`, `null`, "resolves to nothing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, reason := ExpectedValues(evEntry(c.reproduced, c.ledger), []byte(evDoc))
			want := contract.ValueMismatch{Pointer: c.pointer, Recorded: json.RawMessage(c.recorded), Decoded: json.RawMessage(c.decoded)}
			if r.Passed || len(r.Mismatches) != 1 {
				t.Fatalf("%+v, want one mismatch", r)
			}
			got := r.Mismatches[0]
			if got.Pointer != want.Pointer || string(got.Recorded) != c.recorded || string(got.Decoded) != c.decoded {
				t.Errorf("mismatch %s %s %s, want %s %s %s", got.Pointer, got.Recorded, got.Decoded, want.Pointer, want.Recorded, want.Decoded)
			}
			if !strings.HasPrefix(reason, "entry cap-x: expected_values: ") || !strings.Contains(reason, c.inReason) {
				t.Errorf("reason %q, want the entry named and %q", reason, c.inReason)
			}
		})
	}
}

// TestExpectedValuesBothGroupsInOrder: every value is compared, the reproduced group first, then the ledger-dependent
// group, each in catalogue order; every mismatch is reported, not only the first.
func TestExpectedValuesBothGroupsInOrder(t *testing.T) {
	e := evEntry(map[string]string{"/v2/op/result": `"x"`, "/v2/op/signer/key": `"PA"`},
		map[string]string{"/v2/header/close_time": `"0"`, "/v2/header/ledger_seq": `0`})
	r, reason := ExpectedValues(e, []byte(evDoc))
	var got []string
	for _, m := range r.Mismatches {
		got = append(got, m.Pointer)
	}
	if strings.Join(got, " ") != "/v2/op/result /v2/header/close_time /v2/header/ledger_seq" {
		t.Errorf("mismatches %v", got)
	}
	if !strings.Contains(reason, "3 of 4 expected values differ, the first at /v2/op/result") {
		t.Errorf("reason %q", reason)
	}
}

// TestExpectedValuesWithoutDecodedForm: with no decoded form (the file did not decode), the check is not skipped: every
// expected value is a mismatch whose decoded value is null.
func TestExpectedValuesWithoutDecodedForm(t *testing.T) {
	e := evEntry(map[string]string{"/v2/op/result": `"success"`}, map[string]string{"/v2/header/ledger_seq": `17`})
	for _, decoded := range [][]byte{nil, []byte("not json")} {
		r, reason := ExpectedValues(e, decoded)
		if r.Passed || len(r.Mismatches) != 2 || string(r.Mismatches[1].Decoded) != "null" || string(r.Mismatches[1].Recorded) != "17" {
			t.Errorf("%q: %+v", decoded, r)
		}
		if !strings.Contains(reason, "none of its 2 expected values was read again") {
			t.Errorf("reason %q", reason)
		}
	}
}
