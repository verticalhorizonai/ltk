// Package schemas holds the one source of the catalogue's JSON Schema, catalogue.schema.json (draft 2020-12), and
// embeds it. The catalogue validator compiles these bytes and the layout writer copies them beside every catalogue it
// writes, so a corpus folder's copy is byte-equal to this source by construction.
package schemas

import (
	"bytes"
	_ "embed" // the schema file is embedded, so the program carries its one source
)

//go:embed catalogue.schema.json
var catalogueSchema []byte

// CatalogueSchema returns the bytes of schemas/catalogue.schema.json, the catalogue's JSON Schema (draft 2020-12),
// exactly as the file holds them. Each call returns its own copy, so no caller can alter the source.
func CatalogueSchema() []byte {
	return bytes.Clone(catalogueSchema)
}
