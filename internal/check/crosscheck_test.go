package check

// The Go SDK cross-check on the real protocol-30 capture of testdata/real (a LedgerCloseMeta of 1928 bytes, and the
// transaction envelope inside it) and on the fixture corpora of testdata/corpus, whose bytes are not XDR. The Rust XDR
// tool's round trip is a codec double here; `ltk check --also-go` with the real tool runs in the integration tier
// (cmd/ltk/check_integration_test.go).

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
)

const (
	realCapture        = testdata + "/real/capture-p30.xdr"
	realCaptureDecoded = testdata + "/real/capture-p30.json"
	// realPayloadPadding is the offset of the first padding byte after entry 1's 29-byte signed payload in the real
	// capture (the payload starts at offset 712; XDR pads it to 32 bytes with zeros).
	realPayloadPadding = 712 + 29
)

func readReal(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(realCapture)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// realEnvelope is the transaction envelope inside the real capture, encoded by the Go SDK.
func realEnvelope(t *testing.T, raw []byte) []byte {
	t.Helper()
	v, err := xdrcodec.Decode(xdrcodec.TypeLedgerCloseMeta, raw)
	if err != nil {
		t.Fatal(err)
	}
	envs := v.(*xdr.LedgerCloseMeta).TransactionEnvelopes()
	if len(envs) != 1 {
		t.Fatalf("%d envelopes in the capture, want 1", len(envs))
	}
	b, err := xdrcodec.Encode(&envs[0])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var goEntry = contract.CatalogueEntry{ID: model.SeedSignedPayloadSigner, XdrType: xdrcodec.TypeLedgerCloseMeta, FilePath: model.SeedSignedPayloadSigner + "/entry.xdr"}

// TestGoSdkRoundTripRealCapture: the Go SDK decodes and re-encodes the real capture and the envelope inside it byte for
// byte: passed, bytesCompared the file's length, no reason.
func TestGoSdkRoundTripRealCapture(t *testing.T) {
	raw := readReal(t)
	env := realEnvelope(t, raw)
	for _, c := range []struct {
		xdrType string
		file    []byte
	}{{xdrcodec.TypeLedgerCloseMeta, raw}, {xdrcodec.TypeTransactionEnvelope, env}} {
		e := goEntry
		e.XdrType = c.xdrType
		r, reason := GoSdkRoundTrip(GoSDK, e, c.file, nil)
		if !r.Passed || r.Check != contract.CheckNameGoSdkRoundTrip || r.BytesCompared != len(c.file) || reason != "" {
			t.Errorf("%s: %+v %q; want passed, bytesCompared %d", c.xdrType, r, reason, len(c.file))
		}
	}
	if len(raw) != 1928 {
		t.Errorf("the capture is %d bytes, want 1928", len(raw))
	}
}

// TestGoSdkRoundTripFailures: each way the Go SDK's round trip can fail is a failed go_sdk_round_trip naming the entry,
// with round_trip's bytesCompared rule:
//   - real bytes the Go SDK refuses (a non-zero padding byte, eight bytes after the value): nothing re-encoded, 0;
//   - a type the codec does not know: a failure naming the type, no panic, 0;
//   - an unreadable file: 0;
//   - a re-encoding that differs (a codec double; the SDK refuses every non-canonical input of the real capture tried):
//     the shorter length, and the first differing offset in the reason.
func TestGoSdkRoundTripFailures(t *testing.T) {
	raw := readReal(t)
	padded := slices.Clone(raw)
	padded[realPayloadPadding] = 0x01
	trailing := append(slices.Clone(raw), 0, 0, 0, 0, 0, 0, 0, 0)
	differs := func(at int) GoCodec {
		return func(_ string, b []byte) ([]byte, error) {
			out := slices.Clone(b)
			out[at] ^= 0xff
			return out, nil
		}
	}
	shorter := GoCodec(func(_ string, b []byte) ([]byte, error) { return b[:len(b)-4], nil })
	unknown := goEntry
	unknown.XdrType = "LedgerEntry"
	cases := []struct {
		name      string
		codec     GoCodec
		entry     contract.CatalogueEntry
		file      []byte
		readErr   error
		compared  int
		inMessage string
	}{
		{"non-zero padding", GoSDK, goEntry, padded, nil, 0, "the Go SDK does not decode and re-encode the file cap-0040-signed-payload-signer/entry.xdr as LedgerCloseMeta: decoding 1928 bytes as LedgerCloseMeta with the Go SDK: "},
		{"bytes after the value", GoSDK, goEntry, trailing, nil, 0, "the Go SDK does not decode and re-encode the file cap-0040-signed-payload-signer/entry.xdr as LedgerCloseMeta"},
		{"unknown type", GoSDK, unknown, raw, nil, 0, `declares the XDR type "LedgerEntry", which the Go SDK codec does not know`},
		{"unreadable file", GoSDK, goEntry, nil, os.ErrNotExist, 0, "the XDR file cap-0040-signed-payload-signer/entry.xdr cannot be read"},
		{"re-encoding differs", differs(100), goEntry, raw, nil, 1928, "the Go SDK's re-encoded bytes differ from the file cap-0040-signed-payload-signer/entry.xdr at offset 100 (the file is 1928 bytes, the re-encoding 1928)"},
		{"re-encoding shorter", shorter, goEntry, raw, nil, 1924, "at offset 1924 (the file is 1928 bytes, the re-encoding 1924)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, reason := GoSdkRoundTrip(c.codec, c.entry, c.file, c.readErr)
			if r.Passed || r.Check != contract.CheckNameGoSdkRoundTrip || r.BytesCompared != c.compared {
				t.Errorf("%+v; want failed with bytesCompared %d", r, c.compared)
			}
			if !strings.HasPrefix(reason, "entry cap-0040-signed-payload-signer: go_sdk_round_trip: ") || !strings.Contains(reason, c.inMessage) {
				t.Errorf("reason %q\nwant it to name the entry, the check and %q", reason, c.inMessage)
			}
		})
	}
}

// realCorpus writes a corpus of the real capture, as entry 1, into a temporary folder through the layout writer, its
// expected values read from the decoded JSON through the grouping table; file replaces the capture's bytes when set.
func realCorpus(t *testing.T, file []byte) string {
	t.Helper()
	folder := t.TempDir()
	raw := readReal(t)
	if file == nil {
		file = raw
	}
	decoded, err := os.ReadFile(realCaptureDecoded)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := model.SeedByID(model.SeedSignedPayloadSigner)
	path := catalogue.EntryFilePath(seed.ID)
	if err := catalogue.WriteEntryFiles(folder, path, file, decoded); err != nil {
		t.Fatal(err)
	}
	expected, err := catalogue.SplitExpectedValues(seed.ID, decoded)
	if err != nil {
		t.Fatal(err)
	}
	sha, n := catalogue.Measure(file)
	cat := contract.Catalogue{Entries: []contract.CatalogueEntry{{
		ID: seed.ID, Cap: seed.Cap, ProtocolVersion: 30, Source: "the protocol-30 capture of testdata/real", Synthetic: false,
		ExpectedDecodedValues: expected, FilePath: path, XdrType: seed.XdrType, Sha256: sha, Length: n,
	}}}
	if err := catalogue.WriteCatalogue(folder, cat); err != nil {
		t.Fatal(err)
	}
	return folder
}

// TestRunAlsoGo: with the Go SDK codec the runner adds go_sdk_round_trip after expected_values for every entry,
// reported in goSdkRoundTrip beside the Rust tool's roundTrip; without it every goSdkRoundTrip stays null (the other
// tests of this package).
//   - The real capture passes both decoders: the report passes, goSdkRoundTrip.bytesCompared is the file's length.
//   - The same capture with a non-zero padding byte, which the Rust tool's double round-trips, fails only
//     go_sdk_round_trip: the report fails, roundTrip passes, and the one reason is labelled go_sdk_round_trip.
//   - On the fixture corpus, whose bytes are not XDR, the Rust double passes every entry and the Go SDK fails every
//     one; each entry is named.
func TestRunAlsoGo(t *testing.T) {
	t.Run("real capture passes", func(t *testing.T) {
		folder := realCorpus(t, nil)
		o, err := Run(t.Context(), folder, corpusCodec(t, folder, nil), GoSDK)
		if err != nil {
			t.Fatal(err)
		}
		validateReport(t, o)
		e := o.Report.Entries[0]
		if !o.Report.Passed || len(o.Reasons) != 0 || e.GoSdkRoundTrip == nil || !e.GoSdkRoundTrip.Passed ||
			e.GoSdkRoundTrip.Check != contract.CheckNameGoSdkRoundTrip || e.GoSdkRoundTrip.BytesCompared != 1928 || !e.RoundTrip.Passed {
			t.Errorf("%+v %+v %v", o.Report, e.GoSdkRoundTrip, o.Reasons)
		}
		var out bytes.Buffer
		if err := WriteReport(&out, o); err != nil {
			t.Fatal(err)
		}
		s := out.String()
		if i, j := strings.Index(s, `"expectedValues"`), strings.Index(s, `"goSdkRoundTrip": {`); i < 0 || j < i {
			t.Errorf("goSdkRoundTrip is not reported after expectedValues (CHECK_ORDER):\n%s", s)
		}
	})
	t.Run("only the Go SDK fails", func(t *testing.T) {
		padded := readReal(t)
		padded[realPayloadPadding] = 0x01
		folder := realCorpus(t, padded)
		o, err := Run(t.Context(), folder, corpusCodec(t, folder, nil), GoSDK)
		if err != nil {
			t.Fatal(err)
		}
		validateReport(t, o)
		e := o.Report.Entries[0]
		if o.Report.Passed || !e.FileIntegrity.Passed || !e.RoundTrip.Passed || !e.ExpectedValues.Passed ||
			e.GoSdkRoundTrip == nil || e.GoSdkRoundTrip.Passed || e.GoSdkRoundTrip.BytesCompared != 0 {
			t.Errorf("%+v", e)
		}
		if len(o.Reasons) != 1 || !strings.HasPrefix(o.Reasons[0], "entry cap-0040-signed-payload-signer: go_sdk_round_trip: ") {
			t.Errorf("reasons %q: one, labelled go_sdk_round_trip", o.Reasons)
		}
		if s := Summary(o); !strings.HasPrefix(s, "1 of 1 entries failed; catalogue_schema passed; entry cap-0040-signed-payload-signer: go_sdk_round_trip: ") {
			t.Errorf("summary %q", s)
		}
	})
	t.Run("fixture corpus", func(t *testing.T) {
		folder := testdata + "/corpus/valid"
		o, err := Run(t.Context(), folder, corpusCodec(t, folder, nil), GoSDK)
		if err != nil {
			t.Fatal(err)
		}
		validateReport(t, o)
		if o.Report.Passed || len(o.Report.Entries) != 3 || len(o.Reasons) != 3 {
			t.Fatalf("%+v %q", o.Report, o.Reasons)
		}
		for i, e := range o.Report.Entries {
			if !e.RoundTrip.Passed || e.GoSdkRoundTrip == nil || e.GoSdkRoundTrip.Passed || e.GoSdkRoundTrip.BytesCompared != 0 {
				t.Errorf("%s: %+v %+v", e.ID, e.RoundTrip, e.GoSdkRoundTrip)
			}
			if !strings.HasPrefix(o.Reasons[i], "entry "+seedOrder[i]+": go_sdk_round_trip: ") {
				t.Errorf("reason %q", o.Reasons[i])
			}
		}
		if s := Summary(o); !strings.HasPrefix(s, "3 of 3 entries failed; catalogue_schema passed; ") {
			t.Errorf("summary %q", s)
		}
	})
	t.Run("an identity codec passes every entry", func(t *testing.T) {
		folder := testdata + "/corpus/valid"
		identity := GoCodec(func(_ string, b []byte) ([]byte, error) { return slices.Clone(b), nil })
		o, err := Run(t.Context(), folder, corpusCodec(t, folder, nil), identity)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range o.Report.Entries {
			if e.GoSdkRoundTrip == nil || !e.GoSdkRoundTrip.Passed || e.GoSdkRoundTrip.BytesCompared != e.FileIntegrity.Length {
				t.Errorf("%s: %+v", e.ID, e.GoSdkRoundTrip)
			}
		}
		if !o.Report.Passed {
			t.Errorf("%+v", o.Report)
		}
	})
	t.Run("a missing file", func(t *testing.T) {
		folder := realCorpus(t, nil)
		if err := os.Remove(filepath.Join(folder, model.SeedSignedPayloadSigner, "entry.xdr")); err != nil {
			t.Fatal(err)
		}
		o, err := Run(t.Context(), folder, corpusCodec(t, folder, nil), GoSDK)
		if err != nil {
			t.Fatal(err)
		}
		e := o.Report.Entries[0]
		if e.GoSdkRoundTrip == nil || e.GoSdkRoundTrip.Passed || e.GoSdkRoundTrip.BytesCompared != 0 {
			t.Errorf("%+v", e.GoSdkRoundTrip)
		}
	})
}

// TestGoSdkUnknownTypeIsTheCodecs: the name in the failure is the codec's own UnknownTypeError's type.
func TestGoSdkUnknownTypeIsTheCodecs(t *testing.T) {
	_, err := GoSDK("ScVal", []byte{0, 0, 0, 0})
	var u *xdrcodec.UnknownTypeError
	if !errors.As(err, &u) || u.Type != "ScVal" {
		t.Errorf("%v", err)
	}
}
