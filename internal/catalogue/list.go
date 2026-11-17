package catalogue

// The Corpus lister of `ltk list`. A view over a readable catalogue: no validation, no decoding, no container.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// List reads folder's catalogue and returns one ListEntry per catalogue entry, in catalogue order, each copied from
// the entry with the same id. A folder that does not exist or holds no readable catalogue is a usage error (exit 2);
// the listing never fails a check (exit 1) and needs no container (exit 3).
func List(folder string) (contract.ListOutput, error) {
	cat, err := ReadCatalogue(folder)
	switch {
	case errors.Is(err, ErrNoFolder):
		return nil, model.Usage("folder '%s' does not exist", folder)
	case errors.Is(err, ErrNoCatalogue):
		return nil, model.Usage("folder '%s' holds no catalogue (%s)", folder, CatalogueFileName)
	case err != nil:
		return nil, model.Usage("%v", err)
	}
	out := contract.ListOutput{}
	for _, e := range cat.Entries {
		out = append(out, contract.ListEntry{
			ID:              e.ID,
			Cap:             e.Cap,
			ProtocolVersion: e.ProtocolVersion,
			XdrType:         e.XdrType,
			Length:          e.Length,
			Synthetic:       e.Synthetic,
		})
	}
	return out, nil
}

// listHeader is the text form's first line: the ListEntry field names, in the record's order.
var listHeader = []string{"id", "cap", "protocolVersion", "xdrType", "length", "synthetic"}

// WriteListText writes the text form of `ltk list` on standard output: a header of
// the ListEntry field names, then one line per entry; fields separated by one tab, in the order id, cap,
// protocolVersion, xdrType, length, synthetic; synthetic as true or false; every line ends with LF. The whole listing
// is one write.
func WriteListText(w io.Writer, entries contract.ListOutput) error {
	var buf bytes.Buffer
	buf.WriteString(strings.Join(listHeader, "\t") + "\n")
	for _, e := range entries {
		buf.WriteString(strings.Join([]string{e.ID, e.Cap, strconv.Itoa(e.ProtocolVersion), e.XdrType,
			strconv.Itoa(e.Length), strconv.FormatBool(e.Synthetic)}, "\t") + "\n")
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// WriteJSON writes v as the commands' JSON documents are written: two-space indentation, no HTML escaping, one
// trailing newline, in one write.
func WriteJSON(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
