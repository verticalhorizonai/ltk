package model

// The Expected-value grouping table: for each seed entry, which leaves of its decoded JSON are expected values, and to
// which of the two groups each belongs. Generation splits an entry's expected values by it and records the split in the
// catalogue (reproducedByEveryRun, ledgerDependent); no field is added to the catalogue, the group is the array a value
// sits in. `ltk compare` reads the groups from folder A's catalogue, never from this table, so "ledger-dependent" means
// one thing in the generator, the check and the comparison.
//
// The pointers are the Rust XDR tool's JSON decoding of a LedgerCloseMeta, the ledger that holds the shape's
// transaction, as the captures of the deciding test (the feasibility run made before this tool) show them at protocol
// 28, with the leaves that differ between its two runs. Every leaf of those differences is attributed by a row: the
// ledger header's own hash (`/v2/ledger_header/hash`) and the header's `tx_set_result_hash` to `ledger_hash` and
// `transaction_result_set_hash`. Both follow from the other ledger-dependent values: the header's hash covers the
// ledger number, the close time and the other header hashes; the result set's hash covers the result pairs, which
// carry the transaction hash.

import (
	"strings"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// GroupRule is one row of the grouping table: the leaves whose JSON pointer Pattern matches are expected values of
// Group and, in the ledger-dependent group, of Field.
//
// A pattern is a JSON pointer whose segment "*" matches any one segment and whose segment "**" matches any number of
// segments, none included.
type GroupRule struct {
	Pattern string
	Group   contract.ExpectedValueGroup
	// Field is the LedgerDependentField of a ledger-dependent rule; "" in the reproduced group.
	Field contract.LedgerDependentField
}

// Matches reports whether pointer is one of the leaves the rule names.
func (r GroupRule) Matches(pointer string) bool {
	if r.Pattern == "" || pointer == "" {
		return r.Pattern == pointer
	}
	if !strings.HasPrefix(pointer, "/") || !strings.HasPrefix(r.Pattern, "/") {
		return false
	}
	return matchSegments(strings.Split(r.Pattern[1:], "/"), strings.Split(pointer[1:], "/"))
}

func matchSegments(pattern, segs []string) bool {
	for len(pattern) > 0 {
		switch p := pattern[0]; p {
		case "**":
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pattern[1:], segs[i:]) {
					return true
				}
			}
			return false
		default:
			if len(segs) == 0 || (p != "*" && p != segs[0]) {
				return false
			}
		}
		pattern, segs = pattern[1:], segs[1:]
	}
	return len(segs) == 0
}

func reproduced(pattern string) GroupRule {
	return GroupRule{Pattern: pattern, Group: contract.ExpectedValueGroupReproducedByEveryRun}
}

func ledger(field contract.LedgerDependentField, pattern string) GroupRule {
	return GroupRule{Pattern: pattern, Group: contract.ExpectedValueGroupLedgerDependent, Field: field}
}

// The transaction's envelope inside the ledger's transaction set, and its operation.
const (
	txEnvelope = "/*/tx_set/**/txs/*/tx"
	operation  = txEnvelope + "/tx/operations/0/body"
	txResult   = "/*/tx_processing/*/result"
)

// ledgerRules are the ledger-dependent values of every seed:
//   - the ledger number, the close time, the previous-ledger hash, the bucket-list hash, the transaction-set hash, the
//     consensus signature and the fee pool, in the ledger header (and the previous-ledger hash again in the
//     transaction set);
//   - the entries' last-modified ledger, in every ledger-entry change; an account's seq_ledger and seq_time, which
//     are the ledger number and the close time of the ledger that last bumped its sequence number;
//   - when the source account is created in a different ledger, its sequence number and so the envelope's signature
//     and hash: the sequence numbers, the envelope's signatures and the transaction's hash. Every seed's source
//     account is created by its recipe during the run, in a ledger that differs from run to run, so these are
//     applicable to every seed and generation records them for each; `ltk compare` attributes only what the catalogue
//     records;
//   - the ledger header's own hash and its tx_set_result_hash, which cover the values above;
//   - the elements of the ledger header's skip list, bucket-list hashes of earlier ledgers, each recorded at every
//     ledger, a zero hash included, so a capture before ledger 50 and one after it differ only in recorded values.
var ledgerRules = []GroupRule{
	ledger(contract.LedgerDependentFieldLedgerNumber, "/*/ledger_header/header/ledger_seq"),
	ledger(contract.LedgerDependentFieldLedgerNumber, "/*/tx_processing/**/data/account/ext/v1/ext/v2/ext/v3/seq_ledger"),
	ledger(contract.LedgerDependentFieldCloseTime, "/*/ledger_header/header/scp_value/close_time"),
	ledger(contract.LedgerDependentFieldCloseTime, "/*/tx_processing/**/data/account/ext/v1/ext/v2/ext/v3/seq_time"),
	ledger(contract.LedgerDependentFieldPreviousLedgerHash, "/*/ledger_header/header/previous_ledger_hash"),
	ledger(contract.LedgerDependentFieldPreviousLedgerHash, "/*/tx_set/*/previous_ledger_hash"),
	ledger(contract.LedgerDependentFieldBucketListHash, "/*/ledger_header/header/bucket_list_hash"),
	// stellar-core writes the bucket-list hash into the header's skip list from ledger 50 on, so each element of the
	// skip list is a bucket-list hash of an earlier ledger; before ledger 50 every element is zero.
	ledger(contract.LedgerDependentFieldBucketListHash, "/*/ledger_header/header/skip_list/*"),
	ledger(contract.LedgerDependentFieldTransactionSetHash, "/*/ledger_header/header/scp_value/tx_set_hash"),
	ledger(contract.LedgerDependentFieldConsensusSignature, "/*/ledger_header/header/scp_value/ext/signed/signature"),
	// From protocol 30 the header's StellarValue carries its close time in milliseconds and the consensus signature
	// under signed_ms (the protocol-30 capture, testdata/real/capture-p30.json).
	ledger(contract.LedgerDependentFieldCloseTime, "/*/ledger_header/header/scp_value/ext/signed_ms/close_time_ms"),
	ledger(contract.LedgerDependentFieldConsensusSignature, "/*/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/signature"),
	ledger(contract.LedgerDependentFieldFeePool, "/*/ledger_header/header/fee_pool"),
	ledger(contract.LedgerDependentFieldLastModifiedLedger, "/*/tx_processing/**/last_modified_ledger_seq"),
	ledger(contract.LedgerDependentFieldSourceSequenceNumber, txEnvelope+"/tx/seq_num"),
	ledger(contract.LedgerDependentFieldSourceSequenceNumber, "/*/tx_processing/**/data/account/seq_num"),
	ledger(contract.LedgerDependentFieldTransactionSignature, txEnvelope+"/signatures/*/signature"),
	ledger(contract.LedgerDependentFieldTransactionHash, txResult+"/transaction_hash"),
	ledger(contract.LedgerDependentFieldLedgerHash, "/*/ledger_header/hash"),
	ledger(contract.LedgerDependentFieldTransactionResultSetHash, "/*/ledger_header/header/tx_set_result_hash"),
}

// groupingTable is the grouping table, keyed per seed entry: each seed's reproduced values, the shape's own values
// (the operation, the signer key and its payload, the assets and their order, the result), then the ledger-dependent
// rules every seed shares.
var groupingTable = map[string][]GroupRule{
	SeedSignedPayloadSigner: {
		reproduced(txEnvelope + "/tx/source_account"),
		reproduced(operation + "/set_options/signer/key"), // the signed-payload signer: its key and its 29-byte payload
		reproduced(operation + "/set_options/signer/weight"),
		reproduced(txResult + "/result/result/tx_success/0/op_inner/set_options"),
		reproduced("/*/tx_processing/**/updated/data/account/signers/0/key"), // the signer in the account's new state
	},
	SeedPoolShareTrustline: {
		reproduced(txEnvelope + "/tx/source_account"),
		// the assets and their order: assetA's and assetB's code and issuer, in the order the operation carries them
		reproduced(operation + "/change_trust/line/pool_share/liquidity_pool_constant_product/asset_a/credit_alphanum4/asset_code"),
		reproduced(operation + "/change_trust/line/pool_share/liquidity_pool_constant_product/asset_a/credit_alphanum4/issuer"),
		reproduced(operation + "/change_trust/line/pool_share/liquidity_pool_constant_product/asset_b/credit_alphanum4/asset_code"),
		reproduced(operation + "/change_trust/line/pool_share/liquidity_pool_constant_product/asset_b/credit_alphanum4/issuer"),
		reproduced(operation + "/change_trust/line/pool_share/liquidity_pool_constant_product/fee"),
		reproduced(txResult + "/result/result/tx_success/0/op_inner/change_trust"),
		// The created trustline's pool id is no expected value: it follows from the assets and the
		// fee above, and its pointer holds the index of a change whose place in the operation's changes differs from
		// run to run.
	},
	// Entry 3 was not part of the deciding test: its pointers follow the tool's naming for a payment, as this tool's
	// own generations of the entry decode it.
	SeedMuxedAccount: {
		reproduced(txEnvelope + "/tx/source_account"),  // the muxed source
		reproduced(operation + "/payment/destination"), // the muxed destination
		reproduced(operation + "/payment/asset"),
		reproduced(operation + "/payment/amount"),
		reproduced(txResult + "/result/result/tx_success/0/op_inner/payment"),
	},
}

// Grouping returns the grouping table's rows for an entry id, its reproduced rules first, then the ledger-dependent
// rules; false when the id is not a seed's.
func Grouping(entryID string) ([]GroupRule, bool) {
	own, ok := groupingTable[entryID]
	if !ok {
		return nil, false
	}
	return append(append([]GroupRule(nil), own...), ledgerRules...), true
}

// Classify returns the first rule of the entry's grouping that matches pointer: its group, and its field when the
// group is ledger-dependent. False when the leaf is not an expected value of the entry.
func Classify(entryID, pointer string) (GroupRule, bool) {
	rules, _ := Grouping(entryID)
	for _, r := range rules {
		if r.Matches(pointer) {
			return r, true
		}
	}
	return GroupRule{}, false
}
