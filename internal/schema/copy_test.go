package schema

// The Schema copy consistency test: schemas/catalogue.schema.json, the one source, and the copy beside a corpus
// folder's catalogue are byte-equal. It runs on the fixture corpora under testdata/ and holds the committed corpus/ the
// same way.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/schemas"
)

// sourceSchema is schemas/catalogue.schema.json as the repository holds it.
func sourceSchema(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../schemas/catalogue.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSchemaCopiesEqualTheSource: every corpus folder under testdata/ but the one
// built to differ carries a copy byte-equal to schemas/, and the check's comparison agrees.
func TestSchemaCopiesEqualTheSource(t *testing.T) {
	src := sourceSchema(t)
	if !bytes.Equal(src, schemas.CatalogueSchema()) {
		t.Fatal("the embedded schema is not schemas/catalogue.schema.json")
	}
	copies, err := filepath.Glob(filepath.Join(testdata, "*", "*", catalogue.SchemaCopyFileName))
	if err != nil {
		t.Fatal(err)
	}
	pairs, _ := filepath.Glob(filepath.Join(testdata, "compare", "*", "*", catalogue.SchemaCopyFileName))
	copies = append(copies, pairs...)
	if len(copies) < 8 {
		t.Fatalf("found %d schema copies under testdata/: %v", len(copies), copies)
	}
	for _, cp := range copies {
		folder := filepath.Dir(cp)
		b, err := os.ReadFile(cp)
		if err != nil {
			t.Fatal(err)
		}
		differs := filepath.Base(folder) == "schema-copy"
		if bytes.Equal(b, src) == differs {
			t.Errorf("%s: equal to the source %v", filepath.ToSlash(cp), !differs)
		}
		if f := CompareSchemaCopy(folder); (f != nil) != differs {
			t.Errorf("%s: the check's comparison reports %+v", filepath.ToSlash(folder), f)
		}
	}
}

// TestCommittedCorpusSchemaCopy holds corpus/'s copy to the source.
func TestCommittedCorpusSchemaCopy(t *testing.T) {
	const committed = "../../corpus"
	b, err := os.ReadFile(filepath.Join(committed, catalogue.SchemaCopyFileName))
	if os.IsNotExist(err) {
		t.Skip("corpus/ is absent: no copy to hold")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, sourceSchema(t)) {
		t.Error("corpus/catalogue.schema.json differs from schemas/catalogue.schema.json")
	}
}

// TestSchemaCopyDivergence: copies that diverge from the source, by one byte, by one value, by a line
// ending or by a missing trailing newline, are each found unequal; the comparison never rewrites the copy.
func TestSchemaCopyDivergence(t *testing.T) {
	src := sourceSchema(t)
	variants := map[string][]byte{
		"one byte":            append(append([]byte{}, src[:10]...), append([]byte{src[10] ^ 0x20}, src[11:]...)...),
		"one value":           bytes.Replace(src, []byte(`"minItems": 1`), []byte(`"minItems": 2`), 1),
		"CRLF line endings":   bytes.ReplaceAll(src, []byte("\n"), []byte("\r\n")),
		"no trailing newline": bytes.TrimSuffix(src, []byte("\n")),
		"empty":               {},
	}
	for name, v := range variants {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cp := filepath.Join(dir, catalogue.SchemaCopyFileName)
			if err := os.WriteFile(cp, v, 0o644); err != nil {
				t.Fatal(err)
			}
			f := CompareSchemaCopy(dir)
			if f == nil || f.Pointer != "" {
				t.Fatalf("a diverging copy is reported as %+v", f)
			}
			after, _ := os.ReadFile(cp)
			if !bytes.Equal(after, v) {
				t.Error("the comparison rewrote the copy")
			}
		})
	}
}
