package check

// The Check runner: the checks of CHECK_ORDER on a corpus folder, every check for every entry, gathered into one
// CheckReport.

import (
	"context"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/schema"
)

// Outcome is a finished check run: the report `ltk check` prints, and the reasons of every failed check, each naming
// its check (and its entry), in the order the checks ran, for the command's error line on standard error; the report
// is printed on standard output even when it did not pass.
type Outcome struct {
	Report  contract.CheckReport
	Reasons []string
}

// Run runs the checks of CHECK_ORDER on the corpus folder:
//
//  1. catalogue_schema: the catalogue against the embedded schema of schemas/, then the rules the schema cannot express
//     (internal/schema), and the schema copy against the source. A catalogue that is missing, not JSON or invalid gives
//     no trustworthy entry list: no per-entry check runs and entries is []. A schema copy that differs does not stop
//     them, since the catalogue itself was validated against the embedded schema.
//  2. For every entry, in catalogue order, every check runs and is reported, even when an earlier one failed:
//     file_integrity, round_trip (through codec), expected_values (read from the decoded form the round trip
//     produced), then, only when goCodec is not nil (`--also-go`), go_sdk_round_trip (crosscheck.go); without it every
//     goSdkRoundTrip is null.
//  3. passed is true only when catalogue_schema passed and every entry's checks passed, its non-null goSdkRoundTrip
//     included.
//
// folder is reported exactly as given. The error is returned only when the run must stop without a report: an
// interrupt (its context cancelled) or a codec that is unavailable; every check failure is in the report.
func Run(ctx context.Context, folder string, codec Codec, goCodec GoCodec) (Outcome, error) {
	schemaResult, cat := schema.CheckCatalogue(folder)
	out := Outcome{Report: contract.CheckReport{
		Folder:          folder,
		CatalogueSchema: schemaResult,
		Entries:         []contract.EntryCheckResult{},
	}}
	if !schemaResult.Passed {
		f := schemaResult.Failures[0]
		out.Reasons = append(out.Reasons, fmt.Sprintf("catalogue_schema: %d failure(s), the first at %q: %s", len(schemaResult.Failures), f.Pointer, f.Message))
	}
	passed := schemaResult.Passed
	if cat != nil {
		for _, e := range cat.Entries {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			r, reasons, err := checkEntry(ctx, folder, e, codec, goCodec)
			if err != nil {
				return out, err
			}
			out.Report.Entries = append(out.Report.Entries, r)
			out.Reasons = append(out.Reasons, reasons...)
			passed = passed && entryPassed(r)
		}
	}
	out.Report.Passed = passed
	return out, nil
}

// entryPassed reports whether every check of one entry passed: file_integrity, round_trip, expected_values and, when it
// ran, go_sdk_round_trip.
func entryPassed(r contract.EntryCheckResult) bool {
	return r.FileIntegrity.Passed && r.RoundTrip.Passed && r.ExpectedValues.Passed && (r.GoSdkRoundTrip == nil || r.GoSdkRoundTrip.Passed)
}

// checkEntry runs one entry's checks in CHECK_ORDER: file_integrity, round_trip, expected_values, then
// go_sdk_round_trip when goCodec is not nil.
func checkEntry(ctx context.Context, folder string, e contract.CatalogueEntry, codec Codec, goCodec GoCodec) (contract.EntryCheckResult, []string, error) {
	r := contract.EntryCheckResult{ID: e.ID}
	var reasons []string

	fi, err := FileIntegrity(folder, e)
	r.FileIntegrity = fi
	if reason := integrityReason(e, fi, err); reason != "" {
		reasons = append(reasons, reason)
	}

	file, readErr := catalogue.ReadEntryFile(folder, e.FilePath)
	entryJSON, jsonErr := catalogue.ReadDecodedJSON(folder, e.FilePath)
	rt, err := RoundTrip(ctx, codec, e, file, readErr, entryJSON, jsonErr)
	if err != nil {
		return r, nil, err
	}
	r.RoundTrip = rt.Result
	if rt.Reason != "" {
		reasons = append(reasons, rt.Reason)
	}

	ev, reason := ExpectedValues(e, rt.Decoded)
	r.ExpectedValues = ev
	if reason != "" {
		reasons = append(reasons, reason)
	}

	if goCodec != nil {
		gr, reason := GoSdkRoundTrip(goCodec, e, file, readErr)
		r.GoSdkRoundTrip = &gr
		if reason != "" {
			reasons = append(reasons, reason)
		}
	}
	return r, reasons, nil
}

// integrityReason names the entry and which of the hash or the length differs, or why the file could not be
// read; "" when the check passed.
func integrityReason(e contract.CatalogueEntry, fi contract.FileIntegrityResult, err error) string {
	prefix := fmt.Sprintf("entry %s: file_integrity: ", e.ID)
	switch {
	case err != nil:
		return prefix + fmt.Sprintf("the XDR file %s cannot be read: %v", e.FilePath, err)
	case fi.Passed:
		return ""
	case fi.Sha256 != fi.RecordedSha256 && fi.Length != fi.RecordedLength:
		return prefix + fmt.Sprintf("the hash and the length differ (the file is %d bytes, the catalogue records %d)", fi.Length, fi.RecordedLength)
	case fi.Sha256 != fi.RecordedSha256:
		return prefix + fmt.Sprintf("the hash differs (the file's is %s, the catalogue records %s); the length is equal", fi.Sha256, fi.RecordedSha256)
	}
	return prefix + fmt.Sprintf("the length differs (the file is %d bytes, the catalogue records %d); the hash is equal", fi.Length, fi.RecordedLength)
}
