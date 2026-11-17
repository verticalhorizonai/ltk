package check

// The check report's one renderer, for standard output, and the failure's summary, the message of the error line on
// standard error when the run exits 1.

import (
	"fmt"
	"io"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
)

// WriteReport writes the CheckReport as `ltk check` prints it: one JSON document, two-space indentation, keys in
// CHECK_ORDER (the struct order of internal/contract), one trailing newline, in one write.
func WriteReport(w io.Writer, o Outcome) error {
	return catalogue.WriteJSON(w, o.Report)
}

// Summary is the message of the error line of a run whose report did not pass (exit 1, the report itself still
// printed on standard output): how many entries failed, whether catalogue_schema passed, then each failed check's
// reason in the order the checks ran, each naming its entry. The round trip's first differing offset is stated here
// only: the report's RoundTripResult has no field for it. It is "" for a report that passed.
func Summary(o Outcome) string {
	r := o.Report
	if r.Passed {
		return ""
	}
	failed := 0
	for _, e := range r.Entries {
		if !entryPassed(e) {
			failed++
		}
	}
	schemaState := "catalogue_schema passed"
	if !r.CatalogueSchema.Passed {
		schemaState = "catalogue_schema failed"
	}
	head := fmt.Sprintf("%d of %d entries failed; %s", failed, len(r.Entries), schemaState)
	if len(r.Entries) == 0 && !r.CatalogueSchema.Passed {
		head = "catalogue_schema failed; no entry was checked"
	}
	return strings.Join(append([]string{head}, o.Reasons...), "; ")
}
