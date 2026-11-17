package orchestrator

// The verification of `ltk list`, `ltk compare` and `ltk check`, and of their exit codes: each command through the
// built program, from outside, held to its documented behaviour and synopsis and to its output's schema. Unit tier: no
// Docker. Every container-free row runs with a PATH that holds no program (noDockerEnv): such a command never needs
// Docker and never exits 3. Every non-zero exit is held to exactly one ErrorLine valid against its schema
// (requireErrorLine).
//
// The round trip itself needs Stellar's Rust XDR tool in a container: its rows are in phase2_integration_test.go (tag
// integration), run natively.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// The fixture folders of testdata/ that this verification reads.
var (
	fixtureCorpus       = filepath.Join(testdataFolder, "corpus")
	fixtureValid        = filepath.Join(fixtureCorpus, "valid")
	fixtureLedgerOnly   = filepath.Join(testdataFolder, "compare", "ledger-only")
	fixtureReproChanged = filepath.Join(testdataFolder, "compare", "reproduced-changed")
)

// phase2Commands are the three commands this file verifies; the program registers them.
var phase2Commands = []string{"compare", "list", "check"}

// requireRun runs the program in a temporary working folder and holds the result to the exit code want: on 0,
// nothing on standard error; otherwise exactly one ErrorLine whose code corresponds to it, and nothing else on
// standard error. On 2 and 3, nothing on standard output. It returns standard output and the error line (zero on 0).
func requireRun(t *testing.T, env []string, want contract.ExitCode, args ...string) ([]byte, contract.ErrorLine) {
	t.Helper()
	code, stdout, stderr := runLTKIn(t, t.TempDir(), env, args...)
	if code != int(want) {
		t.Fatalf("ltk %q: exit %d, want %d\nstdout %s\nstderr %s", args, code, want, tail(stdout), stderr)
	}
	if code == 0 {
		if len(stderr) != 0 {
			t.Fatalf("ltk %q: exit 0 with stderr %q; want nothing (standard error is silent on exit 0)", args, stderr)
		}
		return stdout, contract.ErrorLine{}
	}
	line := requireErrorLine(t, code, stderr)
	if (want == contract.EXIT_CODES.UsageError || want == contract.EXIT_CODES.EnvironmentMissing) && len(stdout) != 0 {
		t.Fatalf("ltk %q: exit %d with stdout %q; want nothing (exits 2 and 3 write nothing on standard output)", args, code, tail(stdout))
	}
	return stdout, line
}

// absFixture is a fixture folder as an absolute path (the program runs in a temporary working folder).
func absFixture(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestPhase2CommandsRegistered: list, compare and check are registered; each registered command's `--help` and `-h`
// print exactly its documented synopsis, exit 0 and write nothing on standard error.
func TestPhase2CommandsRegistered(t *testing.T) {
	registered := registeredCommands(t)
	for _, name := range phase2Commands {
		if !registered[name] {
			t.Errorf("ltk --help does not list %q; the program registers it", name)
		}
	}
	for _, c := range tableCommands {
		if !registered[c.name] {
			continue
		}
		for _, h := range []string{"--help", "-h"} {
			args := append(strings.Fields(c.name), h)
			stdout, _ := requireRun(t, nil, contract.EXIT_CODES.Success, args...)
			if want := strings.Join(c.synopsis, "\n") + "\n"; string(stdout) != want {
				t.Errorf("ltk %q: stdout %q, want the synopsis %q", args, stdout, want)
			}
		}
	}
}

// listRecord is a ListEntry as a reader reads it from a catalogue, in the field order of the ListEntry type.
type listRecord struct {
	ID              string      `json:"id"`
	Cap             string      `json:"cap"`
	ProtocolVersion json.Number `json:"protocolVersion"`
	XdrType         string      `json:"xdrType"`
	Length          json.Number `json:"length"`
	Synthetic       bool        `json:"synthetic"`
}

// readListRecords reads a catalogue's entries as ListEntry records with the standard library.
func readListRecords(t *testing.T, folder string) []listRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(folder, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Entries []listRecord `json:"entries"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&cat); err != nil {
		t.Fatalf("%s: %v", folder, err)
	}
	return cat.Entries
}

// schemaValidFixtures are the fixture corpora whose catalogue is valid against the catalogue schema (every one but
// schema-pointer, which is broken on purpose).
func schemaValidFixtures() []string {
	var out []string
	for _, d := range []string{"valid", "hash", "length", "round-trip", "expected-value", "schema-copy"} {
		out = append(out, filepath.Join(fixtureCorpus, d))
	}
	for _, p := range []string{fixtureLedgerOnly, fixtureReproChanged} {
		out = append(out, filepath.Join(p, "a"), filepath.Join(p, "b"))
	}
	return out
}

// TestPhase2List: on every schema-valid fixture corpus, with no program on PATH, `ltk list` prints the header and one
// tab-separated line per entry in catalogue order, and `ltk list --json` (flags are accepted before and after the
// folder) prints the ListOutput, valid against list-output.schema.json, two-space indented with a trailing newline,
// each record copied from the catalogue entry of the same id; both exit 0 with nothing on standard error.
func TestPhase2List(t *testing.T) {
	for _, folder := range schemaValidFixtures() {
		folder := absFixture(t, folder)
		t.Run(filepath.Base(filepath.Dir(folder))+"/"+filepath.Base(folder), func(t *testing.T) {
			records := readListRecords(t, folder)
			var text strings.Builder
			text.WriteString("id\tcap\tprotocolVersion\txdrType\tlength\tsynthetic\n")
			for _, r := range records {
				syn := "false"
				if r.Synthetic {
					syn = "true"
				}
				text.WriteString(strings.Join([]string{r.ID, r.Cap, r.ProtocolVersion.String(), r.XdrType, r.Length.String(), syn}, "\t") + "\n")
			}
			stdout, _ := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.Success, "list", folder)
			if string(stdout) != text.String() {
				t.Errorf("ltk list: stdout\n%q\nwant\n%q", stdout, text.String())
			}

			want, err := json.MarshalIndent(records, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, '\n')
			for _, args := range [][]string{{"list", folder, "--json"}, {"list", "--json", folder}, {"list", folder, "--json=true"}} {
				stdout, _ := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.Success, args...)
				requireValid(t, "list-output.schema.json", stdout)
				requireOneDocument(t, stdout)
				if !bytes.Equal(stdout, want) {
					t.Errorf("ltk %q: stdout\n%s\nwant\n%s", args[1:], stdout, want)
				}
			}
		})
	}
}

// TestPhase2ListUsageErrors: a missing folder, an extra argument, an unknown flag, a folder that does not exist, a
// folder with no catalogue and a catalogue that is not JSON are usage errors: exit 2, nothing on standard output, one
// usage_error line. `ltk list` never exits 3: no program is on PATH.
func TestPhase2ListUsageErrors(t *testing.T) {
	valid := absFixture(t, fixtureValid)
	notJSON := t.TempDir()
	if err := os.WriteFile(filepath.Join(notJSON, "catalogue.json"), []byte("{\"entries\": ["), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
	}{
		{"no folder", []string{"list"}},
		{"an extra argument", []string{"list", valid, valid}},
		{"an unknown flag", []string{"list", valid, "--yaml"}},
		{"a folder that does not exist", []string{"list", filepath.Join(t.TempDir(), "corpus-x")}},
		{"a folder with no catalogue", []string{"list", t.TempDir()}},
		{"a catalogue that is not JSON", []string{"list", notJSON}},
		{"--json with no folder", []string{"list", "--json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.UsageError, c.args...)
			if line.Code != contract.ErrorCodeUsageError {
				t.Errorf("code %q", line.Code)
			}
		})
	}
}

// compareReport is a CompareReport as a reader decodes it (numbers kept as text).
type compareReport struct {
	FolderA string `json:"folderA"`
	FolderB string `json:"folderB"`
	Entries []struct {
		ID     string   `json:"id"`
		Equal  []string `json:"equal"`
		Differ []struct {
			Pointer              string  `json:"pointer"`
			LedgerDependentField *string `json:"ledgerDependentField"`
			ValueA               any     `json:"valueA"`
			ValueB               any     `json:"valueB"`
		} `json:"differ"`
	} `json:"entries"`
	Passed bool `json:"passed"`
}

// catalogueEntryRead is the part of a catalogue entry the comparison reads: its id, file path and ledger-dependent
// group.
type catalogueEntryRead struct {
	ID                    string `json:"id"`
	FilePath              string `json:"filePath"`
	ExpectedDecodedValues struct {
		LedgerDependent []struct {
			Field   string `json:"field"`
			Pointer string `json:"pointer"`
		} `json:"ledgerDependent"`
	} `json:"expectedDecodedValues"`
}

// readCatalogueEntries reads a folder's catalogue entries with the standard library.
func readCatalogueEntries(t *testing.T, folder string) []catalogueEntryRead {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(folder, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Entries []catalogueEntryRead `json:"entries"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatal(err)
	}
	return cat.Entries
}

// decodedJSONPath is the entry's decoded JSON file: its XDR file's path with .json in place of the extension.
func decodedJSONPath(folder, filePath string) string {
	return filepath.Join(folder, filepath.FromSlash(strings.TrimSuffix(filePath, filepath.Ext(filePath))+".json"))
}

// verifyCompareReport holds a CompareReport to its documented form from outside: its folders as given; its entries in
// folder A's catalogue order; every `equal` pointer resolves to the same value in both decoded files (in B through
// the change-list matching: an operation's change lists are compared as multisets, an element matched by its ledger
// key and change kind); every difference's valueA and valueB are what its pointer reads in folder A's and folder B's
// decoded file, an element of a change list read in B where the matching pairs it (resolveMatched; null where the
// element is on one side only); its ledgerDependentField is the field of the recorded ledger-dependent pointer in
// folder A's catalogue that equals it or is a prefix of it, or null when none is; and passed is true exactly when no
// field is null. It returns the report and the differences with a null field, as "id pointer".
func verifyCompareReport(t *testing.T, stdout []byte, a, b string) (compareReport, []string) {
	t.Helper()
	requireValid(t, "compare-report.schema.json", stdout)
	requireOneDocument(t, stdout)
	var r compareReport
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.UseNumber()
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.FolderA != a || r.FolderB != b {
		t.Errorf("folderA %q folderB %q, want the folders as given %q %q", r.FolderA, r.FolderB, a, b)
	}
	entriesA := readCatalogueEntries(t, a)
	if len(r.Entries) != len(entriesA) {
		t.Fatalf("%d entries, folder A's catalogue has %d", len(r.Entries), len(entriesA))
	}
	var unlisted []string
	for i, e := range r.Entries {
		ce := entriesA[i]
		if e.ID != ce.ID {
			t.Errorf("entry %d is %q, folder A's catalogue order says %q", i, e.ID, ce.ID)
			continue
		}
		docA := decodeJSONFile(t, decodedJSONPath(a, ce.FilePath))
		docB := decodeJSONFile(t, decodedJSONPath(b, ce.FilePath))
		if len(e.Equal) == 0 {
			t.Errorf("%s: no equal leaf; the two generations share their reproduced values", e.ID)
		}
		for _, p := range e.Equal {
			va, okA := resolvePointer(docA, p)
			vb, okB := resolveMatched(docA, docB, p)
			if !okA || !okB || !sameJSON(va, vb) {
				t.Errorf("%s: equal pointer %s reads %v (%v) in A and %v (%v) in B", e.ID, p, va, okA, vb, okB)
			}
		}
		for _, d := range e.Differ {
			// The pointer is folder A's (a pair, or an element of A only); an element of B only is reported at its
			// pointer in B's file, valueA null.
			va, vb := readPair(docA, docB, d.Pointer)
			if d.ValueA == nil && !sameJSON(vb, d.ValueB) {
				vb, va = readPair(docB, docA, d.Pointer)
			}
			if !sameJSON(va, d.ValueA) || !sameJSON(vb, d.ValueB) || sameJSON(d.ValueA, d.ValueB) {
				t.Errorf("%s: difference at %s reports %v / %v; the decoded files read %v / %v", e.ID, d.Pointer, d.ValueA, d.ValueB, va, vb)
			}
			var fields []string
			for _, ld := range ce.ExpectedDecodedValues.LedgerDependent {
				if d.Pointer == ld.Pointer || strings.HasPrefix(d.Pointer, ld.Pointer+"/") {
					fields = append(fields, ld.Field)
				}
			}
			switch {
			case d.LedgerDependentField == nil && len(fields) != 0:
				t.Errorf("%s: %s is under the recorded ledger-dependent %v but is attributed null", e.ID, d.Pointer, fields)
			case d.LedgerDependentField != nil && !slices.Contains(fields, *d.LedgerDependentField):
				t.Errorf("%s: %s is attributed %q; folder A's catalogue records %v there", e.ID, d.Pointer, *d.LedgerDependentField, fields)
			}
			if d.LedgerDependentField == nil {
				unlisted = append(unlisted, e.ID+" "+d.Pointer)
			}
		}
	}
	if r.Passed != (len(unlisted) == 0) {
		t.Errorf("passed %v with %d differences outside the ledger-dependent list", r.Passed, len(unlisted))
	}
	return r, unlisted
}

// TestPhase2CompareLedgerOnly: two generations that differ only in ledger-dependent values compare with exit 0, nothing
// on standard error, and a report valid against compare-report.schema.json whose every difference is attributed; with
// no program on PATH (never 3).
func TestPhase2CompareLedgerOnly(t *testing.T) {
	a, b := absFixture(t, filepath.Join(fixtureLedgerOnly, "a")), absFixture(t, filepath.Join(fixtureLedgerOnly, "b"))
	stdout, _ := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.Success, "compare", a, b)
	r, unlisted := verifyCompareReport(t, stdout, a, b)
	if !r.Passed || len(unlisted) != 0 {
		t.Errorf("passed %v, unlisted %v", r.Passed, unlisted)
	}
	n, eq := 0, 0
	for _, e := range r.Entries {
		n += len(e.Differ)
		eq += len(e.Equal)
	}
	if n == 0 {
		t.Errorf("no difference reported: the two folders differ in ledger-dependent values")
	}
	t.Logf("%d entries: %d equal leaves and %d ledger-dependent differences, each read again from both decoded files", len(r.Entries), eq, n)
}

// TestPhase2CompareReproducedChanged: a difference outside the ledger-dependent list exits 1 with the report on
// standard output (on exit 1 the report is written as on exit 0), passed false, and one failure line naming the entry
// and the pointer of the first such difference.
func TestPhase2CompareReproducedChanged(t *testing.T) {
	a, b := absFixture(t, filepath.Join(fixtureReproChanged, "a")), absFixture(t, filepath.Join(fixtureReproChanged, "b"))
	stdout, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.Failure, "compare", a, b)
	r, unlisted := verifyCompareReport(t, stdout, a, b)
	if r.Passed || len(unlisted) == 0 {
		t.Fatalf("passed %v, unlisted %v", r.Passed, unlisted)
	}
	if line.Code != contract.ErrorCodeFailure {
		t.Errorf("code %q", line.Code)
	}
	id, pointer, _ := strings.Cut(unlisted[0], " ")
	if want := "difference outside the ledger-dependent list in entry " + id + " at " + pointer; !strings.Contains(line.Message, want) {
		t.Errorf("message %q, want it to contain %q", line.Message, want)
	}
}

// TestPhase2CompareUsageErrors: not exactly two folders, an unknown flag, a folder that does not exist, a folder with
// no catalogue, two catalogues that do not list the same ids, and a catalogue listing an id twice are usage errors:
// exit 2, nothing on standard output, one usage_error line; never 3, with no program on PATH.
func TestPhase2CompareUsageErrors(t *testing.T) {
	a, b := absFixture(t, filepath.Join(fixtureLedgerOnly, "a")), absFixture(t, filepath.Join(fixtureLedgerOnly, "b"))

	// fewer: B with its last entry taken out of the catalogue.
	fewer := copyTree(t, b)
	cat := decodeJSONFile(t, filepath.Join(fewer, "catalogue.json")).(map[string]any)
	entries := cat["entries"].([]any)
	cat["entries"] = entries[:len(entries)-1]
	writeJSONFile(t, filepath.Join(fewer, "catalogue.json"), cat)

	// twice: B with its first entry listed a second time.
	twice := copyTree(t, b)
	cat = decodeJSONFile(t, filepath.Join(twice, "catalogue.json")).(map[string]any)
	entries = cat["entries"].([]any)
	cat["entries"] = append(entries, entries[0])
	writeJSONFile(t, filepath.Join(twice, "catalogue.json"), cat)

	cases := []struct {
		name string
		args []string
	}{
		{"no folder", []string{"compare"}},
		{"one folder", []string{"compare", a}},
		{"three folders", []string{"compare", a, b, b}},
		{"an unknown flag", []string{"compare", a, b, "--json"}},
		{"folder A does not exist", []string{"compare", filepath.Join(t.TempDir(), "corpus-run-a"), b}},
		{"folder B does not exist", []string{"compare", a, filepath.Join(t.TempDir(), "corpus-run-b")}},
		{"folder B has no catalogue", []string{"compare", a, t.TempDir()}},
		{"the catalogues list different ids", []string{"compare", a, fewer}},
		{"the catalogues list different ids, reversed", []string{"compare", fewer, a}},
		{"a catalogue lists an id twice", []string{"compare", a, twice}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.UsageError, c.args...)
			if line.Code != contract.ErrorCodeUsageError {
				t.Errorf("code %q", line.Code)
			}
		})
	}
}

// TestPhase2CheckEnvironmentMissing: with no docker on PATH, `ltk check` on a valid corpus exits 3 before any check:
// nothing on standard output and one environment_missing line whose message names Docker.
func TestPhase2CheckEnvironmentMissing(t *testing.T) {
	_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.EnvironmentMissing, "check", absFixture(t, fixtureValid))
	if line.Code != contract.ErrorCodeEnvironmentMissing || !strings.Contains(line.Message, "Docker") {
		t.Errorf("error line %+v; want environment_missing naming Docker", line)
	}
}

// TestPhase2CheckUsageErrors: with no docker on PATH, so that an environment check would exit 3, every usage error
// exits 2 first: a missing folder, an extra argument, an unknown flag, a folder that does not exist and a folder that
// is a file. `--also-go`, before or after the folder, parses and reaches the environment check (exit 3).
func TestPhase2CheckUsageErrors(t *testing.T) {
	valid := absFixture(t, fixtureValid)
	file := filepath.Join(t.TempDir(), "catalogue.json")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, inMessage string
		args            []string
	}{
		{"no folder", "", []string{"check"}},
		{"an extra argument", "", []string{"check", valid, valid}},
		{"an unknown flag", "", []string{"check", "--also-rust", valid}},
		{"a folder that does not exist", "", []string{"check", filepath.Join(t.TempDir(), "corpus-x")}},
		{"a folder that is a file", "", []string{"check", file}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.UsageError, c.args...)
			if line.Code != contract.ErrorCodeUsageError || !strings.Contains(line.Message, c.inMessage) {
				t.Errorf("error line %+v; want usage_error containing %q", line, c.inMessage)
			}
		})
	}
	for _, args := range [][]string{{"check", valid, "--also-go"}, {"check", "--also-go", valid}} {
		_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.EnvironmentMissing, args...)
		if line.Code != contract.ErrorCodeEnvironmentMissing {
			t.Errorf("%v: error line %+v; want environment_missing (--also-go parses)", args, line)
		}
	}
}
