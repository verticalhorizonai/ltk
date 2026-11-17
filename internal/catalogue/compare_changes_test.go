package catalogue

// The change lists of ltk compare: each `changes`, `tx_changes_before`, `tx_changes_after` and `fee_processing` list
// is matched by (ledger key, change kind) as a multiset.

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// realDecoding reads a Rust-decoded LedgerCloseMeta committed under testdata/real.
func realDecoding(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(testdataRoot + "/real/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// opChangeOrder is the (kind, arm) sequence of the first operation's changes of a decoded document.
func opChangeOrder(t *testing.T, doc string) []string {
	t.Helper()
	n, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := n.Resolve("/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes")
	if !ok {
		t.Fatal("no operation changes")
	}
	var out []string
	for _, e := range list.Elems {
		k, _ := changeIdentity(e)
		out = append(out, k)
	}
	return out
}

// TestCompareRealChangeOrders: entry 2's protocol-30 decodings of 2026-11-16 (runs 1, 2, 4 and 5, committed under
// testdata/real, its four observed orders of the operation's eight changes) compare pairwise with every difference
// attributed to a ledger-dependent field: a reordering alone is no difference. Entry 3's two runs, which kept one
// order, compare the same way.
func TestCompareRealChangeOrders(t *testing.T) {
	type pair struct {
		id   string
		a, b string
	}
	entry2 := []string{
		"cap-0038-pool-share-trustline-p30-run1.json", "cap-0038-pool-share-trustline-p30-run2.json",
		"cap-0038-pool-share-trustline-p30-run4.json", "cap-0038-pool-share-trustline-p30-run5.json",
	}
	var pairs []pair
	orders := map[string]bool{}
	for i, a := range entry2 {
		orders[strings.Join(opChangeOrder(t, realDecoding(t, a)), ",")] = true
		for _, b := range entry2[i+1:] {
			pairs = append(pairs, pair{model.SeedPoolShareTrustline, a, b}, pair{model.SeedPoolShareTrustline, b, a})
		}
	}
	if len(orders) != 4 {
		t.Fatalf("the four decodings hold %d orders of the operation's changes, want 4", len(orders))
	}
	pairs = append(pairs, pair{model.SeedMuxedAccount, "cap-0027-muxed-account-p30-run1.json", "cap-0027-muxed-account-p30-run2.json"})
	for _, p := range pairs {
		t.Run(p.a+" against "+p.b, func(t *testing.T) {
			docA, docB := realDecoding(t, p.a), realDecoding(t, p.b)
			expA, err := SplitExpectedValues(p.id, []byte(docA))
			if err != nil {
				t.Fatal(err)
			}
			r, err := Compare(miniFolder(t, miniEntry{p.id, docA, expA.LedgerDependent}), miniFolder(t, miniEntry{p.id, docB, nil}))
			if err != nil {
				t.Fatal(err)
			}
			ec := r.Entries[0]
			if len(ec.Differ) == 0 {
				t.Fatal("two generations of different ledgers show no difference")
			}
			for _, d := range ec.Differ {
				if d.LedgerDependentField == nil {
					t.Errorf("a difference outside the ledger-dependent list: %s %s / %s", d.Pointer, d.ValueA, d.ValueB)
				}
			}
			if !r.Passed {
				t.Error("passed false")
			}
			// The change lists' elements are all matched: the created trustline's pool id and the pool's own id are
			// equal leaves at side A's pointers.
			var poolIDs int
			for _, e := range ec.Equal {
				if strings.HasSuffix(e, "/created/data/trustline/asset/pool_share") || strings.HasSuffix(e, "/created/data/liquidity_pool/liquidity_pool_id") {
					poolIDs++
				}
			}
			if p.id == model.SeedPoolShareTrustline && poolIDs != 2 {
				t.Errorf("%d equal pool ids, want 2", poolIDs)
			}
		})
	}
}

// change is an account change of kind on account acct with a balance, in the Rust XDR tool's form.
func change(kind, acct string, balance int, lastModified int) string {
	return fmt.Sprintf(`{%q:{"last_modified_ledger_seq":%d,"data":{"account":{"account_id":%q,"balance":"%d"}},"ext":"v0"}}`, kind, lastModified, acct, balance)
}

// trustChange is a trustline change of kind on acct's trustline to the asset code.
func trustChange(kind, acct, code string, balance int) string {
	return fmt.Sprintf(`{%q:{"last_modified_ledger_seq":7,"data":{"trustline":{"account_id":%q,"asset":{"credit_alphanum4":{"asset_code":%q,"issuer":"GI"}},"balance":"%d"}},"ext":"v0"}}`, kind, acct, code, balance)
}

// opDoc is a decoded document whose operation's changes are the given elements.
func opDoc(elems ...string) string {
	return `{"v2":{"tx_processing":[{"tx_apply_processing":{"v4":{"operations":[{"changes":[` + strings.Join(elems, ",") + `]}]}}}]}}`
}

const opChanges = "/v2/tx_processing/0/tx_apply_processing/v4/operations/0/changes"

type diffWant struct {
	pointer, valueA, valueB string
}

func diffsOf(ec contract.EntryComparison) []diffWant {
	var out []diffWant
	for _, d := range ec.Differ {
		out = append(out, diffWant{d.Pointer, string(d.ValueA), string(d.ValueB)})
	}
	return out
}

// TestCompareChangeLists: the multiset matching of a change list.
func TestCompareChangeLists(t *testing.T) {
	g1s, g1u := change("state", "G1", 100, 5), change("updated", "G1", 90, 7)
	g2s := change("state", "G2", 100, 5)
	cases := []struct {
		name   string
		a, b   string
		differ []diffWant
		equal  int // how many equal leaves, -1 for not checked
	}{
		{name: "a reordering alone is no difference",
			a: opDoc(g1s, g1u, g2s, trustChange("state", "G1", "USD", 0)), b: opDoc(trustChange("state", "G1", "USD", 0), g2s, g1u, g1s), equal: 18},
		{name: "a change present in A only is a difference at A's pointer",
			a: opDoc(g1s, g2s, g1u), b: opDoc(g1u, g1s),
			differ: []diffWant{{opChanges + "/1", compactText(g2s), "null"}}, equal: -1},
		{name: "a change present in B only is a difference at B's pointer",
			a: opDoc(g1s), b: opDoc(g2s, g1s),
			differ: []diffWant{{opChanges + "/0", "null", compactText(g2s)}}, equal: -1},
		{name: "an empty list against one change",
			a: opDoc(), b: opDoc(g1s),
			differ: []diffWant{{opChanges + "/0", "null", compactText(g1s)}}, equal: 0},
		{name: "same key, other kind: no pairing",
			a: opDoc(change("state", "G1", 1, 5)), b: opDoc(change("updated", "G1", 1, 5)),
			differ: []diffWant{{opChanges + "/0", compactText(change("state", "G1", 1, 5)), "null"}, {opChanges + "/0", "null", compactText(change("updated", "G1", 1, 5))}}, equal: 0},
		{name: "two same-key same-kind changes are paired in their order",
			a: opDoc(change("state", "G1", 1, 5), change("state", "G1", 2, 5)), b: opDoc(change("state", "G1", 2, 5), change("state", "G1", 1, 5)),
			differ: []diffWant{{opChanges + "/0/state/data/account/balance", `"1"`, `"2"`}, {opChanges + "/1/state/data/account/balance", `"2"`, `"1"`}}, equal: -1},
		{name: "same-key same-kind changes are never collapsed",
			a: opDoc(change("state", "G1", 1, 5), change("state", "G1", 1, 5)), b: opDoc(change("state", "G1", 1, 5)),
			differ: []diffWant{{opChanges + "/1", compactText(change("state", "G1", 1, 5)), "null"}}, equal: -1},
		{name: "a reordered list in which one value changed reports exactly that value",
			a:      opDoc(g1s, trustChange("updated", "G1", "USD", 5), trustChange("updated", "G1", "EUR", 3)),
			b:      opDoc(trustChange("updated", "G1", "EUR", 3), trustChange("updated", "G1", "USD", 6), g1s),
			differ: []diffWant{{opChanges + "/1/updated/data/trustline/balance", `"5"`, `"6"`}}, equal: -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ec := compareDocs(t, c.a, c.b)
			if got := diffsOf(ec); !slices.Equal(got, c.differ) {
				t.Errorf("differ\n%v\nwant\n%v", got, c.differ)
			}
			if c.equal >= 0 && len(ec.Equal) != c.equal {
				t.Errorf("%d equal leaves, want %d: %q", len(ec.Equal), c.equal, ec.Equal)
			}
			for _, d := range ec.Differ {
				if d.LedgerDependentField != nil {
					t.Errorf("%s attributed with nothing recorded", d.Pointer)
				}
			}
		})
	}
}

// TestCompareChangeListsAttribution: a difference inside a matched element is reported at side A's pointer and
// attributed by folder A's recorded pointers; a change present on one side only is never attributed.
func TestCompareChangeListsAttribution(t *testing.T) {
	a := opDoc(change("state", "G1", 1, 5), change("state", "G2", 1, 6))
	b := opDoc(change("state", "G2", 1, 9), change("state", "G1", 1, 8), change("state", "G3", 1, 8))
	ec := compareDocs(t, a, b,
		ld(contract.LedgerDependentFieldLastModifiedLedger, opChanges+"/0/state/last_modified_ledger_seq"),
		ld(contract.LedgerDependentFieldLastModifiedLedger, opChanges+"/1/state/last_modified_ledger_seq"),
		ld(contract.LedgerDependentFieldLastModifiedLedger, opChanges+"/2"),
	)
	want := map[string]string{
		opChanges + "/0/state/last_modified_ledger_seq": "last_modified_ledger",
		opChanges + "/1/state/last_modified_ledger_seq": "last_modified_ledger",
		opChanges + "/2": "", // B's third change, absent from A: B's pointer, never attributed
	}
	if len(ec.Differ) != len(want) {
		t.Fatalf("differ %v", diffsOf(ec))
	}
	for _, d := range ec.Differ {
		got := ""
		if d.LedgerDependentField != nil {
			got = string(*d.LedgerDependentField)
		}
		if w, ok := want[d.Pointer]; !ok || got != w {
			t.Errorf("%s attributed to %q, want %q", d.Pointer, got, w)
		}
	}
	if d := ec.Differ[0]; string(d.ValueA) != "5" || string(d.ValueB) != "8" {
		t.Errorf("A's first change is paired with B's second: %s / %s", d.ValueA, d.ValueB)
	}
}

// TestCompareChangeListNames: the four lists are matched wherever they occur; a list of another name stays positional.
func TestCompareChangeListNames(t *testing.T) {
	x, y := change("state", "G1", 1, 5), change("state", "G2", 1, 5)
	for _, name := range []string{"changes", "tx_changes_before", "tx_changes_after", "fee_processing"} {
		ec := compareDocs(t, fmt.Sprintf(`{"deep":{"%s":[%s,%s]}}`, name, x, y), fmt.Sprintf(`{"deep":{"%s":[%s,%s]}}`, name, y, x))
		if len(ec.Differ) != 0 {
			t.Errorf("%s: %v", name, diffsOf(ec))
		}
	}
	ec := compareDocs(t, fmt.Sprintf(`{"other":[%s,%s]}`, x, y), fmt.Sprintf(`{"other":[%s,%s]}`, y, x))
	if got := differPointers(ec); !slices.Equal(got, []string{"/other/0/state/data/account/account_id", "/other/1/state/data/account/account_id"}) {
		t.Errorf("a list of another name: %q", got)
	}
	// A change list whose other side is not an array is one difference in shape, as before.
	ec = compareDocs(t, fmt.Sprintf(`{"changes":[%s]}`, x), `{"changes":{}}`)
	if got := differPointers(ec); !slices.Equal(got, []string{"/changes"}) {
		t.Errorf("a list against an object: %q", got)
	}
}

// TestCompareUnderivableKeyIsPositional: an element whose ledger key cannot be derived (an unknown entry arm, a change
// kind outside the five, a missing key field) is matched only by its index, with another underivable element at the
// same index.
func TestCompareUnderivableKeyIsPositional(t *testing.T) {
	u1 := `{"state":{"last_modified_ledger_seq":5,"data":{"future_arm":{"id":"u1"}},"ext":"v0"}}`
	u2 := `{"state":{"last_modified_ledger_seq":5,"data":{"future_arm":{"id":"u2"}},"ext":"v0"}}`
	g1 := change("state", "G1", 1, 5)
	// At the same index, two underivable elements are compared leaf by leaf.
	ec := compareDocs(t, opDoc(u1, g1), opDoc(u2, g1))
	if got := diffsOf(ec); !slices.Equal(got, []diffWant{{opChanges + "/0/state/data/future_arm/id", `"u1"`, `"u2"`}}) {
		t.Errorf("same index: %v", got)
	}
	// Moved to another index, an underivable element is not guessed at: it is a difference on each side.
	ec = compareDocs(t, opDoc(u1, g1), opDoc(g1, u1))
	if got := diffsOf(ec); !slices.Equal(got, []diffWant{{opChanges + "/0", compactText(u1), "null"}, {opChanges + "/1", "null", compactText(u1)}}) {
		t.Errorf("another index: %v", got)
	}
	// An underivable element never pairs with a derivable one at its index.
	ec = compareDocs(t, opDoc(u1), opDoc(g1))
	if len(ec.Differ) != 2 {
		t.Errorf("an underivable element against a derivable one: %v", diffsOf(ec))
	}
}

// TestChangeIdentity: the ledger key of a change, derived from the Rust XDR tool's JSON as stellar-core keys a ledger
// entry; a removed change carries the key itself and has the same key as the entry's other changes.
func TestChangeIdentity(t *testing.T) {
	entry := func(kind, arm, body string) string {
		return fmt.Sprintf(`{%q:{"last_modified_ledger_seq":3,"data":{%q:%s},"ext":"v0"}}`, kind, arm, body)
	}
	removed := func(arm, body string) string { return fmt.Sprintf(`{"removed":{%q:%s}}`, arm, body) }
	same := []struct {
		name       string
		entry, key string
	}{
		{"account", entry("state", "account", `{"account_id":"GA","balance":"1","seq_num":"2"}`), removed("account", `{"account_id":"GA"}`)},
		{"trustline", entry("state", "trustline", `{"account_id":"GA","asset":{"pool_share":"LP"},"balance":"0"}`), removed("trustline", `{"asset":{"pool_share":"LP"},"account_id":"GA"}`)},
		{"offer", entry("state", "offer", `{"seller_id":"GA","offer_id":"9","amount":"1"}`), removed("offer", `{"seller_id":"GA","offer_id":"9"}`)},
		{"data", entry("state", "data", `{"account_id":"GA","data_name":"n","data_value":"00"}`), removed("data", `{"account_id":"GA","data_name":"n"}`)},
		{"claimable balance", entry("state", "claimable_balance", `{"balance_id":{"claimable_balance_id_type_v0":"ab"},"amount":"1"}`), removed("claimable_balance", `{"balance_id":{"claimable_balance_id_type_v0":"ab"}}`)},
		{"liquidity pool", entry("state", "liquidity_pool", `{"liquidity_pool_id":"LP","body":{}}`), removed("liquidity_pool", `{"liquidity_pool_id":"LP"}`)},
		{"contract data", entry("state", "contract_data", `{"ext":"v0","contract":"CA","key":{"symbol":"k"},"durability":"persistent","val":{"u32":1}}`), removed("contract_data", `{"contract":"CA","key":{"symbol":"k"},"durability":"persistent"}`)},
		{"contract code", entry("state", "contract_code", `{"ext":"v0","hash":"ab","code":"00"}`), removed("contract_code", `{"hash":"ab"}`)},
		{"config setting", entry("state", "config_setting", `{"contract_max_size_bytes":65536}`), removed("config_setting", `{"config_setting_id":"contract_max_size_bytes"}`)},
		{"ttl", entry("state", "ttl", `{"key_hash":"ab","live_until_ledger_seq":9}`), removed("ttl", `{"key_hash":"ab"}`)},
	}
	keyOf := func(doc string) (string, bool) {
		n, err := Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		k, ok := changeIdentity(n)
		_, key, _ := strings.Cut(k, " ")
		return key, ok
	}
	seen := map[string]string{}
	for _, c := range same {
		ke, okE := keyOf(c.entry)
		kk, okK := keyOf(c.key)
		if !okE || !okK || ke != kk {
			t.Errorf("%s: the entry's key %q (%v) and the removed key %q (%v) differ", c.name, ke, okE, kk, okK)
		}
		if other, dup := seen[ke]; dup {
			t.Errorf("%s and %s share the key %q", c.name, other, ke)
		}
		seen[ke] = c.name
	}
	for _, kind := range []string{"created", "updated", "state", "restored"} {
		if k, ok := changeIdentity(mustParse(t, entry(kind, "account", `{"account_id":"GA"}`))); !ok || !strings.HasPrefix(k, kind+" ") {
			t.Errorf("%s: %q %v", kind, k, ok)
		}
	}
	// Two trustlines of one account to two assets have two keys; one value outside the key does not change it.
	t1, _ := keyOf(entry("state", "trustline", `{"account_id":"GA","asset":{"credit_alphanum4":{"asset_code":"USD","issuer":"GI"}},"balance":"1"}`))
	t2, _ := keyOf(entry("state", "trustline", `{"account_id":"GA","asset":{"credit_alphanum4":{"asset_code":"USD","issuer":"GJ"}},"balance":"1"}`))
	t3, _ := keyOf(entry("state", "trustline", `{"account_id":"GA","asset":{"credit_alphanum4":{"asset_code":"USD","issuer":"GJ"}},"balance":"2"}`))
	if t1 == t2 || t2 != t3 {
		t.Errorf("trustline keys %q %q %q", t1, t2, t3)
	}
	for name, doc := range map[string]string{
		"an unknown arm":               entry("state", "future_arm", `{"id":"x"}`),
		"a kind outside the five":      `{"moved":{"data":{"account":{"account_id":"GA"}}}}`,
		"two kinds in one element":     `{"state":{"data":{"account":{"account_id":"GA"}}},"updated":{"data":{"account":{"account_id":"GA"}}}}`,
		"a missing key field":          entry("state", "trustline", `{"account_id":"GA","balance":"1"}`),
		"no data":                      `{"state":{"last_modified_ledger_seq":3}}`,
		"a scalar":                     `"v0"`,
		"a config setting of two arms": entry("state", "config_setting", `{"a":1,"b":2}`),
		"a removed unknown arm":        removed("future_arm", `{"id":"x"}`),
	} {
		if k, ok := changeIdentity(mustParse(t, doc)); ok {
			t.Errorf("%s: derived %q", name, k)
		}
	}
}

func mustParse(t *testing.T, doc string) *Node {
	t.Helper()
	n, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// compactText is a JSON text without its insignificant white space.
func compactText(s string) string {
	return string(compact([]byte(s)))
}
