//go:build integration

package main

// The container-dependent commands with Docker available: each container-dependent command, run by the built program on
// a machine with Docker running Linux containers, passes the environment check, starts its containers and proceeds
// normally: it writes its report to standard output and exits 0, or 1 per its own definition, never 3; its report shows
// the work its containers did; it leaves nothing in the engine. The fail-fast rows are envcmd_test.go's. The runs of
// `generate --all`, `check corpus/` and the two reproductions are the ones the acceptance tests share
// (cleanstop_test.go). Run serially, with nothing else using Docker, with LTK_BINARY naming a program built for this
// machine:
//
//	go test -tags integration -p 1 -timeout 120m -run 'IntegrationContainerCommandsProceed' ./cmd/ltk/

import (
	"path/filepath"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// TestIntegrationContainerCommandsProceed: each container-dependent command proceeds normally with Docker available.
func TestIntegrationContainerCommandsProceed(t *testing.T) {
	// proceeded holds a run to the "proceeds normally" row: exit 0 with nothing on stderr, or 1 with one failure
	// ErrorLine; never 3; the report on stdout, one JSON value valid against schema.
	proceeded := func(t *testing.T, rec *cleanRun, schema string) {
		t.Helper()
		rec.assertCleanStop(t)
		rec.assertCommittedUntouched(t)
		switch rec.code {
		case 0:
			if len(rec.stderr) != 0 {
				t.Errorf("exit 0 with stderr %s", rec.stderr)
			}
		case int(contract.EXIT_CODES.Failure):
			assertErrorLine(t, rec.stderr, contract.ErrorCodeFailure)
		default:
			t.Fatalf("ltk %v: exit %d stderr %s; want 0 or 1, the command proceeding past the environment check", rec.args, rec.code, rec.stderr)
		}
		oneJSONValue(t, rec.stdout)
		validateOutput(t, schema, rec.stdout)
	}

	t.Run("generate --all --out <folder>", func(t *testing.T) {
		rec := generateAllRun(t)
		proceeded(t, rec, "generate-report.schema.json")
		r := decodeGenerateReport(t, rec.stdout)
		for _, s := range r.Shapes {
			if !s.Generated {
				t.Errorf("%s was not generated on the local network: %v", s.ID, s.Error)
			}
		}
	})
	t.Run("generate --shape <entry id> --out <folder>", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "shape")
		rec := observe(t, func() (int, []byte, []byte) {
			return runLTKIn(t, "", nil, "generate", "--shape", model.SeedMuxedAccount, "--out", out)
		})
		rec.args = []string{"generate", "--shape", model.SeedMuxedAccount, "--out", out}
		t.Logf("ltk generate --shape %s --out %s: exit %d; stderr %s\n%s", model.SeedMuxedAccount, out, rec.code, rec.stderr, rec.stdout)
		proceeded(t, rec, "generate-report.schema.json")
		r := decodeGenerateReport(t, rec.stdout)
		if len(r.Shapes) != 1 || !r.Shapes[0].Generated || !exists(filepath.Join(out, model.SeedMuxedAccount, "entry.xdr")) {
			t.Errorf("the shape was not generated: %+v", r.Shapes)
		}
	})
	t.Run("check corpus/", func(t *testing.T) {
		rec := committedCheckRun(t)
		proceeded(t, rec, "check-report.schema.json")
		for _, e := range decodeReport(t, rec.stdout).Entries {
			if e.RoundTrip.BytesCompared == 0 {
				t.Errorf("%s: no byte went through the Rust XDR tool's container", e.ID)
			}
		}
	})
	t.Run("repro wallet-backend-729", func(t *testing.T) {
		rec := reproductionRun(t, contract.ReproTargetWalletBackend729)
		proceeded(t, rec, "wallet-backend-729-report.schema.json")
		var rep contract.WalletBackend729Report
		if err := decodeStrict(rec.stdout, &rep); err != nil || rep.ProtocolVersion != contract.WALLET_BACKEND_729_PROTOCOL_VERSION || len(rep.Commits) != 2 {
			t.Errorf("%v: %s", err, rec.stdout)
		}
	})
	t.Run("repro js-sdk-1766", func(t *testing.T) {
		rec := reproductionRun(t, contract.ReproTargetJsSdk1766)
		proceeded(t, rec, "js-sdk-1766-report.schema.json")
		var rep contract.JsSdk1766Report
		if err := decodeStrict(rec.stdout, &rep); err != nil || len(rep.Versions) != 2 || len(rep.Versions[0].Steps) == 0 {
			t.Errorf("%v: %s", err, rec.stdout)
		}
	})
}
