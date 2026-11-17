package walletbackend

// The injected deadline through the whole runner, the runner's side: the runner given Options.Deadline, as
// `ltk repro wallet-backend-729` gives it COMMIT_RUN_DEADLINE_MINUTES (cmd/ltk/timelimit.go), on the fake engine and
// the fake clock (nothing waits), where the pre-fix commit never exits before that deadline. A run that reaches the
// safety deadline is a report, never an error: the one the command writes and then maps to exit 1. Its passed is
// false, the report writer accepts its shape, and Reasons names the deadline.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// TestInjectedDeadlineEndsInAReportNotPassed runs the runner with an injected deadline of 3 minutes, unlike the
// 15-minute constant, so the words of the reasons and the number of polls show which deadline ruled: once on a first
// run that keeps retrying, once on a run after the restart that keeps retrying (each run's deadline counts from its
// own start: the transaction's submission for the first run, `docker start` for the second).
func TestInjectedDeadlineEndsInAReportNotPassed(t *testing.T) {
	const deadline = 3 * time.Minute
	run1, run2 := fixture(t, "prefix-2a0a84ea-run1"), fixture(t, "prefix-2a0a84ea-run2")
	for _, tc := range []struct {
		name     string
		pre      []fakeRun
		run      string
		failures int
		restarts int
	}{
		{
			name:     "the first run never exits",
			pre:      []fakeRun{{lines: withoutPanic(run1), exitAfter: -1, ingestAfter: -1}},
			run:      "its first run",
			failures: 4,
			restarts: 0,
		},
		{
			name: "the run after the restart never exits",
			pre: []fakeRun{
				{lines: run1, exitAfter: 3, exitCode: 2, ingestAfter: -1},
				{lines: withoutPanic(run2), exitAfter: -1, ingestAfter: -1},
			},
			run:      "its run after the restart",
			failures: 4,
			restarts: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, fix := expectedRuns(t)
			f := newFakeEngine(t, tc.pre, fix)
			opt := Options{Deadline: deadline, WorkDir: filepath.Join(t.TempDir(), "ltk-0123abcd", "wallet-backend-729"), Entry1: &fakeRecipe{f: f}}
			// Run's own config, the injected deadline included; only the clock and the recipes' network are the unit
			// tier's.
			cfg := runConfig(opt)
			cfg.clk = newFakeClock()
			cfg.recipeNetwork = func(n *orchestration.Network) recipes.Network { return recipes.Network{Passphrase: n.Passphrase} }
			if cfg.deadline != deadline {
				t.Fatalf("Run's config holds the deadline %s; the command injected %s", cfg.deadline, deadline)
			}
			rep, err := run(context.Background(), f, opt, cfg)
			if err != nil {
				t.Fatalf("a run that reaches the deadline is a report, never an error (the deadline is a safety limit): %v", err)
			}

			// The report: not passed; the pre-fix commit's counts are those of the run the deadline ended.
			if rep.Passed {
				t.Fatal("passed: a pre-fix run that never exits before the deadline must give passed false")
			}
			pre, x := rep.Commits[0], rep.Commits[1]
			if pre.Ingested || pre.StoredRow != nil || pre.Retries != tc.failures || pre.Restarts != tc.restarts || pre.Error == nil {
				t.Fatalf("before_fix %+v (error %s)", pre, deref(pre.Error))
			}
			if !x.Ingested || x.Error != nil {
				t.Fatalf("fix %+v: the fix commit is judged as usual", x)
			}

			// Reasons names the deadline the runner was given, for the pre-fix commit only.
			reasons := Reasons(rep)
			want := fmt.Sprintf("commit 2a0a84ea (before_fix): %s reached the %s safety deadline after %d failed attempts on ledger %d, without exiting",
				tc.run, deadline, tc.failures, testLedger)
			if !slices.ContainsFunc(reasons, func(r string) bool { return strings.HasPrefix(r, want) }) {
				t.Fatalf("no reason names the deadline; want one starting %q among %q", want, reasons)
			}
			for _, r := range reasons {
				if strings.Contains(r, "15m0s") || strings.Contains(r, "2c873353") {
					t.Fatalf("reason %q names the 15-minute constant or the fix commit", r)
				}
			}

			// The injected deadline ended the run: at least its polls (less 3, for the fix commit's observation sleeping
			// on the same fake clock while the pre-fix commit is watched), and far fewer than 15 minutes of polls.
			polls := f.ing["2a0a84ea"].polls
			if polls < int(deadline/PollInterval)-3 || polls >= int(15*time.Minute/PollInterval) {
				t.Fatalf("the pre-fix run ended after %d polls of %s; the injected deadline is %s", polls, PollInterval, deadline)
			}
			if !slices.Contains(f.calls, "stop ltk-0123abcd-wb-2a0a84ea") {
				t.Fatalf("the pre-fix ingester was not stopped at the deadline: %q", f.calls)
			}
			if b, err := os.ReadFile(pre.LogPath); err != nil || !strings.Contains(string(b), "(attempt 4/5)") {
				t.Fatalf("the pre-fix log is not saved: %v", err)
			}

			// The report the command writes (its shape is the report's, so the writer does not refuse it) and then,
			// passed false, maps to exit 1 (cmd/ltk/repro_wb.go: the command exits 1 when passed is false); the
			// written document validates against the report's schema.
			b, err := catalogue.WriteWalletBackendReport(t.TempDir(), rep)
			if err != nil {
				t.Fatalf("the report writer refuses the report: %v", err)
			}
			if err := validateReport(t, b); err != nil {
				t.Fatalf("the written report does not validate against the report's schema: %v\n%s", err, b)
			}
		})
	}
}
