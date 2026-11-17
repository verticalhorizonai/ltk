package catalogue

// The Refusal evidence recorder: entry 2's evidence (NetworkRefusalEvidence) that the network refused the same pool
// with its assets in text order. Entry 2's recipe submits the text-order ChangeTrust and returns what it sent and what
// the network answered, raw; the generation pipeline hands both to RefusalEvidence and records the result in entry 2's
// catalogue record. The catalogue check validates the recorded field against the schema, and checks that it is
// present on entry 2 and absent on every other entry; `ltk list` does not show it.

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
)

// RefusalEvidence builds entry 2's NetworkRefusalEvidence from:
//   - submittedEnvelopeXdr, the base64 XDR of the transaction envelope the recipe submitted with the pool's assets in
//     text order, kept verbatim; it must decode as a TransactionEnvelope;
//   - resultXdr, the base64 XDR TransactionResult the network answered (sendTransaction's errorResultXdr, or the failed
//     getTransaction's result).
//
// The result code is decoded through the Go SDK XDR codec and recorded verbatim as stellar-core names it.
// Only a transaction that failed with its ChangeTrust operation's CHANGE_TRUST_MALFORMED is evidence of the refusal;
// anything else is an error naming what the network answered, and generation fails with it: a text-order submission
// the network accepted, or refused for another reason, no longer demonstrates the shape's point.
func RefusalEvidence(submittedEnvelopeXdr, resultXdr string) (contract.NetworkRefusalEvidence, error) {
	var ev contract.NetworkRefusalEvidence
	if _, err := xdrcodec.DecodeBase64(xdrcodec.TypeTransactionEnvelope, submittedEnvelopeXdr); err != nil {
		return ev, fmt.Errorf("the submitted envelope of the text-order pool is not a transaction envelope: %w", err)
	}
	v, err := xdrcodec.DecodeBase64(xdrcodec.TypeTransactionResult, resultXdr)
	if err != nil {
		return ev, fmt.Errorf("the network's answer to the text-order pool is not a transaction result: %w", err)
	}
	code, err := changeTrustRefusal(*v.(*xdr.TransactionResult))
	if err != nil {
		return ev, fmt.Errorf("the network did not refuse the text-order pool with CHANGE_TRUST_MALFORMED: %w", err)
	}
	return contract.NetworkRefusalEvidence{SubmittedEnvelopeXdr: submittedEnvelopeXdr, ResultCode: code}, nil
}

// changeTrustRefusal reads the result code of a transaction that failed on its one ChangeTrust operation, through a
// fee bump's inner result when there is one; the only code it returns is CHANGE_TRUST_MALFORMED.
func changeTrustRefusal(r xdr.TransactionResult) (contract.NetworkRefusalResultCode, error) {
	code := r.Result.Code
	ops, _ := r.Result.GetResults()
	if pair, ok := r.Result.GetInnerResultPair(); ok {
		code = pair.Result.Result.Code
		ops, _ = pair.Result.Result.GetResults()
	}
	if code != xdr.TransactionResultCodeTxFailed {
		return "", fmt.Errorf("the transaction's result is %s, not %s", code, xdr.TransactionResultCodeTxFailed)
	}
	if len(ops) != 1 {
		return "", fmt.Errorf("the failed transaction carries %d operation results, not the one ChangeTrust", len(ops))
	}
	tr, ok := ops[0].GetTr()
	if ops[0].Code != xdr.OperationResultCodeOpInner || !ok {
		return "", fmt.Errorf("the operation's result is %s, not an operation's own result", ops[0].Code)
	}
	ct, ok := tr.GetChangeTrustResult()
	if !ok {
		return "", fmt.Errorf("the operation's result is that of a %s, not of a ChangeTrust", tr.Type)
	}
	if ct.Code != xdr.ChangeTrustResultCodeChangeTrustMalformed {
		return "", fmt.Errorf("the ChangeTrust's result is %s", ct.Code)
	}
	return contract.NetworkRefusalResultCodeChangeTrustMalformed, nil
}
