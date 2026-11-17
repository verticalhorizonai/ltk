package catalogue

// Splitting the expected values by the grouping table.

import (
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// TestSplitExpectedValues: an entry's expected values land in the group the table assigns, the ledger-dependent ones
// with their field, in document order; a rule that names an object records it whole.
func TestSplitExpectedValues(t *testing.T) {
	doc := `{"v2":{"ledger_header":{"hash":"h","header":{"ledger_seq":29,"scp_value":{"close_time":"1794696789"}}},
"tx_set":{"v1":{"previous_ledger_hash":"p","phases":[{"v0":[{"txset_comp_txs_maybe_discounted_fee":{"txs":[{"tx":{"tx":{"source_account":"MA","operations":[{"body":{"payment":{"destination":"MB","asset":{"credit_alphanum4":{"asset_code":"USD","issuer":"G"}},"amount":"1"}}}]}}}]}}]}]}},
"tx_processing":[{"result":{"transaction_hash":"t","result":{"result":{"tx_success":[{"op_inner":{"payment":"success"}}]}}}}]}}`
	got, err := SplitExpectedValues(model.SeedMuxedAccount, []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var rep []string
	for _, v := range got.ReproducedByEveryRun {
		rep = append(rep, v.Pointer+"="+string(v.Value))
	}
	wantRep := []string{
		"/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/source_account=\"MA\"",
		"/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/payment/destination=\"MB\"",
		"/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/payment/asset={\"credit_alphanum4\":{\"asset_code\":\"USD\",\"issuer\":\"G\"}}",
		"/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/payment/amount=\"1\"",
		"/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/payment=\"success\"",
	}
	if strings.Join(rep, "\n") != strings.Join(wantRep, "\n") {
		t.Errorf("reproduced\n%s\nwant\n%s", strings.Join(rep, "\n"), strings.Join(wantRep, "\n"))
	}
	var led []string
	for _, v := range got.LedgerDependent {
		led = append(led, string(v.Field)+" "+v.Pointer+"="+string(v.Value))
	}
	wantLed := []string{
		"ledger_hash /v2/ledger_header/hash=\"h\"",
		"ledger_number /v2/ledger_header/header/ledger_seq=29",
		"close_time /v2/ledger_header/header/scp_value/close_time=\"1794696789\"",
		"previous_ledger_hash /v2/tx_set/v1/previous_ledger_hash=\"p\"",
		"transaction_hash /v2/tx_processing/0/result/transaction_hash=\"t\"",
	}
	if strings.Join(led, "\n") != strings.Join(wantLed, "\n") {
		t.Errorf("ledger-dependent\n%s\nwant\n%s", strings.Join(led, "\n"), strings.Join(wantLed, "\n"))
	}
}

func TestSplitExpectedValuesErrors(t *testing.T) {
	if _, err := SplitExpectedValues("not-a-seed", []byte(`{}`)); err == nil {
		t.Error("an entry that is not a seed has no grouping")
	}
	if _, err := SplitExpectedValues(model.SeedSignedPayloadSigner, []byte(`{`)); err == nil {
		t.Error("decoded JSON that is not JSON")
	}
	got, err := SplitExpectedValues(model.SeedSignedPayloadSigner, []byte(`{}`))
	if err != nil || got.ReproducedByEveryRun == nil || got.LedgerDependent == nil {
		t.Errorf("no expected value: both groups are [] (an array is never written as null): %+v %v", got, err)
	}
}

// TestSplitExpectedValuesSkipList: a decoded header whose skip_list holds non-zero hashes records each element as a
// ledger-dependent value of bucket_list_hash, in document order after the bucket-list hash; the reproduced group and
// every other ledger-dependent value are the split of the same document without the skip list.
func TestSplitExpectedValuesSkipList(t *testing.T) {
	const h0, h1 = "9f4c2a7d1e8b6c3f0a5d9e2b7c4f1a8d3e6b0c9f2a5d8e1b4c7f0a3d6e9b2c5f", "1b4c7f0a3d6e9b2c5f8a1d4e7b0c3f6a9d2e5b8c1f4a7d0e3b6c9f2a5d8e1b4c"
	const zero = "0000000000000000000000000000000000000000000000000000000000000000"
	header := func(skip string) string {
		return `{"v2":{"ledger_header":{"hash":"h","header":{"ledger_seq":57,"bucket_list_hash":"b",` + skip + `"scp_value":{"close_time":"1794696789"}}},
"tx_set":{"v1":{"previous_ledger_hash":"p","phases":[{"v0":[{"txset_comp_txs_maybe_discounted_fee":{"txs":[{"tx":{"tx":{"source_account":"GA","operations":[{"body":{"set_options":{"signer":{"key":"P","weight":1}}}}]}}}]}}]}]}},
"tx_processing":[{"result":{"transaction_hash":"t","result":{"result":{"tx_success":[{"op_inner":{"set_options":"success"}}]}}}}]}}`
	}
	with, err := SplitExpectedValues(model.SeedSignedPayloadSigner, []byte(header(`"skip_list":["`+h0+`","`+h1+`","`+zero+`","`+zero+`"],`)))
	if err != nil {
		t.Fatal(err)
	}
	without, err := SplitExpectedValues(model.SeedSignedPayloadSigner, []byte(header("")))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(with.ReproducedByEveryRun, without.ReproducedByEveryRun, func(a, b contract.ReproducedValue) bool {
		return a.Pointer == b.Pointer && string(a.Value) == string(b.Value)
	}) {
		t.Errorf("the skip list changed the reproduced group:\n%+v\nwant\n%+v", with.ReproducedByEveryRun, without.ReproducedByEveryRun)
	}
	var skip, other []string
	for _, v := range with.LedgerDependent {
		s := string(v.Field) + " " + v.Pointer + "=" + string(v.Value)
		if strings.HasPrefix(v.Pointer, "/v2/ledger_header/header/skip_list") {
			skip = append(skip, s)
		} else {
			other = append(other, s)
		}
	}
	wantSkip := []string{
		`bucket_list_hash /v2/ledger_header/header/skip_list/0="` + h0 + `"`,
		`bucket_list_hash /v2/ledger_header/header/skip_list/1="` + h1 + `"`,
		`bucket_list_hash /v2/ledger_header/header/skip_list/2="` + zero + `"`,
		`bucket_list_hash /v2/ledger_header/header/skip_list/3="` + zero + `"`,
	}
	if strings.Join(skip, "\n") != strings.Join(wantSkip, "\n") {
		t.Errorf("skip list\n%s\nwant\n%s", strings.Join(skip, "\n"), strings.Join(wantSkip, "\n"))
	}
	var wantOther []string
	for _, v := range without.LedgerDependent {
		wantOther = append(wantOther, string(v.Field)+" "+v.Pointer+"="+string(v.Value))
	}
	if strings.Join(other, "\n") != strings.Join(wantOther, "\n") {
		t.Errorf("the other ledger-dependent values\n%s\nwant\n%s", strings.Join(other, "\n"), strings.Join(wantOther, "\n"))
	}

	// ltk compare of a capture before ledger 50 (a zero skip list) with one after it: every skip-list difference is
	// attributed to bucket_list_hash through folder A's recorded values, and the comparison passes.
	zeroDoc := header(`"skip_list":["` + zero + `","` + zero + `","` + zero + `","` + zero + `"],`)
	recorded, err := SplitExpectedValues(model.SeedSignedPayloadSigner, []byte(zeroDoc))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Compare(miniFolder(t, miniEntry{model.SeedSignedPayloadSigner, zeroDoc, recorded.LedgerDependent}),
		miniFolder(t, miniEntry{model.SeedSignedPayloadSigner, header(`"skip_list":["` + h0 + `","` + h1 + `","` + zero + `","` + zero + `"],`), nil}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || len(r.Entries) != 1 || len(r.Entries[0].Differ) != 2 {
		t.Fatalf("compare: %+v", r)
	}
	for _, d := range r.Entries[0].Differ {
		if d.LedgerDependentField == nil || *d.LedgerDependentField != contract.LedgerDependentFieldBucketListHash {
			t.Errorf("%s is attributed to %v, want bucket_list_hash", d.Pointer, d.LedgerDependentField)
		}
	}
}
