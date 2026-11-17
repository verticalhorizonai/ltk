package catalogue

// The change lists of ltk compare. stellar-core emitted entry 2's eight ledger-entry changes in four orders across
// seven runs of the same transactions (the native runs of 2026-11-16), so the elements of a change list are matched by
// what they change, not by where they sit.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// changeLists are the names of the decoded form's lists of LedgerEntryChange, in the Rust XDR tool's JSON of a
// LedgerCloseMeta: an operation's `changes`, a transaction's `tx_changes_before`, `tx_changes_after` and
// `fee_processing`. A list of one of these names is matched wherever it occurs.
var changeLists = map[string]bool{"changes": true, "tx_changes_before": true, "tx_changes_after": true, "fee_processing": true}

// changeKinds are the LedgerEntryChangeType arms, by the Rust XDR tool's names. A `removed` change carries a
// LedgerKey; the others carry a LedgerEntry.
var changeKinds = map[string]bool{"created": true, "updated": true, "removed": true, "state": true, "restored": true}

// ledgerKeyFields are, per LedgerEntryData arm, the fields of the entry that make up its XDR LedgerKey arm, as
// stellar-core keys a ledger entry (LedgerEntryKey), by the Rust XDR tool's field names; the same names are the
// fields of the LedgerKey arm a `removed` change carries. config_setting is keyed by its arm (configSettingKey).
var ledgerKeyFields = map[string][]string{
	"account":           {"account_id"},
	"trustline":         {"account_id", "asset"},
	"offer":             {"seller_id", "offer_id"},
	"data":              {"account_id", "data_name"},
	"claimable_balance": {"balance_id"},
	"liquidity_pool":    {"liquidity_pool_id"},
	"contract_data":     {"contract", "key", "durability"},
	"contract_code":     {"hash"},
	"ttl":               {"key_hash"},
}

// configSetting is the LedgerEntryData arm whose key is the ConfigSettingID: in an entry, the name of the
// ConfigSettingEntry's own arm; in a LedgerKey, its config_setting_id.
const configSetting = "config_setting"

// changeIdentity returns a change's kind and ledger key, as "<kind> <key>", the key a canonical text of the LedgerKey
// arm and its fields; false when the element is not one change of a known kind whose key can be derived (an unknown
// LedgerEntryData or LedgerKey arm, a missing key field): such an element is matched by its index only.
func changeIdentity(n *Node) (string, bool) {
	if n == nil || n.Kind != KindObject || len(n.Keys) != 1 || !changeKinds[n.Keys[0]] {
		return "", false
	}
	kind, body := n.Keys[0], n.Values[0]
	keyed := body // a removed change's LedgerKey: {"<arm>": {<key fields>}}
	if kind != "removed" {
		keyed = body.Member("data") // a LedgerEntry: {"last_modified_ledger_seq", "data": {"<arm>": {…}}, "ext"}
	}
	if keyed == nil || keyed.Kind != KindObject || len(keyed.Keys) != 1 {
		return "", false
	}
	arm, fields := keyed.Keys[0], keyed.Values[0]
	if fields.Kind != KindObject {
		return "", false
	}
	var parts []string
	if arm == configSetting {
		id, ok := configSettingKey(kind, fields)
		if !ok {
			return "", false
		}
		parts = append(parts, id)
	} else {
		names, known := ledgerKeyFields[arm]
		if !known {
			return "", false
		}
		for _, f := range names {
			v := fields.Member(f)
			if v == nil {
				return "", false
			}
			parts = append(parts, canonical(v))
		}
	}
	return kind + " " + arm + "(" + strings.Join(parts, ",") + ")", true
}

// configSettingKey is a config setting's ConfigSettingID as a JSON string: the entry's single arm, or the removed
// key's config_setting_id.
func configSettingKey(kind string, fields *Node) (string, bool) {
	if kind == "removed" {
		id := fields.Member("config_setting_id")
		if id == nil || id.Kind != KindString {
			return "", false
		}
		return canonical(id), true
	}
	if len(fields.Keys) != 1 {
		return "", false
	}
	b, _ := json.Marshal(fields.Keys[0])
	return string(b), true
}

// canonical is a JSON value's text with its objects' keys sorted and no insignificant white space, so two equal
// values (Equal) have one text.
func canonical(n *Node) string {
	switch n.Kind {
	case KindString:
		b, _ := json.Marshal(n.Text)
		return string(b)
	case KindArray:
		parts := make([]string, len(n.Elems))
		for i, e := range n.Elems {
			parts[i] = canonical(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case KindObject:
		keys := slices.Clone(n.Keys)
		slices.Sort(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			b, _ := json.Marshal(k)
			parts[i] = string(b) + ":" + canonical(n.Member(k))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return n.Text
}

// compareChangeList compares two change lists at ptr as multisets. Each element is identified by its (change kind,
// ledger key); A's elements of one identity are paired with B's of the same identity in their order (the first with the
// first, and so on), never collapsed, and each pair is compared as before, at A's element pointer, attributed by folder
// A's recorded pointers (pointers into A's own file). An element left without a partner is present on one side only:
// one difference at its own element pointer in its own side's file, the other side's value null and no ledger-dependent
// field, whatever is recorded (a change one generation made and the other did not is never a ledger-dependent value).
// A's unpaired elements are reported in their order among the pairs, then B's.
//
// An element whose key cannot be derived keeps a positional identity, its index: it pairs only with an element of B
// at the same index whose key cannot be derived either, rather than being guessed at.
func compareChangeList(a, b *Node, ptr string, ec *contract.EntryComparison, attribute func(string) *contract.LedgerDependentField) {
	identity := func(n *Node, i int) string {
		if id, ok := changeIdentity(n); ok {
			return id
		}
		return "\x00index " + strconv.Itoa(i) // no derived identity starts with NUL
	}
	queue := map[string][]int{}
	for j, e := range b.Elems {
		id := identity(e, j)
		queue[id] = append(queue[id], j)
	}
	pairedB := make([]bool, len(b.Elems))
	null := json.RawMessage("null")
	for i, e := range a.Elems {
		p := ptr + "/" + strconv.Itoa(i)
		id := identity(e, i)
		if q := queue[id]; len(q) > 0 {
			queue[id] = q[1:]
			pairedB[q[0]] = true
			compareNodes(e, b.Elems[q[0]], p, ec, attribute)
			continue
		}
		ec.Differ = append(ec.Differ, contract.FieldDifference{Pointer: p, ValueA: compact(e.Raw), ValueB: null})
	}
	for j, e := range b.Elems {
		if !pairedB[j] {
			ec.Differ = append(ec.Differ, contract.FieldDifference{Pointer: ptr + "/" + strconv.Itoa(j), ValueA: null, ValueB: compact(e.Raw)})
		}
	}
}
