package catalogue

// The Run comparator of `ltk compare`. It reads two corpus folders' readable catalogues and, per entry, the decoded
// JSON file beside each XDR file; it decodes nothing, validates no schema and starts no container, so it runs on every
// system without Docker.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// Compare compares the corpus folders folderA and folderB:
//
//  1. Each folder must exist and hold a readable catalogue; the two catalogues must list the same set of entry ids,
//     each once; each entry's decoded JSON file must exist in both folders and be strict JSON. Any violation is a
//     usage error (exit 2) and no report is made.
//  2. For each entry, in the order of folder A's catalogue, the two decoded documents are compared leaf by leaf, by
//     JSON pointer: equal leaves go to `equal`, differing ones to `differ`. The elements of each change list (a
//     `changes`, `tx_changes_before`, `tx_changes_after` or `fee_processing` array, wherever it occurs) are matched
//     by (ledger key, change kind) as a multiset, not by index: see compareChangeList. The pointer
//     reported for a leaf of a matched element is side A's pointer; a change present on one side only is one
//     difference at its own element pointer in its own folder's file, the other side's value null.
//  3. A difference is attributed to the ledger-dependent field whose pointer, recorded in the entry's
//     expectedDecodedValues.ledgerDependent in folder A's catalogue, equals the differing pointer or is its prefix on
//     a segment boundary (the longest such pointer); otherwise its field is null.
//  4. `passed` is true only when no difference has a null field. Bytes, sha256 and length are never compared.
//
// A leaf is a string, number, boolean or null, or an empty object or array. Where the two documents differ in shape at
// a pointer (a value of another kind, an object with other keys, an array of another length), the pointer is one
// difference and carries both values whole. Numbers are compared as their decimal text.
func Compare(folderA, folderB string) (contract.CompareReport, error) {
	report := contract.CompareReport{FolderA: folderA, FolderB: folderB, Entries: []contract.EntryComparison{}}
	catA, err := readComparable(folderA)
	if err != nil {
		return report, err
	}
	catB, err := readComparable(folderB)
	if err != nil {
		return report, err
	}
	if err := sameIDs(folderA, catA, folderB, catB); err != nil {
		return report, err
	}
	byID := map[string]contract.CatalogueEntry{}
	for _, e := range catB.Entries {
		byID[e.ID] = e
	}
	report.Passed = true
	for _, ea := range catA.Entries {
		docA, err := readDecoded(folderA, ea)
		if err != nil {
			return report, err
		}
		docB, err := readDecoded(folderB, byID[ea.ID])
		if err != nil {
			return report, err
		}
		ec := contract.EntryComparison{ID: ea.ID, Equal: []string{}, Differ: []contract.FieldDifference{}}
		attribute := attribution(ea.ExpectedDecodedValues.LedgerDependent)
		compareNodes(docA, docB, "", &ec, attribute)
		for _, d := range ec.Differ {
			if d.LedgerDependentField == nil {
				report.Passed = false
			}
		}
		report.Entries = append(report.Entries, ec)
	}
	return report, nil
}

// readComparable reads a folder's catalogue for the comparison: the folder exists, its catalogue is readable and no
// id appears twice.
func readComparable(folder string) (contract.Catalogue, error) {
	cat, err := ReadCatalogue(folder)
	switch {
	case errors.Is(err, ErrNoFolder):
		return cat, model.Usage("folder '%s' does not exist", folder)
	case errors.Is(err, ErrNoCatalogue):
		return cat, model.Usage("folder '%s' holds no catalogue (%s)", folder, CatalogueFileName)
	case err != nil:
		return cat, model.Usage("%v", err)
	}
	seen := map[string]bool{}
	for _, e := range cat.Entries {
		if seen[e.ID] {
			return cat, model.Usage("the catalogue of folder '%s' lists the entry id %s twice", folder, e.ID)
		}
		seen[e.ID] = true
	}
	return cat, nil
}

// sameIDs is the usage error naming every id one catalogue lists and the other does not (ltk compare step 1).
func sameIDs(folderA string, a contract.Catalogue, folderB string, b contract.Catalogue) error {
	var missing []string
	in := func(c contract.Catalogue, id string) bool {
		return slices.ContainsFunc(c.Entries, func(e contract.CatalogueEntry) bool { return e.ID == id })
	}
	for _, e := range a.Entries {
		if !in(b, e.ID) {
			missing = append(missing, fmt.Sprintf("%s missing from %s", e.ID, folderB))
		}
	}
	for _, e := range b.Entries {
		if !in(a, e.ID) {
			missing = append(missing, fmt.Sprintf("%s missing from %s", e.ID, folderA))
		}
	}
	if len(missing) > 0 {
		return model.Usage("folders do not list the same entry ids: %s", strings.Join(missing, ", "))
	}
	return nil
}

// readDecoded reads and parses an entry's decoded JSON file; a missing or invalid file is a usage error.
func readDecoded(folder string, e contract.CatalogueEntry) (*Node, error) {
	name := DecodedJSONPath(e.FilePath)
	b, err := ReadDecodedJSON(folder, e.FilePath)
	if err != nil {
		return nil, model.Usage("folder '%s' has no readable decoded JSON for entry %s at %s", folder, e.ID, name)
	}
	doc, err := Parse(b)
	if err != nil {
		return nil, model.Usage("the decoded JSON of entry %s in folder '%s' (%s) is not strict JSON: %v", e.ID, folder, name, err)
	}
	return doc, nil
}

// attribution returns the function that names a differing pointer's ledger-dependent field from the recorded values
// of folder A's entry: the field of the longest recorded pointer that equals the pointer or is its prefix on a segment
// boundary; nil when none is, or when the recorded field is not a LedgerDependentField.
func attribution(recorded []contract.LedgerDependentValue) func(pointer string) *contract.LedgerDependentField {
	return func(pointer string) *contract.LedgerDependentField {
		var best *contract.LedgerDependentValue
		for i := range recorded {
			r := &recorded[i]
			if !slices.Contains(contract.Unions["LedgerDependentField"], string(r.Field)) || !IsPointerPrefix(r.Pointer, pointer) {
				continue
			}
			if best == nil || len(r.Pointer) > len(best.Pointer) {
				best = r
			}
		}
		if best == nil {
			return nil
		}
		f := best.Field
		return &f
	}
}

// compareNodes walks a and b together from ptr.
func compareNodes(a, b *Node, ptr string, ec *contract.EntryComparison, attribute func(string) *contract.LedgerDependentField) {
	switch {
	case a.Kind == KindObject && b.Kind == KindObject && len(a.Keys) > 0 && sameKeys(a, b):
		for i, k := range a.Keys {
			va, vb, p := a.Values[i], b.Member(k), ptr+"/"+EscapeToken(k)
			if changeLists[k] && va.Kind == KindArray && vb.Kind == KindArray && len(va.Elems)+len(vb.Elems) > 0 {
				compareChangeList(va, vb, p, ec, attribute)
				continue
			}
			compareNodes(va, vb, p, ec, attribute)
		}
	case a.Kind == KindArray && b.Kind == KindArray && len(a.Elems) > 0 && len(a.Elems) == len(b.Elems):
		for i := range a.Elems {
			compareNodes(a.Elems[i], b.Elems[i], ptr+"/"+strconv.Itoa(i), ec, attribute)
		}
	case Equal(a, b):
		ec.Equal = append(ec.Equal, ptr)
	default:
		ec.Differ = append(ec.Differ, contract.FieldDifference{
			Pointer:              ptr,
			LedgerDependentField: attribute(ptr),
			ValueA:               compact(a.Raw),
			ValueB:               compact(b.Raw),
		})
	}
}

// sameKeys reports whether two objects hold the same set of keys.
func sameKeys(a, b *Node) bool {
	if len(a.Keys) != len(b.Keys) {
		return false
	}
	for _, k := range a.Keys {
		if b.Member(k) == nil {
			return false
		}
	}
	return true
}

// compact is raw JSON text with its insignificant white space removed.
func compact(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}

// Unlisted returns the differences of the report outside the ledger-dependent list: the entry id and pointer of the
// first, in report order, and how many there are (for the standard-error line of exit 1).
func Unlisted(r contract.CompareReport) (id, pointer string, count int) {
	for _, e := range r.Entries {
		for _, d := range e.Differ {
			if d.LedgerDependentField != nil {
				continue
			}
			if count == 0 {
				id, pointer = e.ID, d.Pointer
			}
			count++
		}
	}
	return id, pointer, count
}
