package catalogue

// The Corpus lister on the fixture corpus.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// validListText is the text listing of testdata/corpus/valid: a header of the ListEntry names, then one tab-separated
// line per entry in catalogue order.
const validListText = "id\tcap\tprotocolVersion\txdrType\tlength\tsynthetic\n" +
	"cap-0040-signed-payload-signer\tCAP-0040\t30\tLedgerCloseMeta\t1920\tfalse\n" +
	"cap-0038-pool-share-trustline\tCAP-0038\t28\tLedgerCloseMeta\t2816\tfalse\n" +
	"cap-0027-muxed-account\tCAP-0027\t30\tLedgerCloseMeta\t2048\tfalse\n"

// TestListText: each entry's id, CAP, protocol version, type, length and synthetic flag, as text.
func TestListText(t *testing.T) {
	entries, err := List(validCorpus)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteListText(&out, entries); err != nil {
		t.Fatal(err)
	}
	if out.String() != validListText {
		t.Errorf("text listing\n%q\nwant\n%q", out.String(), validListText)
	}
}

// TestListJSON: one ListOutput array, one ListEntry per catalogue entry in catalogue order, each with exactly the six
// fields copied from the entry with the same id; two-space indentation and one trailing newline.
func TestListJSON(t *testing.T) {
	entries, err := List(validCorpus)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteJSON(&out, entries); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.HasPrefix(s, "[\n  {\n    \"id\": \"cap-0040-signed-payload-signer\",\n    \"cap\": \"CAP-0040\",\n") ||
		!strings.HasSuffix(s, "  }\n]\n") {
		t.Errorf("layout of the JSON listing: %q", s)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("not one JSON array: %v", err)
	}
	cat, _ := ReadCatalogue(validCorpus)
	if len(items) != len(cat.Entries) {
		t.Fatalf("%d items for %d entries", len(items), len(cat.Entries))
	}
	for i, it := range items {
		if len(it) != 6 {
			t.Errorf("item %d has %d fields, want the six of ListEntry", i, len(it))
		}
		var le contract.ListEntry
		b, _ := json.Marshal(it)
		_ = json.Unmarshal(b, &le)
		e := cat.Entries[i]
		if le != (contract.ListEntry{ID: e.ID, Cap: e.Cap, ProtocolVersion: e.ProtocolVersion, XdrType: e.XdrType, Length: e.Length, Synthetic: e.Synthetic}) {
			t.Errorf("item %d %+v is not entry %s's", i, le, e.ID)
		}
	}
}

func TestListEdges(t *testing.T) {
	dir := t.TempDir()
	if err := WriteCatalogue(dir, contract.Catalogue{}); err != nil {
		t.Fatal(err)
	}
	entries, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var text, js bytes.Buffer
	_ = WriteListText(&text, entries)
	_ = WriteJSON(&js, entries)
	if text.String() != "id\tcap\tprotocolVersion\txdrType\tlength\tsynthetic\n" || js.String() != "[]\n" {
		t.Errorf("an empty catalogue: %q %q", text.String(), js.String())
	}
	// The lister does not validate: an invalid but readable catalogue lists what it holds.
	os.WriteFile(filepath.Join(dir, CatalogueFileName), []byte(`{"entries":[{"id":"x","synthetic":true}]}`), 0o644)
	if entries, err := List(dir); err != nil || len(entries) != 1 || entries[0].ID != "x" || !entries[0].Synthetic {
		t.Errorf("a readable, invalid catalogue: %+v %v", entries, err)
	}
	cases := map[string]string{
		filepath.Join(dir, "absent"): "does not exist",
		t.TempDir():                  "holds no catalogue",
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, CatalogueFileName), []byte(`{"entries":[{"length":"x"}]}`), 0o644)
	cases[bad] = "not a readable catalogue"
	for folder, contains := range cases {
		_, err := List(folder)
		var coded *model.CodedError
		if !errors.As(err, &coded) || coded.Code != contract.ErrorCodeUsageError || !strings.Contains(err.Error(), contains) {
			t.Errorf("List(%s): %v, want a usage error containing %q", folder, err, contains)
		}
	}
}
