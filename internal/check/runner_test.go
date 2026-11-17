package check

// The Check runner on the fixture corpora of testdata/, with the round trip's decoder replaced by a double that reads
// the corpus folder; the Rust XDR tool runs in the integration tier.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// corpusCodec is a double of the Rust XDR tool for a fixture corpus whose bytes are not XDR: decoding an entry's XDR
// file gives the decoded JSON beside it, and encoding that JSON gives the file back. reencode overrides what an entry's
// JSON encodes to.
func corpusCodec(t *testing.T, folder string, reencode map[string][]byte) *doubleCodec {
	t.Helper()
	dec, enc := map[string][]byte{}, map[string][]byte{}
	files, _ := filepath.Glob(filepath.Join(folder, "*", "entry.xdr"))
	for _, x := range files {
		xb, err := os.ReadFile(x)
		if err != nil {
			t.Fatal(err)
		}
		jb, err := os.ReadFile(strings.TrimSuffix(x, ".xdr") + ".json")
		if err != nil {
			t.Fatal(err)
		}
		id := filepath.Base(filepath.Dir(x))
		dec[string(xb)] = jb
		enc[string(jb)] = xb
		if b, ok := reencode[id]; ok {
			enc[string(jb)] = b
		}
	}
	return &doubleCodec{
		decode: func(raw []byte) ([]byte, error) {
			if j, ok := dec[string(raw)]; ok {
				return j, nil
			}
			return nil, errors.New("error: xdr value invalid")
		},
		encode: func(j []byte) ([]byte, error) {
			if b, ok := enc[string(j)]; ok {
				return b, nil
			}
			return nil, errors.New("error: json value invalid")
		},
	}
}

var seedOrder = []string{model.SeedSignedPayloadSigner, model.SeedPoolShareTrustline, model.SeedMuxedAccount}

// validateReport holds the report to the check report's schema, the copy under internal/contract/testdata.
func validateReport(t *testing.T, o Outcome) {
	t.Helper()
	var out bytes.Buffer
	if err := WriteReport(&out, o); err != nil {
		t.Fatal(err)
	}
	sb, err := os.ReadFile("../contract/testdata/check-report.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	sdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(sb))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("file:///check-report.schema.json", sdoc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("file:///check-report.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the report does not validate against check-report.schema.json: %v\n%s", err, out.Bytes())
	}
}

func run(t *testing.T, folder string, codec Codec) Outcome {
	t.Helper()
	o, err := Run(context.Background(), folder, codec, nil)
	if err != nil {
		t.Fatal(err)
	}
	validateReport(t, o)
	return o
}

// TestRunValidCorpus: on a corpus that passes, the four checks run in CHECK_ORDER for every entry, in catalogue order;
// each is reported passing; bytesCompared is each file's length; goSdkRoundTrip is null; passed is true and no reason
// is given.
func TestRunValidCorpus(t *testing.T) {
	folder := testdata + "/corpus/valid"
	c := corpusCodec(t, folder, nil)
	o := run(t, folder, c)
	r := o.Report
	if !r.Passed || r.Folder != folder || !r.CatalogueSchema.Passed || len(o.Reasons) != 0 || Summary(o) != "" {
		t.Fatalf("%+v %v", r, o.Reasons)
	}
	if len(r.Entries) != 3 {
		t.Fatalf("%d entries", len(r.Entries))
	}
	lengths := []int{1920, 2816, 2048}
	for i, e := range r.Entries {
		if e.ID != seedOrder[i] {
			t.Errorf("entry %d is %s, want %s", i, e.ID, seedOrder[i])
		}
		if e.FileIntegrity.Check != contract.CheckNameFileIntegrity || e.RoundTrip.Check != contract.CheckNameRoundTrip || e.ExpectedValues.Check != contract.CheckNameExpectedValues {
			t.Errorf("%s: check names %q %q %q", e.ID, e.FileIntegrity.Check, e.RoundTrip.Check, e.ExpectedValues.Check)
		}
		if !e.FileIntegrity.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed || e.GoSdkRoundTrip != nil {
			t.Errorf("%s: %+v", e.ID, e)
		}
		if e.RoundTrip.BytesCompared != lengths[i] {
			t.Errorf("%s: bytesCompared %d, want the file's length %d", e.ID, e.RoundTrip.BytesCompared, lengths[i])
		}
	}
	want := strings.Repeat("decode LedgerCloseMeta,encode LedgerCloseMeta,", 3)
	if strings.Join(c.calls, ",")+"," != want {
		t.Errorf("codec calls %v: one decode then one encode per entry, in catalogue order", c.calls)
	}
	var out bytes.Buffer
	if err := WriteReport(&out, o); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	order := []string{`"folder"`, `"catalogueSchema"`, `"entries"`, `"fileIntegrity"`, `"roundTrip"`, `"expectedValues"`, `"goSdkRoundTrip": null`, `"passed": true` + "\n}\n"}
	at := 0
	for _, k := range order {
		i := strings.Index(s[at:], k)
		if i < 0 {
			t.Fatalf("the report does not hold %s after offset %d, in CHECK_ORDER:\n%s", k, at, s)
		}
		at += i
	}
	if !strings.HasPrefix(s, "{\n  \"folder\": ") {
		t.Errorf("not two-space indented JSON: %q", s[:20])
	}
}

// failingFixture runs a fixture corpus and returns its outcome, asserting it failed.
func failingFixture(t *testing.T, kind string, reencode map[string][]byte) Outcome {
	t.Helper()
	folder := testdata + "/corpus/" + kind
	o := run(t, folder, corpusCodec(t, folder, reencode))
	if o.Report.Passed {
		t.Fatalf("%s: passed true", kind)
	}
	if Summary(o) == "" || len(o.Reasons) == 0 {
		t.Errorf("%s: no reason for the failure", kind)
	}
	return o
}

// entryOf returns the report's entry id.
func entryOf(t *testing.T, o Outcome, id string) contract.EntryCheckResult {
	t.Helper()
	for _, e := range o.Report.Entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no entry %s in the report", id)
	return contract.EntryCheckResult{}
}

// TestRunFailingFixtures: one check failing on one entry fails the report, every entry is still checked and reported,
// and the reason names the entry and the check.
func TestRunFailingFixtures(t *testing.T) {
	t.Run("hash", func(t *testing.T) {
		o := failingFixture(t, "hash", nil)
		e := entryOf(t, o, model.SeedSignedPayloadSigner)
		if e.FileIntegrity.Passed || e.FileIntegrity.Length != e.FileIntegrity.RecordedLength {
			t.Errorf("%+v", e.FileIntegrity)
		}
		if !strings.Contains(Summary(o), "1 of 3 entries failed; catalogue_schema passed; entry cap-0040-signed-payload-signer: file_integrity: the hash differs") {
			t.Errorf("summary %q", Summary(o))
		}
	})
	t.Run("length", func(t *testing.T) {
		o := failingFixture(t, "length", nil)
		e := entryOf(t, o, model.SeedPoolShareTrustline)
		if e.FileIntegrity.Passed || e.FileIntegrity.RecordedLength != 2817 || e.FileIntegrity.Length != 2816 {
			t.Errorf("%+v", e.FileIntegrity)
		}
		if !strings.Contains(Summary(o), "file_integrity: the length differs (the file is 2816 bytes, the catalogue records 2817); the hash is equal") {
			t.Errorf("summary %q", Summary(o))
		}
	})
	t.Run("round-trip", func(t *testing.T) {
		// Entry 3's file carries 8 bytes after its value; the tool re-encodes the value alone.
		x, err := os.ReadFile(filepath.Join(testdata, "corpus", "round-trip", model.SeedMuxedAccount, "entry.xdr"))
		if err != nil {
			t.Fatal(err)
		}
		o := failingFixture(t, "round-trip", map[string][]byte{model.SeedMuxedAccount: x[:len(x)-8]})
		e := entryOf(t, o, model.SeedMuxedAccount)
		if !e.FileIntegrity.Passed || e.RoundTrip.Passed || e.RoundTrip.BytesCompared != 2048 || !e.ExpectedValues.Passed {
			t.Errorf("%+v", e)
		}
		if !strings.Contains(Summary(o), "entry cap-0027-muxed-account: round_trip: the re-encoded bytes differ from the file cap-0027-muxed-account/entry.xdr at offset 2048 (the file is 2056 bytes, the re-encoding 2048)") {
			t.Errorf("summary %q", Summary(o))
		}
	})
	t.Run("expected-value", func(t *testing.T) {
		o := failingFixture(t, "expected-value", nil)
		e := entryOf(t, o, model.SeedSignedPayloadSigner)
		if e.ExpectedValues.Passed || len(e.ExpectedValues.Mismatches) != 1 {
			t.Fatalf("%+v", e.ExpectedValues)
		}
		m := e.ExpectedValues.Mismatches[0]
		if !strings.HasSuffix(m.Pointer, "/result/result/result/tx_success/0/op_inner/set_options") || string(m.Recorded) != `"malformed"` || string(m.Decoded) != `"success"` {
			t.Errorf("%+v", m)
		}
	})
	t.Run("schema-pointer", func(t *testing.T) {
		c := &doubleCodec{}
		o, err := Run(context.Background(), testdata+"/corpus/schema-pointer", c, nil)
		if err != nil {
			t.Fatal(err)
		}
		validateReport(t, o)
		if o.Report.Passed || o.Report.CatalogueSchema.Passed || len(o.Report.Entries) != 0 || o.Report.Entries == nil || len(c.calls) != 0 {
			t.Errorf("a catalogue that fails its schema: no entry is checked, entries is []: %+v, calls %v", o.Report, c.calls)
		}
		if !strings.HasPrefix(Summary(o), "catalogue_schema failed; no entry was checked; catalogue_schema: 2 failure(s), the first at \"/entries/0/length\"") {
			t.Errorf("summary %q", Summary(o))
		}
	})
	t.Run("schema-copy", func(t *testing.T) {
		o := failingFixture(t, "schema-copy", nil)
		if o.Report.CatalogueSchema.Passed || len(o.Report.Entries) != 3 {
			t.Fatalf("a differing schema copy fails catalogue_schema and does not stop the entries: %+v", o.Report)
		}
		for _, e := range o.Report.Entries {
			if !e.FileIntegrity.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed {
				t.Errorf("%+v", e)
			}
		}
		if !strings.HasPrefix(Summary(o), "0 of 3 entries failed; catalogue_schema failed; catalogue_schema: 1 failure(s)") ||
			!strings.Contains(Summary(o), "differs from the source schema") {
			t.Errorf("summary %q", Summary(o))
		}
	})
}

// copyCorpus copies a fixture corpus into a temporary folder.
func copyCorpus(t *testing.T, kind string) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join(testdata, "corpus", kind)
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestRunMissingInputs: a check whose input is missing fails with its own error record. A missing catalogue is one
// failure at pointer "" with entries []; a missing XDR file fails file_integrity (sha256 "", length 0), round_trip
// (bytesCompared 0, nothing decoded) and expected_values (every value's decoded null) of its entry, and the other
// entries are checked.
func TestRunMissingInputs(t *testing.T) {
	t.Run("catalogue", func(t *testing.T) {
		folder := copyCorpus(t, "valid")
		if err := os.Remove(filepath.Join(folder, catalogue.CatalogueFileName)); err != nil {
			t.Fatal(err)
		}
		o := run(t, folder, &doubleCodec{})
		f := o.Report.CatalogueSchema.Failures
		if o.Report.Passed || len(o.Report.Entries) != 0 || len(f) != 1 || f[0].Pointer != "" || !strings.Contains(f[0].Message, "missing") {
			t.Errorf("%+v", o.Report)
		}
	})
	t.Run("catalogue not JSON", func(t *testing.T) {
		folder := copyCorpus(t, "valid")
		if err := os.WriteFile(filepath.Join(folder, catalogue.CatalogueFileName), []byte("{\"entries\": ["), 0o644); err != nil {
			t.Fatal(err)
		}
		o := run(t, folder, &doubleCodec{})
		f := o.Report.CatalogueSchema.Failures
		if o.Report.Passed || len(o.Report.Entries) != 0 || len(f) != 1 || f[0].Pointer != "" {
			t.Errorf("%+v", o.Report)
		}
	})
	t.Run("XDR file", func(t *testing.T) {
		folder := copyCorpus(t, "valid")
		c := corpusCodec(t, folder, nil)
		if err := os.Remove(filepath.Join(folder, model.SeedPoolShareTrustline, "entry.xdr")); err != nil {
			t.Fatal(err)
		}
		o := run(t, folder, c)
		if o.Report.Passed || len(o.Report.Entries) != 3 {
			t.Fatalf("%+v", o.Report)
		}
		e := entryOf(t, o, model.SeedPoolShareTrustline)
		if e.FileIntegrity.Passed || e.FileIntegrity.Sha256 != "" || e.FileIntegrity.Length != 0 {
			t.Errorf("file_integrity %+v", e.FileIntegrity)
		}
		if e.RoundTrip.Passed || e.RoundTrip.BytesCompared != 0 {
			t.Errorf("round_trip %+v", e.RoundTrip)
		}
		if e.ExpectedValues.Passed || len(e.ExpectedValues.Mismatches) == 0 {
			t.Errorf("expected_values %+v", e.ExpectedValues)
		}
		for _, m := range e.ExpectedValues.Mismatches {
			if string(m.Decoded) != "null" {
				t.Errorf("%s: decoded %s, want null", m.Pointer, m.Decoded)
			}
		}
		for _, id := range []string{model.SeedSignedPayloadSigner, model.SeedMuxedAccount} {
			if e := entryOf(t, o, id); !e.FileIntegrity.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed {
				t.Errorf("%s is checked and passes: %+v", id, e)
			}
		}
		if strings.Count(strings.Join(c.calls, ","), "decode") != 2 {
			t.Errorf("calls %v: the missing file is not decoded", c.calls)
		}
		for _, want := range []string{"file_integrity: the XDR file", "round_trip: the XDR file", "expected_values: there is no decoded form"} {
			if !strings.Contains(Summary(o), want) {
				t.Errorf("summary %q lacks %q", Summary(o), want)
			}
		}
	})
}

// TestRunUndecodableEntry: an entry the tool cannot decode fails round_trip (bytesCompared 0) and expected_values;
// file_integrity passes; the report fails.
func TestRunUndecodableEntry(t *testing.T) {
	folder := testdata + "/corpus/valid"
	c := corpusCodec(t, folder, nil)
	inner := c.decode
	calls := 0
	c.decode = func(raw []byte) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("stellar-xdr decode --type LedgerCloseMeta failed: error: xdr value invalid")
		}
		return inner(raw)
	}
	o := run(t, folder, c)
	e := entryOf(t, o, model.SeedPoolShareTrustline)
	if o.Report.Passed || !e.FileIntegrity.Passed || e.RoundTrip.Passed || e.RoundTrip.BytesCompared != 0 || e.ExpectedValues.Passed {
		t.Errorf("%+v", e)
	}
	if !strings.Contains(Summary(o), "entry cap-0038-pool-share-trustline: round_trip: the file cap-0038-pool-share-trustline/entry.xdr is not decodable as LedgerCloseMeta") {
		t.Errorf("summary %q", Summary(o))
	}
}

// TestRunStops: an interrupt or an unavailable decoder stops the run with an error and no report to print.
func TestRunStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, testdata+"/corpus/valid", corpusCodec(t, testdata+"/corpus/valid", nil), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("interrupted: %v", err)
	}
	gone := &doubleCodec{decode: func([]byte) ([]byte, error) { return nil, &UnavailableError{Err: errors.New("no container")} }}
	var u *UnavailableError
	if _, err := Run(context.Background(), testdata+"/corpus/valid", gone, nil); !errors.As(err, &u) {
		t.Errorf("unavailable: %v", err)
	}
}

// TestRunDecodedJSONHeld: the round trip holds each entry's decoded JSON file to the tool's fresh decoding of its XDR
// file. On a copy of the valid corpus, decoded by the tool double of the original: entry 1's file with one leaf edited
// fails its round trip at that pointer (bytesCompared the file's length; its expected values, read from the fresh
// decoding, still pass); entry 2's file removed fails its round trip; entry 3's file reformatted passes.
func TestRunDecodedJSONHeld(t *testing.T) {
	c := corpusCodec(t, testdata+"/corpus/valid", nil)
	folder := copyCorpus(t, "valid")
	one := filepath.Join(folder, model.SeedSignedPayloadSigner, "entry.json")
	b, err := os.ReadFile(one)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), `"set_options": "success"`, `"set_options": "malformed"`, 1)
	if edited == string(b) {
		t.Fatal("entry 1's decoded JSON holds no operation result to edit")
	}
	if err := os.WriteFile(one, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(folder, model.SeedPoolShareTrustline, "entry.json")); err != nil {
		t.Fatal(err)
	}
	three := filepath.Join(folder, model.SeedMuxedAccount, "entry.json")
	b, err = os.ReadFile(three)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(three, compact.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	o := run(t, folder, c)
	if o.Report.Passed || len(o.Report.Entries) != 3 {
		t.Fatalf("%+v", o.Report)
	}
	e := entryOf(t, o, model.SeedSignedPayloadSigner)
	if !e.FileIntegrity.Passed || e.RoundTrip.Passed || e.RoundTrip.BytesCompared != 1920 || !e.ExpectedValues.Passed {
		t.Errorf("entry 1: %+v", e)
	}
	e = entryOf(t, o, model.SeedPoolShareTrustline)
	if !e.FileIntegrity.Passed || e.RoundTrip.Passed || e.RoundTrip.BytesCompared != 2816 || !e.ExpectedValues.Passed {
		t.Errorf("entry 2: %+v", e)
	}
	if e := entryOf(t, o, model.SeedMuxedAccount); !e.RoundTrip.Passed || !e.ExpectedValues.Passed {
		t.Errorf("entry 3, reformatted: %+v", e)
	}
	for _, want := range []string{
		`entry cap-0040-signed-payload-signer: round_trip: the decoded JSON file cap-0040-signed-payload-signer/entry.json differs from the Rust XDR tool's decoding of cap-0040-signed-payload-signer/entry.xdr at "/v2/tx_processing/0/result/result/result/tx_success/0/op_inner/set_options": the file holds "malformed", the decoding "success"`,
		"entry cap-0038-pool-share-trustline: round_trip: the decoded JSON file cap-0038-pool-share-trustline/entry.json cannot be read",
	} {
		if !strings.Contains(Summary(o), want) {
			t.Errorf("summary %q\nwant it to hold %q", Summary(o), want)
		}
	}
}
