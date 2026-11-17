//go:build integration

package orchestrator

// The seed corpus's integration verification, Docker tier. It runs natively, one test binary at a time, with nothing
// else using Docker, and with a PATH that holds no wrapper that runs the go tool in a container: through such a
// wrapper, `go build -o <host path>` writes inside the container and the harness finds no file (doc.go).
//
//   - `ltk check corpus/` run from the module root, as the README and the CI's Linux job run it: exit 0, nothing on
//     standard error, and a report in which the catalogue schema passes and each of the three entries, in seed order,
//     passes file integrity at its recorded length (1928, 2824, 2244 at protocol 30), the Rust XDR tool's round trip
//     over all its bytes, and its expected values; the run held to the clean stop.
//   - The error state: on a copy of the corpus whose entry 3 has lost its XDR file, `ltk check` exits 1 with the report
//     on standard output (entry 3's fileIntegrity sha256 "", length 0, passed false; entries 1 and 2 passed) and one
//     failure line naming entry 3.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// TestSeedIntegrationCheckCommittedCorpus: `ltk check corpus/` from the module root passes every check of the three
// committed entries, each at the length its catalogue record states.
func TestSeedIntegrationCheckCommittedCorpus(t *testing.T) {
	images := committedImages(t)
	_, entries := readCommittedCatalogue(t)
	r := runObserved(t, moduleRoot, integrationEnv("TMPDIR="+t.TempDir()), "check", "corpus/")
	requireSuccess(t, r)
	requireOneDocument(t, r.stdout)
	ck := readCheckReport(t, r.stdout)
	if ck.Folder != "corpus/" || !ck.Passed || !ck.CatalogueSchema.Passed || len(ck.CatalogueSchema.Failures) != 0 {
		t.Errorf("folder %q passed %v catalogueSchema %+v; want corpus/ as given, passed, no failure", ck.Folder, ck.Passed, ck.CatalogueSchema)
	}
	var ids []string
	for _, e := range ck.Entries {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, seedIDs) || len(entries) != len(seedIDs) {
		t.Fatalf("the report lists %q; want the three seeds in catalogue order %q", ids, seedIDs)
	}
	for i, e := range ck.Entries {
		rec := entries[i]
		fi := e.FileIntegrity
		if !fi.Passed || fi.Length != rec.Length || fi.RecordedLength != rec.Length || fi.Sha256 != rec.Sha256 || fi.RecordedSha256 != rec.Sha256 {
			t.Errorf("%s: fileIntegrity %+v; the record says %d bytes, %s", e.ID, fi, rec.Length, rec.Sha256)
		}
		if want := seedLengths[rec.ProtocolVersion][e.ID]; want != 0 && fi.Length != want {
			t.Errorf("%s: %d bytes at protocol %d, want %d (the length known for an entry captured at that protocol)", e.ID, fi.Length, rec.ProtocolVersion, want)
		}
		if !e.RoundTrip.Passed || e.RoundTrip.BytesCompared != rec.Length {
			t.Errorf("%s: roundTrip %+v; want passed over all %d bytes", e.ID, e.RoundTrip, rec.Length)
		}
		if !e.ExpectedValues.Passed || len(e.ExpectedValues.Mismatches) != 0 {
			t.Errorf("%s: expectedValues passed %v with %d mismatches", e.ID, e.ExpectedValues.Passed, len(e.ExpectedValues.Mismatches))
		}
		if string(e.GoSdkRoundTrip) != "null" {
			t.Errorf("%s: goSdkRoundTrip %s; null without --also-go", e.ID, e.GoSdkRoundTrip)
		}
		t.Logf("%s: %d bytes, round trip over %d bytes, expected values passed", e.ID, fi.Length, e.RoundTrip.BytesCompared)
	}
	requireOneLabelledRun(t, r.w, images)
}

// TestSeedIntegrationCheckMissingEntry3, the error state: with entry 3's XDR file removed from a copy of the corpus,
// `ltk check` exits 1, the report on standard output says entry 3's file is missing (sha256 "", length 0, passed
// false) and entries 1 and 2 pass, and the one failure line names entry 3.
func TestSeedIntegrationCheckMissingEntry3(t *testing.T) {
	dir := copyTree(t, committedCorpus)
	if err := os.Remove(filepath.Join(dir, entry3ID, "entry.xdr")); err != nil {
		t.Fatal(err)
	}
	r := runObserved(t, t.TempDir(), integrationEnv("TMPDIR="+t.TempDir()), "check", dir)
	if r.code != int(contract.EXIT_CODES.Failure) {
		t.Fatalf("exit %d, want 1; stderr %s", r.code, r.stderr)
	}
	line := requireErrorLine(t, r.code, r.stderr)
	if !strings.Contains(line.Message, entry3ID) {
		t.Errorf("the failure line %q does not name %s", line.Message, entry3ID)
	}
	ck := readCheckReport(t, r.stdout)
	if ck.Passed || len(ck.Entries) != len(seedIDs) {
		t.Fatalf("passed %v with %d entries; want false with all three", ck.Passed, len(ck.Entries))
	}
	for _, e := range ck.Entries {
		fi := e.FileIntegrity
		if e.ID == entry3ID {
			if fi.Passed || fi.Sha256 != "" || fi.Length != 0 {
				t.Errorf("%s: fileIntegrity %+v; want sha256 \"\", length 0, passed false (a missing file has no hash and no length)", e.ID, fi)
			}
			continue
		}
		if !fi.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed {
			t.Errorf("%s: fileIntegrity %v roundTrip %v expectedValues %v; the entry is whole", e.ID, fi.Passed, e.RoundTrip.Passed, e.ExpectedValues.Passed)
		}
	}
	t.Logf("exit 1, %q", line.Message)
}
