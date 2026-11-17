package orchestrator

// The reader's tools of the verification: what a reader of the corpus and of the commands' output uses, written with
// the standard library and github.com/santhosh-tekuri/jsonschema/v6 only, never with the program's own packages, so
// that a passing row is the contract's verdict and not the code's agreement with itself.
//
//   - compileSchemaFile compiles one of the data model's JSON Schemas (draft 2020-12) from its file;
//   - requireValid validates a document against one of them;
//   - decodeJSON decodes with numbers kept as their text (json.Number), and resolvePointer reads an RFC 6901 pointer;
//   - copyTree copies a fixture folder into a temporary folder, byte for byte;
//   - noDockerEnv is an environment whose PATH holds no program at all.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Paths of the module, from this package's folder. dataModelDocPath is the data model's document of record, when
// present beside the module; a tree without it skips the tests that read it.
const (
	moduleRoot          = "../.."
	outputSchemaFolder  = "../contract/testdata"
	catalogueSchemaPath = "../../schemas/catalogue.schema.json"
	testdataFolder      = "../../testdata"
	dataModelDocPath    = "../../specs/data-model.md"
)

var (
	schemaCacheMu sync.Mutex
	schemaCache   = map[string]*jsonschema.Schema{}
)

// compileSchemaFile compiles the JSON Schema at path once per test process.
func compileSchemaFile(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	schemaCacheMu.Lock()
	defer schemaCacheMu.Unlock()
	if s, ok := schemaCache[path]; ok {
		return s
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the schema %s: %v", path, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("the schema %s is not JSON: %v", path, err)
	}
	url := "https://ltk.invalid/schemas/" + filepath.Base(path) // a name for the resource; nothing is loaded from it
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		t.Fatalf("the schema %s: %v", path, err)
	}
	s, err := c.Compile(url)
	if err != nil {
		t.Fatalf("the schema %s does not compile: %v", path, err)
	}
	schemaCache[path] = s
	return s
}

// outputSchema is the schema fixture of one output document; the output documents' schemas are test fixtures under
// internal/, and schemas/ publishes the catalogue's only.
func outputSchema(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	return compileSchemaFile(t, filepath.Join(outputSchemaFolder, file))
}

// validationError validates doc against s and returns the library's error, nil when doc is valid.
func validationError(t *testing.T, s *jsonschema.Schema, doc []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("not one JSON value: %w", err)
	}
	return s.Validate(inst)
}

// requireValid fails the test unless doc is valid against the output schema file.
func requireValid(t *testing.T, file string, doc []byte) {
	t.Helper()
	if err := validationError(t, outputSchema(t, file), doc); err != nil {
		t.Fatalf("the document does not validate against %s: %v\n%s", file, err, doc)
	}
}

// requireOneDocument holds stdout to the data model's encoding of an output document: one JSON document followed by
// a single \n.
func requireOneDocument(t *testing.T, stdout []byte) {
	t.Helper()
	if !bytes.HasSuffix(stdout, []byte("\n")) || bytes.HasSuffix(stdout, []byte("\n\n")) {
		t.Fatalf("stdout does not end in a single \\n: %q", tail(stdout))
	}
	dec := json.NewDecoder(bytes.NewReader(stdout))
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not a JSON document: %v", err)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one JSON document")
	}
}

// requireTwoSpaceIndent holds stdout to two-space indentation with a trailing newline: it equals its own compact form
// indented by json.Indent with two spaces.
func requireTwoSpaceIndent(t *testing.T, stdout []byte) {
	t.Helper()
	var compact, indented bytes.Buffer
	if err := json.Compact(&compact, stdout); err != nil {
		t.Fatal(err)
	}
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	indented.WriteByte('\n')
	if !bytes.Equal(stdout, indented.Bytes()) {
		t.Fatalf("stdout is not written with two-space indentation and one trailing newline")
	}
}

// tail is the last bytes of b, for a message.
func tail(b []byte) []byte {
	if len(b) > 80 {
		return b[len(b)-80:]
	}
	return b
}

// decodeJSON decodes b with every number kept as its text.
func decodeJSON(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return v
}

// decodeJSONFile is decodeJSON of a file.
func decodeJSONFile(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return decodeJSON(t, b)
}

// resolvePointer reads the RFC 6901 pointer p in doc; ok is false when it resolves to nothing.
func resolvePointer(doc any, p string) (v any, ok bool) {
	if p == "" {
		return doc, true
	}
	if !strings.HasPrefix(p, "/") {
		return nil, false
	}
	v = doc
	for _, tok := range strings.Split(p[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch n := v.(type) {
		case map[string]any:
			if v, ok = n[tok]; !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(n) || strconv.Itoa(i) != tok {
				return nil, false
			}
			v = n[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// sameJSON reports whether two decoded values are the same JSON value, numbers compared by their text.
func sameJSON(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// copyTree copies the folder src into a new temporary folder, byte for byte, and returns the copy.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
	return dst
}

// writeJSONFile writes v as two-space indented JSON with a trailing newline.
func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noDockerEnv is an environment whose PATH is an empty folder: no docker, no program at all.
func noDockerEnv(t *testing.T) []string {
	t.Helper()
	return []string{"PATH=" + t.TempDir()}
}

// changeListNames are the four lists of ledger-entry changes whose elements `ltk compare` matches by (ledger key,
// change kind) as a multiset, never by position; every other list is compared by position.
var changeListNames = map[string]bool{"changes": true, "tx_changes_before": true, "tx_changes_after": true, "fee_processing": true}

// ledgerKeyFields are the fields of each ledger-entry arm that make its ledger key, as the Rust tool's JSON names them
// (by arm); config_setting's key is its own arm.
var ledgerKeyFields = map[string][]string{
	"account":           {"account_id"},
	"trustline":         {"account_id", "asset"},
	"offer":             {"seller_id", "offer_id"},
	"data":              {"account_id", "data_name"},
	"claimable_balance": {"balance_id"},
	"liquidity_pool":    {"liquidity_pool_id"},
	"contract_data":     {"contract", "key", "durability"},
	"contract_code":     {"hash"},
	"ttl":               {"key_hash"},
}

// changeIdentity is a change's (change kind, ledger key) as text; ok is false when the key cannot be derived. A change
// is one object with one key, its kind: `removed` holds a ledger key; every other kind holds an entry whose `data` is
// one object with one key, its arm.
func changeIdentity(change any) (string, bool) {
	m, ok := change.(map[string]any)
	if !ok || len(m) != 1 {
		return "", false
	}
	for kind, body := range m {
		data := body
		if kind != "removed" {
			b, ok := body.(map[string]any)
			if !ok {
				return "", false
			}
			data = b["data"]
		}
		d, ok := data.(map[string]any)
		if !ok || len(d) != 1 {
			return "", false
		}
		for arm, v := range d {
			if arm == "config_setting" {
				cs, ok := v.(map[string]any)
				if !ok || len(cs) != 1 {
					return "", false
				}
				for k := range cs {
					return kind + "|config_setting|" + k, true
				}
			}
			names, known := ledgerKeyFields[arm]
			f, ok := v.(map[string]any)
			if !known || !ok {
				return "", false
			}
			var key []any
			for _, n := range names {
				x, ok := f[n]
				if !ok {
					return "", false
				}
				key = append(key, x)
			}
			b, err := json.Marshal(key)
			if err != nil {
				return "", false
			}
			return kind + "|" + arm + "|" + string(b), true
		}
	}
	return "", false
}

// matchedIndex is the index in to of the element `ltk compare` pairs with from[i]: the k-th element of to with the same
// identity, where from[i] is the k-th of its identity in from; an element whose identity cannot be derived pairs by
// order with the elements of to whose identity cannot be derived, and with no other. ok is false when it has no
// partner.
func matchedIndex(from, to []any, i int) (int, bool) {
	id, derived := changeIdentity(from[i])
	same := func(x any) bool {
		xid, xd := changeIdentity(x)
		return xd == derived && (!derived || xid == id)
	}
	k := 0
	for _, x := range from[:i] {
		if same(x) {
			k++
		}
	}
	for j, x := range to {
		if same(x) {
			if k == 0 {
				return j, true
			}
			k--
		}
	}
	return 0, false
}

// resolveMatched reads in to what the pointer p names in from, following the multiset matching wherever p indexes one
// of the four change lists; ok is false when to has nothing there (the element is on one side only).
func resolveMatched(from, to any, p string) (v any, ok bool) {
	if p == "" {
		return to, true
	}
	if !strings.HasPrefix(p, "/") {
		return nil, false
	}
	prev := ""
	for _, tok := range strings.Split(p[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch f := from.(type) {
		case map[string]any:
			t, isMap := to.(map[string]any)
			if from, ok = f[tok]; !ok || !isMap {
				return nil, false
			}
			if to, ok = t[tok]; !ok {
				return nil, false
			}
		case []any:
			t, isList := to.([]any)
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(f) || !isList {
				return nil, false
			}
			j := i
			if changeListNames[prev] {
				if j, ok = matchedIndex(f, t, i); !ok {
					return nil, false
				}
			}
			if j >= len(t) {
				return nil, false
			}
			from, to = f[i], t[j]
		default:
			return nil, false
		}
		prev = tok
	}
	return to, true
}

// readPair reads the pointer p of from in from and, through the matching, in to; a side with nothing there reads null.
func readPair(from, to any, p string) (vFrom, vTo any) {
	vFrom, ok := resolvePointer(from, p)
	if !ok {
		vFrom = nil
	}
	if vTo, ok = resolveMatched(from, to, p); !ok {
		vTo = nil
	}
	return vFrom, vTo
}
