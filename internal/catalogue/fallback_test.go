package catalogue_test

// The Protocol fallback recorder: the catalogue records it writes, validated against the catalogue schema. An external
// test package, so that it can validate through internal/schema, which imports this package.

import (
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/schema"
)

// validEntry is entry 1 of the fixture corpus testdata/corpus/valid, a complete record that validates.
func validEntry(t *testing.T) contract.CatalogueEntry {
	t.Helper()
	cat, err := catalogue.ReadCatalogue("../../testdata/corpus/valid")
	if err != nil {
		t.Fatal(err)
	}
	return cat.Entries[0]
}

// validates asserts that a catalogue holding e alone passes the catalogue schema.
func validates(t *testing.T, e contract.CatalogueEntry) {
	t.Helper()
	b, err := catalogue.EncodeCatalogue(contract.Catalogue{Entries: []contract.CatalogueEntry{e}})
	if err != nil {
		t.Fatal(err)
	}
	if _, failures := schema.ValidateCatalogue(b); len(failures) != 0 {
		t.Errorf("the record does not validate: %+v", failures)
	}
}

// TestRecordProtocol: an entry generated at the current stable protocol is recorded with that protocol, read from its
// capture, and protocolFallback false; an entry generated on the fallback network is recorded with protocolVersion 28
// and protocolFallback true. Both records validate.
func TestRecordProtocol(t *testing.T) {
	cases := []struct {
		name     string
		captured int
		fallback bool
	}{
		{"current stable protocol", 30, false},
		{"protocol-28 fallback", contract.FALLBACK_PROTOCOL_VERSION, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := validEntry(t)
			e.ProtocolVersion, e.ProtocolFallback = 1, !c.fallback // whatever was there is replaced
			if err := catalogue.RecordProtocol(&e, c.captured, c.fallback); err != nil {
				t.Fatal(err)
			}
			if e.ProtocolVersion != c.captured || e.ProtocolFallback != c.fallback {
				t.Errorf("recorded protocolVersion %d protocolFallback %v; want %d %v", e.ProtocolVersion, e.ProtocolFallback, c.captured, c.fallback)
			}
			validates(t, e)
		})
	}
}

// TestRecordProtocolRefuses (the catalogue schema's rule): the recorder never writes a record the schema refuses or a
// protocol the ledger did not run. A capture from the fallback network whose header states another protocol, and a
// captured protocol below 1, are errors naming the entry, and the record is left unchanged.
func TestRecordProtocolRefuses(t *testing.T) {
	cases := []struct {
		name      string
		captured  int
		fallback  bool
		inMessage string
	}{
		{"fallback network, header states 30", 30, true, "generated on the protocol-28 fallback network, but the captured ledger header states protocol 30"},
		{"no protocol read", 0, false, "the captured ledger header states protocol 0"},
		{"negative protocol on the fallback network", -1, true, "the captured ledger header states protocol -1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := validEntry(t)
			before := e
			err := catalogue.RecordProtocol(&e, c.captured, c.fallback)
			if err == nil || !strings.Contains(err.Error(), "entry "+e.ID+": ") || !strings.Contains(err.Error(), c.inMessage) {
				t.Errorf("error %v; want one naming the entry and saying %q", err, c.inMessage)
			}
			if e.ProtocolVersion != before.ProtocolVersion || e.ProtocolFallback != before.ProtocolFallback {
				t.Errorf("the record changed: %d %v", e.ProtocolVersion, e.ProtocolFallback)
			}
		})
	}
}
