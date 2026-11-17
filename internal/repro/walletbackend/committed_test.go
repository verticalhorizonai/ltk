package walletbackend

// The committed report against the verdict, the runner's side: repro/wallet-backend-729.json, written by `ltk repro
// wallet-backend-729` when the repository's reports were generated and never by a test, is read here and nothing is
// written beside it. The process-level test of the command is cmd/ltk/accept_wb_test.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
)

// committedReportPath is the committed report, from this package's folder: the module root's repro/.
var committedReportPath = filepath.Join("..", "..", "..", "repro", "wallet-backend-729.json")

// committedReport reads the committed report's bytes and decodes them strictly (no field outside the mirror); it
// skips, saying why, when the file is absent.
func committedReport(t *testing.T) ([]byte, contract.WalletBackend729Report) {
	t.Helper()
	b, err := os.ReadFile(committedReportPath)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("skipped: repro/wallet-backend-729.json is absent from the module root; it is written by `ltk repro wallet-backend-729` and committed with the repository")
	}
	if err != nil {
		t.Fatal(err)
	}
	return b, decodeReport(t, b)
}

// decodeReport decodes a report strictly.
func decodeReport(t *testing.T, b []byte) contract.WalletBackend729Report {
	t.Helper()
	var rep contract.WalletBackend729Report
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("the report is not the wallet-backend report document: %v", err)
	}
	return rep
}

// validateReport validates doc against the report's schema, a test fixture, whose if/then holds the report's pass
// rule: passed true only with the pre-fix commit not ingested, retries 5 and
// restarts 1, and the fix commit ingested.
func validateReport(t *testing.T, doc []byte) error {
	t.Helper()
	const name = "wallet-backend-729-report.schema.json"
	sb, err := os.ReadFile(filepath.Join("..", "..", "contract", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	sdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(sb))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("file:///fixtures/"+name, sdoc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("file:///fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

// TestCommittedReportIsReproduced: the committed report is the writer's own document for its value (the report
// writer accepts its shape and writes the same bytes); it validates against the report's schema, pass rule included;
// its passed is true and the verdict finds no failed condition in it (Reasons nil, and none when its passed is set
// aside); and its counts are those of the reproduction as wallet-backend logs it (attempts 1 to 4 logged, the fifth in
// the exit line): the pre-fix commit not ingested, 5 failed attempts in its last run, 1 restart, its error
// wallet-backend's own for ledger L; the fix commit ingested with its SIGNER row at L, 0 retries, 0 restarts; one
// ledger; protocol 28.
func TestCommittedReportIsReproduced(t *testing.T) {
	b, rep := committedReport(t)

	written, err := catalogue.WriteWalletBackendReport(t.TempDir(), rep)
	if err != nil {
		t.Fatalf("the report writer refuses the committed report: %v", err)
	}
	if !bytes.Equal(written, b) {
		t.Errorf("the committed report is not the writer's document for its value:\n%s\nwant\n%s", b, written)
	}
	if err := validateReport(t, b); err != nil {
		t.Errorf("the committed report does not validate against the report's schema: %v", err)
	}

	if !rep.Passed {
		t.Fatalf("the committed report says passed false; reasons %q", Reasons(rep))
	}
	if got := Reasons(rep); got != nil {
		t.Errorf("Reasons of the committed report: %q", got)
	}
	if got := reportReasons(rep); got != nil {
		t.Errorf("the committed report's fields fail the verdict: %q", got)
	}

	if rep.Target != contract.ReproTargetWalletBackend729 || rep.ProtocolVersion != contract.WALLET_BACKEND_729_PROTOCOL_VERSION || len(rep.Commits) != 2 {
		t.Fatalf("report %+v", rep)
	}
	pre, fix := rep.Commits[0], rep.Commits[1]
	L := pre.Ledger
	if L < 1 || fix.Ledger != L {
		t.Errorf("ledgers %d and %d", pre.Ledger, fix.Ledger)
	}
	if pre.Commit != contract.WalletBackendCommit2a0a84ea || pre.Role != contract.FixRoleBeforeFix || pre.Ingested || pre.StoredRow != nil ||
		pre.Retries != contract.PRE_FIX_RETRY_LIMIT || pre.Restarts != contract.PRE_FIX_RESTART_COUNT || pre.Error == nil ||
		!strings.HasPrefix(*pre.Error, "Error ingesting data for ledger "+itoa(L)+" (attempt 1/5): ") {
		t.Errorf("before_fix %+v (error %s)", pre, deref(pre.Error))
	}
	if fix.Commit != contract.WalletBackendCommit2c873353 || fix.Role != contract.FixRoleFix || !fix.Ingested || fix.Error != nil ||
		fix.StoredRow == nil || fix.Retries != 0 || fix.Restarts != 0 {
		t.Errorf("fix %+v", fix)
	}
	if fix.StoredRow != nil {
		var row struct {
			Category string `json:"state_change_category"`
			Ledger   int    `json:"ledger_number"`
		}
		if err := json.Unmarshal(*fix.StoredRow, &row); err != nil || row.Category != "SIGNER" || row.Ledger != L {
			t.Errorf("the fix commit's stored row is not the SIGNER row at ledger %d: %s (%v)", L, *fix.StoredRow, err)
		}
	}
	// Each commit's evidence is named where the runner saves it, <work>/<commit>/wallet-backend.log and
	// store-state.txt, outside the repository's tree (repro/ holds the reports only). The files themselves were in the
	// temporary working folder of the run that wrote the committed report; that they existed then is the command's
	// check, which requires both files before it writes passed true, and a clone does not hold them.
	for _, c := range rep.Commits {
		lp, sp := filepath.ToSlash(c.LogPath), filepath.ToSlash(c.StoreStatePath)
		if path.Base(lp) != logFileName || path.Base(sp) != storeFileName ||
			path.Base(path.Dir(lp)) != string(c.Commit) || path.Dir(lp) != path.Dir(sp) || !path.IsAbs(lp) {
			t.Errorf("commit %s: evidence paths %q and %q", c.Commit, c.LogPath, c.StoreStatePath)
		}
	}
}

// TestCommittedReportChangedToAFailingShapeIsNamed: the committed report, changed to each failing shape of the verdict,
// is named by Reasons once its passed is false, as the runner sets it from the evidence; with passed left true, the
// report schema's pass rule refuses each shape it can express (a shape it cannot, the ledgers, only the verdict names).
// With passed false each changed report is still one the writer accepts, so the command writes it and exits 1.
func TestCommittedReportChangedToAFailingShapeIsNamed(t *testing.T) {
	b, base := committedReport(t)
	L := itoa(base.Commits[0].Ledger)
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name          string
		change        func(r *contract.WalletBackend729Report)
		schemaRejects bool
		want          []string
	}{
		{
			name: "the pre-fix commit ingested the signer",
			change: func(r *contract.WalletBackend729Report) {
				row := *r.Commits[1].StoredRow
				r.Commits[0].Ingested, r.Commits[0].Error, r.Commits[0].StoredRow, r.Commits[0].Retries, r.Commits[0].Restarts = true, nil, &row, 0, 0
			},
			schemaRejects: true,
			want:          []string{"commit 2a0a84ea (before_fix) ingested ledger " + L, "showed 0 failed attempts", "was restarted 0 times, not 1"},
		},
		{
			name: "the pre-fix run reached the safety deadline",
			change: func(r *contract.WalletBackend729Report) {
				r.Commits[0].Retries, r.Commits[0].Restarts = 4, 0
				r.Commits[0].Error = str("its first run reached the 15m0s safety deadline after 4 failed attempts on ledger " + L + ", without exiting; it was restarted 0 times, not 1")
			},
			schemaRejects: true,
			want:          []string{"showed 4 failed attempts on ledger " + L + " in its last run, not 5", "was restarted 0 times, not 1", "reached the 15m0s safety deadline"},
		},
		{
			name:          "the pre-fix commit was not restarted",
			change:        func(r *contract.WalletBackend729Report) { r.Commits[0].Restarts = 0 },
			schemaRejects: true,
			want:          []string{"commit 2a0a84ea (before_fix) was restarted 0 times, not 1"},
		},
		{
			name: "the fix commit did not ingest",
			change: func(r *contract.WalletBackend729Report) {
				r.Commits[1].Ingested, r.Commits[1].StoredRow = false, nil
				r.Commits[1].Error = str("it did not ingest ledger " + L + " within the 15m0s safety deadline (ingest cursor 24)")
			},
			schemaRejects: true,
			want:          []string{"commit 2c873353 (fix): it did not ingest ledger " + L + " within the 15m0s safety deadline"},
		},
		{
			name:   "the two commits name two ledgers",
			change: func(r *contract.WalletBackend729Report) { r.Commits[1].Ledger++ },
			want:   []string{"the two commits name ledgers"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := decodeReport(t, b) // a fresh copy of the committed report
			tc.change(&rep)
			if got := reportReasons(rep); len(got) == 0 {
				t.Fatal("the verdict finds no failed condition in the changed report")
			}
			if tc.schemaRejects {
				doc, err := catalogue.EncodeReproReport(rep)
				if err != nil {
					t.Fatal(err)
				}
				if validateReport(t, doc) == nil {
					t.Error("the report's schema accepts the changed report with passed true")
				}
			}
			rep.Passed = false
			reasons := Reasons(rep)
			joined := strings.Join(reasons, "; ")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("Reasons %q do not name %q", reasons, w)
				}
			}
			doc, err := catalogue.WriteWalletBackendReport(t.TempDir(), rep)
			if err != nil {
				t.Fatalf("the writer refuses the changed report: %v", err)
			}
			if err := validateReport(t, doc); err != nil {
				t.Errorf("the changed report with passed false does not validate against the report's schema: %v", err)
			}
		})
	}
}

// itoa is n in decimal.
func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
