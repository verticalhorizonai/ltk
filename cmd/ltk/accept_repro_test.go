//go:build integration

package main

// The reproduction acceptance tests: `ltk repro wallet-backend-729` and `ltk repro js-sdk-1766` each exit 0, the
// earlier version failing and the fixed one passing. Both tests live in this one file.
//
// Each reproduction is run by the built program from a fresh working directory outside the repository, so the
// repository's own repro/ is never written; the run is the one the integration tier shares (reproductionRun,
// cleanstop_test.go). Run serially, with nothing else using Docker, on a machine with Docker running Linux containers,
// the images present or pullable and the network open to the npm registry, GitHub and the Go module proxy and checksum
// database, with LTK_BINARY naming a program built for it:
//
//	go test -tags integration -p 1 -timeout 120m -run 'AcceptRepro' ./cmd/ltk/

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
)

// acceptReproduction asserts what both targets require of their run: nothing left in the engine; exit 0 and nothing on
// standard error; the report printed byte-identical to repro/<target>.json under the run's working directory, valid
// against its schema; repro/ holding that report only; nothing written under the committed corpus/ or repro/. It
// returns the report's bytes.
func acceptReproduction(t *testing.T, target contract.ReproTarget) []byte {
	t.Helper()
	rec := reproductionRun(t, target)
	rec.assertCleanStop(t)
	rec.assertCommittedUntouched(t)
	if rec.code != 0 || len(rec.stderr) != 0 {
		t.Fatalf("ltk repro %s: exit %d stderr %s; want 0 and nothing\n%s", target, rec.code, rec.stderr, rec.stdout)
	}
	file, err := os.ReadFile(catalogue.ReproReportPath(rec.dir, target))
	if err != nil {
		t.Fatalf("repro/%s.json under the working directory: %v", target, err)
	}
	if !bytes.Equal(rec.stdout, file) {
		t.Errorf("standard output is not byte-identical to repro/%s.json", target)
	}
	oneJSONValue(t, rec.stdout)
	validateOutput(t, string(target)+"-report.schema.json", rec.stdout)
	es, err := os.ReadDir(filepath.Join(rec.dir, catalogue.ReproFolder))
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Name() != string(target)+".json" {
		t.Errorf("repro/ holds %v; want the report only", es)
	}
	return rec.stdout
}

// TestAcceptReproWalletBackend: `ltk repro wallet-backend-729` exits 0 with the earlier
// commit failing and the fixed one passing, every condition of a passing report: target, protocolVersion 28;
// 2a0a84ea before_fix, not ingested, its error set, storedRow null, retries 5 (attempts 1 to 4 are logged and the
// fifth is in the exit line) and restarts 1 with the same failure shown again; 2c873353 fix, ingested, error null,
// storedRow the SIGNER row; both at entry 1's ledger; passed true. Every file the report references exists and is not
// empty, in <temporary directory>/ltk-<runID>/wallet-backend-729/<commit>/, never under repro/;
// the pre-fix log shows the five failures in each of its two runs.
func TestAcceptReproWalletBackend(t *testing.T) {
	stdout := acceptReproduction(t, contract.ReproTargetWalletBackend729)
	var rep contract.WalletBackend729Report
	if err := decodeStrict(stdout, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Target != contract.ReproTargetWalletBackend729 || rep.ProtocolVersion != contract.WALLET_BACKEND_729_PROTOCOL_VERSION ||
		!rep.Passed || len(rep.Commits) != 2 {
		t.Fatalf("%s", stdout)
	}
	pre, fix := rep.Commits[0], rep.Commits[1]
	if pre.Commit != contract.WalletBackendCommit2a0a84ea || pre.Role != contract.FixRoleBeforeFix || pre.Ingested ||
		pre.Error == nil || *pre.Error == "" || pre.StoredRow != nil ||
		pre.Retries != contract.PRE_FIX_RETRY_LIMIT || pre.Restarts != contract.PRE_FIX_RESTART_COUNT {
		t.Errorf("before_fix: %+v", pre)
	}
	var row map[string]json.RawMessage
	if fix.Commit != contract.WalletBackendCommit2c873353 || fix.Role != contract.FixRoleFix || !fix.Ingested || fix.Error != nil ||
		fix.StoredRow == nil || json.Unmarshal(*fix.StoredRow, &row) != nil || string(row["state_change_category"]) != `"SIGNER"` {
		t.Errorf("fix: %+v", fix)
	}
	if pre.Ledger < 1 || pre.Ledger != fix.Ledger {
		t.Errorf("ledgers %d and %d; want entry 1's ledger, the same for both", pre.Ledger, fix.Ledger)
	}
	for _, c := range rep.Commits {
		for _, p := range []string{c.LogPath, c.StoreStatePath} {
			st, err := os.Stat(p)
			if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
				t.Errorf("commit %s: %s is not a saved, non-empty file: %v", c.Commit, p, err)
			}
			s := filepath.ToSlash(p)
			if !strings.Contains(s, "/"+string(contract.ReproTargetWalletBackend729)+"/"+string(c.Commit)+"/") || strings.Contains(s, "/repro/") {
				t.Errorf("commit %s: %s is not in its commit's working folder outside repro/", c.Commit, p)
			}
		}
	}
	log, err := os.ReadFile(pre.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	for k := 1; k < contract.PRE_FIX_RETRY_LIMIT; k++ {
		line := fmt.Sprintf("Error ingesting data for ledger %d (attempt %d/%d)", pre.Ledger, k, contract.PRE_FIX_RETRY_LIMIT)
		if n := strings.Count(string(log), line); n < 1+contract.PRE_FIX_RESTART_COUNT {
			t.Errorf("the pre-fix log holds %q %d times; want it in each of the runs, attempts 1 to 4", line, n)
		}
	}
	exit := fmt.Sprintf("processing ledger %d: ingesting processed data failed after %d attempts", pre.Ledger, contract.PRE_FIX_RETRY_LIMIT)
	if n := strings.Count(string(log), exit); n < 1+contract.PRE_FIX_RESTART_COUNT {
		t.Errorf("the pre-fix log holds %q %d times; want it in each of the runs, after the fifth attempt", exit, n)
	}
}

// TestAcceptReproJsSdk: `ltk repro js-sdk-1766` exits 0 with the earlier release failing and
// the fixed one passing, every condition of a passing report: target; 17.2.0 before_fix, a step failed with a
// non-null error (its build step, with the SDK's error "Assets are not in lexicographic order"),
// roundTripIdentical false; 17.2.1 fix, build, decode and re_encode each passed with error null, roundTripIdentical
// true; passed true.
func TestAcceptReproJsSdk(t *testing.T) {
	stdout := acceptReproduction(t, contract.ReproTargetJsSdk1766)
	var rep contract.JsSdk1766Report
	if err := decodeStrict(stdout, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Target != contract.ReproTargetJsSdk1766 || !rep.Passed || len(rep.Versions) != 2 {
		t.Fatalf("%s", stdout)
	}
	before, fix := rep.Versions[0], rep.Versions[1]
	failed := slices.IndexFunc(before.Steps, func(s contract.SdkStepResult) bool { return !s.Passed && s.Error != nil })
	if before.Version != contract.SdkVersion17_2_0 || before.Role != contract.FixRoleBeforeFix || before.RoundTripIdentical || failed < 0 ||
		before.Steps[failed].Step != contract.SdkReproStepBuild || *before.Steps[failed].Error != "Assets are not in lexicographic order" {
		t.Errorf("17.2.0: %+v", before)
	}
	steps := []contract.SdkReproStep{contract.SdkReproStepBuild, contract.SdkReproStepDecode, contract.SdkReproStepReEncode}
	if fix.Version != contract.SdkVersion17_2_1 || fix.Role != contract.FixRoleFix || !fix.RoundTripIdentical || len(fix.Steps) != len(steps) {
		t.Fatalf("17.2.1: %+v", fix)
	}
	for i, s := range fix.Steps {
		if s.Step != steps[i] || !s.Passed || s.Error != nil {
			t.Errorf("17.2.1 step %d: %+v", i, s)
		}
	}
}
