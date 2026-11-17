//go:build integration

package main

// The Generate-and-compare acceptance test: `ltk generate --all` into an empty folder exits 0, and `ltk compare` of
// that folder with `corpus/` exits 0.
//
// From the module root, the built program generates every shape into a folder that does not exist yet (empty by
// construction), outside the repository, then compares that folder with the committed `corpus/`. Run serially, with
// nothing else using Docker, on a machine with Docker running Linux containers and the images present or pullable,
// with LTK_BINARY naming a program built for it:
//
//	go test -tags integration -p 1 -timeout 120m -run 'AcceptGenerate' ./cmd/ltk/

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
)

// TestAcceptGenerateAndCompare:
//
// Step 1, `ltk generate --all --out <fresh>`: exit 0, nothing on standard error; the GenerateReport's out is <fresh>,
// every shape (the three seeds, in seed order) generated with error null (protocolVersion and protocolFallback are
// recorded, not judged: a fallback is at 28), check non-null and passed, each entry validated, hashed, round-tripped
// and read back; nothing is left in the engine.
//
// Step 2, `ltk compare <fresh> corpus/`: exit 0, nothing on standard error; the CompareReport, valid against its schema,
// with folderA <fresh> and folderB "corpus/", one comparison per seed, every difference attributed to a
// ledger-dependent field (none null), every reproduced value of the entry among the equal fields, and passed true.
// Neither step writes under corpus/ or repro/.
func TestAcceptGenerateAndCompare(t *testing.T) {
	gen := generateAllRun(t)
	gen.assertCleanStop(t)
	gen.assertCommittedUntouched(t)
	if gen.code != 0 || len(gen.stderr) != 0 {
		t.Fatalf("ltk generate --all: exit %d stderr %s; want 0 and nothing\n%s", gen.code, gen.stderr, gen.stdout)
	}
	fresh := filepath.Join(gen.work, "gen")
	r := decodeGenerateReport(t, gen.stdout)
	var ids []string
	for _, s := range r.Shapes {
		ids = append(ids, s.ID)
		if !s.Generated || s.Error != nil || (s.ProtocolFallback && s.ProtocolVersion != contract.FALLBACK_PROTOCOL_VERSION) {
			t.Errorf("shape %+v", s)
		}
		t.Logf("%s: generated at protocol %d, protocolFallback %v", s.ID, s.ProtocolVersion, s.ProtocolFallback)
	}
	if r.Out != fresh || !slices.Equal(ids, seedIDs()) || r.Check == nil || !r.Check.Passed {
		t.Fatalf("out %q shapes %v check %+v; want %q, the three seeds, passed", r.Out, ids, r.Check, fresh)
	}
	for _, e := range r.Check.Entries {
		if !e.FileIntegrity.Passed || !e.RoundTrip.Passed || e.RoundTrip.BytesCompared != e.FileIntegrity.Length ||
			!e.ExpectedValues.Passed || len(e.ExpectedValues.Mismatches) != 0 || e.GoSdkRoundTrip != nil {
			t.Errorf("check of %s: %+v", e.ID, e)
		}
	}

	root := acceptRoot(t)
	before := treeOf(t, committedCorpusDir)
	code, stdout, stderr := runLTKIn(t, root, nil, "compare", fresh, corpusArg)
	t.Logf("ltk compare %s %s: exit %d; stderr %s\n%s", fresh, corpusArg, code, stderr, stdout)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("ltk compare: exit %d stderr %s; want 0 and nothing", code, stderr)
	}
	assertTreeUnchanged(t, "corpus/", before, treeOf(t, committedCorpusDir))
	assertCompareAgainstCorpus(t, stdout, fresh)
}

// assertCompareAgainstCorpus holds `ltk compare <fresh> corpus/`'s report to step 2 above.
func assertCompareAgainstCorpus(t *testing.T, stdout []byte, fresh string) {
	t.Helper()
	oneJSONValue(t, stdout)
	validateOutput(t, "compare-report.schema.json", stdout)
	var rep contract.CompareReport
	if err := decodeStrict(stdout, &rep); err != nil {
		t.Fatalf("stdout is not a CompareReport: %v", err)
	}
	if rep.FolderA != fresh || rep.FolderB != corpusArg || !rep.Passed {
		t.Errorf("folderA %q folderB %q passed %v; want %q, %q, true", rep.FolderA, rep.FolderB, rep.Passed, fresh, corpusArg)
	}
	cat, err := catalogue.ReadCatalogue(fresh)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i, ec := range rep.Entries {
		ids = append(ids, ec.ID)
		for _, d := range ec.Differ {
			if d.LedgerDependentField == nil {
				t.Errorf("%s: %s differs and is on no ledger-dependent list: %s against %s", ec.ID, d.Pointer, d.ValueA, d.ValueB)
			}
		}
		if i >= len(cat.Entries) || cat.Entries[i].ID != ec.ID {
			continue
		}
		for _, v := range cat.Entries[i].ExpectedDecodedValues.ReproducedByEveryRun {
			if !reproducedIsEqual(v.Pointer, ec) {
				t.Errorf("%s: the reproduced value at %s is not among the equal fields", ec.ID, v.Pointer)
			}
		}
		t.Logf("%s: %d equal fields, %d ledger-dependent differences", ec.ID, len(ec.Equal), len(ec.Differ))
	}
	if !slices.Equal(ids, seedIDs()) {
		t.Errorf("compared %v; want the three seeds in catalogue order", ids)
	}
}

// reproducedIsEqual reports whether a reproduced value's pointer is among the comparison's equal fields: the pointer
// itself when it is a leaf; when it holds an object or an array, every leaf under it is equal and none differs (the
// comparison lists leaves, the catalogue may record a whole value).
func reproducedIsEqual(pointer string, ec contract.EntryComparison) bool {
	if slices.Contains(ec.Equal, pointer) {
		return true
	}
	under := func(p string) bool { return strings.HasPrefix(p, pointer+"/") }
	for _, d := range ec.Differ {
		if d.Pointer == pointer || under(d.Pointer) {
			return false
		}
	}
	return slices.ContainsFunc(ec.Equal, under)
}

// decodeStrict decodes b into v, refusing a field v does not have.
func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
