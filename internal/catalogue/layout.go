package catalogue

// The Corpus layout reader/writer. A corpus folder holds:
//
//	<folder>/catalogue.json            the catalogue (Catalogue)
//	<folder>/catalogue.schema.json     the schema copy, byte-equal to schemas/catalogue.schema.json
//	<folder>/<entry id>/entry.xdr      per entry, its XDR file at CatalogueEntry.filePath
//	<folder>/<entry id>/entry.json     and its decoded JSON beside it, .json in place of the XDR file's extension
//
// Every reader of the layout (ltk list, ltk compare, ltk check, a reader in any language) and its one writer
// (ltk generate) go through this file. Paths in the catalogue use "/" only and are opened with filepath.FromSlash.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/schemas"
)

// The file names of a corpus folder's top level.
const (
	// CatalogueFileName is the catalogue's name in its corpus folder.
	CatalogueFileName = "catalogue.json"
	// SchemaCopyFileName is the schema copy's name beside the catalogue.
	SchemaCopyFileName = "catalogue.schema.json"
	// entryFileName is the XDR file's name inside its entry's folder.
	entryFileName = "entry.xdr"
)

// ErrNoFolder is returned when a corpus folder does not exist or is not a folder.
var ErrNoFolder = errors.New("the corpus folder does not exist")

// ErrNoCatalogue is returned when a corpus folder holds no catalogue file.
var ErrNoCatalogue = errors.New("the corpus folder holds no catalogue")

// EntryFilePath is the filePath the writer gives an entry: one folder per entry, named by its id, holding the XDR
// file.
func EntryFilePath(id string) string {
	return id + "/" + entryFileName
}

// DecodedJSONPath is the path of the decoded JSON file beside an XDR file at filePath: the same path with ".json" in
// place of the last segment's extension, or with ".json" added when it has none.
func DecodedJSONPath(filePath string) string {
	return strings.TrimSuffix(filePath, path.Ext(filePath)) + ".json"
}

// local is a catalogue path (forward slashes, relative to folder) as a path of this system.
func local(folder, slashPath string) string {
	return filepath.Join(folder, filepath.FromSlash(slashPath))
}

// CheckFolder returns ErrNoFolder, wrapped with the folder's name, unless folder is an existing folder.
func CheckFolder(folder string) error {
	st, err := os.Stat(folder)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%w: %s", ErrNoFolder, folder)
	}
	return nil
}

// ReadCatalogueFile returns the bytes of folder's catalogue file. A missing file is ErrNoCatalogue; a missing
// folder is ErrNoFolder.
func ReadCatalogueFile(folder string) ([]byte, error) {
	if err := CheckFolder(folder); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(folder, CatalogueFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s has no %s", ErrNoCatalogue, folder, CatalogueFileName)
	}
	return b, err
}

// ReadCatalogue reads folder's catalogue as a readable catalogue: one JSON object that decodes as a Catalogue (a
// field of another type, invalid JSON or a top-level value that is not an object makes it unreadable). It is
// not validated against the schema; that is ltk check's, and ltk list and ltk compare read the catalogue this way.
func ReadCatalogue(folder string) (contract.Catalogue, error) {
	var cat contract.Catalogue
	b, err := ReadCatalogueFile(folder)
	if err != nil {
		return cat, err
	}
	if !bytes.HasPrefix(bytes.TrimLeft(b, " \t\r\n"), []byte("{")) {
		return cat, fmt.Errorf("%s/%s is not a readable catalogue: it is not a JSON object", filepath.ToSlash(folder), CatalogueFileName)
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		return cat, fmt.Errorf("%s/%s is not a readable catalogue: %w", filepath.ToSlash(folder), CatalogueFileName, err)
	}
	return cat, nil
}

// ReadSchemaCopy returns the bytes of the schema copy beside folder's catalogue; a missing copy is an error wrapping
// fs.ErrNotExist.
func ReadSchemaCopy(folder string) ([]byte, error) {
	return os.ReadFile(filepath.Join(folder, SchemaCopyFileName))
}

// ReadEntryFile returns the bytes of an entry's XDR file, at filePath relative to folder.
func ReadEntryFile(folder, filePath string) ([]byte, error) {
	return os.ReadFile(local(folder, filePath))
}

// ReadDecodedJSON returns the bytes of the decoded JSON file beside the XDR file at filePath.
func ReadDecodedJSON(folder, filePath string) ([]byte, error) {
	return os.ReadFile(local(folder, DecodedJSONPath(filePath)))
}

// Measure returns the SHA-256 of b as 64 lowercase hex characters and its length in bytes, over b's exact bytes
// (the catalogue's sha256 and length fields).
func Measure(b []byte) (sha string, length int) {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), len(b)
}

// EncodeCatalogue renders cat as the catalogue file is written: encoding/json with two-space indentation, fields in
// the order of internal/contract, no HTML escaping, LF line endings and one trailing newline.
func EncodeCatalogue(cat contract.Catalogue) ([]byte, error) {
	if cat.Entries == nil {
		cat.Entries = []contract.CatalogueEntry{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cat); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteEntryFiles writes one entry's two files in one step: the XDR bytes at filePath, then the decoded JSON beside it,
// passed through json.Indent so the decoder's field order is kept. Each file is replaced whole. The XDR file is written
// first, so a failure of the second write leaves the XDR file and no catalogue record for it: the catalogue is written
// by WriteCatalogue after every entry, and a second run rewrites both files whole.
func WriteEntryFiles(folder, filePath string, xdrBytes, decodedJSON []byte) error {
	var indented bytes.Buffer
	if err := json.Indent(&indented, decodedJSON, "", "  "); err != nil {
		return fmt.Errorf("the decoded JSON of %s is not JSON: %w", filePath, err)
	}
	indented.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(local(folder, filePath)), 0o755); err != nil {
		return fmt.Errorf("create the folder of %s: %w", filePath, err)
	}
	if err := writeWhole(local(folder, filePath), xdrBytes); err != nil {
		return err
	}
	return writeWhole(local(folder, DecodedJSONPath(filePath)), indented.Bytes())
}

// WriteCatalogue writes folder's schema copy from the embedded schema, then its catalogue. Each file is replaced whole.
// The catalogue is the last file a generation writes, so it never names a file that was not written; a failure of the
// catalogue's write leaves the schema copy (always byte-equal to the source) and the previous catalogue, if any,
// unchanged.
func WriteCatalogue(folder string, cat contract.Catalogue) error {
	b, err := EncodeCatalogue(cat)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return fmt.Errorf("create the corpus folder %s: %w", filepath.ToSlash(folder), err)
	}
	if err := writeWhole(filepath.Join(folder, SchemaCopyFileName), schemas.CatalogueSchema()); err != nil {
		return err
	}
	return writeWhole(filepath.Join(folder, CatalogueFileName), b)
}

// writeWhole replaces the file at name with b: it writes a temporary file beside it and renames it into place, so a
// reader never sees half a file. Something that is not a regular file at name (a folder) fails the write.
func writeWhole(name string, b []byte) error {
	if st, err := os.Lstat(name); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("write %s: something other than a file is in its place", filepath.ToSlash(name))
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.ToSlash(name), err)
	}
	_, werr := tmp.Write(b)
	merr := tmp.Chmod(0o644) // a corpus file is read by anyone, as a committed file is
	cerr := tmp.Close()
	if err := errors.Join(werr, merr, cerr); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", filepath.ToSlash(name), err)
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", filepath.ToSlash(name), err)
	}
	return nil
}
