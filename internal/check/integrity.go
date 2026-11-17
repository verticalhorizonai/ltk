package check

// The check file_integrity: the SHA-256 and the length of an entry's XDR file against its catalogue record.

import (
	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
)

// FileIntegrity hashes and measures the XDR file of entry e, at e.FilePath relative to folder, and compares both with
// the catalogue's sha256 and length, reported as recordedSha256 and recordedLength. passed is true only when both are
// equal: a hash that differs fails it though the length matches, and a length that differs fails it though the hash
// matches. The result belongs to the EntryCheckResult whose id names the entry.
//
// A file that cannot be read (missing, or a folder in its place) is reported with sha256 "" and length 0, passed
// false (a hash of zero bytes would tell a reader the file exists and is empty);
// the error says why, for the check's error line.
func FileIntegrity(folder string, e contract.CatalogueEntry) (contract.FileIntegrityResult, error) {
	r := contract.FileIntegrityResult{
		Check:          contract.CheckNameFileIntegrity,
		RecordedSha256: e.Sha256,
		RecordedLength: e.Length,
	}
	b, err := catalogue.ReadEntryFile(folder, e.FilePath)
	if err != nil {
		return r, err
	}
	r.Sha256, r.Length = catalogue.Measure(b)
	r.Passed = r.Sha256 == e.Sha256 && r.Length == e.Length
	return r, nil
}
