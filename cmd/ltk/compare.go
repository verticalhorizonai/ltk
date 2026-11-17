package main

// `ltk compare <folder A> <folder B>`: the comparison of two generations by their decoded JSON. It
// needs no container, checks no container environment and never exits 3.

import (
	"context"
	"flag"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// compareCommand is `ltk compare`.
func compareCommand() *command {
	return &command{
		name:        "compare",
		synopsis:    []string{"ltk compare <folder A> <folder B>"},
		positionals: []string{"<folder A>", "<folder B>"},
		setup: func(*flag.FlagSet) func(context.Context, invocation) error {
			return func(_ context.Context, inv invocation) error {
				return runCompare(inv)
			}
		},
	}
}

// runCompare prints the CompareReport and exits by it:
//   - 0, the report on stdout and nothing on stderr: every difference is on the ledger-dependent list;
//   - 1, the report on stdout and one ErrorLine naming the first difference outside the list;
//   - 2, nothing on stdout: a folder or its catalogue or a decoded JSON file is missing or unreadable, or the two
//     catalogues do not list the same entry ids.
func runCompare(inv invocation) error {
	report, err := catalogue.Compare(inv.args[0], inv.args[1])
	if err != nil {
		return err
	}
	if err := catalogue.WriteJSON(inv.stdout, report); err != nil {
		return err
	}
	if report.Passed {
		return nil
	}
	id, pointer, n := catalogue.Unlisted(report)
	if n > 1 {
		return model.Failure("difference outside the ledger-dependent list in entry %s at %s (%d differences outside the list in all)", id, pointer, n)
	}
	return model.Failure("difference outside the ledger-dependent list in entry %s at %s", id, pointer)
}
