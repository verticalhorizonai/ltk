//go:build integration

package main

// The Clean-stop verification test: nothing the tool started is left running. And the snapshot helpers the
// integration tier's runs share.
//
// Every run is observed between two snapshots of the whole engine (orchestration.Snapshot: every container, running or
// stopped, every network and every volume). The pass condition: the after-set minus the before-set is empty for each;
// resources present in both, another project's, are not counted. The label-filtered listings (label=ltk.managed=true)
// must be empty before a run (a stale run is cleaned by hand, never by the test) and after it, and are logged after
// each run, so the run's output saves them. Run serially, with nothing else using Docker, on a machine with Docker
// running Linux containers, with LTK_BINARY naming a program built for it:
//
//	go test -tags integration -p 1 -timeout 120m -run 'IntegrationCleanStop' ./cmd/ltk/
//
// The scenarios that finish normally (a generation, `ltk check corpus/`, each reproduction) are the runs the
// acceptance tests make (accept_*_test.go): sharedRun makes each once per test binary, so the reproduction of
// wallet-backend 729, which builds two commits from source, is not run three times.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// cleanRun is one run of the program observed between two snapshots of the engine.
type cleanRun struct {
	// by is the test that made the run; dir its working directory; work the folder made for it; args its arguments.
	by, dir, work string
	args          []string
	code          int
	stdout        []byte
	stderr        []byte
	// left is orchestration.Leftovers(before, after): what the run left in the engine.
	left []string
	// listings are the label-filtered listings after the run, "" when empty.
	listings string
	// corpusBefore, corpusAfter, reproBefore and reproAfter are the committed corpus/ and repro/ around the run.
	corpusBefore, corpusAfter, reproBefore, reproAfter map[string]string
}

// observe runs run between two snapshots of the engine. It stops the test when a resource labelled ltk.managed=true
// exists before the run: such a resource is cleaned by hand, never by the test, which would mask a leak.
func observe(t *testing.T, run func() (int, []byte, []byte)) *cleanRun {
	t.Helper()
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
	rec := &cleanRun{by: t.Name(), corpusBefore: treeOf(t, committedCorpusDir), reproBefore: treeOf(t, committedReproDir)}
	rec.code, rec.stdout, rec.stderr = run()
	after, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rec.left = orchestration.Leftovers(before, after)
	rec.listings = managedListings(t)
	rec.corpusAfter, rec.reproAfter = treeOf(t, committedCorpusDir), treeOf(t, committedReproDir)
	return rec
}

// assertCleanStop holds the run to the clean stop (nothing new in the engine, empty label-filtered listings) and logs
// the label-filtered listings.
func (c *cleanRun) assertCleanStop(t *testing.T) {
	t.Helper()
	if len(c.left) != 0 {
		t.Errorf("ltk %s left %v", strings.Join(c.args, " "), c.left)
	}
	if c.listings != "" {
		t.Errorf("the label-filtered listings are not empty after ltk %s: %s", strings.Join(c.args, " "), c.listings)
	}
	listings := c.listings
	if listings == "" {
		listings = "docker ps -a, docker network ls and docker volume ls filtered by label=ltk.managed=true: empty"
	}
	t.Logf("after ltk %s (exit %d, run by %s): nothing new in the engine: %v; %s", strings.Join(c.args, " "), c.code, c.by, len(c.left) == 0, listings)
}

// assertCommittedUntouched fails when the run wrote under the committed corpus/ or repro/.
func (c *cleanRun) assertCommittedUntouched(t *testing.T) {
	t.Helper()
	assertTreeUnchanged(t, "corpus/", c.corpusBefore, c.corpusAfter)
	assertTreeUnchanged(t, "repro/", c.reproBefore, c.reproAfter)
}

var (
	sharedMu   sync.Mutex
	sharedRuns = map[string]*cleanRun{}
)

// sharedRun runs the program once per test binary for key, between the two snapshots, and returns the same record to
// every later caller. setup is given a new folder that lives until the test binary ends (under harnessDir) and returns
// the working directory and the arguments. The caller asserts what it needs, the clean stop included.
func sharedRun(t *testing.T, key string, setup func(work string) (dir string, args []string)) *cleanRun {
	t.Helper()
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if rec, ok := sharedRuns[key]; ok {
		t.Logf("the run of ltk %s made by %s (exit %d)", strings.Join(rec.args, " "), rec.by, rec.code)
		return rec
	}
	if harnessDir == "" {
		t.Fatalf("no folder for the shared runs: %v", ltkSetupErr)
	}
	work, err := os.MkdirTemp(harnessDir, "run-")
	if err != nil {
		t.Fatal(err)
	}
	dir, args := setup(work)
	rec := observe(t, func() (int, []byte, []byte) { return runLTKIn(t, dir, nil, args...) })
	rec.dir, rec.work, rec.args = dir, work, args
	sharedRuns[key] = rec
	t.Logf("ltk %s (working directory %s): exit %d; stderr %s\n%s", strings.Join(args, " "), dir, rec.code, rec.stderr, rec.stdout)
	return rec
}

// The shared runs of the acceptance targets.

// committedCheckRun is `ltk check corpus/` from the module root.
func committedCheckRun(t *testing.T) *cleanRun {
	root := acceptRoot(t)
	return sharedRun(t, "check corpus/", func(string) (string, []string) { return root, []string{"check", corpusArg} })
}

// generateAllRun is `ltk generate --all --out <work>/gen` from the module root, into a folder that does not exist
// before, so it starts empty.
func generateAllRun(t *testing.T) *cleanRun {
	root := acceptRoot(t)
	return sharedRun(t, "generate --all", func(work string) (string, []string) {
		return root, []string{"generate", "--all", "--out", filepath.Join(work, "gen")}
	})
}

// reproductionRun is `ltk repro target` from a fresh working directory outside the repository, so the
// repository's own repro/ is never written.
func reproductionRun(t *testing.T, target contract.ReproTarget) *cleanRun {
	return sharedRun(t, "repro "+string(target), func(work string) (string, []string) { return work, []string{"repro", string(target)} })
}

// interruptRun starts the program in dir with args, sends it an interrupt once its first container labelled
// ltk.managed=true exists, and returns the run observed between the snapshots.
func interruptRun(t *testing.T, dir string, args ...string) *cleanRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: os.Process.Signal cannot send an interrupt there")
	}
	rec := observe(t, func() (int, []byte, []byte) {
		cmd := ltkCommand(t, dir, nil, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		var waitErr error
		go func() { waitErr = cmd.Wait(); close(done) }()
		defer func() { _ = cmd.Process.Kill() }()
		// The first container appears once the environment check and the image inspection are done: seconds, or the
		// pull of an absent image. Ten minutes bounds a cold pull on a loaded machine with a wide margin.
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
		return exitCodeOfRun(t, waitErr), stdout.Bytes(), stderr.Bytes()
	})
	rec.dir, rec.args = dir, args
	return rec
}

// TestIntegrationCleanStop: after each of these five scenarios, nothing the tool started is left: the difference
// between the engine's containers (running or stopped), networks and volumes before and after the run is empty, and so
// are the label-filtered listings. The exit code of the interrupted generation is not judged; the other runs' exits are
// their commands' own (their acceptance tests judge them), this test judges the teardown only, whatever the outcome.
//  1. `ltk generate --all --out <folder>` interrupted once its first container exists;
//  2. `ltk generate --all --out <folder>` let finish (the run of accept_generate_test.go);
//  3. `ltk repro wallet-backend-729` let finish (accept_repro_test.go's);
//  4. `ltk repro js-sdk-1766` let finish (accept_repro_test.go's);
//  5. `ltk check corpus/` let finish, its round trip's container (accept_check_test.go's).
func TestIntegrationCleanStop(t *testing.T) {
	t.Run("1 generation interrupted", func(t *testing.T) {
		rec := interruptRun(t, acceptRoot(t), "generate", "--all", "--out", filepath.Join(t.TempDir(), "gen"))
		t.Logf("interrupted: exit %d (not judged); stderr %s", rec.code, rec.stderr)
		rec.assertCleanStop(t)
		rec.assertCommittedUntouched(t)
	})
	t.Run("2 generation finished", func(t *testing.T) {
		rec := generateAllRun(t)
		rec.assertCleanStop(t)
		rec.assertCommittedUntouched(t)
	})
	t.Run("3 repro wallet-backend-729", func(t *testing.T) {
		rec := reproductionRun(t, contract.ReproTargetWalletBackend729)
		rec.assertCleanStop(t)
		rec.assertCommittedUntouched(t)
	})
	t.Run("4 repro js-sdk-1766", func(t *testing.T) {
		rec := reproductionRun(t, contract.ReproTargetJsSdk1766)
		rec.assertCleanStop(t)
		rec.assertCommittedUntouched(t)
	})
	t.Run("5 check corpus/", func(t *testing.T) {
		rec := committedCheckRun(t)
		rec.assertCleanStop(t)
		rec.assertCommittedUntouched(t)
	})
}
