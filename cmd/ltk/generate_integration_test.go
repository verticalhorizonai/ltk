//go:build integration

package main

// `ltk generate` with Docker: the built program against the real quickstart images, generating into temporary folders
// outside the repository. Each run is between two snapshots of the engine (cleanStopRun, check_integration_test.go):
// the label-filtered listings are empty after it and nothing new is in the engine. Run serially, with nothing else
// using Docker, on a machine with Docker running Linux containers and the images present or pullable, with LTK_BINARY
// naming a program built for it:
//
//	go test -tags integration -p 1 -timeout 120m -run IntegrationGenerate ./cmd/ltk/
//
// A full generation starts the current quickstart (and the protocol-28 one only if a shape falls back), which takes
// minutes; no progress line is written.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// TestIntegrationGenerateAllCheckAndShape, on all three seed entries:
//  1. `ltk generate --all --out <empty folder>` exits 0 with nothing on stderr: three shapes generated in seed order,
//     each at the current protocol without fallback (or, where a recipe fell back, at 28 with protocolFallback true),
//     the checks passed; one folder per entry with its XDR file and decoded JSON, the catalogue and the schema copy;
//     nothing left in Docker, running or stopped.
//  2. `ltk check <folder> --also-go` exits 0: each entry's Go SDK round trip equals its file.
//  3. `ltk generate --shape cap-0027-muxed-account --out <folder>` exits 0 and changes only that entry's files and its
//     catalogue record.
func TestIntegrationGenerateAllCheckAndShape(t *testing.T) {
	out := filepath.Join(t.TempDir(), "gen")
	code, stdout, stderr := cleanStopRun(t, "generate", "--all", "--out", out)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("ltk generate --all: exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
	}
	t.Logf("ltk generate --all --out %s: exit %d\n%s", out, code, stdout)
	r := decodeGenerateReport(t, stdout)
	ids := []string{model.SeedSignedPayloadSigner, model.SeedPoolShareTrustline, model.SeedMuxedAccount}
	if r.Out != out || len(r.Shapes) != 3 || r.Check == nil || !r.Check.Passed || len(r.Check.Entries) != 3 {
		t.Fatalf("%s", stdout)
	}
	for i, s := range r.Shapes {
		wantProtocol := orchestration.CurrentProtocol
		if s.ProtocolFallback {
			wantProtocol = contract.FALLBACK_PROTOCOL_VERSION
		}
		if s.ID != ids[i] || !s.Generated || s.Error != nil || s.ProtocolVersion != wantProtocol {
			t.Errorf("shape %d: %+v", i, s)
		}
		if r.Check.Entries[i].GoSdkRoundTrip != nil {
			t.Errorf("%s: goSdkRoundTrip is not null (the checks after a generation run without --also-go)", s.ID)
		}
		for _, f := range []string{"entry.xdr", "entry.json"} {
			if !exists(filepath.Join(out, s.ID, f)) {
				t.Errorf("%s/%s was not written", s.ID, f)
			}
		}
	}
	for _, f := range []string{catalogue.CatalogueFileName, catalogue.SchemaCopyFileName} {
		if !exists(filepath.Join(out, f)) {
			t.Errorf("%s was not written", f)
		}
	}

	code, stdout, stderr = cleanStopRun(t, "check", out, "--also-go")
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("ltk check --also-go: exit %d stderr %s; want 0\n%s", code, stderr, stdout)
	}
	t.Logf("ltk check %s --also-go: exit %d\n%s", out, code, stdout)
	cr := decodeReport(t, stdout)
	for _, e := range cr.Entries {
		if g := e.GoSdkRoundTrip; g == nil || !g.Passed || g.BytesCompared != e.FileIntegrity.Length {
			t.Errorf("%s: goSdkRoundTrip %+v", e.ID, g)
		}
	}

	before := treeOf(t, out)
	code, stdout, stderr = cleanStopRun(t, "generate", "--shape", model.SeedMuxedAccount, "--out", out)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("ltk generate --shape: exit %d stderr %s; want 0\n%s", code, stderr, stdout)
	}
	t.Logf("ltk generate --shape %s --out %s: exit %d\n%s", model.SeedMuxedAccount, out, code, stdout)
	after := treeOf(t, out)
	for p, b := range before {
		rel, _ := filepath.Rel(out, p)
		rel = filepath.ToSlash(rel)
		if after[p] != b && !strings.HasPrefix(rel, model.SeedMuxedAccount+"/") && rel != catalogue.CatalogueFileName {
			t.Errorf("--shape %s changed %s", model.SeedMuxedAccount, rel)
		}
	}
	oldCat, newCat := catalogueOf(t, before, out), catalogueOf(t, after, out)
	if len(oldCat.Entries) != 3 || len(newCat.Entries) != 3 {
		t.Fatalf("catalogues of %d and %d entries", len(oldCat.Entries), len(newCat.Entries))
	}
	for i := 0; i < 2; i++ {
		a, _ := catalogue.EncodeCatalogue(contract.Catalogue{Entries: oldCat.Entries[i : i+1]})
		b, _ := catalogue.EncodeCatalogue(contract.Catalogue{Entries: newCat.Entries[i : i+1]})
		if !bytes.Equal(a, b) {
			t.Errorf("--shape %s changed the record of %s", model.SeedMuxedAccount, oldCat.Entries[i].ID)
		}
	}
}

// managedContainers returns the ids of the containers labelled ltk.managed=true.
func managedContainers(t *testing.T) string {
	t.Helper()
	b, err := exec.Command("docker", "ps", "-a", "-q", "--filter", "label="+orchestration.LabelManaged+"=true").CombinedOutput()
	if err != nil {
		t.Fatalf("docker ps: %v: %s", err, b)
	}
	return strings.TrimSpace(string(b))
}

// TestIntegrationGenerateInterrupt: a running `ltk generate --all` is sent SIGINT once its quickstart container exists.
// It exits 1 with one ErrorLine saying it was interrupted and no report; the difference between the snapshots before
// and after and the three label-filtered listings are empty.
func TestIntegrationGenerateInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: os.Process.Signal cannot send an interrupt there")
	}
	if left := managedListings(t); left != "" {
		t.Fatalf("resources of an earlier run exist; remove them by hand (docker rm -f -v and docker network rm, filtered by label=ltk.managed=true): %s", left)
	}
	r, err := orchestration.NewRunner("00000000")
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "gen")
	cmd := ltkCommand(t, "", nil, "generate", "--all", "--out", out)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill() }()

	// The quickstart container appears once the environment check and the image inspection are done: seconds, or the
	// pull of an absent image. Ten minutes bounds a cold pull on a loaded machine with a wide margin; nothing is timed.
	deadline := time.Now().Add(10 * time.Minute)
	for managedContainers(t) == "" {
		select {
		case <-done:
			t.Fatalf("the program ended before its quickstart container existed: stdout %s stderr %s", stdout.Bytes(), stderr.Bytes())
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
	if !strings.HasPrefix(line.Message, "interrupted before the command finished: ltk generate: ") {
		t.Errorf("message %q", line.Message)
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
	t.Logf("ltk generate --all interrupted: exit %d; stderr %s", code, stderr.Bytes())
}
