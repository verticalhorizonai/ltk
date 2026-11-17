package catalogue

// The real protocol-30 capture committed under testdata/real: a SetOptions that adds a signed-payload signer with a
// 29-byte payload, captured as the LedgerCloseMeta of its ledger from the current quickstart, with the Rust XDR tool's
// decoded JSON beside it. The integration tier's `ltk check` corpus is built from it
// (cmd/ltk/check_integration_test.go); this test holds the fixture to what it claims without a container.

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// TestRealCaptureFixture: the capture is 1928 bytes of LedgerCloseMeta that the Go SDK decodes and re-encodes byte for
// byte; its decoded JSON is strict JSON of protocol 30; the grouping table finds in it the shape's own values (the
// signer and its 29-byte payload, the operation's result) and a value of every LedgerDependentField.
func TestRealCaptureFixture(t *testing.T) {
	raw, err := os.ReadFile(testdataRoot + "/real/capture-p30.xdr")
	if err != nil {
		t.Fatal(err)
	}
	if sha, n := Measure(raw); n != 1928 || sha != "cd7c52b4e29e37284c898df96199493e0dee8f4e8bb9ce54c28f051dfd7f11be" {
		t.Errorf("capture-p30.xdr is %d bytes, sha256 %s", n, sha)
	}
	again, err := xdrcodec.RoundTrip(xdrcodec.TypeLedgerCloseMeta, raw)
	if err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("the Go SDK's round trip of the capture: %v", err)
	}
	decoded, err := os.ReadFile(testdataRoot + "/real/capture-p30.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := doc.Resolve("/v2/ledger_header/header/ledger_version"); !ok || v.Text != "30" {
		t.Errorf("ledger_version %v", v)
	}
	ev, err := SplitExpectedValues(model.SeedSignedPayloadSigner, decoded)
	if err != nil {
		t.Fatal(err)
	}
	var reproduced []string
	for _, v := range ev.ReproducedByEveryRun {
		reproduced = append(reproduced, v.Pointer+"="+string(v.Value))
	}
	all := strings.Join(reproduced, "\n")
	for _, want := range []string{
		"/tx/operations/0/body/set_options/signer/key=\"PAUT43KXD5HABJCQLZFEVTQLEMSOUQTQ3ZU5RD5RMXDBP4QJYTHYSAAAAAO",
		"/tx/operations/0/body/set_options/signer/weight=1",
		"/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/set_options=\"success\"",
		"/updated/data/account/signers/0/key=\"PAUT43KXD5HABJCQLZFEVTQLEMSOUQTQ3ZU5RD5RMXDBP4QJYTHYSAAAAAO",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no reproduced value %s in\n%s", want, all)
		}
	}
	fields := map[contract.LedgerDependentField]bool{}
	for _, v := range ev.LedgerDependent {
		fields[v.Field] = true
	}
	for _, f := range contract.Unions["LedgerDependentField"] {
		if !fields[contract.LedgerDependentField(f)] {
			t.Errorf("no ledger-dependent value of %s in the capture", f)
		}
	}
}
