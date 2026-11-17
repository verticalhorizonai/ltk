package main

// `ltk list <folder> [--json]`: the catalogue's entries as tab-separated text or as a ListOutput. It needs no
// container.

import (
	"context"
	"flag"
	"fmt"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
)

// listCommand is `ltk list`.
func listCommand() *command {
	return &command{
		name:        "list",
		synopsis:    []string{"ltk list <folder> [--json]"},
		positionals: []string{"<folder>"},
		setup: func(fs *flag.FlagSet) func(context.Context, invocation) error {
			asJSON := fs.Bool("json", false, "print ListOutput as JSON instead of text")
			return func(_ context.Context, inv invocation) error {
				return runList(inv, *asJSON)
			}
		},
	}
}

// runList prints the listing of the folder's catalogue. Exit 0 when the catalogue was read and every entry printed; a
// missing folder or an unreadable catalogue is a usage error (exit 2) with nothing on stdout.
func runList(inv invocation, asJSON bool) error {
	entries, err := catalogue.List(inv.args[0])
	if err != nil {
		return fmt.Errorf("ltk list: %w", err)
	}
	if asJSON {
		return catalogue.WriteJSON(inv.stdout, entries)
	}
	return catalogue.WriteListText(inv.stdout, entries)
}
