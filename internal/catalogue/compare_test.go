package catalogue

// The Run comparator: the edge inputs first (escaped keys, array indices, scalar roots, changes of shape), then the
// two fixture pairs under testdata/compare.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// miniEntry is one entry of a folder written for a test: its decoded JSON and its recorded ledger-dependent values.
type miniEntry struct {
	id      string
	doc     string
	ledgerD []contract.LedgerDependentValue
}

// miniFolder writes a corpus folder of the given entries through the layout writer.
func miniFolder(t *testing.T, entries ...miniEntry) string {
	t.Helper()
	dir := t.TempDir()
	var cat contract.Catalogue
	for _, e := range entries {
		fp := EntryFilePath(e.id)
		if err := WriteEntryFiles(dir, fp, []byte{0, 1}, []byte(e.doc)); err != nil {
			t.Fatal(err)
		}
		ld := e.ledgerD
		if ld == nil {
			ld = []contract.LedgerDependentValue{}
		}
		sha, n := Measure([]byte{0, 1})
		cat.Entries = append(cat.Entries, contract.CatalogueEntry{ID: e.id, Cap: "CAP-0040", ProtocolVersion: 30, Source: "test",
			ExpectedDecodedValues: contract.ExpectedDecodedValues{ReproducedByEveryRun: []contract.ReproducedValue{}, LedgerDependent: ld},
			FilePath:              fp, XdrType: "LedgerCloseMeta", Sha256: sha, Length: n})
	}
	if err := WriteCatalogue(dir, cat); err != nil {
		t.Fatal(err)
	}
	return dir
}

func ld(field contract.LedgerDependentField, pointer string) contract.LedgerDependentValue {
	return contract.LedgerDependentValue{Field: field, Pointer: pointer, Value: json.RawMessage(`0`)}
}

// compareDocs compares two one-entry folders whose entry e has docs a and b and folder A's recorded values rec.
func compareDocs(t *testing.T, a, b string, rec ...contract.LedgerDependentValue) contract.EntryComparison {
	t.Helper()
	r, err := Compare(miniFolder(t, miniEntry{"e", a, rec}), miniFolder(t, miniEntry{"e", b, nil}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 1 {
		t.Fatalf("%d entries", len(r.Entries))
	}
	return r.Entries[0]
}

func differPointers(ec contract.EntryComparison) []string {
	var out []string
	for _, d := range ec.Differ {
		out = append(out, d.Pointer)
	}
	return out
}

// TestCompareLeaves: leaf by leaf, by JSON pointer, with the edge inputs.
func TestCompareLeaves(t *testing.T) {
	cases := []struct {
		name      string
		a, b      string
		equal     []string
		differ    []string
		valueA    string // of the first difference
		valueB    string
		notPassed bool
	}{
		{name: "equal documents", a: `{"x":1,"y":{"z":"s"}}`, b: `{"y":{"z":"s"},"x":1}`, equal: []string{"/x", "/y/z"}},
		{name: "~0 and ~1 in keys", a: `{"a/b":{"c~d":1}}`, b: `{"a/b":{"c~d":2}}`, differ: []string{"/a~1b/c~0d"}, valueA: "1", valueB: "2", notPassed: true},
		{name: "array indices", a: `{"l":[1,2,3]}`, b: `{"l":[1,5,3]}`, equal: []string{"/l/0", "/l/2"}, differ: []string{"/l/1"}, notPassed: true},
		{name: "a scalar root", a: `7`, b: `8`, differ: []string{""}, valueA: "7", valueB: "8", notPassed: true},
		{name: "an equal scalar root", a: `"r"`, b: `"r"`, equal: []string{""}},
		{name: "a key present in one folder only", a: `{"o":{"k":1,"only":2}}`, b: `{"o":{"k":1}}`, differ: []string{"/o"},
			valueA: `{"k":1,"only":2}`, valueB: `{"k":1}`, notPassed: true},
		{name: "arrays of different lengths", a: `{"l":[1,2]}`, b: `{"l":[1,2,3]}`, differ: []string{"/l"}, valueA: "[1,2]", valueB: "[1,2,3]", notPassed: true},
		{name: "a type change", a: `{"t":{"v1":1}}`, b: `{"t":"v0"}`, differ: []string{"/t"}, valueA: `{"v1":1}`, valueB: `"v0"`, notPassed: true},
		{name: "empty object and array are leaves", a: `{"o":{},"l":[]}`, b: `{"o":{},"l":[]}`, equal: []string{"/o", "/l"}},
		{name: "empty object against empty array", a: `{"o":{}}`, b: `{"o":[]}`, differ: []string{"/o"}, valueA: "{}", valueB: "[]", notPassed: true},
		{name: "64-bit numbers as decimal strings", a: `{"n":9223372036854775807}`, b: `{"n":9223372036854775806}`, differ: []string{"/n"},
			valueA: "9223372036854775807", valueB: "9223372036854775806", notPassed: true},
		{name: "a number and its string", a: `{"n":1}`, b: `{"n":"1"}`, differ: []string{"/n"}, notPassed: true},
		{name: "null and absent are not the same", a: `{"n":null}`, b: `{}`, differ: []string{""}, valueA: `{"n":null}`, valueB: "{}", notPassed: true},
		{name: "a recorded null equal", a: `{"n":null}`, b: `{"n":null}`, equal: []string{"/n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ec := compareDocs(t, c.a, c.b)
			if !slices.Equal(ec.Equal, orEmpty(c.equal)) {
				t.Errorf("equal %q, want %q", ec.Equal, c.equal)
			}
			if got := differPointers(ec); !slices.Equal(got, c.differ) {
				t.Errorf("differ %q, want %q", got, c.differ)
			}
			if c.valueA != "" && (string(ec.Differ[0].ValueA) != c.valueA || string(ec.Differ[0].ValueB) != c.valueB) {
				t.Errorf("values %s / %s, want %s / %s", ec.Differ[0].ValueA, ec.Differ[0].ValueB, c.valueA, c.valueB)
			}
			for _, d := range ec.Differ {
				if d.LedgerDependentField != nil {
					t.Errorf("%s attributed to %s with nothing recorded", d.Pointer, *d.LedgerDependentField)
				}
			}
		})
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// TestCompareAttribution (step 3): a difference is attributed by the recorded pointer that equals it or is its prefix
// on a segment boundary, the longest one; never by a recorded pointer below it; never by a value that is not a
// LedgerDependentField.
func TestCompareAttribution(t *testing.T) {
	a := `{"h":{"seq":1,"seqx":1,"t":{"x":1,"y":2},"l":[1],"deep":{"k":1}}}`
	b := `{"h":{"seq":2,"seqx":2,"t":{"x":3,"y":4},"l":[1,2],"deep":{"k":2}}}`
	ec := compareDocs(t, a, b,
		ld(contract.LedgerDependentFieldLedgerNumber, "/h/seq"),
		ld(contract.LedgerDependentFieldCloseTime, "/h/t"),
		ld(contract.LedgerDependentFieldFeePool, "/h/t/y"),
		ld(contract.LedgerDependentFieldBucketListHash, "/h/l/1"),
		ld("not_a_field", "/h/deep"),
	)
	want := map[string]string{
		"/h/seq":    "ledger_number",
		"/h/seqx":   "", // "/h/seq" is no prefix of "/h/seqx"
		"/h/t/x":    "close_time",
		"/h/t/y":    "fee_pool", // the longest recorded prefix
		"/h/l":      "",         // the recorded pointer is below the difference
		"/h/deep/k": "",         // a field outside the union attributes nothing
	}
	if len(ec.Differ) != len(want) {
		t.Fatalf("differ %q", differPointers(ec))
	}
	for _, d := range ec.Differ {
		got := ""
		if d.LedgerDependentField != nil {
			got = string(*d.LedgerDependentField)
		}
		if w, ok := want[d.Pointer]; !ok || got != w {
			t.Errorf("%s attributed to %q, want %q", d.Pointer, got, w)
		}
	}
	root := compareDocs(t, `{"a":{"b":1}}`, `{"a":{"b":2}}`, ld(contract.LedgerDependentFieldCloseTime, ""))
	if f := root.Differ[0].LedgerDependentField; f == nil || *f != contract.LedgerDependentFieldCloseTime {
		t.Error("the root pointer \"\" is a prefix of every pointer")
	}
}

// TestCompareUsageErrors (step 1; exit 2): a missing folder, a folder with no catalogue or an unreadable one, ids that
// differ, an id listed twice, a missing or invalid decoded JSON file.
func TestCompareUsageErrors(t *testing.T) {
	good := miniFolder(t, miniEntry{"e", `{}`, nil})
	other := miniFolder(t, miniEntry{"f", `{}`, nil})
	twoA := miniFolder(t, miniEntry{"e", `{}`, nil}, miniEntry{"g", `{}`, nil})
	noCat := t.TempDir()
	badCat := t.TempDir()
	os.WriteFile(filepath.Join(badCat, CatalogueFileName), []byte(`{"entries":`), 0o644)
	dup := miniFolder(t, miniEntry{"e", `{}`, nil})
	c, _ := ReadCatalogue(dup)
	c.Entries = append(c.Entries, c.Entries[0])
	WriteCatalogue(dup, c)
	noJSON := miniFolder(t, miniEntry{"e", `{}`, nil})
	os.Remove(filepath.Join(noJSON, "e", "entry.json"))
	badJSON := miniFolder(t, miniEntry{"e", `{}`, nil})
	os.WriteFile(filepath.Join(badJSON, "e", "entry.json"), []byte(`{"a":1,"a":2}`), 0o644)

	cases := []struct {
		name, a, b, contains string
	}{
		{"folder A missing", filepath.Join(noCat, "absent"), good, "does not exist"},
		{"folder B missing", good, filepath.Join(noCat, "absent"), "does not exist"},
		{"no catalogue", good, noCat, "holds no catalogue"},
		{"unreadable catalogue", badCat, good, "not a readable catalogue"},
		{"ids differ", good, other, "folders do not list the same entry ids: e missing from " + other + ", f missing from " + good},
		{"an id missing from B", twoA, good, "folders do not list the same entry ids: g missing from " + good},
		{"an id listed twice", dup, good, "lists the entry id e twice"},
		{"decoded JSON missing", good, noJSON, "has no readable decoded JSON for entry e at e/entry.json"},
		{"decoded JSON not strict", badJSON, good, "is not strict JSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compare(c.a, c.b)
			var coded *model.CodedError
			if !errors.As(err, &coded) || coded.Code != contract.ErrorCodeUsageError {
				t.Fatalf("%v, want a usage error", err)
			}
			if !strings.Contains(err.Error(), c.contains) {
				t.Errorf("message %q does not contain %q", err.Error(), c.contains)
			}
		})
	}
}

// TestCompareFixtureLedgerOnly: two generations whose decoded forms differ only in ledger-dependent values: every
// difference is attributed, passed is true. Each entry reports its equal and differing pointers, each difference with
// its pointer, field, valueA and valueB.
func TestCompareFixtureLedgerOnly(t *testing.T) {
	r, err := Compare(testdataRoot+"/compare/ledger-only/a", testdataRoot+"/compare/ledger-only/b")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed {
		t.Fatalf("passed false: %+v", r)
	}
	if r.FolderA != testdataRoot+"/compare/ledger-only/a" || r.FolderB != testdataRoot+"/compare/ledger-only/b" {
		t.Errorf("folders as given: %q %q", r.FolderA, r.FolderB)
	}
	ids := []string{model.SeedSignedPayloadSigner, model.SeedPoolShareTrustline, model.SeedMuxedAccount}
	seen := map[contract.LedgerDependentField]bool{}
	for i, e := range r.Entries {
		if e.ID != ids[i] {
			t.Errorf("entry %d is %s, want %s (folder A's order)", i, e.ID, ids[i])
		}
		if len(e.Equal) == 0 || len(e.Differ) == 0 {
			t.Errorf("%s: %d equal, %d differ", e.ID, len(e.Equal), len(e.Differ))
		}
		for _, d := range e.Differ {
			if d.LedgerDependentField == nil || len(d.ValueA) == 0 || len(d.ValueB) == 0 || string(d.ValueA) == string(d.ValueB) {
				t.Errorf("%s %s: %+v", e.ID, d.Pointer, d)
				continue
			}
			seen[*d.LedgerDependentField] = true
		}
	}
	// Every ledger-dependent field differs in the pair and is attributed.
	for _, f := range contract.Unions["LedgerDependentField"] {
		if !seen[contract.LedgerDependentField(f)] {
			t.Errorf("no difference was attributed to %s", f)
		}
	}
}

// TestCompareFixtureReproducedChanged: a reproduced value differs, entry 2's operation result; the report names the
// entry and the pointer with a null field, passed is false; Unlisted names the first.
func TestCompareFixtureReproducedChanged(t *testing.T) {
	r, err := Compare(testdataRoot+"/compare/reproduced-changed/a", testdataRoot+"/compare/reproduced-changed/b")
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed {
		t.Fatal("passed true with a reproduced value changed")
	}
	const ptr = "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/change_trust"
	var found bool
	for _, e := range r.Entries {
		for _, d := range e.Differ {
			if d.LedgerDependentField == nil {
				if e.ID != model.SeedPoolShareTrustline || d.Pointer != ptr || string(d.ValueA) != `"success"` || string(d.ValueB) != `"malformed"` {
					t.Errorf("unexpected unlisted difference %s %+v", e.ID, d)
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the changed reproduced value is not reported")
	}
	if id, p, n := Unlisted(r); id != model.SeedPoolShareTrustline || p != ptr || n != 1 {
		t.Errorf("Unlisted = %s %s %d", id, p, n)
	}
}

// TestCompareSameFolder: a folder against itself has no difference and passes; bytes, sha256 and length are not
// compared (step 5): the ledger-only pair's XDR files differ, yet nothing in the report is about bytes.
func TestCompareSameFolder(t *testing.T) {
	r, err := Compare(validCorpus, validCorpus)
	if err != nil || !r.Passed {
		t.Fatalf("%v %+v", err, r)
	}
	for _, e := range r.Entries {
		if len(e.Differ) != 0 {
			t.Errorf("%s: %d differences against itself", e.ID, len(e.Differ))
		}
	}
	if id, _, n := Unlisted(r); n != 0 || id != "" {
		t.Errorf("Unlisted %s %d", id, n)
	}
}

// TestCompareHeaderHashes: the ledger header's own hash and its tx_set_result_hash are attributed to ledger_hash and
// transaction_result_set_hash, so a difference in them passes; a difference in a header field that is on no list
// (ledger_version) still has a null field and fails.
func TestCompareHeaderHashes(t *testing.T) {
	doc := `{"v2":{"ledger_header":{"hash":"%s","header":{"ledger_version":%v,"tx_set_result_hash":"%s","ledger_seq":%d}}}}`
	a := strings.NewReplacer("%s", "aa", "%d", "1", "%v", "28").Replace(doc)
	b := strings.NewReplacer("%s", "bb", "%d", "2", "%v", "29").Replace(doc)
	expA, err := SplitExpectedValues(model.SeedSignedPayloadSigner, []byte(a))
	if err != nil {
		t.Fatal(err)
	}
	ec := compareDocs(t, a, b, expA.LedgerDependent...)
	got := map[string]*contract.LedgerDependentField{}
	for _, d := range ec.Differ {
		got[d.Pointer] = d.LedgerDependentField
	}
	for p, want := range map[string]contract.LedgerDependentField{
		"/v2/ledger_header/hash":                      contract.LedgerDependentFieldLedgerHash,
		"/v2/ledger_header/header/tx_set_result_hash": contract.LedgerDependentFieldTransactionResultSetHash,
		"/v2/ledger_header/header/ledger_seq":         contract.LedgerDependentFieldLedgerNumber,
	} {
		if f := got[p]; f == nil || *f != want {
			t.Errorf("%s must be attributed to %s, got %v", p, want, f)
		}
	}
	if f, ok := got["/v2/ledger_header/header/ledger_version"]; !ok || f != nil {
		t.Error("a field outside the list (ledger_version) has a null field")
	}
}
