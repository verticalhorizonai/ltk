package model

// The Expected-value grouping table.

import (
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

func TestGroupRuleMatches(t *testing.T) {
	cases := []struct {
		pattern, pointer string
		want             bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", false},
		{"/a/b", "/a", false},
		{"/*/b", "/v2/b", true},
		{"/*/b", "/v2/x/b", false},
		{"/a/**/z", "/a/z", true},
		{"/a/**/z", "/a/1/2/3/z", true},
		{"/a/**/z", "/a/1/2/3/zz", false},
		{"/a/**", "/a", true},
		{"/a/**", "/a/b/c", true},
		{"", "", true},
		{"", "/a", false},
		{"/a", "", false},
		{"/a~1b", "/a~1b", true},
	}
	for _, c := range cases {
		if got := (GroupRule{Pattern: c.pattern}).Matches(c.pointer); got != c.want {
			t.Errorf("%q matches %q = %v, want %v", c.pattern, c.pointer, got, c.want)
		}
	}
}

// TestGroupingTable: every seed has its row; a reproduced rule carries no field, a ledger-dependent rule carries a
// value of LedgerDependentField; each seed's ledger-dependent rules cover every value of LedgerDependentField, the
// three that change when the source account is created in a different ledger (its sequence number, the envelope's
// signature and the transaction's hash) included.
func TestGroupingTable(t *testing.T) {
	if _, ok := Grouping("not-a-seed"); ok {
		t.Error("an id that is not a seed's has no grouping")
	}
	for _, s := range Seeds() {
		rules, ok := Grouping(s.ID)
		if !ok {
			t.Fatalf("%s has no row", s.ID)
		}
		fields := map[string]bool{}
		reproducedCount := 0
		for _, r := range rules {
			switch r.Group {
			case contract.ExpectedValueGroupReproducedByEveryRun:
				reproducedCount++
				if r.Field != "" {
					t.Errorf("%s: reproduced rule %s carries field %s", s.ID, r.Pattern, r.Field)
				}
			case contract.ExpectedValueGroupLedgerDependent:
				if !slices.Contains(contract.Unions["LedgerDependentField"], string(r.Field)) {
					t.Errorf("%s: %s: field %q is not a LedgerDependentField", s.ID, r.Pattern, r.Field)
				}
				fields[string(r.Field)] = true
			default:
				t.Errorf("%s: %s: group %q", s.ID, r.Pattern, r.Group)
			}
			if !strings.HasPrefix(r.Pattern, "/") {
				t.Errorf("%s: pattern %q is not a pointer", s.ID, r.Pattern)
			}
		}
		if reproducedCount == 0 {
			t.Errorf("%s: no reproduced value", s.ID)
		}
		for _, f := range contract.Unions["LedgerDependentField"] {
			if !fields[f] {
				t.Errorf("%s: no rule for %s", s.ID, f)
			}
		}
	}
}

// TestClassifyShapeValues: the shape's own values (the operation, the signer key and its payload, the assets and their
// order, the result), at the pointers of the captures of the deciding test (the feasibility run made before this
// tool), classify as reproduced; the created trustline's pool id does not.
func TestClassifyShapeValues(t *testing.T) {
	const tx = "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx"
	const lp = tx + "/tx/operations/0/body/change_trust/line/pool_share/liquidity_pool_constant_product"
	cases := map[string][]string{
		SeedSignedPayloadSigner: {
			tx + "/tx/operations/0/body/set_options/signer/key",
			tx + "/tx/operations/0/body/set_options/signer/weight",
			"/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/set_options",
			"/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/1/updated/data/account/signers/0/key",
		},
		SeedPoolShareTrustline: {
			lp + "/asset_a/credit_alphanum4/asset_code", lp + "/asset_a/credit_alphanum4/issuer",
			lp + "/asset_b/credit_alphanum4/asset_code", lp + "/asset_b/credit_alphanum4/issuer", lp + "/fee",
			"/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/change_trust",
		},
		SeedMuxedAccount: {
			tx + "/tx/source_account", tx + "/tx/operations/0/body/payment/destination",
			"/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/payment",
		},
	}
	for id, pointers := range cases {
		for _, p := range pointers {
			if r, ok := Classify(id, p); !ok || r.Group != contract.ExpectedValueGroupReproducedByEveryRun {
				t.Errorf("%s: %s classifies as %+v %v, want reproduced_by_every_run", id, p, r, ok)
			}
		}
	}
	// The created trustline's pool id is not in entry 2's reproduced group: it follows from the assets and the fee,
	// which are, and its change's index differs from run to run.
	for _, i := range []string{"0", "4", "6"} {
		p := "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/" + i + "/created/data/trustline/asset/pool_share"
		if r, ok := Classify(SeedPoolShareTrustline, p); ok {
			t.Errorf("%s classifies as %+v; the created trustline's pool id is no expected value", p, r)
		}
	}
	if _, ok := Classify(SeedSignedPayloadSigner, "/v2/ledger_header/header/ledger_version"); ok {
		t.Error("a value that is neither the shape's nor ledger-dependent is not an expected value")
	}
}

// decidingTestDiffs are the leaves that differed between the deciding test's two runs of entries 1 and 2, an array
// index written as 0, with the field each is attributed to: every one of them is attributed, the header's own hash to
// ledger_hash and its tx_set_result_hash to transaction_result_set_hash, since they cover the other values.
var decidingTestDiffs = map[string]contract.LedgerDependentField{
	"/v2/ledger_header/hash":                                                                                          contract.LedgerDependentFieldLedgerHash,
	"/v2/ledger_header/header/tx_set_result_hash":                                                                     contract.LedgerDependentFieldTransactionResultSetHash,
	"/v2/ledger_header/header/bucket_list_hash":                                                                       contract.LedgerDependentFieldBucketListHash,
	"/v2/ledger_header/header/fee_pool":                                                                               contract.LedgerDependentFieldFeePool,
	"/v2/ledger_header/header/ledger_seq":                                                                             contract.LedgerDependentFieldLedgerNumber,
	"/v2/ledger_header/header/previous_ledger_hash":                                                                   contract.LedgerDependentFieldPreviousLedgerHash,
	"/v2/ledger_header/header/scp_value/close_time":                                                                   contract.LedgerDependentFieldCloseTime,
	"/v2/ledger_header/header/scp_value/ext/signed/signature":                                                         contract.LedgerDependentFieldConsensusSignature,
	"/v2/ledger_header/header/scp_value/tx_set_hash":                                                                  contract.LedgerDependentFieldTransactionSetHash,
	"/v2/tx_processing/0/fee_processing/0/updated/last_modified_ledger_seq":                                           contract.LedgerDependentFieldLastModifiedLedger,
	"/v2/tx_processing/0/fee_processing/0/state/data/account/seq_num":                                                 contract.LedgerDependentFieldSourceSequenceNumber,
	"/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_ledger":                         contract.LedgerDependentFieldLedgerNumber,
	"/v2/tx_processing/0/fee_processing/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_time":                           contract.LedgerDependentFieldCloseTime,
	"/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/state/last_modified_ledger_seq":                contract.LedgerDependentFieldLastModifiedLedger,
	"/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/created/last_modified_ledger_seq":              contract.LedgerDependentFieldLastModifiedLedger,
	"/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes/0/updated/data/account/seq_num":                  contract.LedgerDependentFieldSourceSequenceNumber,
	"/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/updated/last_modified_ledger_seq":                 contract.LedgerDependentFieldLastModifiedLedger,
	"/v2/tx_processing/0/tx_apply_processing/v4/tx_changes_before/0/state/data/account/ext/v1/ext/v2/ext/v3/seq_time": contract.LedgerDependentFieldCloseTime,
	"/v2/tx_processing/0/result/transaction_hash":                                                                     contract.LedgerDependentFieldTransactionHash,
	"/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/signatures/0/signature":                 contract.LedgerDependentFieldTransactionSignature,
	"/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/seq_num":                             contract.LedgerDependentFieldSourceSequenceNumber,
	"/v2/tx_set/v1/previous_ledger_hash":                                                                              contract.LedgerDependentFieldPreviousLedgerHash,
}

// TestClassifyDecidingTestDiffs: every leaf that differed between the deciding test's two runs is ledger-dependent with
// the field above, for every seed; none is left outside the list.
func TestClassifyDecidingTestDiffs(t *testing.T) {
	for _, s := range Seeds() {
		for p, want := range decidingTestDiffs {
			if want == "" {
				t.Fatalf("%s has no field: every leaf of the deciding test's diffs is attributed", p)
			}
			r, ok := Classify(s.ID, p)
			if !ok || r.Group != contract.ExpectedValueGroupLedgerDependent || r.Field != want {
				t.Errorf("%s: %s classifies as %+v %v, want ledger_dependent %s", s.ID, p, r, ok, want)
			}
		}
	}
}

// TestClassifyProtocol30Header: at protocol 30 the ledger header's close time in milliseconds and its consensus
// signature sit under scp_value/ext/signed_ms (the protocol-30 capture, testdata/real/capture-p30.json); they are the
// ledger-dependent close time and consensus signature.
func TestClassifyProtocol30Header(t *testing.T) {
	want := map[string]contract.LedgerDependentField{
		"/v2/ledger_header/header/scp_value/ext/signed_ms/close_time_ms":                contract.LedgerDependentFieldCloseTime,
		"/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/signature": contract.LedgerDependentFieldConsensusSignature,
	}
	for _, s := range Seeds() {
		for p, f := range want {
			if r, ok := Classify(s.ID, p); !ok || r.Group != contract.ExpectedValueGroupLedgerDependent || r.Field != f {
				t.Errorf("%s: %s classifies as %+v %v, want ledger_dependent %s", s.ID, p, r, ok, f)
			}
		}
		if _, ok := Classify(s.ID, "/v2/ledger_header/header/scp_value/ext/signed_ms/lc_value_signature/node_id"); ok {
			t.Errorf("%s: the validator's node id is the same in every run; it is no expected value", s.ID)
		}
	}
}

// TestClassifySkipList: each element of the ledger header's skip_list is ledger-dependent and attributed to
// bucket_list_hash, for every seed: stellar-core writes the bucket-list hash into the skip list from ledger 50 on. The
// array itself is no leaf the table names, and nothing else changes: the shape's own values stay reproduced.
func TestClassifySkipList(t *testing.T) {
	for _, s := range Seeds() {
		for _, i := range []string{"0", "1", "2", "3"} {
			p := "/v2/ledger_header/header/skip_list/" + i
			r, ok := Classify(s.ID, p)
			if !ok || r.Group != contract.ExpectedValueGroupLedgerDependent || r.Field != contract.LedgerDependentFieldBucketListHash {
				t.Errorf("%s: %s classifies as %+v %v, want ledger_dependent bucket_list_hash", s.ID, p, r, ok)
			}
		}
		if r, ok := Classify(s.ID, "/v2/ledger_header/header/skip_list"); ok {
			t.Errorf("%s: the skip list as a whole classifies as %+v; its elements are the values", s.ID, r)
		}
		if r, ok := Classify(s.ID, "/v2/ledger_header/header/skip_list/0/x"); ok {
			t.Errorf("%s: a pointer below a skip-list element classifies as %+v", s.ID, r)
		}
	}
}
