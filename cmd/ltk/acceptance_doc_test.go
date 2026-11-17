package main

// The acceptance run's document, docs/acceptance-run.md: it says who the reader was, what the run did not cover, and
// the one count it states that the tree can confirm.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
)

// TestAcceptanceRunDocument: the document says the reader was an AI agent session and not a human tester, that no
// person performed the steps, and names what the run did not cover; the number of corpus entries it states is the
// committed catalogue's.
func TestAcceptanceRunDocument(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "docs", "acceptance-run.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(strings.ReplaceAll(string(b), "\r\n", "\n")), " ")
	for _, want := range []string{
		"The reader was an AI agent session, not a human tester",
		"no person performed these steps",
		"a host Go toolchain",
		"first-time image pulls",
		"hands-on use on Linux and Windows",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/acceptance-run.md does not say %q", want)
		}
	}
	cat, err := catalogue.ReadCatalogue(filepath.Join(readmeModuleRoot, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "the corpus's " + numberWord(len(cat.Entries)) + " entries"; !strings.Contains(doc, want) {
		t.Errorf("docs/acceptance-run.md does not say %q, as the committed catalogue counts", want)
	}
}
