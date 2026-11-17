//go:build integration

package recipes_test

// The skip list on a real network: stellar-core writes the bucket-list hash into the ledger header's skip list from
// ledger 50 on, so two captures past ledger 50 carry skip lists that differ, and the grouping table attributes each
// element to bucket_list_hash. The test starts the current quickstart twice, one network at a time; waits on each until
// RPC's latest ledger has passed 50; runs entry 1's recipe; decodes the capture with the Rust XDR tool; and writes it
// as a one-entry corpus folder through the layout writer, its expected values split by the grouping table. It then runs
// the built `ltk compare` (LTK_BINARY) on the two folders, and on the first folder with entry 1 of the committed corpus
// (captured before ledger 50, its skip list zero), the comparison of a fresh generation with the committed corpus.
// Nothing in the program changes for the test: no flag, no environment variable.
//
// Run natively, serially, with nothing else using Docker:
//
//	(cd internal/generate/recipes && PATH=/usr/local/bin:/usr/bin:/bin LTK_BINARY=<ltk> ./recipes.test \
//	    -test.run TestIntegrationSkipList -test.v)
//
// LTK_CAPTURE_DIR keeps the two folders there as skiplist-a and skiplist-b.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

const (
	// skipListFrom is the ledger from which stellar-core writes the bucket-list hash into the skip list.
	skipListFrom = 50
	// skipListPointer is the skip list in the Rust XDR tool's decoding of a LedgerCloseMeta v2.
	skipListPointer = "/v2/ledger_header/header/skip_list"
	// pastLedgerTimeout bounds the wait for a network to pass ledger 50.
	pastLedgerTimeout = 10 * time.Minute
)

// TestIntegrationSkipList runs entry 1's recipe on two networks past ledger 50 and compares the two folders.
func TestIntegrationSkipList(t *testing.T) {
	bin := os.Getenv("LTK_BINARY")
	if bin == "" {
		t.Fatal("LTK_BINARY names no program: build ltk for this system and name it")
	}
	for _, l := range managedListings(t) {
		if l != "" {
			t.Fatalf("ltk.managed resources exist before the test; clean them by hand (the test never cleans them itself, which would mask a leak): %q", l)
		}
	}
	t.Cleanup(func() {
		for i, l := range managedListings(t) {
			if l != "" {
				t.Errorf("label-filtered listing %d not empty after the runs: %q", i, l)
			}
		}
	})
	keep := os.Getenv("LTK_CAPTURE_DIR")
	folder := func(name string) string {
		if keep == "" {
			return filepath.Join(t.TempDir(), name)
		}
		dir := filepath.Join(keep, name)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	r, ok := recipes.ByID(model.SeedSignedPayloadSigner)
	if !ok {
		t.Fatal("no recipe for entry 1")
	}
	ctx := context.Background()

	a, b := folder("skiplist-a"), folder("skiplist-b")
	skipA := captureFolder(ctx, t, r, a, "A")
	skipB := captureFolder(ctx, t, r, b, "B")

	differ := 0
	for i := range skipA {
		if skipA[i] != skipB[i] {
			differ++
		}
	}
	t.Logf("SKIPLIST %d of %d elements differ between capture A and capture B", differ, len(skipA))
	if differ == 0 {
		t.Fatalf("SKIPLIST the two captures past ledger %d carry the same skip list %v: the bucket-list hash stellar-core writes into it from ledger 50 on does not differ", skipListFrom, skipA)
	}

	report := runCompare(t, bin, a, b)
	if !report.Passed {
		t.Errorf("ltk compare of the two captures did not pass")
	}
	skipDiffs := 0
	for _, e := range report.Entries {
		for _, d := range e.Differ {
			if strings.HasPrefix(d.Pointer, skipListPointer+"/") {
				skipDiffs++
			}
		}
	}
	if skipDiffs != differ {
		t.Errorf("ltk compare reports %d skip-list differences; the captures differ in %d elements", skipDiffs, differ)
	}

	// Spec 3.10 item 2: a fresh capture past ledger 50 compared with the committed corpus's entry 1, captured before
	// ledger 50 (its skip list zero), the fresh folder first.
	committed := folder("skiplist-committed")
	committedEntry(t, committed)
	report = runCompare(t, bin, a, committed)
	if !report.Passed {
		t.Errorf("ltk compare of capture A with the committed entry 1 did not pass")
	}
}

// captureFolder starts the current quickstart, waits until it has passed ledger 50, runs the recipe, decodes the
// capture with the Rust XDR tool and writes it into dir as a one-entry corpus folder; it tears the network down before
// it returns, and returns the capture's skip list.
func captureFolder(ctx context.Context, t *testing.T, r recipes.Recipe, dir, name string) []string {
	t.Helper()
	runID, err := orchestration.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := orchestration.NewRunner(runID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := run.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Reap(ctx); err != nil {
			t.Errorf("Reap: %v", err)
		}
		after, err := run.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if left := orchestration.Leftovers(before, after); len(left) != 0 {
			t.Errorf("Leftovers after teardown: %q", left)
		}
		t.Logf("capture %s: run %s torn down", name, runID)
	}()
	if err := run.EnsureImages(ctx, []string{orchestration.QuickstartCurrent}); err != nil {
		t.Fatal(err)
	}
	n, err := run.StartQuickstart(ctx, orchestration.QuickstartCurrent, orchestration.CurrentProtocol)
	if err != nil {
		t.Fatal(err)
	}
	rpc := generate.NewRPCClient(n.RPCURL)
	latest := waitPastLedger(ctx, t, rpc, skipListFrom)
	t.Logf("capture %s: run %s at protocol %d, latest ledger %d", name, runID, n.Protocol, latest)

	c, err := r.Run(ctx, recipes.Network{RPC: rpc, Friendbot: generate.NewFriendbot(n.FriendbotURL), Passphrase: n.Passphrase})
	if err != nil {
		t.Fatalf("capture %s: %s: %v", name, r.ID(), err)
	}
	if c.Ledger <= skipListFrom {
		t.Fatalf("capture %s: the shape's ledger is %d, not past %d", name, c.Ledger, skipListFrom)
	}
	tool, err := run.StartXDRTool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := tool.Decode(ctx, c.XdrType, c.XDR)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := catalogue.SplitExpectedValues(r.ID(), decoded)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := model.SeedByID(r.ID())
	record := contract.CatalogueEntry{
		ID:                    r.ID(),
		Cap:                   seed.Cap,
		Source:                "captured by the skip-list integration test from a local stellar-core network past ledger 50",
		Synthetic:             seed.Synthetic,
		ExpectedDecodedValues: expected,
		FilePath:              catalogue.EntryFilePath(r.ID()),
		XdrType:               c.XdrType,
	}
	record.Sha256, record.Length = catalogue.Measure(c.XDR)
	if err := catalogue.RecordProtocol(&record, c.Protocol, false); err != nil {
		t.Fatal(err)
	}
	if err := catalogue.WriteEntryFiles(dir, record.FilePath, c.XDR, decoded); err != nil {
		t.Fatal(err)
	}
	if err := catalogue.WriteCatalogue(dir, contract.Catalogue{Entries: []contract.CatalogueEntry{record}}); err != nil {
		t.Fatal(err)
	}

	doc, err := catalogue.Parse(decoded)
	if err != nil {
		t.Fatal(err)
	}
	list := resolve(doc, skipListPointer)
	if list == nil || list.Kind != catalogue.KindArray {
		t.Fatalf("capture %s: no skip list at %s", name, skipListPointer)
	}
	var skip []string
	for _, e := range list.Elems {
		skip = append(skip, e.Text)
	}
	recorded := 0
	for _, v := range expected.LedgerDependent {
		if strings.HasPrefix(v.Pointer, skipListPointer+"/") {
			recorded++
			if v.Field != contract.LedgerDependentFieldBucketListHash {
				t.Errorf("capture %s: %s is recorded as %s", name, v.Pointer, v.Field)
			}
		}
	}
	if recorded != len(skip) {
		t.Errorf("capture %s: %d skip-list values recorded as ledger-dependent, %d in the capture", name, recorded, len(skip))
	}
	t.Logf("CAPTURE %s ledger=%d protocol=%d bytes=%d sha256=%s tx=%s folder=%s", name, c.Ledger, c.Protocol, record.Length, record.Sha256, c.TxHash, dir)
	t.Logf("CAPTURE %s skip_list=%s", name, strings.Join(skip, ","))
	return skip
}

// waitPastLedger polls RPC's getLedgers for the ledger after ledger until RPC serves it, and returns the latest ledger.
// RPC answers an error for a start ledger it does not have yet.
func waitPastLedger(ctx context.Context, t *testing.T, rpc *generate.RPCClient, ledger uint32) uint32 {
	t.Helper()
	deadline := time.Now().Add(pastLedgerTimeout)
	var last error
	for time.Now().Before(deadline) {
		got, err := rpc.GetLedgers(ctx, ledger+1, 1)
		switch {
		case err != nil:
			last = err
		case len(got.Ledgers) == 1 && got.Ledgers[0].Sequence == ledger+1:
			return got.LatestLedger
		default:
			last = fmt.Errorf("getLedgers from ledger %d served %d ledgers (latest %d)", ledger+1, len(got.Ledgers), got.LatestLedger)
		}
		time.Sleep(recipes.PollInterval)
	}
	t.Fatalf("the network did not pass ledger %d within %s (last answer: %v)", ledger, pastLedgerTimeout, last)
	return 0
}

// committedEntry writes into dir a one-entry corpus folder of the committed corpus's entry 1: its two files and its
// catalogue record, unchanged.
func committedEntry(t *testing.T, dir string) {
	t.Helper()
	const corpus = "../../../corpus"
	cat, err := catalogue.ReadCatalogue(corpus)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cat.Entries {
		if e.ID != model.SeedSignedPayloadSigner {
			continue
		}
		raw, err := catalogue.ReadEntryFile(corpus, e.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := catalogue.ReadDecodedJSON(corpus, e.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := catalogue.WriteEntryFiles(dir, e.FilePath, raw, decoded); err != nil {
			t.Fatal(err)
		}
		if err := catalogue.WriteCatalogue(dir, contract.Catalogue{Entries: []contract.CatalogueEntry{e}}); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("the committed corpus has no entry 1")
}

// runCompare runs `ltk compare a b` with the built program, logs its exit code and every difference with its
// attribution, and returns its report; an exit other than 0 fails the test.
func runCompare(t *testing.T, bin, a, b string) contract.CompareReport {
	t.Helper()
	cmd := exec.Command(bin, "compare", a, b)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		t.Fatalf("ltk compare: %v", err)
	}
	t.Logf("COMPARE ltk compare %s %s: exit %d; stderr %q", a, b, code, strings.TrimSpace(stderr.String()))
	var report contract.CompareReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("ltk compare's standard output is not a CompareReport: %v\n%s", err, stdout.String())
	}
	for _, e := range report.Entries {
		for _, d := range e.Differ {
			field := "null"
			if d.LedgerDependentField != nil {
				field = string(*d.LedgerDependentField)
			}
			t.Logf("COMPARE %s differ %s -> %s (A %s, B %s)", e.ID, d.Pointer, field, d.ValueA, d.ValueB)
		}
	}
	t.Logf("COMPARE passed=%v", report.Passed)
	if code != 0 {
		t.Errorf("ltk compare %s %s exited %d, want 0", a, b, code)
	}
	return report
}
