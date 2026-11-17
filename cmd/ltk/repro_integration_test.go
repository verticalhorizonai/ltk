//go:build integration

package main

// `ltk repro js-sdk-1766` and `ltk repro wallet-backend-729` with Docker: the built program against the real images,
// each run from a t.TempDir() working directory, so the repository's own repro/ is never written. Each run is between
// two snapshots of the engine (cleanStopRunIn, check_integration_test.go): the label-filtered listings are empty after
// it and nothing new is in the engine. Run serially, with nothing else using Docker, on a machine with Docker running
// Linux containers, the images present or pullable and the network open to the npm registry, GitHub and the Go module
// proxy and checksum database, with LTK_BINARY naming a program built for it:
//
//	go test -tags integration -p 1 -timeout 120m -run IntegrationRepro ./cmd/ltk/
//
// The wallet-backend reproduction builds wallet-backend at two commits from source and runs each pre-fix run to its
// exit; no progress line is written, so the run is silent until it ends.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// reproRun runs `ltk repro target` from a fresh working directory between two snapshots of the engine (the run the
// acceptance tests share, reproductionRun in cleanstop_test.go: one run per test binary) and asserts what every passed
// reproduction shows: nothing left in the engine, exit 0, nothing on stderr, the report on stdout byte-identical to
// repro/<target>.json, which validates against its schema, and repro/ holding that report only.
func reproRun(t *testing.T, target contract.ReproTarget, schemaFile string) []byte {
	t.Helper()
	rec := reproductionRun(t, target)
	rec.assertCleanStop(t)
	dir, code, stdout, stderr := rec.dir, rec.code, rec.stdout, rec.stderr
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %s; want 0 and nothing", code, stderr)
	}
	file, err := os.ReadFile(catalogue.ReproReportPath(dir, target))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout, file) {
		t.Errorf("stdout is not byte-identical to repro/%s.json, the report the command saved", target)
	}
	validateOutput(t, schemaFile, stdout)
	es, err := os.ReadDir(filepath.Join(dir, catalogue.ReproFolder))
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Name() != string(target)+".json" {
		t.Errorf("repro/ holds %v; want the report only", es)
	}
	return stdout
}

// TestIntegrationReproJsSdk: the one script runs against 17.2.0 and 17.2.1 in the pinned Node container; 17.2.0's build
// step fails with the error "Assets are not in lexicographic order", 17.2.1 passes build, decode and re_encode with
// roundTripIdentical true; passed true, exit 0; nothing is left in Docker.
func TestIntegrationReproJsSdk(t *testing.T) {
	stdout := reproRun(t, contract.ReproTargetJsSdk1766, "js-sdk-1766-report.schema.json")
	var rep contract.JsSdk1766Report
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || rep.Target != contract.ReproTargetJsSdk1766 || len(rep.Versions) != 2 {
		t.Fatalf("%s", stdout)
	}
	before, fix := rep.Versions[0], rep.Versions[1]
	if before.Version != contract.SdkVersion17_2_0 || before.Role != contract.FixRoleBeforeFix || len(before.Steps) != 1 ||
		before.Steps[0].Step != contract.SdkReproStepBuild || before.Steps[0].Passed || before.Steps[0].Error == nil ||
		*before.Steps[0].Error != "Assets are not in lexicographic order" || before.RoundTripIdentical {
		t.Errorf("17.2.0: %+v", before)
	}
	want := []contract.SdkReproStep{contract.SdkReproStepBuild, contract.SdkReproStepDecode, contract.SdkReproStepReEncode}
	if fix.Version != contract.SdkVersion17_2_1 || fix.Role != contract.FixRoleFix || len(fix.Steps) != 3 || !fix.RoundTripIdentical {
		t.Fatalf("17.2.1: %+v", fix)
	}
	for i, s := range fix.Steps {
		if s.Step != want[i] || !s.Passed || s.Error != nil {
			t.Errorf("17.2.1 step %d: %+v", i, s)
		}
	}
}

// TestIntegrationReproWalletBackend: wallet-backend is
// built at 2a0a84ea and 2c873353, both run against their own databases on the protocol-28 network, entry 1's
// transaction is submitted; 2a0a84ea fails five times and exits, and does the same after one restart, without
// ingesting (error set, storedRow null); 2c873353 ingests the same ledger (storedRow an object, error null); passed
// true, exit 0. Each commit's log and store state are files in the working folder outside the working directory
// (<temporary directory>/ltk-<runID>/wallet-backend-729/<commit>/), left in place; the pre-fix log holds the five
// failures of each run; nothing is left in Docker.
func TestIntegrationReproWalletBackend(t *testing.T) {
	stdout := reproRun(t, contract.ReproTargetWalletBackend729, "wallet-backend-729-report.schema.json")
	var rep contract.WalletBackend729Report
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || rep.ProtocolVersion != contract.WALLET_BACKEND_729_PROTOCOL_VERSION || len(rep.Commits) != 2 {
		t.Fatalf("%s", stdout)
	}
	pre, fix := rep.Commits[0], rep.Commits[1]
	if pre.Commit != contract.WalletBackendCommit2a0a84ea || pre.Role != contract.FixRoleBeforeFix || pre.Ingested ||
		pre.Error == nil || *pre.Error == "" || pre.StoredRow != nil ||
		pre.Retries != contract.PRE_FIX_RETRY_LIMIT || pre.Restarts != contract.PRE_FIX_RESTART_COUNT {
		t.Errorf("before_fix: %+v", pre)
	}
	if fix.Commit != contract.WalletBackendCommit2c873353 || fix.Role != contract.FixRoleFix || !fix.Ingested ||
		fix.Error != nil || fix.StoredRow == nil {
		t.Errorf("fix: %+v", fix)
	}
	if pre.Ledger < 1 || pre.Ledger != fix.Ledger {
		t.Errorf("ledgers %d and %d; want entry 1's ledger, the same for both", pre.Ledger, fix.Ledger)
	}
	tmp := filepath.Clean(os.TempDir()) + string(filepath.Separator)
	for _, c := range rep.Commits {
		for _, p := range []string{c.LogPath, c.StoreStatePath} {
			st, err := os.Stat(p)
			if err != nil || !st.Mode().IsRegular() {
				t.Errorf("commit %s: %s is not a saved file: %v", c.Commit, p, err)
			}
			rel := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), tmp))
			if !strings.HasPrefix(filepath.Clean(p), tmp) || !strings.HasPrefix(rel, "ltk-") ||
				!strings.Contains(rel, "/"+string(contract.ReproTargetWalletBackend729)+"/"+string(c.Commit)+"/") {
				t.Errorf("commit %s: %s is not under <temporary directory>/ltk-<runID>/wallet-backend-729/%s/", c.Commit, p, c.Commit)
			}
		}
	}
	if pre.StoreStatePath == fix.StoreStatePath || pre.LogPath == fix.LogPath {
		t.Errorf("the two commits share an evidence file: %+v", rep.Commits)
	}
	log, err := os.ReadFile(pre.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	// wallet-backend logs attempts 1 to 4 as "Error ingesting data for ledger L (attempt k/5)" and the fifth as the
	// line it exits with, "… processing ledger L: ingesting processed data failed after 5 attempts"; each of the two
	// runs (the first, and the one after the restart) shows them all.
	var want []string
	for k := 1; k < contract.PRE_FIX_RETRY_LIMIT; k++ {
		want = append(want, fmt.Sprintf("Error ingesting data for ledger %d (attempt %d/%d)", pre.Ledger, k, contract.PRE_FIX_RETRY_LIMIT))
	}
	want = append(want, fmt.Sprintf("processing ledger %d: ingesting processed data failed after %d attempts", pre.Ledger, contract.PRE_FIX_RETRY_LIMIT))
	for _, line := range want {
		if n := strings.Count(string(log), line); n < 2 {
			t.Errorf("the pre-fix log holds %q %d times; want it in each of the two runs, the first and the one after the restart", line, n)
		}
	}
}

// TestIntegrationReproWalletBackendInterrupt, as for `ltk generate`: a running `ltk repro wallet-backend-729` is sent
// SIGINT once its first container exists. It exits 1 with one ErrorLine saying it was interrupted and no report, writes
// nothing under repro/; the difference between the snapshots before and after and the three label-filtered listings are
// empty.
func TestIntegrationReproWalletBackendInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: os.Process.Signal cannot send an interrupt there")
	}
	if left := managedListings(t); left != "" {
		t.Fatalf("resources of an earlier run exist; remove them by hand (docker rm -f -v, docker network rm and docker volume rm, filtered by label=ltk.managed=true): %s", left)
	}
	r, err := orchestration.NewRunner("00000000")
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := ltkCommand(t, dir, nil, "repro", string(contract.ReproTargetWalletBackend729))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill() }()

	// The first container appears once the environment check and the image inspection are done: seconds, or the pull
	// of an absent image. Ten minutes bounds a cold pull on a loaded machine with a wide margin.
	deadline := time.Now().Add(10 * time.Minute)
	for managedContainers(t) == "" {
		select {
		case <-done:
			t.Fatalf("the program ended before its first container existed: stdout %s stderr %s", stdout.Bytes(), stderr.Bytes())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no container labelled ltk.managed=true appeared")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	<-done
	code := exitCodeOfRun(t, waitErr)
	if code != int(contract.EXIT_CODES.Failure) || stdout.Len() != 0 {
		t.Fatalf("exit %d stdout %q stderr %s; want 1 and no report", code, stdout.Bytes(), stderr.Bytes())
	}
	line := assertErrorLine(t, stderr.Bytes(), contract.ErrorCodeFailure)
	if !strings.HasPrefix(line.Message, "interrupted before the command finished: ltk repro wallet-backend-729: ") {
		t.Errorf("message %q", line.Message)
	}
	if exists(filepath.Join(dir, catalogue.ReproFolder)) {
		t.Errorf("an interrupted run wrote under repro/")
	}
	after, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if left := orchestration.Leftovers(before, after); len(left) != 0 {
		t.Errorf("the interrupted run left %v", left)
	}
	if left := managedListings(t); left != "" {
		t.Errorf("the label-filtered listings are not empty after the interrupt: %s", left)
	}
	t.Logf("ltk repro wallet-backend-729 interrupted: exit %d; stderr %s", code, stderr.Bytes())
}
