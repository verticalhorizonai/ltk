package check

// The check expected_values: each entry's expected values of both groups, the values reproduced by every run and the
// values that depend on the ledger alike, read again from its decoded form and compared with the catalogue. On a
// committed file both groups read back from the same bytes; the groups differ only in what `ltk compare` requires to be
// equal between two generations.

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
)

// jsonNull is the JSON null a ValueMismatch reports as `decoded` when the pointer resolves to nothing.
var jsonNull = json.RawMessage("null")

// ExpectedValues reads each expected value of entry e again from decoded, the JSON form Stellar's Rust XDR tool
// produced freshly from the file in this run (never the committed decoded JSON), and compares it with the value the
// catalogue records:
//   - both groups are compared, expectedDecodedValues.reproducedByEveryRun then expectedDecodedValues.ledgerDependent,
//     each in catalogue order;
//   - two values are equal by JSON value equality: the same kind, strings equal once decoded, numbers equal as their
//     decimal text (a 64-bit value never loses precision), objects with the same keys holding equal values, arrays
//     element by element;
//   - each unequal value is a ValueMismatch of its pointer, the recorded value and the decoded value; a pointer that
//     resolves to nothing reports `decoded` as null, and a recorded null is matched only by a decoded null that is
//     there, so the two are told apart (the reason says which);
//   - with no decoded form (decoded is nil: the file could not be read or decoded), every expected value is a mismatch
//     whose decoded is null: the check fails with its own record, never skipped.
//
// passed is true only when mismatches is []. The reason names the entry, the number of mismatches and the first; ""
// when the check passed.
func ExpectedValues(e contract.CatalogueEntry, decoded []byte) (contract.ExpectedValuesResult, string) {
	result := contract.ExpectedValuesResult{Check: contract.CheckNameExpectedValues, Mismatches: []contract.ValueMismatch{}}
	type expected struct {
		pointer string
		value   json.RawMessage
	}
	var values []expected
	for _, v := range e.ExpectedDecodedValues.ReproducedByEveryRun {
		values = append(values, expected{v.Pointer, v.Value})
	}
	for _, v := range e.ExpectedDecodedValues.LedgerDependent {
		values = append(values, expected{v.Pointer, v.Value})
	}

	var doc *catalogue.Node
	noForm := ""
	if decoded == nil {
		noForm = "there is no decoded form (the round trip did not decode the file)"
	} else if d, err := catalogue.Parse(decoded); err != nil {
		noForm = "the decoded form is not JSON: " + err.Error()
	} else {
		doc = d
	}

	first := ""
	for _, v := range values {
		recorded := compactJSON(v.value)
		if doc == nil {
			result.Mismatches = append(result.Mismatches, contract.ValueMismatch{Pointer: v.pointer, Recorded: recorded, Decoded: jsonNull})
			continue
		}
		found, ok := doc.Resolve(v.pointer)
		rec, recErr := catalogue.Parse(recorded)
		switch {
		case !ok:
			result.Mismatches = append(result.Mismatches, contract.ValueMismatch{Pointer: v.pointer, Recorded: recorded, Decoded: jsonNull})
			if first == "" {
				first = fmt.Sprintf("at %s the pointer resolves to nothing in the decoded form; the catalogue records %s", v.pointer, recorded)
			}
		case recErr != nil || !catalogue.Equal(rec, found):
			got := compactJSON(found.Raw)
			result.Mismatches = append(result.Mismatches, contract.ValueMismatch{Pointer: v.pointer, Recorded: recorded, Decoded: got})
			if first == "" {
				first = fmt.Sprintf("at %s the decoded form holds %s; the catalogue records %s", v.pointer, got, recorded)
			}
		}
	}
	result.Passed = len(result.Mismatches) == 0
	switch {
	case result.Passed:
		return result, ""
	case doc == nil:
		return result, fmt.Sprintf("entry %s: expected_values: %s, so none of its %d expected values was read again", e.ID, noForm, len(values))
	}
	return result, fmt.Sprintf("entry %s: expected_values: %d of %d expected values differ, the first %s",
		e.ID, len(result.Mismatches), len(values), first)
}

// compactJSON is raw without insignificant whitespace; a missing value (nil) is JSON null.
func compactJSON(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return jsonNull
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}
