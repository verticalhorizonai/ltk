package main

// `ltk list` and `ltk compare` at the process boundary, with their exit codes: the built program on the fixture
// corpora, through the shared harness.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

const (
	fixtureValid         = "../../testdata/corpus/valid"
	fixtureLedgerOnlyA   = "../../testdata/compare/ledger-only/a"
	fixtureLedgerOnlyB   = "../../testdata/compare/ledger-only/b"
	fixtureReproducedA   = "../../testdata/compare/reproduced-changed/a"
	fixtureReproducedB   = "../../testdata/compare/reproduced-changed/b"
	outputSchemaFixtures = "../../internal/contract/testdata"
)

// validListing is `ltk list` of testdata/corpus/valid in text mode.
const validListing = "id\tcap\tprotocolVersion\txdrType\tlength\tsynthetic\n" +
	"cap-0040-signed-payload-signer\tCAP-0040\t30\tLedgerCloseMeta\t1920\tfalse\n" +
	"cap-0038-pool-share-trustline\tCAP-0038\t28\tLedgerCloseMeta\t2816\tfalse\n" +
	"cap-0027-muxed-account\tCAP-0027\t30\tLedgerCloseMeta\t2048\tfalse\n"

// noDockerEnv is an environment whose PATH holds no program at all: a container-free command runs the same.
func noDockerEnv(t *testing.T) []string {
	return []string{"PATH=" + t.TempDir()}
}

// validateOutput validates a command's JSON document against its JSON Schema fixture.
func validateOutput(t *testing.T, schemaFile string, doc []byte) {
	t.Helper()
	sb, err := os.ReadFile(filepath.Join(outputSchemaFixtures, schemaFile))
	if err != nil {
		t.Fatal(err)
	}
	sdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(sb))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	url := "file:///fixtures/" + schemaFile
	if err := c.AddResource(url, sdoc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("stdout is not one JSON document: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("stdout does not validate against %s: %v", schemaFile, err)
	}
}

// TestProcessList: exit 0, the listing on stdout, nothing on stderr, in text and with --json (before or after the
// folder), on a PATH with no docker; the JSON validates as a ListOutput.
func TestProcessList(t *testing.T) {
	code, stdout, stderr := runLTK(t, noDockerEnv(t), "list", fixtureValid)
	if code != 0 || string(stdout) != validListing || len(stderr) != 0 {
		t.Errorf("ltk list: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, args := range [][]string{{"list", fixtureValid, "--json"}, {"list", "--json", fixtureValid}, {"list", "-json=true", fixtureValid}} {
		code, stdout, stderr := runLTK(t, noDockerEnv(t), args...)
		if code != 0 || len(stderr) != 0 || !bytes.HasSuffix(stdout, []byte("}\n]\n")) || !bytes.HasPrefix(stdout, []byte("[\n  {\n    \"id\": ")) {
			t.Errorf("ltk %q: exit %d stdout %q stderr %q", args, code, stdout, stderr)
			continue
		}
		validateOutput(t, "list-output.schema.json", stdout)
		var out contract.ListOutput
		if err := json.Unmarshal(stdout, &out); err != nil || len(out) != 3 || out[1].ID != "cap-0038-pool-share-trustline" || out[1].Length != 2816 {
			t.Errorf("ListOutput %+v %v", out, err)
		}
	}
	if code, stdout, _ := runLTK(t, nil, "list", fixtureValid, "--json=false"); code != 0 || string(stdout) != validListing {
		t.Errorf("--json=false: exit %d %q", code, stdout)
	}
}

// TestProcessListUsageErrors: no folder, an unknown flag, an extra argument, a folder that does not exist, a folder
// with no readable catalogue: exit 2, nothing on stdout, one usage_error line. ltk list never exits 1 or 3.
func TestProcessListUsageErrors(t *testing.T) {
	empty := t.TempDir()
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "catalogue.json"), []byte("{"), 0o644)
	cases := []struct {
		args     []string
		contains string
	}{
		{[]string{"list"}, "missing argument <folder>"},
		{[]string{"list", fixtureValid, "--also-go"}, "flag provided but not defined"},
		{[]string{"list", fixtureValid, "extra"}, `unexpected argument "extra"`},
		{[]string{"list", "corpus-x"}, "ltk list: folder 'corpus-x' does not exist"},
		{[]string{"list", empty}, "holds no catalogue"},
		{[]string{"list", bad, "--json"}, "not a readable catalogue"},
	}
	for _, c := range cases {
		code, stdout, stderr := runLTKIn(t, t.TempDir(), noDockerEnv(t), c.args...)
		if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
			t.Errorf("ltk %q: exit %d stdout %q, want 2 and nothing", c.args, code, stdout)
			continue
		}
		if line := assertErrorLine(t, stderr, contract.ErrorCodeUsageError); !strings.Contains(line.Message, c.contains) {
			t.Errorf("ltk %q: message %q does not contain %q", c.args, line.Message, c.contains)
		}
	}
}

// TestProcessCompareLedgerOnly: only ledger-dependent differences: exit 0, the CompareReport on stdout with every
// difference attributed, nothing on stderr, with no docker on PATH.
func TestProcessCompareLedgerOnly(t *testing.T) {
	code, stdout, stderr := runLTK(t, noDockerEnv(t), "compare", fixtureLedgerOnlyA, fixtureLedgerOnlyB)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %q, want 0 and nothing", code, stderr)
	}
	validateOutput(t, "compare-report.schema.json", stdout)
	var r contract.CompareReport
	if err := json.Unmarshal(stdout, &r); err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.FolderA != fixtureLedgerOnlyA || r.FolderB != fixtureLedgerOnlyB || len(r.Entries) != 3 {
		t.Fatalf("report: passed %v folders %q %q entries %d", r.Passed, r.FolderA, r.FolderB, len(r.Entries))
	}
	for _, e := range r.Entries {
		if len(e.Equal) == 0 || len(e.Differ) == 0 {
			t.Errorf("%s: equal %d differ %d", e.ID, len(e.Equal), len(e.Differ))
		}
		for _, d := range e.Differ {
			if d.LedgerDependentField == nil {
				t.Errorf("%s %s is not attributed", e.ID, d.Pointer)
			}
		}
	}
	if !bytes.HasPrefix(stdout, []byte("{\n  \"folderA\": ")) || !bytes.HasSuffix(stdout, []byte("\n}\n")) {
		t.Errorf("layout of the report: %q…", stdout[:min(len(stdout), 40)])
	}
}

// TestProcessCompareReproducedChanged: a difference outside the ledger-dependent list: exit 1, the report on stdout
// naming the entry and the pointer with a null field, and one failure line naming both.
func TestProcessCompareReproducedChanged(t *testing.T) {
	code, stdout, stderr := runLTK(t, noDockerEnv(t), "compare", fixtureReproducedA, fixtureReproducedB)
	if code != int(contract.EXIT_CODES.Failure) {
		t.Fatalf("exit %d, want 1; stderr %q", code, stderr)
	}
	validateOutput(t, "compare-report.schema.json", stdout)
	const ptr = "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/change_trust"
	want := `{
          "pointer": "` + ptr + `",
          "ledgerDependentField": null,
          "valueA": "success",
          "valueB": "malformed"
        }`
	if !bytes.Contains(stdout, []byte(want)) || !bytes.Contains(stdout, []byte(`"passed": false`)) {
		t.Errorf("the report does not show the unlisted difference: %s", stdout)
	}
	line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
	if line.Message != "difference outside the ledger-dependent list in entry cap-0038-pool-share-trustline at "+ptr {
		t.Errorf("message %q", line.Message)
	}
}

// TestProcessCompareUsageErrors: not exactly two folders, a missing folder, a folder with no catalogue, catalogues with
// different ids: exit 2, nothing on stdout, one usage_error line; never 3, though no docker is on PATH.
func TestProcessCompareUsageErrors(t *testing.T) {
	empty := t.TempDir()
	cases := []struct {
		args     []string
		contains string
	}{
		{[]string{"compare", fixtureLedgerOnlyA}, "missing argument <folder B>"},
		{[]string{"compare", fixtureLedgerOnlyA, fixtureLedgerOnlyB, fixtureValid}, "unexpected argument"},
		{[]string{"compare", "corpus-run-a", fixtureLedgerOnlyB}, "folder 'corpus-run-a' does not exist"},
		{[]string{"compare", fixtureLedgerOnlyA, empty}, "holds no catalogue"},
		{[]string{"compare", fixtureLedgerOnlyA, "--json", fixtureLedgerOnlyB}, "flag provided but not defined"},
	}
	for _, c := range cases {
		code, stdout, stderr := runLTK(t, noDockerEnv(t), c.args...)
		if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
			t.Errorf("ltk %q: exit %d stdout %q, want 2 and nothing", c.args, code, stdout)
			continue
		}
		if line := assertErrorLine(t, stderr, contract.ErrorCodeUsageError); !strings.Contains(line.Message, c.contains) {
			t.Errorf("ltk %q: message %q does not contain %q", c.args, line.Message, c.contains)
		}
	}
	// Two catalogues with different ids: the message names the missing id and the folder that lacks it.
	one := t.TempDir()
	cat, _ := os.ReadFile(filepath.Join(fixtureLedgerOnlyA, "catalogue.json"))
	var c contract.Catalogue
	_ = json.Unmarshal(cat, &c)
	c.Entries = c.Entries[:2]
	b, _ := json.Marshal(c)
	os.WriteFile(filepath.Join(one, "catalogue.json"), b, 0o644)
	code, stdout, stderr := runLTK(t, nil, "compare", fixtureLedgerOnlyA, one)
	if code != 2 || len(stdout) != 0 {
		t.Fatalf("exit %d stdout %q", code, stdout)
	}
	if line := assertErrorLine(t, stderr, contract.ErrorCodeUsageError); line.Message != "folders do not list the same entry ids: cap-0027-muxed-account missing from "+one {
		t.Errorf("message %q", line.Message)
	}
}

// runIntoClosedPipe runs the program with a standard output whose reader is closed before it starts, and returns its
// exit code and standard error.
func runIntoClosedPipe(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	errFile := filepath.Join(t.TempDir(), "stderr")
	errOut, err := os.Create(errFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd := ltkCommand(t, "", nil, args...)
	cmd.Stdout, cmd.Stderr = w, errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	code := exitCodeOfRun(t, cmd.Wait())
	errOut.Close()
	stderr, _ := os.ReadFile(errFile)
	return code, stderr
}

// TestProcessListAndCompareClosedPipe: `ltk list <folder> --json` and `ltk compare` writing into a pipe whose reader is
// gone exit 0 with nothing on standard error, a failing comparison included.
func TestProcessListAndCompareClosedPipe(t *testing.T) {
	for _, args := range [][]string{
		{"list", fixtureValid, "--json"},
		{"list", fixtureValid},
		{"compare", fixtureLedgerOnlyA, fixtureLedgerOnlyB},
		{"compare", fixtureReproducedA, fixtureReproducedB},
	} {
		if code, stderr := runIntoClosedPipe(t, args...); code != 0 || len(stderr) != 0 {
			t.Errorf("ltk %q into a closed pipe: exit %d stderr %q; want 0 and nothing", args, code, stderr)
		}
	}
}
