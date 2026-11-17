package schema

// The Catalogue schema validator, with its edge inputs: several violations at once, integers written with a fraction
// or an exponent, a byte-order mark, trailing data, a missing catalogue or schema copy. Also the schema file against
// the data model of record, when present beside the module, and a catalogue built from the seed definitions.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/schemas"
)

const (
	testdata      = "../../testdata"
	validCorpus   = testdata + "/corpus/valid"
	dataModelPath = "../../specs/data-model.md"
)

func validBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(validCorpus, catalogue.CatalogueFileName))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// edited is the valid catalogue decoded generically, changed by edit, encoded again.
func edited(t *testing.T, edit func(entries []any, doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(validBytes(t)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	entries, _ := doc["entries"].([]any)
	edit(entries, doc)
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func entry(entries []any, i int) map[string]any { return entries[i].(map[string]any) }

func pointers(fs []contract.SchemaFailure) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Pointer)
	}
	return out
}

// TestSchemaFileIsTheDataModelBlock: schemas/catalogue.schema.json is the catalogue schema block of the data model of
// record exactly, when that document is present beside the module, and the embedded bytes are the file's.
func TestSchemaFileIsTheDataModelBlock(t *testing.T) {
	file, err := os.ReadFile("../../schemas/catalogue.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(file, schemas.CatalogueSchema()) {
		t.Error("the embedded schema differs from schemas/catalogue.schema.json")
	}
	b, err := os.ReadFile(dataModelPath)
	if os.IsNotExist(err) {
		t.Skipf("%s is absent (it is not exported with the repository): no block to hold the schema to", dataModelPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(b), "\r\n", "\n")
	_, rest, ok := strings.Cut(doc, "### JSON Schema — `schemas/catalogue.schema.json`\n")
	if !ok {
		t.Fatal("the data model has no catalogue schema heading")
	}
	_, body, ok := strings.Cut(rest, "```json\n")
	if !ok {
		t.Fatal("the heading has no ```json block")
	}
	block, _, ok := strings.Cut(body, "\n```")
	if !ok {
		t.Fatal("the block is not closed")
	}
	if string(file) != block+"\n" {
		t.Error("schemas/catalogue.schema.json differs from the data model's catalogue schema block; rewrite it from the block")
	}
}

// TestValidCatalogue: the fixture's catalogue validates against the embedded
// schema; catalogue_schema passes with failures [] and the catalogue is returned for the per-entry checks.
func TestValidCatalogue(t *testing.T) {
	r, cat := CheckCatalogue(validCorpus)
	if !r.Passed || r.Check != contract.CheckNameCatalogueSchema || r.Failures == nil || len(r.Failures) != 0 {
		t.Fatalf("%+v", r)
	}
	if cat == nil || len(cat.Entries) != 3 || cat.Entries[1].NetworkRefusalEvidence == nil {
		t.Fatalf("the catalogue is not returned whole: %+v", cat)
	}
	if b, _ := json.Marshal(r); string(b) != `{"check":"catalogue_schema","passed":true,"failures":[]}` {
		t.Errorf("as written: %s", b)
	}
}

// TestSchemaFailurePointers: every violation is reported with the JSON pointer of the offending value; several are all
// reported; none collapses into one.
func TestSchemaFailurePointers(t *testing.T) {
	cases := []struct {
		name     string
		doc      func(t *testing.T) []byte
		pointers []string
		contains string // in the first failure's message
	}{
		{"a missing required field points at the field", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) { delete(entry(e, 1), "sha256") })
		}, []string{"/entries/1/sha256"}, `missing required property "sha256"`},
		{"a wrong type", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) { entry(e, 0)["length"] = "1920" })
		}, []string{"/entries/0/length"}, "integer"},
		{"several violations", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) {
				entry(e, 0)["length"] = -1
				entry(e, 2)["cap"] = ""
				delete(entry(e, 2), "source")
				entry(e, 1)["sha256"] = strings.Repeat("A", 64)
			})
		}, []string{"/entries/0/length", "/entries/1/sha256", "/entries/2/cap", "/entries/2/source"}, ""},
		{"protocolFallback true at a protocol other than 28", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) { entry(e, 0)["protocolFallback"] = true })
		}, []string{"/entries/0/protocolVersion"}, ""},
		{"empty entries", func(t *testing.T) []byte {
			return edited(t, func(_ []any, d map[string]any) { d["entries"] = []any{} })
		}, []string{"/entries"}, ""},
		{"an extra field of an entry", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) { entry(e, 2)["provenance"] = "x" })
		}, []string{"/entries/2/provenance"}, `"provenance" is not allowed`},
		{"an extra field at the top", func(t *testing.T) []byte {
			return edited(t, func(_ []any, d map[string]any) { d["version"] = 1 })
		}, []string{"/version"}, ""},
		{"uppercase hex in sha256", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) {
				entry(e, 0)["sha256"] = strings.ToUpper(entry(e, 0)["sha256"].(string))
			})
		}, []string{"/entries/0/sha256"}, ""},
		{"1920.0 as an integer", func(t *testing.T) []byte {
			return bytes.Replace(validBytes(t), []byte(`"length": 1920`), []byte(`"length": 1920.0`), 1)
		}, []string{"/entries/0/length"}, "whole number"},
		{"an exponent as an integer", func(t *testing.T) []byte {
			return bytes.Replace(validBytes(t), []byte(`"protocolVersion": 28`), []byte(`"protocolVersion": 2.8e1`), 1)
		}, []string{"/entries/1/protocolVersion"}, "whole number"},
		{"an integer no int holds", func(t *testing.T) []byte {
			return bytes.Replace(validBytes(t), []byte(`"length": 2048`), []byte(`"length": 99999999999999999999999`), 1)
		}, []string{"/entries/2/length"}, "whole number"},
		{"a filePath with a backslash", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) { entry(e, 0)["filePath"] = `a\b.xdr` })
		}, []string{"/entries/0/filePath"}, ""},
		{"an expected-value pointer that is not a pointer", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) {
				rep := entry(e, 0)["expectedDecodedValues"].(map[string]any)["reproducedByEveryRun"].([]any)
				rep[0].(map[string]any)["pointer"] = "v2/x"
			})
		}, []string{"/entries/0/expectedDecodedValues/reproducedByEveryRun/0/pointer"}, ""},
		{"a ledger-dependent field outside the enum", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) {
				l := entry(e, 0)["expectedDecodedValues"].(map[string]any)["ledgerDependent"].([]any)
				l[0].(map[string]any)["field"] = "ledger_version"
			})
		}, []string{"/entries/0/expectedDecodedValues/ledgerDependent/0/field"}, ""},
		{"entry 2's evidence absent", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) { delete(entry(e, 1), "networkRefusalEvidence") })
		}, []string{"/entries/1"}, "must carry networkRefusalEvidence"},
		{"entry 2's resultCode another value", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) {
				entry(e, 1)["networkRefusalEvidence"].(map[string]any)["resultCode"] = "CHANGE_TRUST_INVALID_LIMIT"
			})
		}, []string{"/entries/1/networkRefusalEvidence/resultCode"}, ""},
		{"evidence present on entry 1", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) {
				entry(e, 0)["networkRefusalEvidence"] = entry(e, 1)["networkRefusalEvidence"]
			})
		}, []string{"/entries/0/networkRefusalEvidence"}, "entry 2"},
		{"duplicate ids", func(t *testing.T) []byte {
			return edited(t, func(e []any, _ map[string]any) { entry(e, 2)["id"] = entry(e, 0)["id"] })
		}, []string{"/entries/2/id"}, "already the id of /entries/0"},
		{"invalid JSON at the root", func(t *testing.T) []byte { return []byte(`{"entries": [`) }, []string{""}, "not strict JSON"},
		{"a byte-order mark", func(t *testing.T) []byte { return append([]byte{0xEF, 0xBB, 0xBF}, validBytes(t)...) }, []string{""}, "byte-order mark"},
		{"trailing data", func(t *testing.T) []byte { return append(validBytes(t), []byte("{}")...) }, []string{""}, "data after"},
		{"a duplicate key", func(t *testing.T) []byte {
			return bytes.Replace(validBytes(t), []byte(`"cap": "CAP-0038",`), []byte(`"cap": "CAP-0038", "cap": "CAP-0038",`), 1)
		}, []string{"/entries/1/cap"}, "twice"},
		{"a top-level array", func(t *testing.T) []byte { return []byte(`[]`) }, []string{""}, ""},
	}
	ptrPattern := regexp.MustCompile(`^(/([^~/]|~[01])*)*$`)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cat, failures := ValidateCatalogue(c.doc(t))
			if cat != nil {
				t.Error("a failing catalogue must not be returned")
			}
			if got := pointers(failures); !slices.Equal(got, c.pointers) {
				t.Fatalf("pointers %q, want %q; failures %+v", got, c.pointers, failures)
			}
			for _, f := range failures {
				if f.Message == "" || !ptrPattern.MatchString(f.Pointer) {
					t.Errorf("failure %+v: a message and an RFC 6901 pointer", f)
				}
			}
			if !strings.Contains(failures[0].Message, c.contains) {
				t.Errorf("message %q does not contain %q", failures[0].Message, c.contains)
			}
		})
	}
}

// TestSchemaFailureOrderIsStable: failures are sorted by pointer, an index as a number, the same from run to run.
func TestSchemaFailureOrderIsStable(t *testing.T) {
	doc := edited(t, func(e []any, d map[string]any) {
		var many []any
		for i := 0; i < 12; i++ {
			x := map[string]any{}
			for k, v := range entry(e, 0) {
				x[k] = v
			}
			x["id"] = "e" + strings.Repeat("x", i)
			x["length"] = "bad"
			many = append(many, x)
		}
		d["entries"] = many
	})
	_, first := ValidateCatalogue(doc)
	for i := 0; i < 5; i++ {
		if _, again := ValidateCatalogue(doc); !slices.Equal(pointers(again), pointers(first)) {
			t.Fatal("the failures' order changes from run to run")
		}
	}
	if len(first) != 12 || first[1].Pointer != "/entries/1/length" || first[2].Pointer != "/entries/2/length" || first[11].Pointer != "/entries/11/length" {
		t.Errorf("order %q", pointers(first))
	}
}

// TestCheckCatalogueFixture: the schema-pointer fixture fails catalogue_schema at its two pointers and gives no
// catalogue, so entries is []: there is no trustworthy list to check.
func TestCheckCatalogueFixture(t *testing.T) {
	r, cat := CheckCatalogue(testdata + "/corpus/schema-pointer")
	if r.Passed || cat != nil {
		t.Fatalf("passed %v catalogue %v", r.Passed, cat != nil)
	}
	if got := pointers(r.Failures); !slices.Equal(got, []string{"/entries/0/length", "/entries/1/sha256"}) {
		t.Errorf("pointers %q", got)
	}
	// The other failure corpora hold a valid catalogue: their failures belong to the later checks, not to
	// catalogue_schema.
	for _, kind := range []string{"hash", "length", "round-trip", "expected-value"} {
		if r, cat := CheckCatalogue(testdata + "/corpus/" + kind); !r.Passed || cat == nil {
			t.Errorf("%s: catalogue_schema %+v", kind, r)
		}
	}
}

// TestCheckCatalogueMissingOrInvalid: a missing catalogue and one that is not JSON are each one failure at
// pointer ""; no catalogue is returned.
func TestCheckCatalogueMissingOrInvalid(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, catalogue.SchemaCopyFileName), schemas.CatalogueSchema(), 0o644)
	r, cat := CheckCatalogue(dir)
	if r.Passed || cat != nil || !slices.Equal(pointers(r.Failures), []string{""}) || !strings.Contains(r.Failures[0].Message, "missing") {
		t.Errorf("a missing catalogue: %+v", r)
	}
	os.WriteFile(filepath.Join(dir, catalogue.CatalogueFileName), []byte("{ nope"), 0o644)
	r, cat = CheckCatalogue(dir)
	if r.Passed || cat != nil || !slices.Equal(pointers(r.Failures), []string{""}) || !strings.Contains(r.Failures[0].Message, "not strict JSON") {
		t.Errorf("an invalid catalogue: %+v", r)
	}
}

// TestCheckCatalogueSchemaCopy: a schema copy that differs from the source is one failure
// at pointer "" naming the copy; the catalogue itself was valid, so it is still returned for the per-entry checks. A
// missing copy fails the same way.
func TestCheckCatalogueSchemaCopy(t *testing.T) {
	r, cat := CheckCatalogue(testdata + "/corpus/schema-copy")
	if r.Passed || cat == nil || len(r.Failures) != 1 || r.Failures[0].Pointer != "" ||
		r.Failures[0].Message != "corpus schema copy catalogue.schema.json differs from the source schema under schemas/" {
		t.Errorf("a differing copy: %+v (catalogue returned %v)", r, cat != nil)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, catalogue.CatalogueFileName), validBytes(t), 0o644)
	r, cat = CheckCatalogue(dir)
	if r.Passed || cat == nil || len(r.Failures) != 1 || !strings.Contains(r.Failures[0].Message, "missing beside the catalogue") {
		t.Errorf("a missing copy: %+v", r)
	}
	// Both: a missing catalogue and a missing copy are two failures, both at "".
	r, _ = CheckCatalogue(t.TempDir())
	if len(r.Failures) != 2 {
		t.Errorf("a folder with neither: %+v", r)
	}
}

// TestSeedCatalogueValidates: a catalogue built from the seed definitions, with fixture values for what the
// generation records, validates against schemas/.
func TestSeedCatalogueValidates(t *testing.T) {
	var cat contract.Catalogue
	for _, s := range model.Seeds() {
		e := contract.CatalogueEntry{
			ID: s.ID, Cap: s.Cap, ProtocolVersion: 30, Source: "local stellar-core", Synthetic: s.Synthetic,
			ExpectedDecodedValues: contract.ExpectedDecodedValues{ReproducedByEveryRun: []contract.ReproducedValue{}, LedgerDependent: []contract.LedgerDependentValue{}},
			FilePath:              catalogue.EntryFilePath(s.ID), XdrType: s.XdrType, Sha256: strings.Repeat("0", 64), Length: 1,
		}
		if s.RecordsNetworkRefusal {
			e.NetworkRefusalEvidence = &contract.NetworkRefusalEvidence{SubmittedEnvelopeXdr: "AAAA", ResultCode: contract.NetworkRefusalResultCodeChangeTrustMalformed}
		}
		cat.Entries = append(cat.Entries, e)
	}
	b, err := catalogue.EncodeCatalogue(cat)
	if err != nil {
		t.Fatal(err)
	}
	if got, failures := ValidateCatalogue(b); got == nil || len(failures) != 0 {
		t.Errorf("the seeds' catalogue: %+v", failures)
	}
}
