package main

// No file cites a record the repository does not carry: a comment or a document says what the code does and the fact
// behind it, and does not point a reader at a numbered decision, a design-decision, requirement, criterion or task
// id, or a planning tool by name.
//
// The scan reads the same files as the scan for statements of duration or cost: every file git lists under the module
// root, tracked or untracked and not ignored, outside the build's own planning files (isPackageOwn), binary files
// skipped. Outside a git work tree it skips, saying why. It does not read this file, which holds the patterns and
// their samples.

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const citationsSelf = "cmd/ltk/citations_test.go"

var citationPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a numbered decision", regexp.MustCompile(`(?i)\brulings?\s+\d`)},
	{"a design-decision id", regexp.MustCompile(`\bAD-\d`)},
	{"a requirement or criterion id", regexp.MustCompile(`\b(?:AC-)?REQ-\d|\bNEG-\d`)},
	{"a numbered task", regexp.MustCompile(`(?i)\btasks?\s+\d+\b`)},
	{"a planning tool", regexp.MustCompile(`(?i)\b(?:criteria ledger|wiring check|wiring census|gap register)\b`)},
}

// citationFindings returns one finding per match in text, "<line>: <pattern>: <match>".
func citationFindings(text string) []string {
	var f []string
	for i, line := range strings.Split(text, "\n") {
		for _, p := range citationPatterns {
			for _, m := range p.re.FindAllString(line, -1) {
				f = append(f, strings.Join([]string{strconv.Itoa(i + 1), p.name, m}, ": "))
			}
		}
	}
	return f
}

// TestNoFileCitesARecordTheRepositoryDoesNotCarry: no file of the repository cites a numbered decision, a planning
// id or a planning tool.
func TestNoFileCitesARecordTheRepositoryDoesNotCarry(t *testing.T) {
	scanned := 0
	for _, p := range gitFiles(t, true) {
		if isPackageOwn(p) || p == citationsSelf {
			continue
		}
		b, err := os.ReadFile(filepath.Join(readmeModuleRoot, filepath.FromSlash(p)))
		if os.IsNotExist(err) {
			continue // tracked, deleted in the work tree
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.IndexByte(b, 0) >= 0 {
			continue // binary
		}
		scanned++
		for _, f := range citationFindings(string(b)) {
			t.Errorf("%s:%s", p, f)
		}
	}
	if scanned == 0 {
		t.Fatal("no file was scanned")
	}
	t.Logf("%d files scanned outside the package's own entries", scanned)
}

// TestCitationPatterns proves the scan is not vacuous and spares what the repository does say.
func TestCitationPatterns(t *testing.T) {
	for _, s := range []string{
		"// Ruling 19(c): the state of a container is its status.",
		"as rulings 3 and 13 say",
		"// (AD-09) the fallback",
		"REQ-04-01 and AC-REQ-04-01-03",
		"NEG-01-07-01",
		"built by task 178",
		"so the wiring check reads it as a script",
		"the spellings the criteria ledger looks for",
	} {
		if len(citationFindings(s)) == 0 {
			t.Errorf("%q is not flagged", s)
		}
	}
	for _, s := range []string{
		"wallet-backend pull request 729 and issue 716",
		"CAP-0040, CAP-0038 and CAP-0027",
		"the multitasking kernel", "a ruling of the network",
		"READ-1 and SPREAD-2",
		"the task of decoding 3 entries",
	} {
		if f := citationFindings(s); len(f) != 0 {
			t.Errorf("%q is flagged: %v", s, f)
		}
	}
}
