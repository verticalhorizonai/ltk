//go:build integration

package walletbackend_test

// The integration tier of the wallet-backend reproduction, run on a machine with Docker: the runner builds both
// commits, starts the protocol-28 network, both TimescaleDBs and both ingesters, submits entry 1, and the verdict is:
// the pre-fix commit fails five times on ledger L and exits, does the same after its restart, and
// the fix commit's cursor reaches L with its SIGNER row. The runner's deadline stays COMMIT_RUN_DEADLINE_MINUTES per
// run. Then the clean stop: the before/after difference of every container, network and volume (run with nothing else
// using Docker) and the three label-filtered listings, empty. The first build on a machine downloads every module.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/repro/walletbackend"
)

// managedListings are the three label-filtered listings of every ltk.managed resource.
func managedListings(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, args := range [][]string{
		{"ps", "-a", "-q", "--filter", "label=ltk.managed=true"},
		{"network", "ls", "-q", "--filter", "label=ltk.managed=true"},
		{"volume", "ls", "-q", "--filter", "label=ltk.managed=true"},
	} {
		b, err := exec.Command("docker", args...).Output()
		if err != nil {
			t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
		}
		out = append(out, strings.TrimSpace(string(b)))
	}
	return out
}

// TestIntegrationWalletBackend729 is the reproduction of wallet-backend 729 on Docker: both commits built from source
// and verified, each on its own fresh database, both ingesting before the transaction, the pre-fix commit failing and
// exiting twice, the fix commit storing the signer row, and the report passing.
func TestIntegrationWalletBackend729(t *testing.T) {
	for _, l := range managedListings(t) {
		if l != "" {
			t.Fatalf("ltk.managed resources exist before the test; clean them by hand: %q", l)
		}
	}
	runID, err := orchestration.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	r, err := orchestration.NewRunner(runID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Reap(ctx); err != nil {
			t.Errorf("Reap: %v", err)
		}
		after, err := r.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		left := orchestration.Leftovers(before, after)
		t.Logf("run %s: snapshot difference after teardown (want none): %q", runID, left)
		if len(left) != 0 {
			t.Errorf("Leftovers after teardown: %q", left)
		}
		for i, l := range managedListings(t) {
			t.Logf("label-filtered listing %d after teardown: %q", i, l)
			if l != "" {
				t.Errorf("label-filtered listing %d not empty after teardown: %q", i, l)
			}
		}
	})
	if err := r.EnsureImages(ctx, orchestration.WalletBackendImages); err != nil {
		t.Fatal(err)
	}
	entry1, ok := recipes.ByID(model.SeedSignedPayloadSigner)
	if !ok {
		t.Fatal("no recipe for entry 1")
	}
	work := filepath.Join(os.TempDir(), "ltk-"+runID, "wallet-backend-729")
	rep, err := walletbackend.Run(ctx, r, walletbackend.Options{
		Deadline: contract.COMMIT_RUN_DEADLINE_MINUTES * time.Minute,
		WorkDir:  work,
		Entry1:   entry1,
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.MarshalIndent(rep, "", "  ")
	t.Logf("run %s: the WalletBackend729Report:\n%s", runID, doc)
	pre, fix := rep.Commits[0], rep.Commits[1]
	L := pre.Ledger

	// The evidence: every line of each log that names ledger L, and the store states.
	for _, c := range rep.Commits {
		b, err := os.ReadFile(c.LogPath)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "ledger "+strconv.Itoa(L)) || strings.Contains(l, "==== ltk:") || strings.Contains(l, "Starting ingestion") {
				if len(l) > 260 {
					l = l[:260] + "…"
				}
				lines = append(lines, l)
			}
		}
		t.Logf("%s (%s) log %s, %d bytes; its lines about ledger %d, the starts and the restart marker:\n%s", c.Commit, c.Role, c.LogPath, len(b), L, strings.Join(lines, "\n"))
		s, err := os.ReadFile(c.StoreStatePath)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s (%s) store state %s:\n%s", c.Commit, c.Role, c.StoreStatePath, s)
	}
	if !rep.Passed {
		t.Fatalf("not reproduced: %q", walletbackend.Reasons(rep))
	}

	// The pre-fix commit: five failures and an exit in each run, the restart's marker between them.
	b, err := os.ReadFile(pre.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Split(string(b), "==== ltk: wallet-backend 2a0a84ea was restarted here")
	if len(runs) != 2 {
		t.Fatalf("the pre-fix log holds %d runs", len(runs))
	}
	attempt := regexp.MustCompile(`Error ingesting data for ledger ` + strconv.Itoa(L) + ` \(attempt (\d)/5\)`)
	final := regexp.MustCompile(`processing ledger ` + strconv.Itoa(L) + `: ingesting processed data failed after 5 attempts`)
	for i, run := range runs {
		if n := len(attempt.FindAllString(run, -1)); n < 4 || !final.MatchString(run) {
			t.Fatalf("pre-fix run %d: %d attempt lines, final line %v", i+1, n, final.MatchString(run))
		}
	}
	if pre.Ingested || pre.Retries != 5 || pre.Restarts != 1 || pre.StoredRow != nil {
		t.Fatalf("before_fix %+v", pre)
	}
	// The fix commit: the SIGNER row of ledger L.
	var row map[string]any
	if !fix.Ingested || fix.StoredRow == nil || json.Unmarshal(*fix.StoredRow, &row) != nil || row["state_change_category"] != "SIGNER" || row["ledger_number"] != float64(L) {
		t.Fatalf("fix %+v", fix)
	}
}
