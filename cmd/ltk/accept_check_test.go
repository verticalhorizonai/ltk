//go:build integration

package main

// The Committed corpus acceptance test: `ltk check corpus/` exits 0, with three entries each validated, hashed,
// round-tripped and read back.
//
// The built program runs `ltk check corpus/` from the module root, the folder argument exactly `corpus/`, against the
// committed corpus as cloned, with Stellar's Rust XDR tool of the current quickstart image doing the round trip; no
// generation runs first and nothing produced after the clone is needed but the digest-pinned image. A clean clone is
// the exported repository: run there, this measures that a fresh clone checks without any generation. Run serially,
// with nothing else using Docker, on a machine with Docker running Linux containers, with LTK_BINARY naming a program
// built for it:
//
//	go test -tags integration -p 1 -timeout 120m -run 'AcceptCommittedCorpus' ./cmd/ltk/

import (
	"testing"
)

// TestAcceptCommittedCorpusCheck: exit 0 and nothing on standard
// error (the program writes no progress lines); the CheckReport for folder "corpus/" with catalogue_schema passed
// and no failure, the three seed entries in catalogue order, each validated, hashed (sha256 and length equal to the
// recorded ones), round-tripped (every byte compared) and read back (expected values, no mismatch) with goSdkRoundTrip
// null, and passed true. No file under corpus/ or repro/ is written; nothing is left in the engine.
func TestAcceptCommittedCorpusCheck(t *testing.T) {
	rec := committedCheckRun(t)
	if rec.code != 0 || len(rec.stderr) != 0 {
		t.Fatalf("ltk check corpus/: exit %d stderr %s; want 0 and nothing\n%s", rec.code, rec.stderr, rec.stdout)
	}
	assertCommittedCheckReport(t, decodeReport(t, rec.stdout))
	rec.assertCommittedUntouched(t)
	rec.assertCleanStop(t)
}

// TestAcceptCommittedCorpusNoGeneration: `ltk check corpus/` generates nothing and
// starts no network: the run leaves no network and no volume behind (the snapshots), and its report's every round
// trip was done by the Rust XDR tool's one container (bytesCompared equal to each file's length). The order of the
// tool's calls (one `docker run` of the current quickstart, `--network none`, no published port, nothing but
// stellar-xdr decode and encode) is held by TestProcessCheckCommittedCorpusFake in the unit tier, where the calls are
// logged.
func TestAcceptCommittedCorpusNoGeneration(t *testing.T) {
	rec := committedCheckRun(t)
	if rec.code != 0 {
		t.Fatalf("exit %d stderr %s", rec.code, rec.stderr)
	}
	r := decodeReport(t, rec.stdout)
	for _, e := range r.Entries {
		if e.RoundTrip.BytesCompared != e.FileIntegrity.Length || e.RoundTrip.BytesCompared == 0 {
			t.Errorf("%s: the round trip compared %d of %d bytes", e.ID, e.RoundTrip.BytesCompared, e.FileIntegrity.Length)
		}
	}
	rec.assertCleanStop(t)
}
