package orchestrator

// The file-format check of the module's committed files, asserted from outside the packages that write and read
// them:
//
//   - every JSON Schema block of the data model's document, when present beside the module, is written exactly to its
//     file, the catalogue's to
//     schemas/catalogue.schema.json and each other format's to internal/contract/testdata/<format>.schema.json;
//   - schemas/ holds only the catalogue's schema and its embedding file;
//   - every catalogue under testdata/ validates against the catalogue schema, but schema-pointer, which fails at the
//     two pointers it was built to break and nowhere else;
//   - every schema copy under testdata/ is byte-equal to schemas/catalogue.schema.json, but schema-copy's, which
//     differs;
//   - every decoded JSON sample under testdata/ is one strict JSON document (the decoded JSON is the Rust XDR tool's
//     output and has no schema of its own).

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// reSchemaBlockHeading is a JSON Schema heading of the data model: "### JSON Schema — `<path>`".
var reSchemaBlockHeading = regexp.MustCompile("(?m)^### JSON Schema — `([^`]+)`[ \t]*$")

// dataModelSchemaBlocks returns each JSON Schema block of the data model's document by the file path its heading names,
// each block's text from the line after its ```json fence through the newline before the closing fence.
func dataModelSchemaBlocks(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(dataModelDocPath)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s is absent (the exported repository does not carry it): no data model to hold the files to", dataModelDocPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if strings.Contains(doc, "\r") {
		t.Fatalf("%s holds a carriage return; the data model is written with LF", dataModelDocPath)
	}
	blocks := map[string]string{}
	locs := reSchemaBlockHeading.FindAllStringSubmatchIndex(doc, -1)
	for i, loc := range locs {
		path := doc[loc[2]:loc[3]]
		section := doc[loc[1]:]
		if i+1 < len(locs) {
			section = doc[loc[1]:locs[i+1][0]]
		}
		const fence = "\n```json\n"
		open := strings.Index(section, fence)
		if open < 0 {
			t.Fatalf("%s: the heading of %s has no ```json block", dataModelDocPath, path)
		}
		body := section[open+len(fence):]
		end := strings.Index(body, "\n```\n")
		if end < 0 {
			t.Fatalf("%s: the block of %s is not closed", dataModelDocPath, path)
		}
		if strings.Contains(body[:end], "```") {
			t.Fatalf("%s: the block of %s holds another fence", dataModelDocPath, path)
		}
		if _, dup := blocks[path]; dup {
			t.Fatalf("%s: two blocks name %s", dataModelDocPath, path)
		}
		blocks[path] = body[:end+1]
	}
	return blocks
}

// TestPhase2SchemaBlocksAreTheirFiles: the data model holds eight JSON Schema blocks, the catalogue's under schemas/
// and the seven output documents' under internal/contract/testdata/, and each file is its block, byte for byte (the
// block's text ends with its newline). internal/contract/testdata holds no other schema file.
func TestPhase2SchemaBlocksAreTheirFiles(t *testing.T) {
	blocks := dataModelSchemaBlocks(t)
	want := []string{
		"internal/contract/testdata/check-report.schema.json",
		"internal/contract/testdata/compare-report.schema.json",
		"internal/contract/testdata/error-line.schema.json",
		"internal/contract/testdata/generate-report.schema.json",
		"internal/contract/testdata/js-sdk-1766-report.schema.json",
		"internal/contract/testdata/list-output.schema.json",
		"internal/contract/testdata/wallet-backend-729-report.schema.json",
		"schemas/catalogue.schema.json",
	}
	var paths []string
	for p := range blocks {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if !slices.Equal(paths, want) {
		t.Fatalf("the data model's JSON Schema blocks name %v, want %v", paths, want)
	}
	for _, p := range paths {
		got, err := os.ReadFile(filepath.Join(moduleRoot, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if string(got) != blocks[p] {
			t.Errorf("%s is not its block of %s byte for byte (%d bytes, the block %d)", p, dataModelDocPath, len(got), len(blocks[p]))
		}
		var v map[string]any
		if err := json.Unmarshal(got, &v); err != nil || v["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s is not one JSON Schema of draft 2020-12: %v", p, err)
		}
	}
	files, err := filepath.Glob(filepath.Join(outputSchemaFolder, "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(want)-1 {
		t.Errorf("internal/contract/testdata holds %d schema files, the data model %d output schemas", len(files), len(want)-1)
	}
}

// TestPhase2SchemasFolderHoldsOnlyTheCatalogueSchema: schemas/ holds catalogue.schema.json and its embedding file,
// nothing else; the output documents' schemas are test fixtures under internal/.
func TestPhase2SchemasFolderHoldsOnlyTheCatalogueSchema(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(moduleRoot, "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"catalogue.schema.json", "embed.go"}) {
		t.Errorf("schemas/ holds %v, want [catalogue.schema.json embed.go]", names)
	}
}

// testdataFiles lists the files under testdata/ named name, as slash paths relative to testdata/.
func testdataFiles(t *testing.T, name string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(testdataFolder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			rel, err := filepath.Rel(testdataFolder, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// leafLocations are the instance locations of the leaves of a validation error, each with its keyword path.
func leafLocations(err *jsonschema.ValidationError, out map[string][]string) {
	if len(err.Causes) == 0 {
		loc := "/" + strings.Join(err.InstanceLocation, "/")
		if len(err.InstanceLocation) == 0 {
			loc = ""
		}
		out[loc] = append(out[loc], err.Error())
		return
	}
	for _, c := range err.Causes {
		leafLocations(c, out)
	}
}

// TestPhase2TestdataCataloguesValidate: every catalogue.json under testdata/ (eleven: the seven fixture corpora
// and the four folders of the two compare pairs) validates against schemas/catalogue.schema.json, but corpus/schema-pointer's, which is
// broken on purpose and fails at exactly its two pointers: a string at /entries/0/length and the required sha256
// missing from /entries/1 (internal/catalogue/fixtures_test.go's table of the fixtures).
func TestPhase2TestdataCataloguesValidate(t *testing.T) {
	cats := testdataFiles(t, "catalogue.json")
	if len(cats) != 11 {
		t.Fatalf("testdata/ holds %d catalogues, want 11 (7 corpora, 2 compare pairs): %v", len(cats), cats)
	}
	sch := compileSchemaFile(t, catalogueSchemaPath)
	for _, c := range cats {
		b, err := os.ReadFile(filepath.Join(testdataFolder, filepath.FromSlash(c)))
		if err != nil {
			t.Fatal(err)
		}
		verr := validationError(t, sch, b)
		if c != "corpus/schema-pointer/catalogue.json" {
			if verr != nil {
				t.Errorf("%s does not validate against the catalogue schema: %v", c, verr)
			}
			continue
		}
		var ve *jsonschema.ValidationError
		if !errors.As(verr, &ve) {
			t.Fatalf("%s validates (%v); it is broken on purpose at /entries/0/length and /entries/1/sha256", c, verr)
		}
		leaves := map[string][]string{}
		leafLocations(ve, leaves)
		var locs []string
		for l := range leaves {
			locs = append(locs, l)
		}
		sort.Strings(locs)
		if !slices.Equal(locs, []string{"/entries/0/length", "/entries/1"}) {
			t.Errorf("%s fails at %v, want [/entries/0/length /entries/1]: %v", c, locs, leaves)
		}
		if m := strings.Join(leaves["/entries/1"], " "); !strings.Contains(m, "sha256") {
			t.Errorf("%s at /entries/1: %q, want the missing sha256", c, m)
		}
	}
}

// TestPhase2TestdataSchemaCopiesEqualTheSource: every catalogue.schema.json under
// testdata/ is byte-equal to schemas/catalogue.schema.json, but corpus/schema-copy's, which differs from it.
func TestPhase2TestdataSchemaCopiesEqualTheSource(t *testing.T) {
	src, err := os.ReadFile(catalogueSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	copies := testdataFiles(t, "catalogue.schema.json")
	if len(copies) != 11 {
		t.Fatalf("testdata/ holds %d schema copies, want 11: %v", len(copies), copies)
	}
	for _, c := range copies {
		b, err := os.ReadFile(filepath.Join(testdataFolder, filepath.FromSlash(c)))
		if err != nil {
			t.Fatal(err)
		}
		differs := c == "corpus/schema-copy/catalogue.schema.json"
		if bytes.Equal(b, src) == differs {
			t.Errorf("%s: byte-equal to schemas/catalogue.schema.json is %v, want %v", c, !differs, !differs)
		}
	}
}

// TestPhase2TestdataDecodedJSONIsStrictJSON: every decoded JSON sample under testdata/ (each entry's entry.json and
// the real captures' decodings) is one strict JSON document, LF-only, with no BOM.
func TestPhase2TestdataDecodedJSONIsStrictJSON(t *testing.T) {
	n := 0
	err := filepath.WalkDir(testdataFolder, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".json" || strings.HasSuffix(p, ".schema.json") || d.Name() == "catalogue.json" {
			return err
		}
		n++
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) || bytes.Contains(b, []byte("\r")) || !json.Valid(b) {
			t.Errorf("%s is not one strict JSON document written with LF and no BOM", filepath.ToSlash(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 8 {
		t.Errorf("found %d decoded JSON samples under testdata/", n)
	}
}
