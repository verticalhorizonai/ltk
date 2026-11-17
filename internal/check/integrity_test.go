package check

// The check file_integrity on the fixture corpora.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

const testdata = "../../testdata"

func integrityOf(t *testing.T, folder string) map[string]contract.FileIntegrityResult {
	t.Helper()
	cat, err := catalogue.ReadCatalogue(folder)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]contract.FileIntegrityResult{}
	for _, e := range cat.Entries {
		r, err := FileIntegrity(folder, e)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		if r.Check != contract.CheckNameFileIntegrity || r.RecordedSha256 != e.Sha256 || r.RecordedLength != e.Length || len(r.Sha256) != 64 {
			t.Errorf("%s: %+v", e.ID, r)
		}
		out[e.ID] = r
	}
	return out
}

// TestFileIntegrityPasses: each entry's file is hashed and measured; equal to the catalogue, it passes.
func TestFileIntegrityPasses(t *testing.T) {
	for id, r := range integrityOf(t, testdata+"/corpus/valid") {
		if !r.Passed || r.Sha256 != r.RecordedSha256 || r.Length != r.RecordedLength {
			t.Errorf("%s: %+v", id, r)
		}
	}
}

// TestFileIntegrityHashOrLength: a hash that differs fails though the length matches; a length that differs fails
// though the hash matches; the computed and recorded values are both reported, and only the altered entry fails, under
// its own id.
func TestFileIntegrityHashOrLength(t *testing.T) {
	cases := []struct {
		kind, failing  string
		hashDiffers    bool
		lengthDiffers  bool
		computedLength int
	}{
		{"hash", model.SeedSignedPayloadSigner, true, false, 1920},
		{"length", model.SeedPoolShareTrustline, false, true, 2816},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			for id, r := range integrityOf(t, testdata+"/corpus/"+c.kind) {
				if id != c.failing {
					if !r.Passed {
						t.Errorf("%s fails though only %s was altered", id, c.failing)
					}
					continue
				}
				if r.Passed || (r.Sha256 != r.RecordedSha256) != c.hashDiffers || (r.Length != r.RecordedLength) != c.lengthDiffers ||
					r.Length != c.computedLength {
					t.Errorf("%s: %+v", id, r)
				}
			}
		})
	}
}

// TestFileIntegrityUnreadable: a missing file, or a folder in its place, is reported as sha256 "" and length 0 with
// passed false, the recorded values kept, and an error saying why.
func TestFileIntegrityUnreadable(t *testing.T) {
	dir := t.TempDir()
	e := contract.CatalogueEntry{ID: "x", FilePath: "x/entry.xdr", Sha256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Length: 0}
	r, err := FileIntegrity(dir, e)
	if err == nil || r.Passed || r.Sha256 != "" || r.Length != 0 || r.RecordedSha256 != e.Sha256 || r.Check != contract.CheckNameFileIntegrity {
		t.Errorf("a missing file: %+v %v", r, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "x", "entry.xdr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r, err := FileIntegrity(dir, e); err == nil || r.Passed || r.Sha256 != "" {
		t.Errorf("a folder in the file's place: %+v %v", r, err)
	}
	// An empty file is not a missing one: it has the hash of zero bytes and passes against that record.
	os.Remove(filepath.Join(dir, "x", "entry.xdr"))
	os.WriteFile(filepath.Join(dir, "x", "entry.xdr"), nil, 0o644)
	if r, err := FileIntegrity(dir, e); err != nil || !r.Passed || r.Sha256 != e.Sha256 {
		t.Errorf("an empty file: %+v %v", r, err)
	}
}
