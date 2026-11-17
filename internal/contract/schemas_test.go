package contract

// The output documents' JSON Schemas are test fixtures under testdata/, written exactly as the schema blocks of the
// data model of record. When that document is present beside the module, this test holds each fixture to its block.
// The tests of the commands validate their outputs against these fixtures.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

const dataModelPath = "../../specs/data-model.md"

var (
	reSchemaHeading = regexp.MustCompile("(?m)^### JSON Schema — `internal/contract/testdata/([^`]+)`\\s*$")
	reSchemaFile    = regexp.MustCompile("(?m)^\\*\\*Schema file:\\*\\* `internal/contract/testdata/([^`]+)`")
)

// schemaBlocks returns each output document's schema block of the data model, by its fixture's file name.
func schemaBlocks(t *testing.T, doc string) map[string]string {
	t.Helper()
	blocks := map[string]string{}
	for _, loc := range reSchemaHeading.FindAllStringSubmatchIndex(doc, -1) {
		name := doc[loc[2]:loc[3]]
		rest := doc[loc[1]:]
		open := strings.Index(rest, "```json\n")
		if open < 0 {
			t.Fatalf("%s: the heading of %s has no ```json block", dataModelPath, name)
		}
		body := rest[open+len("```json\n"):]
		end := strings.Index(body, "\n```")
		if end < 0 {
			t.Fatalf("%s: the block of %s is not closed", dataModelPath, name)
		}
		if _, dup := blocks[name]; dup {
			t.Fatalf("%s: two blocks for %s", dataModelPath, name)
		}
		blocks[name] = body[:end+1]
	}
	return blocks
}

func normalize(b string) string {
	return strings.TrimRight(strings.ReplaceAll(b, "\r\n", "\n"), "\n")
}

// TestOutputSchemaFixturesMatchDataModel: each fixture is byte-equal, modulo the trailing newline, to its block in the
// data model of record; every block has a fixture and every fixture a block; each section's "Schema file" line names
// a block.
func TestOutputSchemaFixturesMatchDataModel(t *testing.T) {
	b, err := os.ReadFile(dataModelPath)
	if os.IsNotExist(err) {
		t.Skipf("%s is absent (it is not exported with the repository): no data model to hold the fixtures to", dataModelPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(b), "\r\n", "\n")
	blocks := schemaBlocks(t, doc)
	want := []string{"check-report.schema.json", "compare-report.schema.json", "error-line.schema.json",
		"generate-report.schema.json", "js-sdk-1766-report.schema.json", "list-output.schema.json",
		"wallet-backend-729-report.schema.json"}
	var names []string
	for n := range blocks {
		names = append(names, n)
	}
	sort.Strings(names)
	if !slices.Equal(names, want) {
		t.Fatalf("the data model's output schema blocks are %v, want %v", names, want)
	}
	for _, m := range reSchemaFile.FindAllStringSubmatch(doc, -1) {
		if _, ok := blocks[m[1]]; !ok {
			t.Errorf("the data model's \"Schema file\" line names %s, which has no JSON Schema block", m[1])
		}
	}
	files, err := filepath.Glob(filepath.Join("testdata", "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []string
	for _, f := range files {
		fixtures = append(fixtures, filepath.Base(f))
	}
	sort.Strings(fixtures)
	if !slices.Equal(fixtures, want) {
		t.Errorf("testdata holds %v, want exactly %v", fixtures, want)
	}
	for _, n := range names {
		got, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if normalize(string(got)) != normalize(blocks[n]) {
			t.Errorf("testdata/%s differs from its block in %s; rewrite it from the block", n, dataModelPath)
		}
	}
}

// TestOutputSchemaFixturesAreDraft202012 runs without the data model: each fixture is one JSON object of draft 2020-12
// that forbids extra fields at its top level.
func TestOutputSchemaFixturesAreDraft202012(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 7 {
		t.Fatalf("testdata holds %d schema fixtures, want 7", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Schema     string          `json:"$schema"`
			Type       string          `json:"type"`
			Additional json.RawMessage `json:"additionalProperties"`
		}
		if err := json.Unmarshal(b, &s); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if s.Schema != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s: $schema %q", f, s.Schema)
		}
		if s.Type == "object" && string(s.Additional) != "false" {
			t.Errorf("%s: a top-level object must set additionalProperties false", f)
		}
	}
}
