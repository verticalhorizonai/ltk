package recipes

// The Recipe determinism test: Intended() is computed offline, from constants and deterministic keys, and is the same
// on every call; its pointers are exactly the entry's reproduced group in the Expected-value grouping table; and, once
// the committed corpus exists, it equals the reproduced values the corpus's catalogue records.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// TestIntendedIsStableAndOffline: two calls, on two registries, give the same map; no network is involved (Intended
// takes none); every pointer is a pointer into a LedgerCloseMeta v2's decoded JSON and every value is JSON.
func TestIntendedIsStableAndOffline(t *testing.T) {
	for i, r := range Registry() {
		a, b := r.Intended(), Registry()[i].Intended()
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: two calls of Intended differ", r.ID())
		}
		if len(a) == 0 {
			t.Errorf("%s: Intended is empty", r.ID())
		}
		for p, v := range a {
			if !strings.HasPrefix(p, "/v2/") || strings.HasSuffix(p, "/") || strings.Contains(p, "*") {
				t.Errorf("%s: %q is not a concrete pointer into a LedgerCloseMeta v2", r.ID(), p)
			}
			if !json.Valid(v) {
				t.Errorf("%s: %s's value %q is not JSON", r.ID(), p, v)
			}
		}
	}
}

// TestIntendedCoversTheReproducedGroup: every pointer of Intended() is classified as reproduced by the entry's grouping
// table, and every reproduced rule of the table is matched by a pointer of Intended(). Entry 2's table has no
// reproduced rule for the created trustline's pool id, which follows from the assets and the fee, and its Intended()
// no such pointer.
func TestIntendedCoversTheReproducedGroup(t *testing.T) {
	for _, r := range Registry() {
		rules, ok := model.Grouping(r.ID())
		if !ok {
			t.Fatalf("%s: no grouping", r.ID())
		}
		intended := r.Intended()
		for p := range intended {
			rule, ok := model.Classify(r.ID(), p)
			if !ok || rule.Group != contract.ExpectedValueGroupReproducedByEveryRun {
				t.Errorf("%s: Intended's %s is not in the reproduced group of the grouping table (rule %+v, %v)", r.ID(), p, rule, ok)
			}
		}
		for _, rule := range rules {
			if rule.Group != contract.ExpectedValueGroupReproducedByEveryRun {
				continue
			}
			covered := false
			for p := range intended {
				if rule.Matches(p) {
					covered = true
				}
			}
			if !covered {
				t.Errorf("%s: the reproduced rule %s has no value in Intended", r.ID(), rule.Pattern)
			}
		}
	}
}

// TestIntendedValues: the values are the recipes' constants and keys, as the shapes define them.
func TestIntendedValues(t *testing.T) {
	str := func(m map[string]json.RawMessage, suffix string) string {
		t.Helper()
		for p, v := range m {
			if strings.HasSuffix(p, suffix) {
				var s string
				if err := json.Unmarshal(v, &s); err != nil {
					t.Fatalf("%s: %v", p, err)
				}
				return s
			}
		}
		t.Fatalf("no pointer ends with %s", suffix)
		return ""
	}

	e1 := signedPayloadSigner{}.Intended()
	p := str(e1, "/set_options/signer/key")
	sp, err := strkey.DecodeSignedPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Payload()) != model.SignedPayloadLength || sp.Signer() != key(model.SeedSignedPayloadSigner, "signer").Address() {
		t.Errorf("entry 1's signer %s: payload of %d bytes from %s", p, len(sp.Payload()), sp.Signer())
	}
	if str(e1, "/signers/0/key") != p || string(e1[operationPath+"/set_options/signer/weight"]) != "1" {
		t.Error("entry 1: the signer in the account's state or the weight is not the operation's")
	}

	e2 := poolShareTrustline{}.Intended()
	if str(e2, "/asset_a/credit_alphanum4/issuer") != model.PoolIssuerLowerBytes || str(e2, "/asset_b/credit_alphanum4/issuer") != model.PoolIssuerHigherBytes {
		t.Error("entry 2's assets are not in key-byte order")
	}
	if str(e2, "/asset_a/credit_alphanum4/asset_code") != "USD" || str(e2, "/asset_b/credit_alphanum4/asset_code") != "USD" {
		t.Error("entry 2's asset codes are not USD")
	}
	if fee := string(e2[operationPath+"/change_trust/line/pool_share/liquidity_pool_constant_product/fee"]); fee != "30" {
		t.Errorf("entry 2's pool fee is %s; want 30", fee)
	}
	// The pool id of the created trustline is not a reproduced value (it follows from the assets and the
	// fee above); no pointer of Intended reaches into the operation's changes.
	for p := range e2 {
		if strings.HasPrefix(p, opChangesPath) {
			t.Errorf("entry 2's Intended holds %s; the created trustline's pool id is not a reproduced value", p)
		}
	}

	e3 := muxedPayment{}.Intended()
	for suffix, want := range map[string]struct {
		role string
		id   uint64
	}{"/tx/source_account": {"source", muxedSourceID}, "/payment/destination": {"destination", muxedDestinationID}} {
		var m xdr.MuxedAccount
		if err := m.SetAddress(str(e3, suffix)); err != nil {
			t.Fatal(err)
		}
		if id, err := m.GetId(); err != nil || id != want.id || m.ToAccountId().Address() != key(model.SeedMuxedAccount, want.role).Address() {
			t.Errorf("entry 3's %s is %s; want the %s role muxed with id %d", suffix, m.Address(), want.role, want.id)
		}
	}
}

// TestIntendedEqualsTheCommittedCorpus holds the recipes to the committed corpus with no container: the committed
// corpus's catalogue (written by `ltk generate --all`, never by hand) holds one entry per
// recipe and no other; for each recipe, Intended() equals the reproduced values the catalogue records for its entry,
// pointer for pointer, in both directions; and every reproduced-group key of the entry's Expected-value grouping table
// is covered by a recorded value, while no recorded value sits in the other group (each ledger-dependent value is
// classified as the field the catalogue names). It skips, saying why, only when corpus/ is absent: the exported
// repository always holds it.
func TestIntendedEqualsTheCommittedCorpus(t *testing.T) {
	folder := filepath.Join("..", "..", "..", "corpus")
	if _, err := os.Stat(folder); errors.Is(err, os.ErrNotExist) {
		t.Skip("skipped: the committed corpus/ is absent from the module root; it is written by `ltk generate --all` and committed with the repository, and every export of the repository holds it")
	}
	cat, err := catalogue.ReadCatalogue(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Entries) != len(Registry()) {
		t.Errorf("the committed catalogue holds %d entries; the recipe registry %d", len(cat.Entries), len(Registry()))
	}
	for i, e := range cat.Entries {
		if _, ok := ByID(e.ID); !ok {
			t.Errorf("the committed catalogue's entries[%d] %s has no recipe", i, e.ID)
		}
	}
	for _, r := range Registry() {
		var entry *contract.CatalogueEntry
		for i := range cat.Entries {
			if cat.Entries[i].ID == r.ID() {
				entry = &cat.Entries[i]
			}
		}
		if entry == nil {
			t.Errorf("%s: the committed catalogue has no entry for the recipe", r.ID())
			continue
		}
		intended := r.Intended()
		recorded := map[string]json.RawMessage{}
		for _, v := range entry.ExpectedDecodedValues.ReproducedByEveryRun {
			if _, dup := recorded[v.Pointer]; dup {
				t.Errorf("%s: the catalogue records %s twice", r.ID(), v.Pointer)
			}
			recorded[v.Pointer] = json.RawMessage(v.Value)
		}
		for p, want := range intended {
			got, ok := recorded[p]
			if !ok {
				t.Errorf("%s: the catalogue records no reproduced value at %s", r.ID(), p)
				continue
			}
			if !sameJSON(t, got, want) {
				t.Errorf("%s: %s is %s in the catalogue; the recipe submits %s", r.ID(), p, got, want)
			}
		}
		for p := range recorded {
			if _, ok := intended[p]; !ok {
				t.Errorf("%s: the catalogue records %s, which the recipe does not intend", r.ID(), p)
			}
		}

		// Every reproduced-group key of the grouping table is covered by a recorded value.
		rules, ok := model.Grouping(r.ID())
		if !ok {
			t.Fatalf("%s: no grouping", r.ID())
		}
		for _, rule := range rules {
			if rule.Group != contract.ExpectedValueGroupReproducedByEveryRun {
				continue
			}
			covered := false
			for p := range recorded {
				covered = covered || rule.Matches(p)
			}
			if !covered {
				t.Errorf("%s: the reproduced key %s of the grouping table has no value in the committed catalogue", r.ID(), rule.Pattern)
			}
		}
		for p := range recorded {
			if rule, ok := model.Classify(r.ID(), p); !ok || rule.Group != contract.ExpectedValueGroupReproducedByEveryRun {
				t.Errorf("%s: the catalogue records %s as reproduced; the grouping table says %+v (%v)", r.ID(), p, rule, ok)
			}
		}
		for _, v := range entry.ExpectedDecodedValues.LedgerDependent {
			if rule, ok := model.Classify(r.ID(), v.Pointer); !ok || rule.Group != contract.ExpectedValueGroupLedgerDependent || rule.Field != v.Field {
				t.Errorf("%s: the catalogue records %s as ledger-dependent %s; the grouping table says %+v (%v)", r.ID(), v.Pointer, v.Field, rule, ok)
			}
		}
	}
}

// sameJSON compares two JSON values as values (numbers as their decimal text).
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	na, err := catalogue.Parse(bytes.TrimSpace(a))
	if err != nil {
		t.Fatal(err)
	}
	nb, err := catalogue.Parse(bytes.TrimSpace(b))
	if err != nil {
		t.Fatal(err)
	}
	return catalogue.Equal(na, nb)
}
