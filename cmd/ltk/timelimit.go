package main

// The Pre-fix time-limit guard and its log capture reference, for `ltk repro wallet-backend-729`. The pre-fix loop
// itself (five failures, an exit, one restart, each run bounded by the deadline) is in the runner,
// internal/repro/walletbackend, which cannot import this package: the guard owns the deadline the runner is given, the
// reading of the report the runner returns, and the reference to the saved logs that back it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// commitRunDeadline is the safety deadline of each run of a wallet-backend commit: COMMIT_RUN_DEADLINE_MINUTES
// (15 minutes). It is a safety limit only, never the primary stop: the pre-fix commit is judged by its five
// failures, its exit and the same after one restart. It is passed to the runner as walletbackend.Options.Deadline;
// no test waits for it to run out.
const commitRunDeadline = time.Duration(contract.COMMIT_RUN_DEADLINE_MINUTES) * time.Minute

// guardWalletBackend holds the runner's report to the reproduction's verdict and to the shape of the wallet-backend
// report (schemas/), and returns the reasons of a report not passed, then where each commit's log is saved. A report
// is passed only when the runner says so and:
//   - the before_fix commit did not ingest, with retries PRE_FIX_RETRY_LIMIT (5) and restarts PRE_FIX_RESTART_COUNT (1):
//     a run that reached the deadline before showing its five failures and its exit shows fewer, and is not
//     reproduced, whatever the runner concluded;
//   - the fix commit ingested;
//   - each commit's saved log and store state exist as files where the report names them: they back the verdict.
//
// A report the guard does not pass is marked passed false, so the saved report, standard output and the exit agree.
// The reasons are the runner's when it judged the report not passed and gave them, with the guard's missing evidence;
// otherwise the guard's own.
func guardWalletBackend(rep *contract.WalletBackend729Report, runnerReasons []string) []string {
	if len(rep.Commits) != 2 {
		return runnerReasons // not the report's shape (two commits): the writer refuses it
	}
	var verdict, evidence []string
	pre, fix := rep.Commits[0], rep.Commits[1]
	deadline := fmt.Sprintf("each run bounded by the safety deadline of %d minutes", contract.COMMIT_RUN_DEADLINE_MINUTES)
	switch {
	case pre.Ingested:
		verdict = append(verdict, fmt.Sprintf("commit %s (%s) ingested ledger %d: the failure did not show", pre.Commit, pre.Role, pre.Ledger))
	case pre.Retries != contract.PRE_FIX_RETRY_LIMIT || pre.Restarts != contract.PRE_FIX_RESTART_COUNT:
		verdict = append(verdict, fmt.Sprintf("commit %s (%s) showed %d of %d failures and %d of %d restarts, %s: not reproduced",
			pre.Commit, pre.Role, pre.Retries, contract.PRE_FIX_RETRY_LIMIT, pre.Restarts, contract.PRE_FIX_RESTART_COUNT, deadline))
	}
	if !fix.Ingested {
		verdict = append(verdict, fmt.Sprintf("commit %s (%s) did not ingest ledger %d, %s", fix.Commit, fix.Role, fix.Ledger, deadline))
	}
	for _, c := range rep.Commits {
		for _, ev := range []struct{ what, path string }{{"log", c.LogPath}, {"store state", c.StoreStatePath}} {
			if err := savedFile(ev.path); err != nil {
				evidence = append(evidence, fmt.Sprintf("the %s of commit %s is not saved: %v", ev.what, c.Commit, err))
			}
		}
	}
	if rep.Passed && len(verdict) == 0 && len(evidence) == 0 {
		return nil
	}
	var reasons []string
	if !rep.Passed && len(runnerReasons) > 0 {
		reasons = mergeReasons(runnerReasons, evidence)
	} else {
		reasons = mergeReasons(runnerReasons, append(verdict, evidence...))
	}
	rep.Passed = false
	return append(reasons, fmt.Sprintf("the logs are saved at %s (%s) and %s (%s)",
		filepath.ToSlash(pre.LogPath), pre.Commit, filepath.ToSlash(fix.LogPath), fix.Commit)) // forward slashes always
}

// savedFile is nil when path names a regular file.
func savedFile(path string) error {
	if path == "" {
		return errors.New("the report names no path")
	}
	st, err := os.Stat(path)
	switch {
	case err != nil:
		return fmt.Errorf("%s cannot be read", filepath.ToSlash(path))
	case !st.Mode().IsRegular():
		return fmt.Errorf("%s is not a file", filepath.ToSlash(path))
	}
	return nil
}
