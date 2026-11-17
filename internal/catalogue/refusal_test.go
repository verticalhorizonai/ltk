package catalogue

// The Refusal evidence recorder.

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// The text-order submission of the deciding test, the feasibility run made before this tool, and the network's answer
// to it in the first of its two final runs: status ERROR, txFAILED, CHANGE_TRUST_MALFORMED.
const (
	decidingTextOrderEnvelope = "AAAAAgAAAABHkNor3ig51PdlHkIM4Y2NM3dLEk4QZcsGgutgDbA4bAAAAGQAAAAiAAAAAwAAAAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAABgAAAAMAAAAAAAAAAVVTRAAAAAAAP8C+p0Z0AnwzxH9Rb0Dno52w/xg/BHtR0MEYc8n+2CwAAAABVVNEAAAAAAAeFJFtRyqAPNR7qoSWtFvKor9CHvlxnFd0g4dQ3SPlhQAAAB5//////////wAAAAAAAAABDbA4bAAAAED90nNIEQOzH4TiUGJfNzNZ7PSNqdXjYxwTV+jQvUczLKmFmf24VwmitXzRrlWJY0qSkUiwZED7UK/MhRBOr9MN"
	decidingErrorResult       = "AAAAAAAAAGT/////AAAAAQAAAAAAAAAG/////wAAAAA="
)

// resultB64 is the base64 XDR of a TransactionResult with code txCode and, when op is set, its one operation result.
func resultB64(t *testing.T, txCode xdr.TransactionResultCode, op *xdr.OperationResult) string {
	t.Helper()
	r := xdr.TransactionResult{FeeCharged: 100, Result: xdr.TransactionResultResult{Code: txCode}}
	switch {
	case op != nil:
		ops := []xdr.OperationResult{*op}
		r.Result.Results = &ops
	case txCode == xdr.TransactionResultCodeTxSuccess || txCode == xdr.TransactionResultCodeTxFailed:
		r.Result.Results = &[]xdr.OperationResult{} // the union's arm for these codes carries the results, here none
	}
	b, err := xdrcodec.Encode(&r)
	if err != nil {
		t.Fatal(err)
	}
	return xdrcodec.ToBase64(b)
}

func changeTrustOp(code xdr.ChangeTrustResultCode) *xdr.OperationResult {
	return &xdr.OperationResult{Code: xdr.OperationResultCodeOpInner, Tr: &xdr.OperationResultTr{
		Type: xdr.OperationTypeChangeTrust, ChangeTrustResult: &xdr.ChangeTrustResult{Code: code}}}
}

// TestRefusalEvidenceRecords: the deciding test's text-order submission and the network's refusal become entry 2's
// evidence: the envelope verbatim and the result code CHANGE_TRUST_MALFORMED, decoded through the Go SDK codec; the
// record validates in entry 2's catalogue record and is read back as written.
func TestRefusalEvidenceRecords(t *testing.T) {
	ev, err := RefusalEvidence(decidingTextOrderEnvelope, decidingErrorResult)
	if err != nil {
		t.Fatal(err)
	}
	if ev.SubmittedEnvelopeXdr != decidingTextOrderEnvelope || ev.ResultCode != contract.NetworkRefusalResultCodeChangeTrustMalformed || string(ev.ResultCode) != "CHANGE_TRUST_MALFORMED" {
		t.Errorf("%+v", ev)
	}
	// The evidence of a constructed result is the same code.
	if ev, err := RefusalEvidence(decidingTextOrderEnvelope, resultB64(t, xdr.TransactionResultCodeTxFailed, changeTrustOp(xdr.ChangeTrustResultCodeChangeTrustMalformed))); err != nil || ev.ResultCode != contract.NetworkRefusalResultCodeChangeTrustMalformed {
		t.Errorf("%+v %v", ev, err)
	}
	// Written into entry 2's record and read back.
	folder := t.TempDir()
	cat, err := ReadCatalogue(testdataRoot + "/corpus/valid")
	if err != nil {
		t.Fatal(err)
	}
	for i := range cat.Entries {
		if cat.Entries[i].ID == model.SeedPoolShareTrustline {
			cat.Entries[i].NetworkRefusalEvidence = &ev
		}
	}
	if err := WriteCatalogue(folder, cat); err != nil {
		t.Fatal(err)
	}
	back, err := ReadCatalogue(folder)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Entries[1].NetworkRefusalEvidence; back.Entries[1].ID != model.SeedPoolShareTrustline || got == nil || *got != ev {
		t.Errorf("read back %+v", got)
	}
}

// TestRefusalEvidenceRefuses (generation fails when the text-order submission is not refused as the shape
// demonstrates): anything but a failed transaction whose ChangeTrust is CHANGE_TRUST_MALFORMED is an error naming what
// the network answered; an envelope or a result that is not XDR of its type is an error.
func TestRefusalEvidenceRefuses(t *testing.T) {
	payment := &xdr.OperationResult{Code: xdr.OperationResultCodeOpInner, Tr: &xdr.OperationResultTr{
		Type: xdr.OperationTypePayment, PaymentResult: &xdr.PaymentResult{Code: xdr.PaymentResultCodePaymentUnderfunded}}}
	cases := []struct {
		name, envelope, result, inError string
	}{
		{"accepted", decidingTextOrderEnvelope, resultB64(t, xdr.TransactionResultCodeTxSuccess, changeTrustOp(xdr.ChangeTrustResultCodeChangeTrustSuccess)), "TransactionResultCodeTxSuccess"},
		{"another transaction code", decidingTextOrderEnvelope, resultB64(t, xdr.TransactionResultCodeTxBadSeq, nil), "TransactionResultCodeTxBadSeq"},
		{"another ChangeTrust code", decidingTextOrderEnvelope, resultB64(t, xdr.TransactionResultCodeTxFailed, changeTrustOp(xdr.ChangeTrustResultCodeChangeTrustLowReserve)), "ChangeTrustResultCodeChangeTrustLowReserve"},
		{"another operation", decidingTextOrderEnvelope, resultB64(t, xdr.TransactionResultCodeTxFailed, payment), "not of a ChangeTrust"},
		{"no operation result", decidingTextOrderEnvelope, resultB64(t, xdr.TransactionResultCodeTxFailed, nil), "0 operation results"},
		{"an operation that did not run", decidingTextOrderEnvelope, resultB64(t, xdr.TransactionResultCodeTxFailed, &xdr.OperationResult{Code: xdr.OperationResultCodeOpNoAccount}), "OperationResultCodeOpNoAccount"},
		{"a result that is not XDR", decidingTextOrderEnvelope, "AAAA", "not a transaction result"},
		{"a result that is not base64", decidingTextOrderEnvelope, "!!", "not a transaction result"},
		{"an envelope that is not XDR", "AAAA", decidingErrorResult, "not a transaction envelope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, err := RefusalEvidence(c.envelope, c.result)
			if err == nil || !strings.Contains(err.Error(), c.inError) {
				t.Errorf("error %v, want one naming %q", err, c.inError)
			}
			if ev != (contract.NetworkRefusalEvidence{}) {
				t.Errorf("evidence %+v returned with an error", ev)
			}
		})
	}
}
