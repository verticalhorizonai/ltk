package catalogue

// The Repro report writer's tests. The writer's failure at the command level, run as root, is in
// cmd/ltk/repro_test.go.

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

func strp(s string) *string { return &s }

// jsSdkPassed is a passed report of `ltk repro js-sdk-1766`.
func jsSdkPassed() contract.JsSdk1766Report {
	return contract.JsSdk1766Report{
		Target: contract.ReproTargetJsSdk1766,
		Versions: []contract.SdkVersionResult{
			{Version: contract.SdkVersion17_2_0, Role: contract.FixRoleBeforeFix, Steps: []contract.SdkStepResult{
				{Step: contract.SdkReproStepBuild, Passed: false, Error: strp("Assets are not in lexicographic order")},
			}},
			{Version: contract.SdkVersion17_2_1, Role: contract.FixRoleFix, Steps: []contract.SdkStepResult{
				{Step: contract.SdkReproStepBuild, Passed: true},
				{Step: contract.SdkReproStepDecode, Passed: true},
				{Step: contract.SdkReproStepReEncode, Passed: true},
			}, RoundTripIdentical: true},
		},
		Passed: true,
	}
}

// walletBackendPassed is a passed report (exit 0) of `ltk repro wallet-backend-729`.
func walletBackendPassed() contract.WalletBackend729Report {
	row := contract.JsonObject(json.RawMessage(`{"hash":"3d5b","ledger_number":14}`))
	return contract.WalletBackend729Report{
		Target:          contract.ReproTargetWalletBackend729,
		ProtocolVersion: contract.WALLET_BACKEND_729_PROTOCOL_VERSION,
		Commits: []contract.WalletBackendCommitResult{
			{Commit: contract.WalletBackendCommit2a0a84ea, Role: contract.FixRoleBeforeFix, Ledger: 14,
				Error: strp("processing ledger 14: unsupported signer key type ed25519SignedPayload"), Retries: 5, Restarts: 1,
				LogPath: "/tmp/ltk-3f9a1c2e/wallet-backend-729/2a0a84ea/wallet-backend.log", StoreStatePath: "/tmp/ltk-3f9a1c2e/wallet-backend-729/2a0a84ea/store-state.sql"},
			{Commit: contract.WalletBackendCommit2c873353, Role: contract.FixRoleFix, Ledger: 14, Ingested: true, StoredRow: &row,
				LogPath: "/tmp/ltk-3f9a1c2e/wallet-backend-729/2c873353/wallet-backend.log", StoreStatePath: "/tmp/ltk-3f9a1c2e/wallet-backend-729/2c873353/store-state.sql"},
		},
		Passed: true,
	}
}

// validateAgainst validates doc against schemaFile, an output schema kept as a test fixture in
// internal/contract/testdata.
func validateAgainst(t *testing.T, schemaFile string, doc []byte) {
	t.Helper()
	sb, err := os.ReadFile(filepath.Join("..", "contract", "testdata", schemaFile))
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
		t.Fatalf("not one JSON document: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("does not validate against %s: %v\n%s", schemaFile, err, doc)
	}
}

// reproTree returns the names under dir/repro.
func reproTree(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, ReproFolder))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// assertDocumentForm: one JSON document, two-space indentation, LF only, one trailing newline.
func assertDocumentForm(t *testing.T, b []byte, firstField string) {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("{\n  \""+firstField+"\": ")) || !bytes.HasSuffix(b, []byte("}\n")) ||
		bytes.HasSuffix(b, []byte("\n\n")) || bytes.Contains(b, []byte("\r")) || bytes.Contains(b, []byte("\t")) {
		t.Errorf("not one two-space indented LF document with one trailing newline:\n%q", b)
	}
}

// TestWriteJsSdkReport: the report is written whole to repro/js-sdk-1766.json under the working directory, repro/
// created when absent; the bytes returned are the file's; the file validates against the js-sdk report's schema and
// holds 17.2.0's exact error text "Assets are not in lexicographic order" and 17.2.1's three passed steps with
// roundTripIdentical true; repro/ holds only the report.
func TestWriteJsSdkReport(t *testing.T) {
	dir := t.TempDir()
	b, err := WriteJsSdkReport(dir, jsSdkPassed())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(ReproReportPath(dir, contract.ReproTargetJsSdk1766))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, file) {
		t.Errorf("the returned bytes are not the file's")
	}
	validateAgainst(t, "js-sdk-1766-report.schema.json", file)
	assertDocumentForm(t, file, "target")
	if !bytes.Contains(file, []byte(`"error": "Assets are not in lexicographic order"`)) {
		t.Errorf("17.2.0's error text is not in the report:\n%s", file)
	}
	var back contract.JsSdk1766Report
	dec := json.NewDecoder(bytes.NewReader(file))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&back); err != nil {
		t.Fatal(err)
	}
	fix := back.Versions[1]
	if !back.Passed || !fix.RoundTripIdentical || len(fix.Steps) != 3 || back.Versions[0].Steps[0].Passed {
		t.Errorf("read back %+v", back)
	}
	for _, s := range fix.Steps {
		if !s.Passed || s.Error != nil {
			t.Errorf("17.2.1 step %+v", s)
		}
	}
	if got := reproTree(t, dir); len(got) != 1 || got[0] != "js-sdk-1766.json" {
		t.Errorf("repro/ holds %v", got)
	}
	if ReproReportPath("w", contract.ReproTargetJsSdk1766) != filepath.Join("w", "repro", "js-sdk-1766.json") {
		t.Errorf("path %s", ReproReportPath("w", contract.ReproTargetJsSdk1766))
	}
}

// TestWriteWalletBackendReport: the report is written whole to repro/wallet-backend-729.json, validates against the
// wallet-backend report's schema, carries each commit's ledger, ingested, error, storedRow, retries, restarts and the
// two evidence paths; a second report replaces the first whole, and repro/ then holds the two reports and nothing
// else.
func TestWriteWalletBackendReport(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteJsSdkReport(dir, jsSdkPassed()); err != nil {
		t.Fatal(err)
	}
	failed := walletBackendPassed()
	failed.Passed = false
	failed.Commits[0].Retries = 3
	if _, err := WriteWalletBackendReport(dir, failed); err != nil {
		t.Fatal(err)
	}
	b, err := WriteWalletBackendReport(dir, walletBackendPassed())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(ReproReportPath(dir, contract.ReproTargetWalletBackend729))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, file) {
		t.Errorf("the returned bytes are not the file's")
	}
	validateAgainst(t, "wallet-backend-729-report.schema.json", file)
	assertDocumentForm(t, file, "target")
	var back contract.WalletBackend729Report
	dec := json.NewDecoder(bytes.NewReader(file))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&back); err != nil {
		t.Fatal(err)
	}
	pre, fix := back.Commits[0], back.Commits[1]
	if !back.Passed || pre.Retries != 5 || pre.Restarts != 1 || pre.Ingested || pre.StoredRow != nil || pre.Error == nil ||
		!fix.Ingested || fix.StoredRow == nil || fix.Error != nil || pre.Ledger != 14 || fix.Ledger != 14 ||
		!strings.HasSuffix(pre.LogPath, "2a0a84ea/wallet-backend.log") || !strings.HasSuffix(fix.StoreStatePath, "2c873353/store-state.sql") {
		t.Errorf("read back %+v", back)
	}
	got := reproTree(t, dir)
	if len(got) != 2 || got[0] != "js-sdk-1766.json" || got[1] != "wallet-backend-729.json" {
		t.Errorf("repro/ holds %v; want the two reports only", got)
	}
}

// TestWriteReproReportRefusesShapes: a report not in its schema's shape (another target, protocol, order, count, or a
// version with no step) is not written, and repro/ is not even created.
func TestWriteReproReportRefusesShapes(t *testing.T) {
	wb := []func(*contract.WalletBackend729Report){
		func(r *contract.WalletBackend729Report) { r.Target = contract.ReproTargetJsSdk1766 },
		func(r *contract.WalletBackend729Report) { r.ProtocolVersion = 30 },
		func(r *contract.WalletBackend729Report) { r.Commits = r.Commits[:1] },
		func(r *contract.WalletBackend729Report) { r.Commits[0], r.Commits[1] = r.Commits[1], r.Commits[0] },
	}
	for i, mutate := range wb {
		dir := t.TempDir()
		r := walletBackendPassed()
		mutate(&r)
		if _, err := WriteWalletBackendReport(dir, r); err == nil {
			t.Errorf("wallet-backend case %d was written", i)
		}
		if _, err := os.Stat(filepath.Join(dir, ReproFolder)); err == nil {
			t.Errorf("wallet-backend case %d created repro/", i)
		}
	}
	js := []func(*contract.JsSdk1766Report){
		func(r *contract.JsSdk1766Report) { r.Target = contract.ReproTargetWalletBackend729 },
		func(r *contract.JsSdk1766Report) { r.Versions = r.Versions[1:] },
		func(r *contract.JsSdk1766Report) { r.Versions[0], r.Versions[1] = r.Versions[1], r.Versions[0] },
		func(r *contract.JsSdk1766Report) { r.Versions[0].Steps = nil },
	}
	for i, mutate := range js {
		dir := t.TempDir()
		r := jsSdkPassed()
		mutate(&r)
		if _, err := WriteJsSdkReport(dir, r); err == nil {
			t.Errorf("js-sdk case %d was written", i)
		}
		if _, err := os.Stat(filepath.Join(dir, ReproFolder)); err == nil {
			t.Errorf("js-sdk case %d created repro/", i)
		}
	}
}

// TestWriteReproReportFails (a write that fails, at the writer; the command-level rows are in cmd/ltk/repro_test.go):
// a directory in the report's place, or a file in repro/'s place, fails the write with a message naming the path;
// nothing else is written (no temporary file is left) and the obstacle is left as it was.
func TestWriteReproReportFails(t *testing.T) {
	t.Run("a directory in the report's place", func(t *testing.T) {
		dir := t.TempDir()
		blocked := ReproReportPath(dir, contract.ReproTargetJsSdk1766)
		if err := os.MkdirAll(filepath.Join(blocked, "inside"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := WriteJsSdkReport(dir, jsSdkPassed())
		if err == nil || !strings.Contains(err.Error(), "write "+filepath.ToSlash(blocked)+": something other than a file is in its place") {
			t.Fatalf("error %v", err)
		}
		if got := reproTree(t, dir); len(got) != 1 || got[0] != "js-sdk-1766.json" {
			t.Errorf("repro/ holds %v", got)
		}
		if _, err := os.Stat(filepath.Join(blocked, "inside")); err != nil {
			t.Errorf("the directory in the way was changed: %v", err)
		}
	})
	t.Run("a file in repro/'s place", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ReproFolder), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := WriteWalletBackendReport(dir, walletBackendPassed())
		if err == nil || !strings.Contains(err.Error(), "create the folder "+filepath.ToSlash(filepath.Join(dir, ReproFolder))) {
			t.Fatalf("error %v", err)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, ReproFolder)); string(b) != "x" {
			t.Errorf("the file in repro/'s place was changed")
		}
	})
}
