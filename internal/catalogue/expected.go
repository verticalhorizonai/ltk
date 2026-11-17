package catalogue

// Splitting an entry's expected values into the catalogue's two groups: generation reads the entry's decoded JSON
// through the Expected-value grouping table of internal/model and records each expected value, at its pointer, in the
// group the table assigns. The group is the array the value sits in; no field is added to the catalogue.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// SplitExpectedValues returns the expected decoded values of entry entryID read from its decoded JSON: every value
// whose pointer a rule of the entry's grouping names, in document order, in the rule's group and, for a
// ledger-dependent value, with the rule's field. A rule that names an object or an array records it whole.
func SplitExpectedValues(entryID string, decodedJSON []byte) (contract.ExpectedDecodedValues, error) {
	out := contract.ExpectedDecodedValues{
		ReproducedByEveryRun: []contract.ReproducedValue{},
		LedgerDependent:      []contract.LedgerDependentValue{},
	}
	if _, ok := model.Grouping(entryID); !ok {
		return out, fmt.Errorf("no grouping for entry %q: it is not a seed entry", entryID)
	}
	doc, err := Parse(decodedJSON)
	if err != nil {
		return out, fmt.Errorf("the decoded JSON of %s: %w", entryID, err)
	}
	var walk func(n *Node, ptr string)
	walk = func(n *Node, ptr string) {
		if r, ok := model.Classify(entryID, ptr); ok && ptr != "" {
			var v bytes.Buffer
			_ = json.Compact(&v, n.Raw) // n.Raw is valid JSON: Parse read it
			value := json.RawMessage(v.Bytes())
			if r.Group == contract.ExpectedValueGroupLedgerDependent {
				out.LedgerDependent = append(out.LedgerDependent, contract.LedgerDependentValue{Field: r.Field, Pointer: ptr, Value: value})
			} else {
				out.ReproducedByEveryRun = append(out.ReproducedByEveryRun, contract.ReproducedValue{Pointer: ptr, Value: value})
			}
			return
		}
		for i, k := range n.Keys {
			walk(n.Values[i], ptr+"/"+EscapeToken(k))
		}
		for i, e := range n.Elems {
			walk(e, ptr+"/"+strconv.Itoa(i))
		}
	}
	walk(doc, "")
	return out, nil
}
