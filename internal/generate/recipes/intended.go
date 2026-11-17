package recipes

// The JSON pointers of Intended(): where the Rust XDR tool's decoded JSON of a captured LedgerCloseMeta puts the
// values a recipe submits. They are the decoding's own paths, read from the first captures of every recipe on a real
// local network: a LedgerCloseMeta v2 whose generalized transaction set holds the shape's one transaction in its
// classic phase, and whose transaction meta is v4. The same paths hold in the captures at protocol 28 of the deciding
// test, the feasibility run made before this tool. A capture of another layout fails the pipeline's expected-values
// check and, through it, the shape: its intended values are not found in the capture, so it cannot work there.

import "encoding/json"

const (
	// envelopePath is the shape's transaction envelope in the ledger's transaction set.
	envelopePath = "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx"
	// txSourcePath is the transaction's source account.
	txSourcePath = envelopePath + "/tx/source_account"
	// operationPath is the body of the transaction's one operation.
	operationPath = envelopePath + "/tx/operations/0/body"
	// opResultPath is the result of the transaction's one operation, inside a successful result.
	opResultPath = "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner"
	// opChangesPath is the ledger-entry changes of the transaction's one operation.
	opChangesPath = "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes"
)

// jsonValue is v as JSON. The values are strings and small integers, so it cannot fail.
func jsonValue(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
