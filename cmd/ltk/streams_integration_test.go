//go:build integration

package main

// The `ltk check` rows of the Stream and exit-code contract test with a real Docker: every row that runs `ltk check` is
// in the integration tier, because the built program's round trip is done by Stellar's Rust XDR tool in its container.
// The rows that need no Docker are streams_test.go's. Run serially, with nothing else using Docker, with LTK_BINARY
// naming a program built for this machine:
//
//	go test -tags integration -p 1 -timeout 120m -run 'IntegrationStreams' ./cmd/ltk/

import (
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// TestIntegrationStreamsCheck, the streams and exit codes of `ltk check`:
//   - `ltk check corpus/` from the module root: exit 0, the CheckReport on standard output as exactly one JSON value,
//     nothing on standard error;
//   - `ltk check` of each failure corpus of testdata/ (each fails the check it is made for, whatever the real tool
//     makes of its bytes): exit 1, the CheckReport (passed false) on standard output as one JSON
//     value, and one failure ErrorLine on standard error, which holds no report line;
//   - a reader that closes the pipe: `ltk check corpus/` into a pipe whose reader is gone: exit 0, nothing on standard
//     error.
//
// Every run leaves nothing in the engine.
func TestIntegrationStreamsCheck(t *testing.T) {
	t.Run("check corpus/", func(t *testing.T) {
		rec := committedCheckRun(t)
		rec.assertCleanStop(t)
		if rec.code != 0 || len(rec.stderr) != 0 {
			t.Fatalf("exit %d stderr %s; want 0 and nothing", rec.code, rec.stderr)
		}
		oneJSONValue(t, rec.stdout)
		decodeReport(t, rec.stdout)
	})
	for _, kind := range []string{"hash", "length", "expected-value", "round-trip", "schema-copy", "schema-pointer"} {
		t.Run("check of the failure corpus "+kind, func(t *testing.T) {
			code, stdout, stderr := cleanStopRun(t, "check", fixtureCorpus+kind)
			if code != int(contract.EXIT_CODES.Failure) {
				t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
			}
			oneJSONValue(t, stdout)
			if r := decodeReport(t, stdout); r.Passed {
				t.Errorf("passed true on a failure corpus")
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			t.Logf("ltk check %s: exit 1; %s", kind, line.Message)
		})
	}
	t.Run("check corpus/ into a closed pipe", func(t *testing.T) {
		root := acceptRoot(t)
		rec := observe(t, func() (int, []byte, []byte) {
			code, stderr := runIntoClosedReader(t, ltkCommand(t, root, nil, "check", corpusArg))
			return code, nil, stderr
		})
		rec.args = []string{"check", corpusArg, "(into a closed pipe)"}
		rec.assertCleanStop(t)
		if rec.code != 0 || len(rec.stderr) != 0 {
			t.Errorf("exit %d stderr %q; want 0 and nothing", rec.code, rec.stderr)
		}
	})
}
