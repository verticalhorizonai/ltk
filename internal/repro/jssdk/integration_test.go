//go:build integration

package jssdk_test

// The integration tier of the js-sdk reproduction, run on a machine with Docker: the runner drives both releases end
// to end in node:22-slim, and the verdict is: 17.2.0 rejects at build with exactly "Assets are not in
// lexicographic order", 17.2.1 round-trips to the same bytes. Then the clean stop: the before/after difference of every
// container, network and volume (run with nothing else using Docker) and the three label-filtered listings, empty.

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/repro/jssdk"
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

// TestIntegrationJsSdk1766 is the reproduction of js-stellar-sdk 1766 on Docker: 17.2.0 rejects the trustline at
// build, 17.2.1 round-trips it to the same bytes, and the report passes.
func TestIntegrationJsSdk1766(t *testing.T) {
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
	if err := r.EnsureImages(ctx, orchestration.JsSdkImages); err != nil {
		t.Fatal(err)
	}
	rep, err := jssdk.Run(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.MarshalIndent(rep, "", "  ")
	t.Logf("run %s: the JsSdk1766Report:\n%s", runID, doc)
	if !rep.Passed {
		t.Fatalf("not reproduced: %q", jssdk.Reasons(rep))
	}
	v0, v1 := rep.Versions[0], rep.Versions[1]
	if v0.Version != "17.2.0" || len(v0.Steps) != 1 || v0.Steps[0].Step != "build" || v0.Steps[0].Passed || *v0.Steps[0].Error != "Assets are not in lexicographic order" {
		t.Fatalf("17.2.0: %+v", v0)
	}
	if v1.Version != "17.2.1" || len(v1.Steps) != 3 || !v1.RoundTripIdentical {
		t.Fatalf("17.2.1: %+v", v1)
	}
}
