//go:build integration

package main

// `ltk check` with Docker: the built program on a corpus of real XDR that the test builds in a temporary folder, with
// Stellar's Rust XDR tool of the current quickstart image doing the round trip. The corpus holds the real protocol-30
// capture of testdata/real (a SetOptions adding a signed-payload signer with a 29-byte payload, as entry 1; its
// expected values read from its decoded JSON through the grouping table) and the transaction envelope inside it, taken
// out with the Go SDK codec (a second XDR type, no expected value).
//
// Run serially, on a machine with Docker running Linux containers, with LTK_BINARY naming a program built for it:
//
//	go test -tags integration -p 1 -run Integration ./cmd/ltk/
//
// With --also-go the Go SDK's round trip of each entry runs beside the Rust tool's.
//
// Every run is followed by the clean-stop check: no resource labelled ltk.managed=true, and nothing new in the engine's
// containers, networks and volumes.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
)

const (
	realCapture        = "../../testdata/real/capture-p30.xdr"
	realCaptureDecoded = "../../testdata/real/capture-p30.json"
	// envelopeEntryID names the corpus's second entry: the capture's transaction envelope.
	envelopeEntryID = "cap-0040-signed-payload-signer-envelope"
	// realCaptureEnvelopePointer is the capture's one transaction envelope in its decoded JSON.
	realCaptureEnvelopePointer = "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0"
)

// buildRealCorpus writes the real-XDR corpus into a new temporary folder through the layout writer, as ltk generate
// writes a corpus, and returns the folder.
func buildRealCorpus(t *testing.T) string {
	t.Helper()
	folder := t.TempDir()
	raw, err := os.ReadFile(realCapture)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := os.ReadFile(realCaptureDecoded)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := model.SeedByID(model.SeedSignedPayloadSigner)
	path := catalogue.EntryFilePath(seed.ID)
	if err := catalogue.WriteEntryFiles(folder, path, raw, decoded); err != nil {
		t.Fatal(err)
	}
	expected, err := catalogue.SplitExpectedValues(seed.ID, decoded)
	if err != nil {
		t.Fatal(err)
	}
	sha, n := catalogue.Measure(raw)
	cat := contract.Catalogue{Entries: []contract.CatalogueEntry{{
		ID: seed.ID, Cap: seed.Cap, ProtocolVersion: 30, Source: "the protocol-30 capture of testdata/real (cmd/ltk/check_integration_test.go)",
		Synthetic: false, ExpectedDecodedValues: expected, FilePath: path, XdrType: seed.XdrType, Sha256: sha, Length: n,
	}}}

	// The envelope of the capture's one transaction, taken out and encoded with the Go SDK codec.
	v, err := xdrcodec.Decode(xdrcodec.TypeLedgerCloseMeta, raw)
	if err != nil {
		t.Fatal(err)
	}
	envs := v.(*xdr.LedgerCloseMeta).TransactionEnvelopes()
	if len(envs) != 1 {
		t.Fatalf("the capture holds %d transaction envelopes, want 1", len(envs))
	}
	envBytes, err := xdrcodec.Encode(&envs[0])
	if err != nil {
		t.Fatal(err)
	}
	// Its decoded JSON beside it, which `ltk check` holds to the tool's decoding of the file: the
	// envelope's own subtree of the capture's decoded JSON, the Rust tool's form of the same TransactionEnvelope.
	doc, err := catalogue.Parse(decoded)
	if err != nil {
		t.Fatal(err)
	}
	envJSON, ok := doc.Resolve(realCaptureEnvelopePointer)
	if !ok {
		t.Fatalf("the capture's decoded JSON has no envelope at %s", realCaptureEnvelopePointer)
	}
	envPath := catalogue.EntryFilePath(envelopeEntryID)
	if err := catalogue.WriteEntryFiles(folder, envPath, envBytes, envJSON.Raw); err != nil {
		t.Fatal(err)
	}
	envSha, envN := catalogue.Measure(envBytes)
	cat.Entries = append(cat.Entries, contract.CatalogueEntry{
		ID: envelopeEntryID, Cap: seed.Cap, ProtocolVersion: 30, Source: "the transaction envelope of the protocol-30 capture, encoded by the Go SDK",
		Synthetic: false, FilePath: envPath, XdrType: xdrcodec.TypeTransactionEnvelope, Sha256: envSha, Length: envN,
		ExpectedDecodedValues: contract.ExpectedDecodedValues{ReproducedByEveryRun: []contract.ReproducedValue{}, LedgerDependent: []contract.LedgerDependentValue{}},
	})
	if err := catalogue.WriteCatalogue(folder, cat); err != nil {
		t.Fatal(err)
	}
	return folder
}

// managedListings returns the label-filtered listings of the clean-stop check joined: empty when no ltk resource
// exists.
func managedListings(t *testing.T) string {
	t.Helper()
	var out []string
	for _, args := range [][]string{
		{"ps", "-a", "-q", "--filter", "label=ltk.managed=true"},
		{"network", "ls", "-q", "--filter", "label=ltk.managed=true"},
		{"volume", "ls", "-q", "--filter", "label=ltk.managed=true"},
	} {
		b, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, b)
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			out = append(out, "docker "+strings.Join(args, " ")+": "+s)
		}
	}
	return strings.Join(out, "; ")
}

// cleanStop runs the program as `ltk check folder` between two snapshots of the engine and asserts the clean stop: the
// label-filtered listings are empty and nothing new is in the engine.
func cleanStop(t *testing.T, folder string) (code int, stdout, stderr []byte) {
	t.Helper()
	return cleanStopRun(t, "check", folder)
}

// cleanStopRun runs the program with args between two snapshots of the engine and asserts the clean stop.
func cleanStopRun(t *testing.T, args ...string) (code int, stdout, stderr []byte) {
	t.Helper()
	return cleanStopRunIn(t, "", args...)
}

// cleanStopRunIn is cleanStopRun with the working directory dir ("" for the test's own): `ltk repro` writes repro/
// under its working directory, so its runs are in a t.TempDir() and the repository's own repro/ is never written.
func cleanStopRunIn(t *testing.T, dir string, args ...string) (code int, stdout, stderr []byte) {
	t.Helper()
	rec := observe(t, func() (int, []byte, []byte) { return runLTKIn(t, dir, nil, args...) })
	rec.args = args
	rec.assertCleanStop(t)
	return rec.code, rec.stdout, rec.stderr
}

// TestIntegrationCheckRealCorpusPasses: on real XDR every check passes: exit 0, nothing on stderr, the full report on
// stdout, each entry decoded and re-encoded by the Rust XDR tool byte for byte (bytesCompared the file's length) and
// entry 1's expected values of both groups read again from the tool's decoded form.
func TestIntegrationCheckRealCorpusPasses(t *testing.T) {
	folder := buildRealCorpus(t)
	code, stdout, stderr := cleanStop(t, folder)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
	}
	r := decodeReport(t, stdout)
	if !r.Passed || r.Folder != folder || !r.CatalogueSchema.Passed || len(r.Entries) != 2 {
		t.Fatalf("%s", stdout)
	}
	lengths := []int{1928, -1}
	for i, e := range r.Entries {
		if !e.FileIntegrity.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed || e.GoSdkRoundTrip != nil {
			t.Errorf("%s: %+v", e.ID, e)
		}
		if e.RoundTrip.BytesCompared != e.FileIntegrity.Length || (lengths[i] > 0 && e.RoundTrip.BytesCompared != lengths[i]) {
			t.Errorf("%s: bytesCompared %d, the file is %d bytes", e.ID, e.RoundTrip.BytesCompared, e.FileIntegrity.Length)
		}
	}
	t.Logf("ltk check %s: exit %d\n%s", folder, code, stdout)
}

// TestIntegrationCheckRealCorpusFailures: each failure kind on real bytes exits 1 with the report on stdout and an
// ErrorLine naming the entry and the check, a decoded JSON file that differs from the tool's decoding included.
func TestIntegrationCheckRealCorpusFailures(t *testing.T) {
	entryFile := func(folder string) string {
		return filepath.Join(folder, filepath.FromSlash(catalogue.EntryFilePath(model.SeedSignedPayloadSigner)))
	}
	editCatalogue := func(t *testing.T, folder string, edit func(e *contract.CatalogueEntry)) {
		t.Helper()
		cat, err := catalogue.ReadCatalogue(folder)
		if err != nil {
			t.Fatal(err)
		}
		edit(&cat.Entries[0])
		if err := catalogue.WriteCatalogue(folder, cat); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name      string
		alter     func(t *testing.T, folder string)
		failed    func(e contract.EntryCheckResult) bool
		inMessage string
	}{
		{"a flipped byte fails file_integrity", func(t *testing.T, folder string) {
			b, err := os.ReadFile(entryFile(folder))
			if err != nil {
				t.Fatal(err)
			}
			b[100] ^= 0x01
			if err := os.WriteFile(entryFile(folder), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}, func(e contract.EntryCheckResult) bool { return !e.FileIntegrity.Passed }, "file_integrity: the hash differs"},
		{"a changed recorded value fails expected_values", func(t *testing.T, folder string) {
			editCatalogue(t, folder, func(e *contract.CatalogueEntry) {
				for i, v := range e.ExpectedDecodedValues.ReproducedByEveryRun {
					if strings.HasSuffix(v.Pointer, "/op_inner/set_options") {
						e.ExpectedDecodedValues.ReproducedByEveryRun[i].Value = []byte(`"malformed"`)
					}
				}
			})
		}, func(e contract.EntryCheckResult) bool {
			return e.FileIntegrity.Passed && e.RoundTrip.Passed && !e.ExpectedValues.Passed && len(e.ExpectedValues.Mismatches) == 1 &&
				string(e.ExpectedValues.Mismatches[0].Decoded) == `"success"`
		}, "expected_values: 1 of"},
		{"bytes the tool cannot decode fail round_trip", func(t *testing.T, folder string) {
			b, err := os.ReadFile(entryFile(folder))
			if err != nil {
				t.Fatal(err)
			}
			b = b[:1000]
			if err := os.WriteFile(entryFile(folder), b, 0o644); err != nil {
				t.Fatal(err)
			}
			editCatalogue(t, folder, func(e *contract.CatalogueEntry) { e.Sha256, e.Length = catalogue.Measure(b) })
		}, func(e contract.EntryCheckResult) bool {
			return e.FileIntegrity.Passed && !e.RoundTrip.Passed && e.RoundTrip.BytesCompared == 0 && !e.ExpectedValues.Passed
		}, "round_trip: the file cap-0040-signed-payload-signer/entry.xdr is not decodable as LedgerCloseMeta"},
		{"an edited leaf of the decoded JSON file fails round_trip", func(t *testing.T, folder string) {
			name := filepath.Join(folder, model.SeedSignedPayloadSigner, "entry.json")
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			edited := strings.Replace(string(b), `"set_options": "success"`, `"set_options": "malformed"`, 1)
			if edited == string(b) {
				t.Fatal("the decoded JSON holds no operation result to edit")
			}
			if err := os.WriteFile(name, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
		}, func(e contract.EntryCheckResult) bool {
			return e.FileIntegrity.Passed && !e.RoundTrip.Passed && e.RoundTrip.BytesCompared == 1928 && e.ExpectedValues.Passed
		}, `round_trip: the decoded JSON file cap-0040-signed-payload-signer/entry.json differs from the Rust XDR tool's decoding of cap-0040-signed-payload-signer/entry.xdr at "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/set_options"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			folder := buildRealCorpus(t)
			c.alter(t, folder)
			code, stdout, stderr := cleanStop(t, folder)
			if code != int(contract.EXIT_CODES.Failure) {
				t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
			}
			r := decodeReport(t, stdout)
			if r.Passed || len(r.Entries) != 2 || r.Entries[0].ID != model.SeedSignedPayloadSigner || !c.failed(r.Entries[0]) {
				t.Errorf("%s", stdout)
			}
			if e := r.Entries[1]; !e.FileIntegrity.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed {
				t.Errorf("the other entry is checked and passes: %+v", e)
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			if !strings.Contains(line.Message, "entry "+model.SeedSignedPayloadSigner+": "+c.inMessage) {
				t.Errorf("message %q, want %q", line.Message, c.inMessage)
			}
			t.Logf("ltk check: exit %d; stderr %s", code, stderr)
		})
	}
}

// TestIntegrationCheckAlsoGo: `ltk check --also-go` on the real-XDR corpus decodes and re-encodes each entry with the
// Go SDK as well as with the Rust XDR tool: exit 0, each entry's goSdkRoundTrip passed with bytesCompared the file's
// length, beside its roundTrip. With a non-zero padding byte in entry 1's signed payload, recorded by hash and length,
// the Go SDK refuses the file: go_sdk_round_trip fails and names the entry, apart from the Rust tool's round_trip.
func TestIntegrationCheckAlsoGo(t *testing.T) {
	folder := buildRealCorpus(t)
	code, stdout, stderr := cleanStopRun(t, "check", folder, "--also-go")
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
	}
	r := decodeReport(t, stdout)
	if !r.Passed || len(r.Entries) != 2 {
		t.Fatalf("%s", stdout)
	}
	for _, e := range r.Entries {
		g := e.GoSdkRoundTrip
		if g == nil || !g.Passed || g.Check != contract.CheckNameGoSdkRoundTrip || g.BytesCompared != e.FileIntegrity.Length || !e.RoundTrip.Passed {
			t.Errorf("%s: roundTrip %+v goSdkRoundTrip %+v", e.ID, e.RoundTrip, g)
		}
	}
	t.Logf("ltk check %s --also-go: exit %d\n%s", folder, code, stdout)

	file := filepath.Join(folder, filepath.FromSlash(catalogue.EntryFilePath(model.SeedSignedPayloadSigner)))
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	b[712+29] = 0x01 // the first padding byte after the 29-byte signed payload (internal/check/crosscheck_test.go)
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalogue.ReadCatalogue(folder)
	if err != nil {
		t.Fatal(err)
	}
	cat.Entries[0].Sha256, cat.Entries[0].Length = catalogue.Measure(b)
	if err := catalogue.WriteCatalogue(folder, cat); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = cleanStopRun(t, "check", folder, "--also-go")
	if code != int(contract.EXIT_CODES.Failure) {
		t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
	}
	r = decodeReport(t, stdout)
	if e := r.Entries[0]; r.Passed || e.GoSdkRoundTrip == nil || e.GoSdkRoundTrip.Passed || e.GoSdkRoundTrip.BytesCompared != 0 {
		t.Errorf("%s", stdout)
	}
	line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
	if !strings.Contains(line.Message, "entry "+model.SeedSignedPayloadSigner+": go_sdk_round_trip: ") {
		t.Errorf("message %q", line.Message)
	}
	t.Logf("ltk check --also-go, padding byte altered: exit %d; roundTrip %+v; stderr %s", code, r.Entries[0].RoundTrip, stderr)
}
