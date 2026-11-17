package schema

// The Catalogue schema validator: the check catalogue_schema, the first check `ltk check` runs. Every failure is a
// SchemaFailure named by the JSON pointer of the offending catalogue value.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/schemas"
)

// CheckCatalogue runs catalogue_schema on the corpus folder:
//   - the catalogue is read as strict JSON (UTF-8 without BOM, no duplicate key, nothing after the document) and
//     validated against the embedded schema of schemas/, never the folder's copy; then the two rules the schema cannot
//     express run (unique ids, and the refusal evidence on entry 2 only) and the integer fields are held to integers
//     the catalogue's Go type can hold;
//   - a missing catalogue, or one that is not strict JSON, is one SchemaFailure at pointer "";
//   - in the same check, the schema copy beside the catalogue is compared with the source byte for byte; a copy that
//     differs, or is missing, is one SchemaFailure at pointer "".
//
// The catalogue is returned when its entries can be trusted for the per-entry checks: when it was read, is valid
// against the schema and breaks neither rule. A schema copy that differs does not withhold it, because the catalogue
// itself was validated against the embedded schema; a nil catalogue means entries is [] in the check report.
func CheckCatalogue(folder string) (contract.CatalogueSchemaResult, *contract.Catalogue) {
	result := contract.CatalogueSchemaResult{Check: contract.CheckNameCatalogueSchema, Failures: []contract.SchemaFailure{}}
	var cat *contract.Catalogue
	b, err := catalogue.ReadCatalogueFile(folder)
	switch {
	case errors.Is(err, catalogue.ErrNoCatalogue):
		result.Failures = append(result.Failures, contract.SchemaFailure{Pointer: "",
			Message: fmt.Sprintf("the catalogue %s is missing from the corpus folder", catalogue.CatalogueFileName)})
	case err != nil:
		result.Failures = append(result.Failures, contract.SchemaFailure{Pointer: "",
			Message: fmt.Sprintf("the catalogue %s cannot be read: %v", catalogue.CatalogueFileName, err)})
	default:
		var failures []contract.SchemaFailure
		cat, failures = ValidateCatalogue(b)
		result.Failures = append(result.Failures, failures...)
	}
	if f := CompareSchemaCopy(folder); f != nil {
		result.Failures = append(result.Failures, *f)
	}
	result.Passed = len(result.Failures) == 0
	return result, cat
}

// CompareSchemaCopy compares the schema copy beside folder's catalogue with the one source under schemas/, byte for
// byte. It returns nil when they are equal, else the SchemaFailure at pointer "" naming the copy.
func CompareSchemaCopy(folder string) *contract.SchemaFailure {
	b, err := catalogue.ReadSchemaCopy(folder)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &contract.SchemaFailure{Pointer: "", Message: fmt.Sprintf("the corpus schema copy %s is missing beside the catalogue", catalogue.SchemaCopyFileName)}
	case err != nil:
		return &contract.SchemaFailure{Pointer: "", Message: fmt.Sprintf("the corpus schema copy %s cannot be read: %v", catalogue.SchemaCopyFileName, err)}
	case !bytes.Equal(b, schemas.CatalogueSchema()):
		return &contract.SchemaFailure{Pointer: "", Message: fmt.Sprintf("corpus schema copy %s differs from the source schema under schemas/", catalogue.SchemaCopyFileName)}
	}
	return nil
}

// ValidateCatalogue validates catalogue bytes: strict JSON, then the embedded schema, then the two entry rules and the
// integer rule. Every failure is reported, each with its own pointer, sorted by pointer. The
// catalogue is returned only when there is no failure.
func ValidateCatalogue(b []byte) (*contract.Catalogue, []contract.SchemaFailure) {
	doc, err := catalogue.Parse(b)
	if err != nil {
		var je *catalogue.JSONError
		ptr := ""
		if errors.As(err, &je) {
			ptr = je.Pointer
		}
		return nil, []contract.SchemaFailure{{Pointer: ptr, Message: "the catalogue is not strict JSON: " + err.Error()}}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil { // Parse accepted the bytes, so this does not happen; reported rather than ignored
		return nil, []contract.SchemaFailure{{Pointer: "", Message: "the catalogue is not JSON: " + err.Error()}}
	}
	sch, err := compiled()
	if err != nil {
		return nil, []contract.SchemaFailure{{Pointer: "", Message: "the embedded catalogue schema does not compile: " + err.Error()}}
	}
	var failures []contract.SchemaFailure
	var ve *jsonschema.ValidationError
	if err := sch.Validate(inst); errors.As(err, &ve) {
		failures = leafFailures(ve)
	} else if err != nil {
		failures = []contract.SchemaFailure{{Pointer: "", Message: err.Error()}}
	}
	failures = append(failures, entryRules(doc, failures)...)
	sortFailures(failures)
	if len(failures) > 0 {
		return nil, failures
	}
	var cat contract.Catalogue
	if err := json.Unmarshal(b, &cat); err != nil {
		return nil, []contract.SchemaFailure{{Pointer: "", Message: "the catalogue does not decode: " + err.Error()}}
	}
	return &cat, nil
}

// schemaURL names the embedded schema inside the validator; nothing is loaded from it.
const schemaURL = "file:///ltk/schemas/catalogue.schema.json"

var (
	compileOnce sync.Once
	compiledSch *jsonschema.Schema
	compileErr  error
)

// compiled is the embedded schema of schemas/, compiled once with draft 2020-12, the draft its $schema names.
func compiled() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.CatalogueSchema()))
		if err != nil {
			compileErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schemaURL, doc); err != nil {
			compileErr = err
			return
		}
		compiledSch, compileErr = c.Compile(schemaURL)
	})
	return compiledSch, compileErr
}

// leafFailures flattens a validation error to its leaf causes, one SchemaFailure each: every offending pointer, not
// just the first. A missing required property points at the property itself (/entries/1/sha256), and so does
// a property the schema does not allow, rather than at the object holding it.
func leafFailures(ve *jsonschema.ValidationError) []contract.SchemaFailure {
	if len(ve.Causes) > 0 {
		var out []contract.SchemaFailure
		for _, c := range ve.Causes {
			out = append(out, leafFailures(c)...)
		}
		return out
	}
	ptr := pointer(ve.InstanceLocation)
	switch k := ve.ErrorKind.(type) {
	case *kind.Required:
		var out []contract.SchemaFailure
		for _, name := range k.Missing {
			out = append(out, contract.SchemaFailure{Pointer: ptr + "/" + catalogue.EscapeToken(name),
				Message: fmt.Sprintf("missing required property %q", name)})
		}
		return out
	case *kind.AdditionalProperties:
		var out []contract.SchemaFailure
		for _, name := range k.Properties {
			out = append(out, contract.SchemaFailure{Pointer: ptr + "/" + catalogue.EscapeToken(name),
				Message: fmt.Sprintf("property %q is not allowed", name)})
		}
		return out
	}
	msg := ve.Error() // "at '<pointer>': <what is wrong>" for a leaf; the pointer is the failure's own field
	if _, rest, ok := strings.Cut(msg, "': "); ok && strings.HasPrefix(msg, "at '") {
		msg = rest
	}
	return []contract.SchemaFailure{{Pointer: ptr, Message: msg}}
}

// pointer renders an instance location as an RFC 6901 JSON pointer.
func pointer(tokens []string) string {
	var sb strings.Builder
	for _, t := range tokens {
		sb.WriteString("/" + catalogue.EscapeToken(t))
	}
	return sb.String()
}

// entryRules are the rules that run after the schema, inside catalogue_schema, over the entries the document holds,
// and the integer rule:
//   - every id is unique: a repeated id fails at /entries/<i>/id;
//   - networkRefusalEvidence is present on the entry whose id is entry 2's (internal/model/seeds.go), failing at
//     /entries/<i> when absent, and absent on every other entry, failing at /entries/<i>/networkRefusalEvidence;
//   - protocolVersion and length are written as integers with no fraction or exponent that an int holds, failing at
//     their pointer; a pointer the schema already failed is not reported again.
func entryRules(doc *catalogue.Node, already []contract.SchemaFailure) []contract.SchemaFailure {
	entries := doc.Member("entries")
	if entries == nil || entries.Kind != catalogue.KindArray {
		return nil
	}
	var out []contract.SchemaFailure
	firstAt := map[string]int{}
	for i, e := range entries.Elems {
		at := "/entries/" + strconv.Itoa(i)
		if e.Kind != catalogue.KindObject {
			continue
		}
		for _, field := range []string{"protocolVersion", "length"} {
			n := e.Member(field)
			p := at + "/" + field
			if n == nil || n.Kind != catalogue.KindNumber || failedAt(already, p) {
				continue
			}
			if _, err := strconv.Atoi(n.Text); err != nil {
				out = append(out, contract.SchemaFailure{Pointer: p,
					Message: fmt.Sprintf("%s is %s; it must be written as a whole number with no fraction or exponent", field, n.Text)})
			}
		}
		id := e.Member("id")
		if id == nil || id.Kind != catalogue.KindString {
			continue
		}
		if first, dup := firstAt[id.Text]; dup {
			out = append(out, contract.SchemaFailure{Pointer: at + "/id",
				Message: fmt.Sprintf("the entry id %q is already the id of /entries/%d; each id is unique in the catalogue", id.Text, first)})
		} else {
			firstAt[id.Text] = i
		}
		seed, isSeed := model.SeedByID(id.Text)
		has := e.Member("networkRefusalEvidence") != nil
		switch {
		case isSeed && seed.RecordsNetworkRefusal && !has:
			out = append(out, contract.SchemaFailure{Pointer: at,
				Message: fmt.Sprintf("entry %s must carry networkRefusalEvidence (entry 2's evidence of the network's refusal)", id.Text)})
		case !(isSeed && seed.RecordsNetworkRefusal) && has:
			out = append(out, contract.SchemaFailure{Pointer: at + "/networkRefusalEvidence",
				Message: fmt.Sprintf("networkRefusalEvidence is present on entry 2 (%s) only", model.SeedPoolShareTrustline)})
		}
	}
	return out
}

func failedAt(failures []contract.SchemaFailure, p string) bool {
	return slices.ContainsFunc(failures, func(f contract.SchemaFailure) bool { return f.Pointer == p })
}

// sortFailures orders failures by pointer, an array index compared as a number, then by message, so a report is the
// same from run to run.
func sortFailures(fs []contract.SchemaFailure) {
	slices.SortStableFunc(fs, func(a, b contract.SchemaFailure) int {
		if c := comparePointers(a.Pointer, b.Pointer); c != 0 {
			return c
		}
		return strings.Compare(a.Message, b.Message)
	})
}

func comparePointers(a, b string) int {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		ai, aerr := strconv.Atoi(as[i])
		bi, berr := strconv.Atoi(bs[i])
		if aerr == nil && berr == nil {
			return ai - bi
		}
		return strings.Compare(as[i], bs[i])
	}
	return len(as) - len(bs)
}
