//go:build integration

package orchestrator

// The integration verification of `ltk check` on real XDR, Docker tier: `ltk check` through the built program on a
// corpus of real XDR that this test builds as a reader would, with the standard library and the corpus layout only
// (each entry's XDR file at <entry id>/entry.xdr, its decoded JSON at <entry id>/entry.json, the catalogue and its
// schema copy at the folder's top), never with the program's own packages:
//
//	<folder>/catalogue.json                           one entry, written by hand from the catalogue's documented fields
//	<folder>/catalogue.schema.json                    schemas/catalogue.schema.json, byte for byte
//	<folder>/cap-0040-signed-payload-signer/entry.xdr testdata/real/capture-p30.xdr (a real protocol-30 capture)
//	<folder>/cap-0040-signed-payload-signer/entry.json testdata/real/capture-p30.json (its decoded JSON)
//
// The entry's expected values are read from the decoded JSON at pointers named here, in both groups. Stellar's Rust XDR
// tool of the current quickstart image does the round trip. Each run is held to the clean stop: the difference of every
// container (running or stopped), network and volume of the engine before and after the run is empty, and the three
// listings filtered by label=ltk.managed=true are empty.
//
// Run natively, one package at a time, with nothing else using Docker, and with a PATH that holds no wrapper that runs
// the go tool in a container; see doc.go for the cross-compilation.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

const (
	// realEntryID is entry 1's id, the CAP-0040 signed-payload signer.
	realEntryID = "cap-0040-signed-payload-signer"
	// realEntryFilePath is its XDR file in the corpus layout, <entry id>/entry.xdr.
	realEntryFilePath = realEntryID + "/entry.xdr"
	// realCaptureLength is the length of testdata/real/capture-p30.xdr: the round trip compares every byte.
	realCaptureLength = 1928
	txPrefix          = "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx"
)

// realReproduced are the reproduced-by-every-run pointers of the entry: the source account, the signed-payload
// signer and its weight, the operation's result and the signer as the account holds it after the operation.
var realReproduced = []string{
	txPrefix + "/tx/source_account",
	txPrefix + "/tx/operations/0/body/set_options/signer/key",
	txPrefix + "/tx/operations/0/body/set_options/signer/weight",
	"/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/set_options",
	"/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/signers/0/key",
}

// realLedgerDependent are the ledger-dependent pointers of the entry with their LedgerDependentField; protocol 30's
// close_time_ms and value signature are grouped as close_time and consensus_signature.
var realLedgerDependent = [][2]string{
	{"ledger_hash", "/v2/ledger_header/hash"},
	{"previous_ledger_hash", "/v2/ledger_header/header/previous_ledger_hash"},
	{"transaction_set_hash", "/v2/ledger_header/header/scp_value/tx_set_hash"},
	{"close_time", "/v2/ledger_header/header/scp_value/close_time"},
	{"close_time", "/v2/ledger_header/header/scp_value/ext/signed_ms/close_time_ms"},
	{"consensus_signature", "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/signature"},
	{"transaction_result_set_hash", "/v2/ledger_header/header/tx_set_result_hash"},
	{"bucket_list_hash", "/v2/ledger_header/header/bucket_list_hash"},
	{"ledger_number", "/v2/ledger_header/header/ledger_seq"},
	{"fee_pool", "/v2/ledger_header/header/fee_pool"},
	{"source_sequence_number", txPrefix + "/tx/seq_num"},
	{"transaction_signature", txPrefix + "/signatures/0/signature"},
	{"transaction_hash", "/v2/tx_processing/0/result/transaction_hash"},
	{"last_modified_ledger", "/v2/tx_processing/0/fee_processing/0/state/last_modified_ledger_seq"},
}

// Records of the catalogue, in the field order of the catalogue's schema.
type (
	readerValue struct {
		Field   string          `json:"field,omitempty"`
		Pointer string          `json:"pointer"`
		Value   json.RawMessage `json:"value"`
	}
	readerEntry struct {
		ID                    string `json:"id"`
		Cap                   string `json:"cap"`
		ProtocolVersion       int    `json:"protocolVersion"`
		ProtocolFallback      bool   `json:"protocolFallback"`
		Source                string `json:"source"`
		Synthetic             bool   `json:"synthetic"`
		ExpectedDecodedValues struct {
			ReproducedByEveryRun []readerValue `json:"reproducedByEveryRun"`
			LedgerDependent      []readerValue `json:"ledgerDependent"`
		} `json:"expectedDecodedValues"`
		FilePath string `json:"filePath"`
		XdrType  string `json:"xdrType"`
		Sha256   string `json:"sha256"`
		Length   int    `json:"length"`
	}
)

// buildReaderCorpus writes the real-XDR corpus into a new temporary folder with the standard library and returns the
// folder and the XDR file's SHA-256.
func buildReaderCorpus(t *testing.T) (folder, sum string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testdataFolder, "real", "capture-p30.xdr"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != realCaptureLength {
		t.Fatalf("testdata/real/capture-p30.xdr is %d bytes, want %d", len(raw), realCaptureLength)
	}
	decoded, err := os.ReadFile(filepath.Join(testdataFolder, "real", "capture-p30.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(catalogueSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeJSON(t, decoded)
	read := func(p string) json.RawMessage {
		v, ok := resolvePointer(doc, p)
		if !ok {
			t.Fatalf("the decoded capture has nothing at %s", p)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var protocol int
	if err := json.Unmarshal(read("/v2/ledger_header/header/ledger_version"), &protocol); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	sum = hex.EncodeToString(h[:])
	e := readerEntry{
		ID: realEntryID, Cap: "CAP-0040", ProtocolVersion: protocol, ProtocolFallback: false,
		Source:    "the real protocol-30 capture of testdata/real, laid out by internal/orchestrator's phase-2 verification",
		Synthetic: false, FilePath: realEntryFilePath, XdrType: "LedgerCloseMeta", Sha256: sum, Length: len(raw),
	}
	for _, p := range realReproduced {
		e.ExpectedDecodedValues.ReproducedByEveryRun = append(e.ExpectedDecodedValues.ReproducedByEveryRun, readerValue{Pointer: p, Value: read(p)})
	}
	for _, fp := range realLedgerDependent {
		e.ExpectedDecodedValues.LedgerDependent = append(e.ExpectedDecodedValues.LedgerDependent, readerValue{Field: fp[0], Pointer: fp[1], Value: read(fp[1])})
	}

	folder = filepath.Join(t.TempDir(), "corpus")
	if err := os.MkdirAll(filepath.Join(folder, realEntryID), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		filepath.FromSlash(realEntryFilePath):    raw,
		filepath.Join(realEntryID, "entry.json"): decoded,
		"catalogue.schema.json":                  schema,
	} {
		if err := os.WriteFile(filepath.Join(folder, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONFile(t, filepath.Join(folder, "catalogue.json"), struct {
		Entries []readerEntry `json:"entries"`
	}{[]readerEntry{e}})
	cat, err := os.ReadFile(filepath.Join(folder, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validationError(t, compileSchemaFile(t, catalogueSchemaPath), cat); err != nil {
		t.Fatalf("the reader's catalogue does not validate against the catalogue schema: %v", err)
	}
	return folder, sum
}

// docker runs the docker CLI found on PATH and returns its standard output.
func docker(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, errOut.Bytes())
	}
	return out.String()
}

// engineSnapshot is every container (running or stopped), network and volume of the engine, by kind and id: the
// clean stop compares it before and after a run.
func engineSnapshot(t *testing.T) []string {
	t.Helper()
	var out []string
	for kind, args := range map[string][]string{
		"container": {"ps", "-a", "-q", "--no-trunc"},
		"network":   {"network", "ls", "-q", "--no-trunc"},
		"volume":    {"volume", "ls", "-q"},
	} {
		for _, id := range strings.Fields(docker(t, args...)) {
			out = append(out, kind+" "+id)
		}
	}
	slices.Sort(out)
	return out
}

// managedListings runs the three listings filtered by label=ltk.managed=true, logs them whole and returns the rows
// past their header lines (none when nothing labelled exists).
func managedListings(t *testing.T) []string {
	t.Helper()
	var rows []string
	for _, args := range [][]string{
		{"ps", "-a", "--filter", "label=ltk.managed=true"},
		{"network", "ls", "--filter", "label=ltk.managed=true"},
		{"volume", "ls", "--filter", "label=ltk.managed=true"},
	} {
		out := docker(t, args...)
		t.Logf("$ docker %s\n%s", strings.Join(args, " "), out)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		rows = append(rows, lines[1:]...)
	}
	return rows
}

// checkWithCleanStop runs `ltk check folder` between two engine snapshots and holds the run to the clean stop: no
// labelled resource before; nothing new after, and the labelled listings empty.
func checkWithCleanStop(t *testing.T, folder string) (code int, stdout, stderr []byte) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("the integration tier needs the docker CLI on PATH: %v", err)
	}
	if rows := managedListings(t); len(rows) != 0 {
		t.Fatalf("resources labelled ltk.managed=true exist before the run: %q", rows)
	}
	before := engineSnapshot(t)
	code, stdout, stderr = runLTKIn(t, t.TempDir(), nil, "check", folder)
	after := engineSnapshot(t)
	var left []string
	for _, r := range after {
		if !slices.Contains(before, r) {
			left = append(left, r)
		}
	}
	if len(left) != 0 {
		t.Errorf("the run left %q (the clean stop: the before/after difference is empty)", left)
	}
	if rows := managedListings(t); len(rows) != 0 {
		t.Errorf("the labelled listings are not empty after the run: %q", rows)
	}
	t.Logf("snapshot: %d resources before, %d after, %d new", len(before), len(after), len(left))
	return code, stdout, stderr
}

// checkReportRead is a CheckReport as a reader decodes it.
type checkReportRead struct {
	Folder          string `json:"folder"`
	CatalogueSchema struct {
		Check    string            `json:"check"`
		Passed   bool              `json:"passed"`
		Failures []json.RawMessage `json:"failures"`
	} `json:"catalogueSchema"`
	Entries []struct {
		ID            string `json:"id"`
		FileIntegrity struct {
			Passed         bool   `json:"passed"`
			Sha256         string `json:"sha256"`
			Length         int    `json:"length"`
			RecordedSha256 string `json:"recordedSha256"`
			RecordedLength int    `json:"recordedLength"`
		} `json:"fileIntegrity"`
		RoundTrip struct {
			Check         string `json:"check"`
			Passed        bool   `json:"passed"`
			BytesCompared int    `json:"bytesCompared"`
		} `json:"roundTrip"`
		ExpectedValues struct {
			Passed     bool              `json:"passed"`
			Mismatches []json.RawMessage `json:"mismatches"`
		} `json:"expectedValues"`
		GoSdkRoundTrip json.RawMessage `json:"goSdkRoundTrip"`
	} `json:"entries"`
	Passed bool `json:"passed"`
}

// readCheckReport holds stdout to the check report's format (valid against check-report.schema.json, two-space indented
// with one trailing newline, keys in CHECK_ORDER) and decodes it.
func readCheckReport(t *testing.T, stdout []byte) checkReportRead {
	t.Helper()
	requireValid(t, "check-report.schema.json", stdout)
	requireTwoSpaceIndent(t, stdout)
	s := string(stdout)
	order := []string{`"folder"`, `"catalogueSchema"`, `"entries"`, `"fileIntegrity"`, `"roundTrip"`, `"expectedValues"`, `"goSdkRoundTrip"`}
	for i := 1; i < len(order); i++ {
		if strings.Index(s, order[i-1]) > strings.Index(s, order[i]) {
			t.Errorf("the report writes %s before %s (CHECK_ORDER)", order[i], order[i-1])
		}
	}
	if strings.LastIndex(s, `"passed"`) < strings.LastIndex(s, `"goSdkRoundTrip"`) {
		t.Errorf("the report's passed is not its last field")
	}
	var r checkReportRead
	if err := json.Unmarshal(stdout, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestPhase2IntegrationCheckRealXDR: on the reader's real-XDR corpus `ltk check` exits 0 with nothing on standard
// error and a report in which the entry is decoded and re-encoded by the Rust XDR tool byte for byte, bytesCompared
// 1928, and its expected values of both groups are read again; with one byte of the XDR file altered it exits 1 with
// the report (file_integrity failed) and one failure line naming the entry. Each run leaves the engine as it found it.
func TestPhase2IntegrationCheckRealXDR(t *testing.T) {
	t.Run("passes", func(t *testing.T) {
		folder, sum := buildReaderCorpus(t)
		code, stdout, stderr := checkWithCleanStop(t, folder)
		t.Logf("$ ltk check %s\nexit %d\nstdout:\n%sstderr: %q", folder, code, stdout, stderr)
		if code != int(contract.EXIT_CODES.Success) || len(stderr) != 0 {
			t.Fatalf("exit %d stderr %s; want 0 and nothing", code, stderr)
		}
		r := readCheckReport(t, stdout)
		if r.Folder != folder || !r.Passed || !r.CatalogueSchema.Passed || len(r.CatalogueSchema.Failures) != 0 || len(r.Entries) != 1 {
			t.Fatalf("report: %s", stdout)
		}
		e := r.Entries[0]
		if e.ID != realEntryID {
			t.Errorf("entry %q, want %q", e.ID, realEntryID)
		}
		if !e.FileIntegrity.Passed || e.FileIntegrity.Sha256 != sum || e.FileIntegrity.RecordedSha256 != sum ||
			e.FileIntegrity.Length != realCaptureLength || e.FileIntegrity.RecordedLength != realCaptureLength {
			t.Errorf("fileIntegrity %+v; want passed, %s, %d", e.FileIntegrity, sum, realCaptureLength)
		}
		if e.RoundTrip.Check != "round_trip" || !e.RoundTrip.Passed || e.RoundTrip.BytesCompared != realCaptureLength {
			t.Errorf("roundTrip %+v; want passed with bytesCompared %d (every byte of the file)", e.RoundTrip, realCaptureLength)
		}
		if !e.ExpectedValues.Passed || len(e.ExpectedValues.Mismatches) != 0 {
			t.Errorf("expectedValues %+v; want passed with no mismatch over %d values", e.ExpectedValues, len(realReproduced)+len(realLedgerDependent))
		}
		if string(e.GoSdkRoundTrip) != "null" {
			t.Errorf("goSdkRoundTrip %s; want null without --also-go", e.GoSdkRoundTrip)
		}
	})

	t.Run("a corrupted byte fails", func(t *testing.T) {
		folder, sum := buildReaderCorpus(t)
		xdrFile := filepath.Join(folder, filepath.FromSlash(realEntryFilePath))
		b, err := os.ReadFile(xdrFile)
		if err != nil {
			t.Fatal(err)
		}
		b[realCaptureLength/2] ^= 0xff
		if err := os.WriteFile(xdrFile, b, 0o644); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := checkWithCleanStop(t, folder)
		t.Logf("$ ltk check %s (byte %d altered)\nexit %d\nstdout:\n%sstderr: %s", folder, realCaptureLength/2, code, stdout, stderr)
		if code != int(contract.EXIT_CODES.Failure) {
			t.Fatalf("exit %d; want 1 (ltk check fails on a single altered byte)", code)
		}
		line := requireErrorLine(t, code, stderr)
		if line.Code != contract.ErrorCodeFailure || !strings.Contains(line.Message, realEntryID) {
			t.Errorf("error line %+v; want failure naming %s", line, realEntryID)
		}
		r := readCheckReport(t, stdout)
		if r.Passed || len(r.Entries) != 1 {
			t.Fatalf("report: %s", stdout)
		}
		fi := r.Entries[0].FileIntegrity
		if fi.Passed || fi.Sha256 == sum || fi.RecordedSha256 != sum || fi.Length != realCaptureLength {
			t.Errorf("fileIntegrity %+v; want failed on the hash, the length %d", fi, realCaptureLength)
		}
	})
}
