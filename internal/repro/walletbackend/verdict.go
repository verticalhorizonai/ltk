package walletbackend

// The wallet-backend reproduction verdict: pure functions of the collected evidence and of the report, unit-tested on
// recorded logs (testdata/), separate from the container work.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// Evidence is what the runner collected from both commits after entry 1's transaction landed in ledger Ledger.
type Evidence struct {
	// Ledger is L, the ledger of entry 1's transaction.
	Ledger int
	// Deadline is the safety deadline of each run, for the reasons' words.
	Deadline time.Duration
	// PreFix is 2a0a84ea's evidence; Fix is 2c873353's.
	PreFix, Fix CommitEvidence
}

// CommitEvidence is one commit's evidence.
type CommitEvidence struct {
	// Live is true when its ingest cursor was above 0 before the transaction was sent; Baseline is that cursor.
	Live     bool
	Baseline int
	// Runs are its runs after the transaction was sent: one for the fix commit, two for the pre-fix commit when it
	// exited and was restarted.
	Runs []RunEvidence
	// Restarts counts the `docker start` calls made.
	Restarts int
	// Cursor is its ingest cursor after its runs; Row its SIGNER row at L, nil when there is none.
	Cursor int
	Row    json.RawMessage
}

// RunEvidence is one run of a commit's ingester.
type RunEvidence struct {
	// Failures is the number of failed attempts on ledger L its log shows (failures()).
	Failures int
	// Exited is true when the container stopped by itself; ExitCode is then its code.
	Exited   bool
	ExitCode int
	// Ingested is true when the store held ledger L (cursor at L or later and the SIGNER row) during the run.
	Ingested bool
	// DeadlineReached is true when the deadline passed with none of the above.
	DeadlineReached bool
	// Lines are the run's log lines, timestamped.
	Lines []string
}

// ingested is the commit's outcome on ledger L: its cursor at L or later and its SIGNER row stored.
func (ev CommitEvidence) ingested(ledger int) bool { return ev.Cursor >= ledger && ev.Row != nil }

// lastRun is the commit's last run (the restart's for the pre-fix commit); the zero value when it has none.
func (ev CommitEvidence) lastRun() RunEvidence {
	if len(ev.Runs) == 0 {
		return RunEvidence{}
	}
	return ev.Runs[len(ev.Runs)-1]
}

// logLines is the commit's log file: its runs in order, the restart marker between them.
func (ev CommitEvidence) logLines(c commit) []string {
	var out []string
	for i, r := range ev.Runs {
		if i > 0 {
			out = append(out, restartMarker(c))
		}
		out = append(out, r.Lines...)
	}
	return out
}

// lastIngestError is the ingest error of the commit's latest run that logged one.
func (ev CommitEvidence) lastIngestError(ledger int) string {
	for i := len(ev.Runs) - 1; i >= 0; i-- {
		if e := ingestError(ev.Runs[i].Lines, ledger); e != "" {
			return e
		}
	}
	return ""
}

// runName names a pre-fix run in a reason.
func runName(i int) string {
	if i == 0 {
		return "its first run"
	}
	return "its run after the restart"
}

// preFixReasons are the failed conditions of the pre-fix commit: it was ingesting before the transaction; each of its
// two runs failed PRE_FIX_RETRY_LIMIT times on ledger L and exited, before the deadline; it was restarted
// PRE_FIX_RESTART_COUNT time; its store never held L; each run's log is not empty.
func (e Evidence) preFixReasons() []string {
	ev, L := e.PreFix, e.Ledger
	var reasons []string
	if !ev.Live {
		reasons = append(reasons, "it was not ingesting before the transaction was sent")
	}
	for i, r := range ev.Runs {
		switch {
		case r.Ingested:
			reasons = append(reasons, fmt.Sprintf("%s ingested ledger %d", runName(i), L))
		case r.DeadlineReached:
			reasons = append(reasons, fmt.Sprintf("%s reached the %s safety deadline after %d failed attempts on ledger %d, without exiting", runName(i), e.Deadline, r.Failures, L))
		case r.Failures != contract.PRE_FIX_RETRY_LIMIT:
			reasons = append(reasons, fmt.Sprintf("%s exited (code %d) after %d failed attempts on ledger %d, not %d", runName(i), r.ExitCode, r.Failures, L, contract.PRE_FIX_RETRY_LIMIT))
		}
		if len(r.Lines) == 0 {
			reasons = append(reasons, fmt.Sprintf("the log of %s is empty", runName(i)))
		}
	}
	if ev.Restarts != contract.PRE_FIX_RESTART_COUNT {
		reasons = append(reasons, fmt.Sprintf("it was restarted %d times, not %d", ev.Restarts, contract.PRE_FIX_RESTART_COUNT))
	}
	if ev.Cursor >= L {
		reasons = append(reasons, fmt.Sprintf("its ingest cursor reached %d, at or past ledger %d", ev.Cursor, L))
	} else if ev.Live && ev.Cursor < ev.Baseline {
		reasons = append(reasons, fmt.Sprintf("its ingest cursor %d fell below its baseline %d", ev.Cursor, ev.Baseline))
	}
	if ev.Row != nil {
		reasons = append(reasons, fmt.Sprintf("it stored a SIGNER row at ledger %d", L))
	}
	return reasons
}

// fixReasons are the failed conditions of the fix commit: it was ingesting before
// the transaction, and its cursor reached L with its SIGNER row stored, before the deadline.
func (e Evidence) fixReasons() []string {
	ev, L := e.Fix, e.Ledger
	var reasons []string
	if !ev.Live {
		reasons = append(reasons, "it was not ingesting before the transaction was sent")
	}
	if !ev.ingested(L) {
		switch r := ev.lastRun(); {
		case ev.Cursor >= L:
			reasons = append(reasons, fmt.Sprintf("its ingest cursor reached %d but it stored no SIGNER row at ledger %d", ev.Cursor, L))
		case r.Exited:
			reasons = append(reasons, fmt.Sprintf("it exited with code %d before ingesting ledger %d (ingest cursor %d)", r.ExitCode, L, ev.Cursor))
		case r.DeadlineReached:
			reasons = append(reasons, fmt.Sprintf("it did not ingest ledger %d within the %s safety deadline (ingest cursor %d)", L, e.Deadline, ev.Cursor))
		default:
			reasons = append(reasons, fmt.Sprintf("it did not ingest ledger %d (ingest cursor %d)", L, ev.Cursor))
		}
		if msg := ev.lastIngestError(L); msg != "" {
			reasons = append(reasons, "its log: "+msg)
		}
	}
	return reasons
}

// reasons are every failed condition, each naming its commit; none means reproduced.
func (e Evidence) reasons() []string {
	var out []string
	for _, r := range e.preFixReasons() {
		out = append(out, fmt.Sprintf("commit %s (%s): %s", commits[0].short, commits[0].role, r))
	}
	for _, r := range e.fixReasons() {
		out = append(out, fmt.Sprintf("commit %s (%s): %s", commits[1].short, commits[1].role, r))
	}
	return out
}

// fillResult sets the commit's outcome fields of its report entry from the evidence:
// ingested; error, a non-empty string when not ingested (the ingest error wallet-backend logged when the commit showed
// exactly its expected failure, else every failed condition then that error); storedRow when ingested; retries, the
// failures of its last run (the restart's; each of the two runs must show them); restarts.
func fillResult(res *contract.WalletBackendCommitResult, e Evidence, c commit) {
	ev, own := e.Fix, e.fixReasons()
	if c.role == contract.FixRoleBeforeFix {
		ev, own = e.PreFix, e.preFixReasons()
	}
	res.Retries = ev.lastRun().Failures
	res.Restarts = ev.Restarts
	res.Ingested = ev.ingested(e.Ledger)
	if res.Ingested {
		row := contract.JsonObject(ev.Row)
		res.StoredRow = &row
		return
	}
	logged := ev.lastIngestError(e.Ledger)
	var msg string
	switch {
	case len(own) == 0 && logged != "":
		msg = logged
	case len(own) == 0:
		msg = fmt.Sprintf("it did not ingest ledger %d", e.Ledger)
	case logged != "" && c.role == contract.FixRoleBeforeFix:
		msg = strings.Join(own, "; ") + "; its log: " + logged
	default:
		msg = strings.Join(own, "; ")
	}
	res.Error = &msg
}

// Reasons returns why the report's verdict is not reproduced, one sentence each, for the ErrorLine of `ltk repro
// wallet-backend-729` (the report schema has no field for them); nil when rep.Passed is true. It reads the report
// alone: its conditions are those of the report's pass rule (reportReasons). A condition the report's
// fields cannot show (a run that reached the safety deadline without exiting, a commit not ingesting before the
// transaction) is in that commit's error, which the runner writes from the evidence: the pre-fix commit's error is a
// reason whenever its fields fail (so a run that reached the deadline is named as such), and the only
// reason when every field shows the expected behaviour.
func Reasons(rep contract.WalletBackend729Report) []string {
	if rep.Passed {
		return nil
	}
	reasons := reportReasons(rep)
	if len(reasons) > 0 {
		return reasons
	}
	pre, fix := rep.Commits[0], rep.Commits[1]
	// Every field shows the expected behaviour: the condition only the evidence shows is in the pre-fix commit's error.
	if pre.Error != nil && *pre.Error != "" {
		return []string{commitName(pre) + ": " + *pre.Error}
	}
	return []string{"the report says not reproduced, though its fields show the expected behaviour of both commits; their logs hold the evidence: " +
		pre.LogPath + ", " + fix.LogPath}
}

// commitName names a commit of the report in a reason.
func commitName(r contract.WalletBackendCommitResult) string {
	return fmt.Sprintf("commit %s (%s)", r.Commit, r.Role)
}

// reportReasons are the conditions of the report's pass rule its fields fail, whatever its passed says; none
// when they show the expected behaviour: the target and protocol 28; 2a0a84ea (before_fix) then 2c873353 (fix), on
// entry 1's one ledger; the pre-fix commit not ingested, with retries PRE_FIX_RETRY_LIMIT (5) in its last run and
// restarts PRE_FIX_RESTART_COUNT (1), its error following when one of these fails; the fix commit
// ingested.
func reportReasons(rep contract.WalletBackend729Report) []string {
	var reasons []string
	if rep.Target != contract.ReproTargetWalletBackend729 {
		reasons = append(reasons, fmt.Sprintf("the report's target is %q, not %s", rep.Target, contract.ReproTargetWalletBackend729))
	}
	if rep.ProtocolVersion != contract.WALLET_BACKEND_729_PROTOCOL_VERSION {
		reasons = append(reasons, fmt.Sprintf("the network ran protocol %d, not %d", rep.ProtocolVersion, contract.WALLET_BACKEND_729_PROTOCOL_VERSION))
	}
	if len(rep.Commits) != len(commits) {
		return append(reasons, fmt.Sprintf("the report holds %d commits, not %s and %s", len(rep.Commits), commits[0].short, commits[1].short))
	}
	for i, c := range commits {
		if r := rep.Commits[i]; r.Commit != c.short || r.Role != c.role {
			reasons = append(reasons, fmt.Sprintf("commit %d is %s (%s), not %s (%s)", i, r.Commit, r.Role, c.short, c.role))
		}
	}
	if len(reasons) > 0 {
		return reasons
	}
	pre, fix := rep.Commits[0], rep.Commits[1]
	if pre.Ledger < 1 || pre.Ledger != fix.Ledger {
		reasons = append(reasons, fmt.Sprintf("the two commits name ledgers %d and %d, not entry 1's one ledger", pre.Ledger, fix.Ledger))
	}
	preFailed := len(reasons)
	if pre.Ingested {
		reasons = append(reasons, fmt.Sprintf("%s ingested ledger %d", commitName(pre), pre.Ledger))
	}
	if pre.Retries != contract.PRE_FIX_RETRY_LIMIT {
		reasons = append(reasons, fmt.Sprintf("%s showed %d failed attempts on ledger %d in its last run, not %d", commitName(pre), pre.Retries, pre.Ledger, contract.PRE_FIX_RETRY_LIMIT))
	}
	if pre.Restarts != contract.PRE_FIX_RESTART_COUNT {
		reasons = append(reasons, fmt.Sprintf("%s was restarted %d times, not %d", commitName(pre), pre.Restarts, contract.PRE_FIX_RESTART_COUNT))
	}
	// The runner's own words for the pre-fix commit's failed conditions, from the evidence: a run that reached the
	// deadline without exiting says so there, and only there.
	if len(reasons) > preFailed && pre.Error != nil && *pre.Error != "" {
		reasons = append(reasons, commitName(pre)+": "+*pre.Error)
	}
	if !fix.Ingested {
		if fix.Error != nil && *fix.Error != "" {
			reasons = append(reasons, commitName(fix)+": "+*fix.Error)
		} else {
			reasons = append(reasons, fmt.Sprintf("%s did not ingest ledger %d", commitName(fix), fix.Ledger))
		}
	}
	return reasons
}
