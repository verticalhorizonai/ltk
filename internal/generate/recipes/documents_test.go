package recipes

// The Shape recipe documents: one document per recipe, <entry id>.md beside the code, that states the transactions the
// shape submits, the CAP and the protocol version it declares, and how each expected value is read back out of the
// capture. This test holds each document to the code beside it, so a recipe change that leaves its document behind
// fails.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// docAbbreviations are the pointer prefixes the documents write as one letter, defined in each document.
var docAbbreviations = []struct{ letter, prefix string }{
	{"L", operationPath + "/change_trust/line/pool_share/liquidity_pool_constant_product"},
	{"T", envelopePath},
	{"P", "/v2/tx_processing/0"},
}

// TestRecipeDocuments: every recipe of the registry has its document, and it names the entry, its CAP, its XDR type,
// the protocol it declares (CurrentProtocol, the fallback 28, and where the recorded version is read from), the RPC
// methods its transactions go through and the operation they carry, the Rust XDR tool's decode command, and every
// reproduced pointer of Intended() with its value. No document states a duration or a cost.
func TestRecipeDocuments(t *testing.T) {
	operations := map[string]string{
		model.SeedSignedPayloadSigner: "`SetOptions`",
		model.SeedPoolShareTrustline:  "`ChangeTrust`",
		model.SeedMuxedAccount:        "`Payment`",
	}
	timing := regexp.MustCompile(`(?i)\b(took|cost|costs|duration|elapsed|\d+(\.\d+)?\s*(ms|s|sec|secs|seconds?|minutes?|mins?|hours?|h))\b`)
	for _, r := range Registry() {
		b, err := os.ReadFile(r.ID() + ".md")
		if err != nil {
			t.Errorf("%s: no recipe document: %v", r.ID(), err)
			continue
		}
		doc := string(b)
		seed, _ := model.SeedByID(r.ID())
		for _, want := range []string{
			"# Recipe: `" + r.ID() + "`", "`" + seed.Cap + "`", "`" + seed.XdrType + "`",
			"`CurrentProtocol` (" + strconv.Itoa(orchestration.CurrentProtocol) + ")", "protocol " + strconv.Itoa(orchestration.FallbackProtocol),
			"/v2/ledger_header/header/ledger_version", "`getLedgerEntries`", "`sendTransaction`", "`getTransaction`", "`getLedgers`",
			"`metadataXdr`", operations[r.ID()],
			"stellar-xdr decode --type LedgerCloseMeta --input single --output json-formatted",
			"`reproducedByEveryRun`", "`ledgerDependent`",
		} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s.md does not state %q", r.ID(), want)
			}
		}
		for _, a := range docAbbreviations {
			if strings.Contains(doc, "`"+a.letter+"/") && !strings.Contains(doc, "`"+a.letter+" = "+abbreviate(a.prefix, a.letter)+"`") {
				t.Errorf("%s.md uses %s/ without defining it as %s", r.ID(), a.letter, a.prefix)
			}
		}
		for ptr, v := range r.Intended() {
			if !strings.Contains(doc, "`"+abbreviate(ptr, "")+"`") {
				t.Errorf("%s.md does not give the pointer %s (as `%s`)", r.ID(), ptr, abbreviate(ptr, ""))
			}
			var s string
			text := string(v)
			if json.Unmarshal(v, &s) == nil {
				text = s
			}
			if !strings.Contains(doc, text) {
				t.Errorf("%s.md does not give %s's value %s", r.ID(), ptr, v)
			}
		}
		if m := timing.FindString(doc); m != "" {
			t.Errorf("%s.md states a duration or a cost: %q", r.ID(), m)
		}
	}
}

// abbreviate writes ptr with its longest abbreviated prefix as its letter (the documents' notation); except, a letter
// whose own definition is being written, which is then written with the shorter letters only.
func abbreviate(ptr, except string) string {
	for _, a := range docAbbreviations {
		if a.letter == except {
			continue
		}
		if strings.HasPrefix(ptr, a.prefix+"/") {
			return a.letter + strings.TrimPrefix(ptr, a.prefix)
		}
	}
	return ptr
}

// TestRecipeDocumentsNameEveryLedgerDependentRule: each document's "Dependent on the ledger" section names every
// ledger-dependent rule of the grouping table for its entry: the rule's field, and the last named key of its pattern
// (written alone or as the end of a path). A key named `signature` is named by its field, the consensus signature or
// the envelope's signature.
func TestRecipeDocumentsNameEveryLedgerDependentRule(t *testing.T) {
	for _, r := range Registry() {
		b, err := os.ReadFile(r.ID() + ".md")
		if err != nil {
			t.Fatalf("%s: no recipe document: %v", r.ID(), err)
		}
		doc := strings.ReplaceAll(string(b), "\r\n", "\n")
		start := strings.Index(doc, "\n### Dependent on the ledger")
		if start < 0 {
			t.Errorf("%s.md has no section \"Dependent on the ledger\"", r.ID())
			continue
		}
		section := doc[start+1:]
		if end := strings.Index(section, "\n## "); end >= 0 {
			section = section[:end]
		}
		rules, _ := model.Grouping(r.ID())
		for _, rule := range rules {
			if rule.Field == "" {
				continue
			}
			field := "`" + string(rule.Field) + "`"
			if !strings.Contains(section, field) {
				t.Errorf("%s.md does not name the ledger-dependent field %s", r.ID(), field)
			}
			segs := strings.Split(rule.Pattern, "/")
			key := ""
			for i := len(segs) - 1; i >= 0 && key == ""; i-- {
				if segs[i] != "*" && segs[i] != "**" {
					key = segs[i]
				}
			}
			if key == "signature" {
				continue
			}
			if !strings.Contains(section, "`"+key+"`") && !strings.Contains(section, "/"+key+"`") {
				t.Errorf("%s.md does not name the key %q of the ledger-dependent rule %s", r.ID(), key, rule.Pattern)
			}
		}
	}
}

// changeOrder writes the order of an operation's ledger-entry changes as entry 2's recipe document does: c created,
// s state, u updated, each with its entry (`account`, `liquidity_pool`, `trustline:pool` for the pool-share trustline,
// `trustline:<first four characters of the issuer>` for an asset's); a `state` followed by the same entry's `updated`
// is one `s/u`.
func changeOrder(changes []map[string]json.RawMessage) ([]string, error) {
	type step struct{ kind, entry string }
	var steps []step
	for i, c := range changes {
		if len(c) != 1 {
			return nil, fmt.Errorf("change %d has %d kinds, want 1", i, len(c))
		}
		for kind, raw := range c {
			var e struct {
				Data map[string]struct {
					Asset map[string]json.RawMessage `json:"asset"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &e); err != nil || len(e.Data) != 1 {
				return nil, fmt.Errorf("change %d (%s): not a ledger entry with one data arm", i, kind)
			}
			short := map[string]string{"created": "c", "state": "s", "updated": "u"}[kind]
			if short == "" {
				return nil, fmt.Errorf("change %d: kind %q is not created, state or updated", i, kind)
			}
			for arm, d := range e.Data {
				entry := arm
				if arm == "trustline" {
					if _, ok := d.Asset["pool_share"]; ok {
						entry = "trustline:pool"
					} else {
						var credit struct{ Issuer string }
						for _, a := range d.Asset {
							_ = json.Unmarshal(a, &credit)
						}
						if len(credit.Issuer) < 4 {
							return nil, fmt.Errorf("change %d: a trustline with no issuer", i)
						}
						entry = "trustline:" + credit.Issuer[:4]
					}
				}
				steps = append(steps, step{short, entry})
			}
		}
	}
	var out []string
	for i := 0; i < len(steps); i++ {
		if steps[i].kind == "s" && i+1 < len(steps) && steps[i+1] == (step{"u", steps[i].entry}) {
			out = append(out, "s/u "+steps[i].entry)
			i++
			continue
		}
		out = append(out, steps[i].kind+" "+steps[i].entry)
	}
	return out, nil
}

// TestCommittedEntry2OrderIsInItsDocument: the order of the operation's changes in the committed
// corpus/cap-0038-pool-share-trustline/entry.json is a row of the order table in entry 2's recipe document, so the
// document's observation is held to the committed data. It skips, saying why, only when corpus/ is absent.
func TestCommittedEntry2OrderIsInItsDocument(t *testing.T) {
	path := filepath.Join("..", "..", "..", "corpus", model.SeedPoolShareTrustline, "entry.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skipf("no committed corpus at %s", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		V2 struct {
			TxProcessing []struct {
				TxApplyProcessing struct {
					V4 struct {
						Operations []struct {
							Changes []map[string]json.RawMessage `json:"changes"`
						} `json:"operations"`
					} `json:"v4"`
				} `json:"tx_apply_processing"`
			} `json:"tx_processing"`
		} `json:"v2"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.V2.TxProcessing) != 1 || len(doc.V2.TxProcessing[0].TxApplyProcessing.V4.Operations) != 1 {
		t.Fatalf("%s: not one transaction with one operation", path)
	}
	order, err := changeOrder(doc.V2.TxProcessing[0].TxApplyProcessing.V4.Operations[0].Changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 5 {
		t.Fatalf("the committed entry's changes read as %q, want the shape's five entries", order)
	}
	md, err := os.ReadFile(model.SeedPoolShareTrustline + ".md")
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	in := false
	for _, l := range strings.Split(strings.ReplaceAll(string(md), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "| Runs | Protocol |"):
			in = true
		case in && strings.HasPrefix(l, "|---"):
		case in && strings.HasPrefix(l, "|"):
			cells := strings.Split(strings.Trim(l, "|"), "|")
			rows = append(rows, strings.TrimSpace(cells[len(cells)-1]))
		case in:
			in = false
		}
	}
	if len(rows) == 0 {
		t.Fatal("entry 2's recipe document has no order table")
	}
	if want := strings.Join(order, ", "); !slices.Contains(rows, want) {
		t.Errorf("the committed entry's order %q is not a row of the document's table %q", want, rows)
	}
}

// TestChangeOrder holds the reader of change orders to the document's notation.
func TestChangeOrder(t *testing.T) {
	var changes []map[string]json.RawMessage
	src := `[{"created":{"data":{"trustline":{"asset":{"pool_share":"L"}}}}},
		{"state":{"data":{"account":{}}}},{"updated":{"data":{"account":{}}}},
		{"state":{"data":{"trustline":{"asset":{"credit_alphanum4":{"asset_code":"USD","issuer":"GCG2X"}}}}}},
		{"created":{"data":{"liquidity_pool":{}}}},
		{"updated":{"data":{"trustline":{"asset":{"credit_alphanum4":{"asset_code":"USD","issuer":"GCG2X"}}}}}}]`
	if err := json.Unmarshal([]byte(src), &changes); err != nil {
		t.Fatal(err)
	}
	got, err := changeOrder(changes)
	want := []string{"c trustline:pool", "s/u account", "s trustline:GCG2", "c liquidity_pool", "u trustline:GCG2"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("changeOrder = %q, %v; want %q", got, err, want)
	}
	if _, err := changeOrder([]map[string]json.RawMessage{{"removed": json.RawMessage(`{"data":{"account":{}}}`)}}); err == nil {
		t.Error("a removed change was read")
	}
}
