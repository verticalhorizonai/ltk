package catalogue

// The Corpus layout reader/writer and the fixture corpus's language-neutral layout. The "without the tool" tests read
// the fixture with the standard library only (os, crypto/sha256, encoding/json), as a reader in any language would.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/schemas"
)

const validCorpus = testdataRoot + "/corpus/valid"

// rawCatalogue reads a catalogue with encoding/json alone: each entry as a map of its fields.
func rawCatalogue(t *testing.T, folder string) []map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(folder, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Entries
}

// resolve is an RFC 6901 resolution written for the test, independent of the package: what a reader in another
// language does with a pointer.
func resolve(t *testing.T, doc any, pointer string) (any, bool) {
	t.Helper()
	if pointer == "" {
		return doc, true
	}
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch v := doc.(type) {
		case map[string]any:
			x, ok := v[tok]
			if !ok {
				return nil, false
			}
			doc = x
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			doc = v[i]
		default:
			return nil, false
		}
	}
	return doc, true
}

// TestBrowseCorpusFolder: in the corpus folder, without ltk, the catalogue and its schema copy are at the top, and
// every entry's XDR file is at its filePath with its decoded JSON beside it.
func TestBrowseCorpusFolder(t *testing.T) {
	for _, name := range []string{"catalogue.json", "catalogue.schema.json"} {
		if st, err := os.Stat(filepath.Join(validCorpus, name)); err != nil || !st.Mode().IsRegular() {
			t.Errorf("%s is not at the top of the corpus folder: %v", name, err)
		}
	}
	entries := rawCatalogue(t, validCorpus)
	if len(entries) != 3 {
		t.Fatalf("%d entries, want the three seeds", len(entries))
	}
	for _, e := range entries {
		var fp string
		if err := json.Unmarshal(e["filePath"], &fp); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(fp, `\`) || strings.HasPrefix(fp, "/") {
			t.Errorf("filePath %q: relative, with / as its only separator", fp)
		}
		ext := filepath.Ext(fp)
		for _, p := range []string{fp, strings.TrimSuffix(fp, ext) + ".json"} {
			if st, err := os.Stat(filepath.Join(validCorpus, filepath.FromSlash(p))); err != nil || !st.Mode().IsRegular() {
				t.Errorf("%s is not found in the corpus folder: %v", p, err)
			}
		}
	}
}

// TestReadDecodedJSONWithoutTheTool: an entry's decoded JSON opens with any JSON parser, and every expected value of
// both groups is found at its pointer, equal to the recorded value.
func TestReadDecodedJSONWithoutTheTool(t *testing.T) {
	for _, e := range rawCatalogue(t, validCorpus) {
		var fp string
		var ev contract.ExpectedDecodedValues
		_ = json.Unmarshal(e["filePath"], &fp)
		if err := json.Unmarshal(e["expectedDecodedValues"], &ev); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(validCorpus, filepath.FromSlash(strings.TrimSuffix(fp, ".xdr")+".json")))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: the decoded JSON is not JSON: %v", fp, err)
		}
		check := func(pointer string, recorded json.RawMessage) {
			got, ok := resolve(t, doc, pointer)
			var want any
			_ = json.Unmarshal(recorded, &want)
			if !ok {
				t.Errorf("%s: %s resolves to nothing", fp, pointer)
			} else if gb, wb := mustJSON(t, got), mustJSON(t, want); gb != wb {
				t.Errorf("%s: %s is %s, the catalogue records %s", fp, pointer, gb, wb)
			}
		}
		if len(ev.ReproducedByEveryRun) == 0 || len(ev.LedgerDependent) == 0 {
			t.Errorf("%s: both groups are expected to hold values", fp)
		}
		for _, v := range ev.ReproducedByEveryRun {
			check(v.Pointer, v.Value)
		}
		for _, v := range ev.LedgerDependent {
			check(v.Pointer, v.Value)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestReadXDRFileWithoutTheTool: the captured bytes open as a plain file; their SHA-256 in lowercase hex and their
// length are the catalogue's.
func TestReadXDRFileWithoutTheTool(t *testing.T) {
	for _, e := range rawCatalogue(t, validCorpus) {
		var fp, sha string
		var length int
		_ = json.Unmarshal(e["filePath"], &fp)
		_ = json.Unmarshal(e["sha256"], &sha)
		_ = json.Unmarshal(e["length"], &length)
		b, err := os.ReadFile(filepath.Join(validCorpus, filepath.FromSlash(fp)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != sha || len(b) != length {
			t.Errorf("%s: sha256 %x length %d; the catalogue records %s and %d", fp, sum, len(b), sha, length)
		}
	}
}

// TestReadCatalogueFields: every entry holds its fields (id, cap, protocolVersion, source, synthetic,
// expectedDecodedValues, filePath, xdrType, sha256, length and protocolFallback), each readable through the layout
// reader as its contract type; entry 2 alone carries networkRefusalEvidence, holding submittedEnvelopeXdr and the
// resultCode CHANGE_TRUST_MALFORMED.
func TestReadCatalogueFields(t *testing.T) {
	cat, err := ReadCatalogue(validCorpus)
	if err != nil {
		t.Fatal(err)
	}
	raw := rawCatalogue(t, validCorpus)
	required := []string{"id", "cap", "protocolVersion", "protocolFallback", "source", "synthetic", "expectedDecodedValues",
		"filePath", "xdrType", "sha256", "length"}
	for i, e := range cat.Entries {
		for _, f := range required {
			if _, ok := raw[i][f]; !ok {
				t.Errorf("entry %d: field %s is not in the catalogue", i, f)
			}
		}
		if e.ID == "" || e.Cap == "" || e.ProtocolVersion < 1 || e.Source == "" || e.FilePath == "" || e.XdrType == "" ||
			len(e.Sha256) != 64 || e.Length <= 0 || e.ExpectedDecodedValues.ReproducedByEveryRun == nil ||
			e.ExpectedDecodedValues.LedgerDependent == nil {
			t.Errorf("entry %d: a field did not read: %+v", i, e)
		}
		seed, ok := model.SeedByID(e.ID)
		if !ok || seed.Cap != e.Cap || seed.XdrType != e.XdrType || seed.Synthetic != e.Synthetic {
			t.Errorf("entry %d: %s %s %s %v is not a seed's identity", i, e.ID, e.Cap, e.XdrType, e.Synthetic)
		}
		_, has := raw[i]["networkRefusalEvidence"]
		if has != (e.ID == model.SeedPoolShareTrustline) || has != (e.NetworkRefusalEvidence != nil) {
			t.Errorf("entry %s: networkRefusalEvidence present %v; only entry 2 carries it", e.ID, has)
		}
		if e.ID == model.SeedPoolShareTrustline {
			ev := e.NetworkRefusalEvidence
			if ev.SubmittedEnvelopeXdr == "" || ev.ResultCode != contract.NetworkRefusalResultCodeChangeTrustMalformed {
				t.Errorf("entry 2's evidence: %+v", ev)
			}
			if !strings.Contains(string(raw[i]["networkRefusalEvidence"]), `"resultCode": "CHANGE_TRUST_MALFORMED"`) {
				t.Errorf("entry 2's evidence as written: %s", raw[i]["networkRefusalEvidence"])
			}
		}
	}
	if len(cat.Entries) != 3 || cat.Entries[0].ID != model.SeedSignedPayloadSigner || cat.Entries[1].ID != model.SeedPoolShareTrustline ||
		cat.Entries[2].ID != model.SeedMuxedAccount {
		t.Errorf("the fixture catalogue's entries are not the three seeds in seed order: %d", len(cat.Entries))
	}
}

func TestReadCatalogueErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCatalogue(filepath.Join(dir, "absent")); !errors.Is(err, ErrNoFolder) {
		t.Errorf("an absent folder: %v", err)
	}
	if _, err := ReadCatalogue(file); !errors.Is(err, ErrNoFolder) {
		t.Errorf("a file given as the folder: %v", err)
	}
	if _, err := ReadCatalogue(dir); !errors.Is(err, ErrNoCatalogue) {
		t.Errorf("a folder with no catalogue: %v", err)
	}
	for _, body := range []string{`not json`, `[]`, `null`, `{"entries": 3}`, `{"entries": [{"length": "x"}]}`, "\xEF\xBB\xBF{}"} {
		if err := os.WriteFile(filepath.Join(dir, "catalogue.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ReadCatalogue(dir)
		if err == nil || errors.Is(err, ErrNoCatalogue) || errors.Is(err, ErrNoFolder) || !strings.Contains(err.Error(), "not a readable catalogue") {
			t.Errorf("catalogue %q: %v, want an unreadable catalogue", body, err)
		}
	}
	// Readable though not valid: extra fields and missing ones read; validation is ltk check's.
	if err := os.WriteFile(filepath.Join(dir, "catalogue.json"), []byte(`{"entries":[{"id":"x","extra":1}],"more":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cat, err := ReadCatalogue(dir); err != nil || len(cat.Entries) != 1 || cat.Entries[0].ID != "x" {
		t.Errorf("a readable catalogue: %+v %v", cat, err)
	}
}

func TestPaths(t *testing.T) {
	if got := EntryFilePath("cap-0040-signed-payload-signer"); got != "cap-0040-signed-payload-signer/entry.xdr" {
		t.Errorf("EntryFilePath: %q", got)
	}
	for in, want := range map[string]string{
		"e/entry.xdr":    "e/entry.json",
		"entry.xdr":      "entry.json",
		"a.b/c":          "a.b/c.json",
		"a/b.c.xdr":      "a/b.c.json",
		"deep/x/lcm.bin": "deep/x/lcm.json",
	} {
		if got := DecodedJSONPath(in); got != want {
			t.Errorf("DecodedJSONPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEncodeCatalogue: two-space indentation, the fields in the order of internal/contract, LF only, one trailing
// newline, no HTML escaping, and [] for an empty list.
func TestEncodeCatalogue(t *testing.T) {
	b, err := EncodeCatalogue(contract.Catalogue{Entries: []contract.CatalogueEntry{{
		ID: "e<1>", Cap: "CAP-0040", ProtocolVersion: 30, Source: "a & b", FilePath: "e/entry.xdr", XdrType: "LedgerCloseMeta",
		Sha256: strings.Repeat("a", 64), Length: 3,
		ExpectedDecodedValues: contract.ExpectedDecodedValues{ReproducedByEveryRun: []contract.ReproducedValue{}, LedgerDependent: []contract.LedgerDependentValue{}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "{\n  \"entries\": [\n    {\n      \"id\": \"e<1>\",\n      \"cap\"") {
		t.Errorf("indentation or order: %q", s[:min(len(s), 80)])
	}
	if strings.Contains(s, "\r") || !strings.HasSuffix(s, "}\n") || strings.HasSuffix(s, "\n\n") {
		t.Errorf("line endings: %q", s)
	}
	if !strings.Contains(s, `"source": "a & b"`) {
		t.Error("HTML characters must be written as they are")
	}
	if strings.Contains(s, "networkRefusalEvidence") {
		t.Error("an absent networkRefusalEvidence must be omitted")
	}
	order := []string{`"id"`, `"cap"`, `"protocolVersion"`, `"protocolFallback"`, `"source"`, `"synthetic"`,
		`"expectedDecodedValues"`, `"filePath"`, `"xdrType"`, `"sha256"`, `"length"`}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k)
		if i < last {
			t.Errorf("%s is out of the struct order", k)
		}
		last = i
	}
	if b, _ := EncodeCatalogue(contract.Catalogue{}); string(b) != "{\n  \"entries\": []\n}\n" {
		t.Errorf("an empty catalogue: %q", b)
	}
}

// TestWriteEntryFiles: the XDR file at its filePath (forward slashes, opened through FromSlash), its decoded JSON
// beside it through json.Indent with the decoder's key order kept, each replaced whole on a second write.
func TestWriteEntryFiles(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEntryFiles(dir, "e1/entry.xdr", []byte{0, 1, 2}, []byte(`{"z":1,"a":{"y":[1,2]}}`)); err != nil {
		t.Fatal(err)
	}
	x, _ := os.ReadFile(filepath.Join(dir, "e1", "entry.xdr"))
	j, _ := os.ReadFile(filepath.Join(dir, "e1", "entry.json"))
	if !bytes.Equal(x, []byte{0, 1, 2}) {
		t.Errorf("XDR file %v", x)
	}
	want := "{\n  \"z\": 1,\n  \"a\": {\n    \"y\": [\n      1,\n      2\n    ]\n  }\n}\n"
	if string(j) != want {
		t.Errorf("decoded JSON %q, want %q", j, want)
	}
	if err := WriteEntryFiles(dir, "e1/entry.xdr", []byte{9}, []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	x, _ = os.ReadFile(filepath.Join(dir, "e1", "entry.xdr"))
	j, _ = os.ReadFile(filepath.Join(dir, "e1", "entry.json"))
	if !bytes.Equal(x, []byte{9}) || string(j) != "[]\n" {
		t.Errorf("a second write replaces both files whole: %v %q", x, j)
	}
	if err := WriteEntryFiles(dir, "e2/entry.xdr", []byte{1}, []byte(`{not json`)); err == nil {
		t.Error("decoded JSON that is not JSON must fail")
	} else if _, err := os.Stat(filepath.Join(dir, "e2", "entry.xdr")); err == nil {
		t.Error("nothing is written when the decoded JSON is not JSON")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "e1", ".*tmp*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left: %v", leftovers)
	}
}

// TestWriteEntryFilesSecondWriteFails: a folder in the decoded JSON's place makes the second of the entry's two writes
// fail, as root too. The XDR file is written, the error names the JSON file; a second run with the obstacle in place
// fails the same way and leaves the XDR file as the first wrote it; a second run once it is gone writes both.
func TestWriteEntryFilesSecondWriteFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "e1", "entry.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		err := WriteEntryFiles(dir, "e1/entry.xdr", []byte{7, 7}, []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "entry.json") {
			t.Fatalf("run %d: %v, want the JSON file's write to fail naming it", run, err)
		}
		if x, err := os.ReadFile(filepath.Join(dir, "e1", "entry.xdr")); err != nil || !bytes.Equal(x, []byte{7, 7}) {
			t.Errorf("run %d: the XDR file %v %v", run, x, err)
		}
	}
	if err := os.Remove(filepath.Join(dir, "e1", "entry.json")); err != nil {
		t.Fatal(err)
	}
	if err := WriteEntryFiles(dir, "e1/entry.xdr", []byte{7, 7}, []byte(`{}`)); err != nil {
		t.Fatalf("the second run once the obstacle is gone: %v", err)
	}
	if j, _ := os.ReadFile(filepath.Join(dir, "e1", "entry.json")); string(j) != "{}\n" {
		t.Errorf("decoded JSON %q", j)
	}
}

// TestWriteCatalogue: the schema copy is the embedded schema byte for byte, and the catalogue is EncodeCatalogue's.
func TestWriteCatalogue(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "made", "by", "the", "writer")
	cat := contract.Catalogue{Entries: []contract.CatalogueEntry{{ID: "x"}}}
	if err := WriteCatalogue(dir, cat); err != nil {
		t.Fatal(err)
	}
	cp, _ := os.ReadFile(filepath.Join(dir, SchemaCopyFileName))
	if !bytes.Equal(cp, schemas.CatalogueSchema()) {
		t.Error("the schema copy differs from schemas/catalogue.schema.json")
	}
	got, _ := os.ReadFile(filepath.Join(dir, CatalogueFileName))
	want, _ := EncodeCatalogue(cat)
	if !bytes.Equal(got, want) {
		t.Errorf("catalogue %q", got)
	}
	if read, err := ReadCatalogue(dir); err != nil || len(read.Entries) != 1 || read.Entries[0].ID != "x" {
		t.Errorf("read back: %+v %v", read, err)
	}
}

// TestWriteCatalogueSecondWriteFails: a folder in the catalogue's place makes the second of WriteCatalogue's two writes
// fail, as root too: the schema copy is written (byte-equal to the source), no catalogue is; a second run with the
// obstacle in place fails the same way; once it is gone the second run writes the catalogue.
func TestWriteCatalogueSecondWriteFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, CatalogueFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	cat := contract.Catalogue{Entries: []contract.CatalogueEntry{{ID: "x"}}}
	for run := 1; run <= 2; run++ {
		err := WriteCatalogue(dir, cat)
		if err == nil || !strings.Contains(err.Error(), CatalogueFileName) {
			t.Fatalf("run %d: %v, want the catalogue's write to fail naming it", run, err)
		}
		if cp, err := os.ReadFile(filepath.Join(dir, SchemaCopyFileName)); err != nil || !bytes.Equal(cp, schemas.CatalogueSchema()) {
			t.Errorf("run %d: the schema copy is not the source: %v", run, err)
		}
	}
	if err := os.Remove(filepath.Join(dir, CatalogueFileName)); err != nil {
		t.Fatal(err)
	}
	if err := WriteCatalogue(dir, cat); err != nil {
		t.Fatalf("the second run once the obstacle is gone: %v", err)
	}
	names, _ := os.ReadDir(dir)
	var got []string
	for _, n := range names {
		got = append(got, n.Name())
	}
	if !slices.Equal(got, []string{CatalogueFileName, SchemaCopyFileName}) {
		t.Errorf("the folder holds %v", got)
	}
}

func TestMeasure(t *testing.T) {
	sha, n := Measure(nil)
	if sha != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" || n != 0 {
		t.Errorf("Measure(nil) = %s %d", sha, n)
	}
	sha, n = Measure([]byte("abc"))
	if sha != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" || n != 3 {
		t.Errorf("Measure(abc) = %s %d", sha, n)
	}
}
