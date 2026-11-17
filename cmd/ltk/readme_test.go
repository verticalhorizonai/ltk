package main

// The README consistency test.
//
// The README shows each run as a fenced `text` block: `$ <command>`, the standard output byte for byte, then
// `[exit N]`. This file holds the README to the program and to the committed data:
//
//   - `ltk --help`, `ltk list corpus/` and `ltk list corpus/ --json` are run again, from the module root, and must be
//     byte-identical; `ltk check corpus/` is run again the same way in the integration tier
//     (readme_integration_test.go), because its round trip needs Docker;
//   - the two `ltk repro` runs are the committed reports in repro/, byte for byte (the command prints the bytes it
//     writes);
//   - the `ltk generate --all --out corpus` run is the committed corpus's: its records and hashes are the catalogue's;
//   - every command of `ltk --help` is documented with a run and its exit code, and every exit code agrees
//     with the report's `passed`;
//   - the first page states the scope (three entries are a seed; each entry's id, CAP, protocol, type, length and
//     synthetic flag as the catalogue records them; which entries the deciding test, the feasibility run made before
//     this tool, captured; the protocol-28 lengths; entry 2's evidence; entry 3 and wallet-backend 716; entry 2's
//     change order with the run counts its recipe records; the reproductions' outcomes as repro/ records them; what
//     the kit's first release would add; what the seed does not do), every protocol-28 fallback of the catalogue, and
//     the statement of how the work was done, including AI tools;
//   - LICENSE is the Apache License 2.0;
//   - neither planning declaration line ("Product kind:", "Database:") is in the README.
//
// README.md's CRLF line endings are read as LF, so a Windows checkout with autocrlf gives the same result as CI.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/repro/walletbackend"
)

// readmeModuleRoot is the module root, relative to this package: the working directory of every README run.
const readmeModuleRoot = "../.."

// readmeRun is one run the README shows: the command after `$ `, its standard output and its exit code.
type readmeRun struct {
	line    int
	command string
	stdout  string
	exit    int
}

// args is the command's arguments after `ltk`, split on spaces (no README command quotes an argument).
func (r readmeRun) args() []string { return strings.Fields(r.command)[1:] }

var reExitLine = regexp.MustCompile(`^\[exit ([0-9]+)\]$`)

// readReadme returns README.md's text with CRLF read as LF.
func readReadme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "README.md"))
	if err != nil {
		t.Fatalf("README.md: %v", err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// readmeRuns parses every run of the README: a ```text block whose first line is `$ ltk …` and whose last line is
// `[exit N]`. A run block without its exit line is an error.
func readmeRuns(text string) ([]readmeRun, error) {
	var runs []readmeRun
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if lines[i] != "```text" || i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "$ ltk") {
			continue
		}
		run := readmeRun{line: i + 2, command: strings.TrimPrefix(lines[i+1], "$ ")}
		end := -1
		for j := i + 2; j < len(lines); j++ {
			if lines[j] == "```" {
				end = j
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("line %d: the run of %q has no closing fence", run.line, run.command)
		}
		m := reExitLine.FindStringSubmatch(lines[end-1])
		if end-1 < i+2 || m == nil {
			return nil, fmt.Errorf("line %d: the run of %q does not end with [exit N]", run.line, run.command)
		}
		run.exit, _ = strconv.Atoi(m[1])
		if body := lines[i+2 : end-1]; len(body) > 0 {
			run.stdout = strings.Join(body, "\n") + "\n"
		}
		runs = append(runs, run)
		i = end
	}
	return runs, nil
}

// readmeRunsOf returns the README's runs, failing the test when they do not parse.
func readmeRunsOf(t *testing.T, text string) []readmeRun {
	t.Helper()
	runs, err := readmeRuns(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatal("README.md shows no run (```text blocks starting with `$ ltk`)")
	}
	return runs
}

// readmeRunOf returns the one run of command; it fails the test when there is none or more than one.
func readmeRunOf(t *testing.T, runs []readmeRun, command string) readmeRun {
	t.Helper()
	var found []readmeRun
	for _, r := range runs {
		if r.command == command {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("README.md shows %d runs of %q, want 1", len(found), command)
	}
	return found[0]
}

// readmeFirstPage is the README's first page: everything before its "## Contents" heading.
func readmeFirstPage(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, "\n## Contents\n")
	if i < 0 {
		t.Fatal(`README.md has no "## Contents" heading after its first page`)
	}
	return text[:i]
}

// readmeSection is the text of the heading's section, up to the next heading of the same or a higher level.
func readmeSection(t *testing.T, text, heading string) string {
	t.Helper()
	level := strings.Index(heading, " ")
	i := strings.Index(text, "\n"+heading+"\n")
	if i < 0 {
		t.Fatalf("README.md has no %q section", heading)
	}
	rest := text[i+len(heading)+2:]
	for _, l := range regexp.MustCompile(`(?m)^#{1,`+strconv.Itoa(level)+`} `).FindAllStringIndex(rest, -1) {
		return rest[:l[0]]
	}
	return rest
}

// TestReadmeRunsAreTheProgramsOutput: `ltk --help`, `ltk list corpus/` and
// `ltk list corpus/ --json`, run from the module root on a PATH with no program at all, exit as the README states,
// write nothing to standard error, and print the README's output byte for byte.
func TestReadmeRunsAreTheProgramsOutput(t *testing.T) {
	runs := readmeRunsOf(t, readReadme(t))
	for _, command := range []string{"ltk --help", "ltk list corpus/", "ltk list corpus/ --json"} {
		t.Run(command, func(t *testing.T) {
			r := readmeRunOf(t, runs, command)
			code, stdout, stderr := runLTKIn(t, readmeModuleRoot, noDockerEnv(t), r.args()...)
			if code != r.exit || len(stderr) != 0 {
				t.Errorf("%s: exit %d stderr %q; the README (line %d) states exit %d and nothing on stderr", command, code, stderr, r.line, r.exit)
			}
			if string(stdout) != r.stdout {
				t.Errorf("%s: standard output differs from the README's (line %d)\n--- program\n%s--- README\n%s", command, r.line, stdout, r.stdout)
			}
		})
	}
}

// TestReadmeDocumentsEveryCommand: every synopsis the
// program prints for `ltk --help` is in the README, with at least one run of that command; every run states an exit
// code of the exit-code map, and a run whose report has `passed` exits 0 exactly when it is true.
func TestReadmeDocumentsEveryCommand(t *testing.T) {
	text := readReadme(t)
	runs := readmeRunsOf(t, text)
	help := readmeRunOf(t, runs, "ltk --help")
	synopses := strings.Split(strings.TrimSuffix(help.stdout, "\n"), "\n")[1:]
	if len(synopses) != 7 {
		t.Fatalf("`ltk --help` in the README lists %d synopses, want the Master Command Table's 7: %q", len(synopses), synopses)
	}
	for _, syn := range synopses {
		if !strings.Contains(text, "`"+syn+"`") {
			t.Errorf("the synopsis %q is not written in the README's text", syn)
		}
		// The command's fixed words: those before its first placeholder or optional flag.
		fixed := syn
		if i := strings.IndexAny(syn, "<["); i >= 0 {
			fixed = strings.TrimSpace(syn[:i])
		}
		n := 0
		for _, r := range runs {
			if r.command == fixed || strings.HasPrefix(r.command, fixed+" ") {
				n++
			}
		}
		if n == 0 {
			t.Errorf("no run of %q (%s) is shown with its output", fixed, syn)
		}
	}
	for _, r := range runs {
		if r.exit < 0 || r.exit > 3 {
			t.Errorf("line %d: %s states exit %d, not a code of the exit-code map", r.line, r.command, r.exit)
		}
		if !strings.HasPrefix(r.stdout, "{") {
			continue
		}
		var doc struct {
			Passed *bool `json:"passed"`
			Check  *struct {
				Passed bool `json:"passed"`
			} `json:"check"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
			t.Errorf("line %d: the output of %s is not one JSON document: %v", r.line, r.command, err)
			continue
		}
		passed := doc.Passed
		if passed == nil && doc.Check != nil {
			passed = &doc.Check.Passed
		}
		if passed != nil && (*passed != (r.exit == 0)) {
			t.Errorf("line %d: %s states exit %d with passed %v", r.line, r.command, r.exit, *passed)
		}
	}
}

// TestReadmeReproRunsAreTheCommittedReports: the README's two `ltk repro` runs are the reports committed in repro/,
// byte for byte (the command prints the bytes it writes).
func TestReadmeReproRunsAreTheCommittedReports(t *testing.T) {
	runs := readmeRunsOf(t, readReadme(t))
	for _, target := range []string{"wallet-backend-729", "js-sdk-1766"} {
		r := readmeRunOf(t, runs, "ltk repro "+target)
		b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "repro", target+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if r.stdout != strings.ReplaceAll(string(b), "\r\n", "\n") {
			t.Errorf("the README's run of ltk repro %s (line %d) is not repro/%s.json byte for byte", target, r.line, target)
		}
	}
}

// TestReadmeGenerateRunIsTheCommittedCorpus: the README's `ltk generate --all --out corpus` run wrote the committed
// corpus: each shape is the catalogue's record (id, protocol, fallback) and generated, and its check reports each
// committed file's SHA-256 and length; the compare and --shape runs are consistent with their own commands.
func TestReadmeGenerateRunIsTheCommittedCorpus(t *testing.T) {
	runs := readmeRunsOf(t, readReadme(t))
	cat, err := catalogue.ReadCatalogue(filepath.Join(readmeModuleRoot, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	var rep contract.GenerateReport
	r := readmeRunOf(t, runs, "ltk generate --all --out corpus")
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Out != "corpus" || len(rep.Shapes) != len(cat.Entries) || rep.Check == nil || len(rep.Check.Entries) != len(cat.Entries) {
		t.Fatalf("the generate run: out %q, %d shapes, check %v; want corpus and the catalogue's %d entries", rep.Out, len(rep.Shapes), rep.Check != nil, len(cat.Entries))
	}
	for i, e := range cat.Entries {
		s, c := rep.Shapes[i], rep.Check.Entries[i]
		if s.ID != e.ID || s.ProtocolVersion != e.ProtocolVersion || s.ProtocolFallback != e.ProtocolFallback || !s.Generated {
			t.Errorf("shape %d of the generate run is %+v; the catalogue records %s at %d (fallback %v)", i, s, e.ID, e.ProtocolVersion, e.ProtocolFallback)
		}
		if c.ID != e.ID || c.FileIntegrity.Sha256 != e.Sha256 || c.FileIntegrity.Length != e.Length {
			t.Errorf("the generate run checked %s as %s, %d bytes; the catalogue records %s, %d", c.ID, c.FileIntegrity.Sha256, c.FileIntegrity.Length, e.Sha256, e.Length)
		}
	}

	for _, r := range runs {
		args := r.args()
		switch {
		case args[0] == "compare":
			var cr contract.CompareReport
			if err := json.Unmarshal([]byte(r.stdout), &cr); err != nil {
				t.Fatal(err)
			}
			if len(args) != 3 || cr.FolderA != args[1] || cr.FolderB != args[2] {
				t.Errorf("line %d: the compare report's folders %q, %q are not the command's %q", r.line, cr.FolderA, cr.FolderB, args[1:])
			}
			for _, e := range cr.Entries {
				for _, d := range e.Differ {
					if d.LedgerDependentField == nil && cr.Passed {
						t.Errorf("line %d: %s differs at %s outside the ledger-dependent list, yet passed", r.line, e.ID, d.Pointer)
					}
				}
			}
		case args[0] == "generate" && args[1] == "--shape":
			var gr contract.GenerateReport
			if err := json.Unmarshal([]byte(r.stdout), &gr); err != nil {
				t.Fatal(err)
			}
			if len(gr.Shapes) != 1 || gr.Shapes[0].ID != args[2] || gr.Out != args[4] {
				t.Errorf("line %d: the --shape report generated %+v into %q; the command asks %s into %s", r.line, gr.Shapes, gr.Out, args[2], args[4])
			}
		}
	}
}

// TestReadmeFirstPage: the first page states the scope, and what it states is what the committed
// catalogue, the recipe document of entry 2 and the committed reports record.
func TestReadmeFirstPage(t *testing.T) {
	text := readReadme(t)
	page := readmeFirstPage(t, text)
	cat, err := catalogue.ReadCatalogue(filepath.Join(readmeModuleRoot, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Entries) != 3 {
		t.Fatalf("the committed catalogue has %d entries; the first page states three", len(cat.Entries))
	}
	flat := strings.Join(strings.Fields(page), " ") // the page's words, its line breaks read as spaces
	for _, phrase := range []string{
		"Three entries are a seed, not a corpus.",
		"### What each entry demonstrates",
		"### What the kit's first release would add",
		"It does not replay stored entries into an indexer.",
		"It makes no comparison with stellar-core's golden files.",
		"No command compares anything with a golden file.",
		"It has no server, no database and no web interface.",
		"It does not claim the bytes reproduce.",
		"the most recent stable protocol",
		"1,920 bytes", "2,816 bytes", "protocol 28 only",
		"`CHANGE_TRUST_MALFORMED`",
		"wallet-backend issue 716",
		"the seed does not reproduce it",
		"An observation, not a defect",
	} {
		if !strings.Contains(flat, phrase) {
			t.Errorf("the first page does not say %q", phrase)
		}
	}

	// The entries' table: id, CAP, protocol, type, length and synthetic as the catalogue records them (and so as `ltk
	// list corpus/` prints them), and which entries the deciding test, the feasibility run made before this tool,
	// captured: entries 1 and 2, with entry 3 unverified.
	rows := readmeTableRows(page, "| # | Entry id | CAP | Protocol version | XDR type | Length (bytes) | Synthetic | Captured in the deciding test |")
	if len(rows) != len(cat.Entries) {
		t.Fatalf("the first page's entry table has %d rows, want %d", len(rows), len(cat.Entries))
	}
	for i, e := range cat.Entries {
		want := []string{strconv.Itoa(i + 1), "`" + e.ID + "`", e.Cap, strconv.Itoa(e.ProtocolVersion), e.XdrType, strconv.Itoa(e.Length), strconv.FormatBool(e.Synthetic)}
		got := rows[i]
		if len(got) != 8 || strings.Join(got[:7], "|") != strings.Join(want, "|") {
			t.Errorf("entry table row %d is %q, want %q and the deciding test's column", i+1, got, want)
			continue
		}
		covered := got[7]
		if i < 2 && covered != "yes" || i == 2 && !strings.Contains(covered, "unverified") {
			t.Errorf("entry %d: the deciding test's column says %q (entries 1 and 2 captured in it, entry 3 unverified)", i+1, covered)
		}
		if !strings.Contains(flat, fmt.Sprintf("**Entry %d, `%s` (%s", i+1, e.ID, e.Cap)) {
			t.Errorf("the first page does not say what entry %d (%s) demonstrates", i+1, e.ID)
		}
	}

	// The protocol and the fallback: every fallback is named; with none, the page says so.
	if missing := firstPageNamesFallbacks(page, cat.Entries); len(missing) != 0 {
		t.Errorf("the first page does not state the protocol-28 fallback of %v", missing)
	}
	fellBack := false
	for _, e := range cat.Entries {
		fellBack = fellBack || e.ProtocolFallback
		if !strings.Contains(flat, fmt.Sprintf("captured at protocol %d", e.ProtocolVersion)) {
			t.Errorf("the first page does not state protocol %d, at which %s was captured", e.ProtocolVersion, e.ID)
		}
	}
	if none := strings.Contains(flat, "no entry fell back to protocol 28"); none == fellBack {
		t.Errorf("the first page says no entry fell back (%v), and the catalogue records a fallback (%v)", none, fellBack)
	}

	// Entry 2's change order: the run counts are the recipe document's.
	runsTotal, orders := recipeOrderCounts(t)
	for _, phrase := range []string{fmt.Sprintf("In %s recorded runs", numberWord(runsTotal)), fmt.Sprintf("there were %s different orders", numberWord(orders))} {
		if !strings.Contains(flat, phrase) {
			t.Errorf("the first page does not say %q, as entry 2's recipe document counts", phrase)
		}
	}

	// The reproductions' outcomes, as repro/ records them.
	var wb contract.WalletBackend729Report
	var js contract.JsSdk1766Report
	readmeReport(t, "wallet-backend-729", &wb)
	readmeReport(t, "js-sdk-1766", &js)
	if !wb.Passed || !js.Passed || len(wb.Commits) != 2 || len(js.Versions) != 2 {
		t.Fatalf("the committed reports are not passing before/after reports: %+v %+v", wb, js)
	}
	pre, fix := wb.Commits[0], wb.Commits[1]
	for _, phrase := range []string{
		fmt.Sprintf("at protocol %d", wb.ProtocolVersion),
		fmt.Sprintf("which landed in ledger %d", pre.Ledger),
		fmt.Sprintf("`%s` (the commit before the fix)", pre.Commit),
		fmt.Sprintf("`%s` (the fix)", fix.Commit),
		fmt.Sprintf("(`retries: %d`)", pre.Retries),
		fmt.Sprintf("(`restarts: %d`)", pre.Restarts),
		"stored the `SIGNER` row",
		`with "` + derefString(js.Versions[0].Steps[0].Error) + `"`,
		"The report's `passed` is `true`.",
	} {
		if !strings.Contains(flat, phrase) {
			t.Errorf("the first page does not say %q, as repro/ records it", phrase)
		}
	}
	if pre.Ingested || !fix.Ingested || fix.StoredRow == nil || !bytes.Contains(*fix.StoredRow, []byte(`"state_change_category": "SIGNER"`)) ||
		!js.Versions[1].RoundTripIdentical {
		t.Errorf("the committed reports do not record what the first page states: %+v", wb)
	}
}

// TestReadmeStatesHowTheWorkWasDone: the first page states how the work was done,
// including the use of AI tools.
func TestReadmeStatesHowTheWorkWasDone(t *testing.T) {
	page := readmeFirstPage(t, readReadme(t))
	s := strings.Join(strings.Fields(readmeSection(t, page, "### How this was made")), " ")
	for _, phrase := range []string{"written by AI coding agents", "human spec owner", "were not edited by hand"} {
		if !strings.Contains(s, phrase) {
			t.Errorf("the statement of how the work was done does not say %q", phrase)
		}
	}
}

// TestLicenceIsApache2: LICENSE is the Apache License 2.0, its terms whole,
// and the README states it.
func TestLicenceIsApache2(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	lic := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(strings.TrimLeft(lic, " \n"), "Apache License\n                           Version 2.0, January 2004\n") {
		t.Error("LICENSE does not begin with the Apache License 2.0's title")
	}
	for _, heading := range []string{
		"TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION",
		"1. Definitions.",
		"2. Grant of Copyright License.",
		"3. Grant of Patent License.",
		"4. Redistribution.",
		"5. Submission of Contributions.",
		"6. Trademarks.",
		"7. Disclaimer of Warranty.",
		"8. Limitation of Liability.",
		"9. Accepting Warranty or Additional Liability.",
		"END OF TERMS AND CONDITIONS",
		"APPENDIX: How to apply the Apache License to your work.",
		`Licensed under the Apache License, Version 2.0 (the "License");`,
	} {
		if !strings.Contains(lic, heading) {
			t.Errorf("LICENSE lacks %q", heading)
		}
	}
	if strings.Contains(lic, "Copyright [yyyy] [name of copyright owner]") || !regexp.MustCompile(`(?m)^   Copyright [0-9]{4} \S`).MatchString(lic) {
		t.Error("LICENSE's notice does not name its year and copyright holder")
	}
	if page := readmeFirstPage(t, readReadme(t)); !strings.Contains(page, "Apache License 2.0: see [`LICENSE`](LICENSE).") {
		t.Error("the README does not state the licence")
	}
}

// TestReadmeHasNoDeclarationLines: the two planning declaration lines, "Product kind:" and "Database:", are not in the
// README.
func TestReadmeHasNoDeclarationLines(t *testing.T) {
	for _, l := range strings.Split(readReadme(t), "\n") {
		if strings.HasPrefix(l, "Product kind:") || strings.HasPrefix(l, "Database:") {
			t.Errorf("README.md holds the planning declaration line %q", l)
		}
	}
}

// readmeExitCodesHeader is the header of the Conventions' table of exit codes.
const readmeExitCodesHeader = "| Exit | `code` on standard error | Meaning |"

// TestReadmeExitCodesAreThePrograms: the Conventions' table of exit codes names each non-zero exit by the `code` the
// program writes on its error line (the exit-code mapper's own map, exitCodeOf), and exit 0 by no code at all; the
// notation's key names, which the program never prints, are not used.
func TestReadmeExitCodesAreThePrograms(t *testing.T) {
	text := readReadme(t)
	// The table sits in a list item, indented by two spaces.
	conventions := strings.ReplaceAll(readmeSection(t, text, "## Conventions"), "\n  |", "\n|")
	rows := readmeTableRows(conventions, readmeExitCodesHeader)
	if len(rows) != len(exitCodeOf)+1 {
		t.Fatalf("the exit-code table has %d rows, want exit 0 and the program's %d error codes", len(rows), len(exitCodeOf))
	}
	byExit := map[string][]string{}
	for _, row := range rows {
		if len(row) != 3 {
			t.Fatalf("the exit-code table's row %q has %d cells, want 3", row, len(row))
		}
		byExit[row[0]] = row
	}
	if row, ok := byExit[fmt.Sprintf("`%d`", contract.EXIT_CODES.Success)]; !ok || strings.Contains(row[1], "`") {
		t.Errorf("the exit-code table's row for exit %d is %q; want one that names no code (nothing is written)", contract.EXIT_CODES.Success, row)
	}
	for code, exit := range exitCodeOf {
		row, ok := byExit[fmt.Sprintf("`%d`", exit)]
		if !ok || row[1] != "`"+string(code)+"`" {
			t.Errorf("the exit-code table's row for exit %d is %q; the program writes the code %q", exit, row, code)
		}
	}
	for _, name := range []string{"`usageError`", "`environmentMissing`"} {
		if strings.Contains(text, name) {
			t.Errorf("the README names an exit code %s, which the program never prints", name)
		}
	}
}

// TestReadmeSaysExactlyWhatItHolds: the README says what its three entries are (each the close metadata of one
// ledger, not a LedgerEntry); states the protocol's order of a pool's assets in full; does not present the compare
// sample's differences as the same in every run; names the label and the names of everything the tool starts; and
// warns that `ltk repro` overwrites the committed report and that `ltk compare` reads only the decoded JSON. Entry 2's
// recipe document states the same order.
func TestReadmeSaysExactlyWhatItHolds(t *testing.T) {
	text := readReadme(t)
	flat := strings.Join(strings.Fields(text), " ")
	for _, gone := range []string{
		"three ledger entries",
		"The protocol orders a pool's assets by key bytes.",
		"everything else in them is the same in every run",
	} {
		if strings.Contains(flat, gone) {
			t.Errorf("the README still says %q", gone)
		}
	}
	page := strings.Join(strings.Fields(readmeFirstPage(t, text)), " ")
	for _, want := range []string{
		"three corpus entries, each the close metadata (`LedgerCloseMeta`) of one ledger, captured from stellar-core",
		"by asset type, then asset code, then issuer, and compares issuers by their key bytes",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the first page does not say %q", want)
		}
	}
	section := func(heading string) string {
		return strings.Join(strings.Fields(readmeSection(t, text, heading)), " ")
	}
	checks := []struct{ heading, want string }{
		{"## Conventions", "which pointers differ, and how many, depends on whether the two generations land in the same ledgers"},
		{"## ltk compare", "which pointers differ, and how many, depends on whether the two generations land in the same ledgers"},
		{"## ltk compare", "The sample shows one pair of runs; what holds in every run is `passed`."},
		{"## ltk compare", "run `ltk check` on a corpus before comparing it"},
		{"## Platforms", "`" + orchestration.LabelManaged + "=true` and `" + orchestration.LabelRun + "=<run id>`"},
		{"## Platforms", "is named `ltk-<run id>-…`"},
		{"## Platforms", "`docker ps -a --filter label=" + orchestration.LabelManaged + "=true`"},
		{"### The two reproductions", "overwrites the committed report"},
		{"### The two reproductions", "`git checkout repro/`"},
		{"## Development", "overwrites the committed report"},
		{"## Development", "`git checkout repro/`"},
	}
	for _, c := range checks {
		if !strings.Contains(section(c.heading), c.want) {
			t.Errorf("the README's %q section does not say %q", c.heading, c.want)
		}
	}
	if strings.Contains(flat, "(12, 15 and 26 ledger-dependent differences)") {
		t.Error("the compare sample's title gives its counts as if they were fixed")
	}
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "internal", "generate", "recipes", "cap-0038-pool-share-trustline.md"))
	if err != nil {
		t.Fatal(err)
	}
	recipe := strings.Join(strings.Fields(string(b)), " ")
	if !strings.Contains(recipe, "by asset type, then asset code, then issuer, and compares issuers by their key bytes") ||
		strings.Contains(recipe, "(the protocol's order)") {
		t.Error("entry 2's recipe document does not state the protocol's order of a pool's assets in full")
	}
}

// readmeTableRows returns the cells of the rows of the Markdown table whose header is header.
func readmeTableRows(text, header string) [][]string {
	var rows [][]string
	in := false
	for _, l := range strings.Split(text, "\n") {
		switch {
		case l == header:
			in = true
		case in && strings.HasPrefix(l, "|---"):
		case in && strings.HasPrefix(l, "|"):
			cells := strings.Split(strings.Trim(l, "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			rows = append(rows, cells)
		case in:
			return rows
		}
	}
	return rows
}

// recipeOrderCounts reads entry 2's recipe document: the number of runs and of different orders its observation
// table records.
func recipeOrderCounts(t *testing.T) (runs, orders int) {
	t.Helper()
	f, err := os.Open(filepath.Join(readmeModuleRoot, "internal", "generate", "recipes", "cap-0038-pool-share-trustline.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "| Runs | Protocol |"):
			in = true
		case in && strings.HasPrefix(l, "|---"):
		case in && strings.HasPrefix(l, "|"):
			n, err := strconv.Atoi(strings.TrimSpace(strings.Split(l, "|")[1]))
			if err != nil {
				t.Fatalf("entry 2's recipe: the row %q has no run count", l)
			}
			runs += n
			orders++
		case in:
			return runs, orders
		}
	}
	if orders == 0 {
		t.Fatal("entry 2's recipe document has no table of change orders")
	}
	return runs, orders
}

// numberWord writes n as the README does: a word up to twelve, digits above.
func numberWord(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

// readmeReport decodes the committed report of target.
func readmeReport(t *testing.T, target string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "repro", target+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// derefString is *s, or "" for nil.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestReadmeRunsParse holds the README's run reader to its rules: a block without [exit N] is an error, the output is
// the lines between the command and the exit line, and other blocks are not runs.
func TestReadmeRunsParse(t *testing.T) {
	runs, err := readmeRuns("x\n```text\n$ ltk list f\na\tb\n[exit 2]\n```\n```sh\n$ ltk no\n```\n```text\n$ ltk --help\n[exit 0]\n```\n")
	if err != nil || len(runs) != 2 || runs[0].stdout != "a\tb\n" || runs[0].exit != 2 || runs[1].stdout != "" || runs[0].command != "ltk list f" {
		t.Errorf("runs %+v, err %v", runs, err)
	}
	if _, err := readmeRuns("```text\n$ ltk list f\nout\n```\n"); err == nil {
		t.Error("a run with no exit line was read")
	}
}

// readmeUpstreamLinks are the upstream items the README links: wallet-backend's pull request 729 and issue 716, and
// js-stellar-sdk's pull request 1766 and its two releases.
var readmeUpstreamLinks = []string{
	"https://github.com/stellar/wallet-backend/pull/729",
	"https://github.com/stellar/wallet-backend/issues/716",
	"https://github.com/stellar/js-stellar-sdk/pull/1766",
	"https://github.com/stellar/js-stellar-sdk/releases/tag/v17.2.0",
	"https://github.com/stellar/js-stellar-sdk/releases/tag/v17.2.1",
}

// readmeRunSpecificFields are the wallet-backend report's fields that differ from run to run, which the README names.
var readmeRunSpecificFields = []string{"ledger", "logPath", "storeStatePath", "ingested_at", "ledger_created_at", "ledger_number", "to_id", "operation_id"}

// TestReadmeUpstreamAndPrerequisites: the README links the upstream pull requests, issue and releases, says "pull
// request 1766", and gives the two wallet-backend commits' full hashes as the runner pins them; its Platforms section
// lists every pinned image with its digest, its size and each command that pulls it (each command's image set, no
// more), and the network sources the reproductions fetch from; it says which two commands run for minutes without
// output, gives the Windows build line, names the wallet-backend report's run-specific fields, and states no
// build-machine path; and its integration command is the one internal/orchestrator/doc.go gives.
func TestReadmeUpstreamAndPrerequisites(t *testing.T) {
	text := readReadme(t)
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range append(append([]string{}, readmeUpstreamLinks...),
		"js-stellar-sdk pull request 1766",
		"`"+walletbackend.CommitBeforeFixHash+"`", "`"+walletbackend.CommitFixHash+"`",
		"go build -o ltk.exe ./cmd/ltk",
		"`ltk generate` and `ltk repro wallet-backend-729` run for minutes without output",
		"https://github.com/stellar/wallet-backend", "proxy.golang.org", "sum.golang.org", "registry.npmjs.org",
		"contacts no Stellar network but the local one it starts",
	) {
		if !strings.Contains(flat, want) {
			t.Errorf("the README does not say %q", want)
		}
	}
	for _, p := range []string{"/var/folders/", "/tmp/Test", "$VH_WORK"} {
		if strings.Contains(text, p) {
			t.Errorf("the README holds the build machine's path %q", p)
		}
	}

	// The pinned images: each command's set, every image with its digest, its size and the commands that pull it.
	platforms := readmeSection(t, text, "## Platforms")
	rows := readmeTableRows(platforms, "| Image | Pinned digest | Size | Pulled by |")
	sets := map[string][]string{
		"`ltk generate`":                 orchestration.GenerateImages,
		"`ltk check`":                    orchestration.CheckImages,
		"`ltk repro wallet-backend-729`": orchestration.WalletBackendImages,
		"`ltk repro js-sdk-1766`":        orchestration.JsSdkImages,
	}
	reSize := regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(kB|MB|GB)$`)
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) != 4 {
			t.Errorf("the images table's row %q has %d cells, want 4", row, len(row))
			continue
		}
		var image string
		for _, set := range sets {
			for _, im := range set {
				repo, digest, _ := strings.Cut(im, "@")
				if strings.Contains(row[0], "`"+repo+"`") && strings.Contains(row[1], "`"+digest+"`") {
					image = im
				}
			}
		}
		if image == "" {
			t.Errorf("the images table's row %q names no pinned image with its full digest", row)
			continue
		}
		seen[image] = true
		if !reSize.MatchString(row[2]) {
			t.Errorf("the images table gives %s the size %q, not a size as `docker images` shows it", image, row[2])
		}
		for command, set := range sets {
			if pulls, listed := slices.Contains(set, image), strings.Contains(row[3], command); pulls != listed {
				t.Errorf("the images table says %s is pulled by %s: %v; the command's image set says %v", image, command, listed, pulls)
			}
		}
	}
	for _, set := range sets {
		for _, im := range set {
			if !seen[im] {
				t.Errorf("the images table does not list the pinned image %s", im)
			}
		}
	}

	// The wallet-backend report's run-specific fields.
	section := readmeSection(t, text, "## ltk repro wallet-backend-729")
	for _, f := range readmeRunSpecificFields {
		if !strings.Contains(section, "`"+f+"`") {
			t.Errorf("the wallet-backend section does not name the run-specific field %q", f)
		}
	}

	// One integration command, the same in the README and in internal/orchestrator/doc.go.
	var commands []string
	for _, l := range strings.Split(readmeSection(t, text, "## Development"), "\n") {
		if strings.HasPrefix(l, "go test -tags integration") {
			commands = append(commands, l)
		}
	}
	if len(commands) != 1 {
		t.Fatalf("the Development section gives %d integration commands, want 1: %q", len(commands), commands)
	}
	doc, err := os.ReadFile(filepath.Join(readmeModuleRoot, "internal", "orchestrator", "doc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(doc), "go test -tags integration"); n != 1 || !strings.Contains(string(doc), "`"+commands[0]+"`") {
		t.Errorf("internal/orchestrator/doc.go gives %d integration commands, and not the README's %q", n, commands[0])
	}
}
