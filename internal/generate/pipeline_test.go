package generate

// The Generation pipeline, the Protocol selector and Single shape generation in the unit tier: the real pipeline,
// driven with stub recipes, a fake launcher and a fake decoder, so no container runs. Each recipe's Intended() is
// its real recipe's; the fake decoder writes a decoded form that holds those values at their pointers, or a changed
// one where a test says so.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/schema"
)

// The text-order submission of the deciding test, the feasibility run made before this tool, and the network's answer
// (status ERROR, txFAILED, CHANGE_TRUST_MALFORMED), as internal/catalogue's refusal test holds them.
const (
	decidingTextOrderEnvelope = "AAAAAgAAAABHkNor3ig51PdlHkIM4Y2NM3dLEk4QZcsGgutgDbA4bAAAAGQAAAAiAAAAAwAAAAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAABgAAAAMAAAAAAAAAAVVTRAAAAAAAP8C+p0Z0AnwzxH9Rb0Dno52w/xg/BHtR0MEYc8n+2CwAAAABVVNEAAAAAAAeFJFtRyqAPNR7qoSWtFvKor9CHvlxnFd0g4dQ3SPlhQAAAB5//////////wAAAAAAAAABDbA4bAAAAED90nNIEQOzH4TiUGJfNzNZ7PSNqdXjYxwTV+jQvUczLKmFmf24VwmitXzRrlWJY0qSkUiwZED7UK/MhRBOr9MN"
	decidingErrorResult       = "AAAAAAAAAGT/////AAAAAQAAAAAAAAAG/////wAAAAA="
)

// lowReserveResult is a TransactionResult whose one ChangeTrust failed with CHANGE_TRUST_LOW_RESERVE: a refusal for
// another reason than the one entry 2 records.
func lowReserveResult(t *testing.T) string {
	t.Helper()
	ops := []xdr.OperationResult{{Code: xdr.OperationResultCodeOpInner, Tr: &xdr.OperationResultTr{
		Type: xdr.OperationTypeChangeTrust, ChangeTrustResult: &xdr.ChangeTrustResult{Code: xdr.ChangeTrustResultCodeChangeTrustLowReserve}}}}
	r := xdr.TransactionResult{FeeCharged: 100, Result: xdr.TransactionResultResult{Code: xdr.TransactionResultCodeTxFailed, Results: &ops}}
	b, err := xdrcodec.Encode(&r)
	if err != nil {
		t.Fatal(err)
	}
	return xdrcodec.ToBase64(b)
}

// events is the ordered log of what the fakes saw.
type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = append(e.log, fmt.Sprintf(format, args...))
}

func (e *events) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.log...)
}

// fakeLauncher starts and stops fake quickstart networks, one at a time, as the runner would.
type fakeLauncher struct {
	ev       *events
	active   int
	startErr map[int]error // per protocol
	stopErr  error
}

func (l *fakeLauncher) StartQuickstart(ctx context.Context, image string, protocol int) (*orchestration.Network, error) {
	l.ev.add("start %d", protocol)
	if l.active != 0 {
		return nil, fmt.Errorf("a quickstart is already running; the names ltk-<runID>-net and -core are taken")
	}
	if err := l.startErr[protocol]; err != nil {
		return nil, err
	}
	l.active++
	return &orchestration.Network{Image: image, Protocol: protocol, Passphrase: orchestration.StandalonePassphrase, Name: "net-" + strconv.Itoa(protocol)}, nil
}

func (l *fakeLauncher) StopQuickstart(ctx context.Context, n *orchestration.Network) error {
	l.ev.add("stop %d", n.Protocol)
	if l.stopErr != nil {
		return l.stopErr
	}
	l.active--
	return nil
}

// fakeRPC carries the network's protocol to the stub recipes; its methods are never called.
type fakeRPC struct{ protocol int }

func (fakeRPC) GetLedgerEntries(context.Context, []string) (recipes.LedgerEntries, error) {
	return recipes.LedgerEntries{}, errors.New("not used")
}
func (fakeRPC) SendTransaction(context.Context, string) (recipes.SentTransaction, error) {
	return recipes.SentTransaction{}, errors.New("not used")
}
func (fakeRPC) GetTransaction(context.Context, string) (recipes.Transaction, error) {
	return recipes.Transaction{}, errors.New("not used")
}
func (fakeRPC) GetLedgers(context.Context, uint32, int) (recipes.Ledgers, error) {
	return recipes.Ledgers{}, errors.New("not used")
}

// stubRecipe is a seed's recipe with its real Intended() and a Run the test decides per protocol.
type stubRecipe struct {
	real recipes.Recipe
	ev   *events
	// run returns the capture at a protocol; nil means a capture that works.
	run func(ctx context.Context, protocol int) (recipes.Capture, error)
}

func (s stubRecipe) ID() string                           { return s.real.ID() }
func (s stubRecipe) Intended() map[string]json.RawMessage { return s.real.Intended() }
func (s stubRecipe) Run(ctx context.Context, net recipes.Network) (recipes.Capture, error) {
	p := net.RPC.(fakeRPC).protocol
	s.ev.add("run %s %d", s.ID(), p)
	if s.run != nil {
		return s.run(ctx, p)
	}
	return goodCapture(s.ID(), p), nil
}

// goodCapture is the capture of id at protocol: fake bytes that name both, and entry 2's refusal.
func goodCapture(id string, protocol int) recipes.Capture {
	c := recipes.Capture{EntryID: id, XdrType: "LedgerCloseMeta", XDR: []byte(fmt.Sprintf("xdr %s at %d", id, protocol)), Ledger: 7, Protocol: protocol}
	if id == model.SeedPoolShareTrustline {
		c.Refusal = &recipes.Refusal{EnvelopeXdr: decidingTextOrderEnvelope, ResultXdr: decidingErrorResult, Stage: "sendTransaction", Status: "ERROR"}
	}
	return c
}

// fakeDecoder decodes a fake capture to a document holding the recipe's intended values, plus one ledger-dependent
// leaf; change, when set, alters the values of a capture before they are written.
type fakeDecoder struct {
	ev     *events
	change func(raw string, values map[string]json.RawMessage)
	err    error
}

func (d *fakeDecoder) Decode(ctx context.Context, xdrType string, raw []byte) ([]byte, error) {
	d.ev.add("decode %s", raw)
	if d.err != nil {
		return nil, d.err
	}
	fields := strings.Fields(string(raw)) // "xdr <id> at <protocol>"
	rec, ok := recipes.ByID(fields[1])
	if !ok {
		return nil, fmt.Errorf("unknown capture %q", raw)
	}
	values := rec.Intended()
	values["/v2/ledger_header/header/ledger_seq"] = json.RawMessage(fields[3]) // differs between the two networks
	if d.change != nil {
		d.change(string(raw), values)
	}
	return document(values), nil
}

// document builds the JSON document holding each value at its pointer; a numeric segment is an array index.
func document(values map[string]json.RawMessage) []byte {
	var root any = map[string]any{}
	set := func(ptr string, v json.RawMessage) {
		toks := strings.Split(ptr[1:], "/")
		var put func(node any, i int) any
		put = func(node any, i int) any {
			if i == len(toks) {
				return v
			}
			if n, err := strconv.Atoi(toks[i]); err == nil {
				arr, _ := node.([]any)
				for len(arr) <= n {
					arr = append(arr, map[string]any{})
				}
				arr[n] = put(arr[n], i+1)
				return arr
			}
			obj, ok := node.(map[string]any)
			if !ok {
				obj = map[string]any{}
			}
			obj[toks[i]] = put(obj[toks[i]], i+1)
			return obj
		}
		root = put(root, 0)
	}
	for _, p := range sortedKeys(values) {
		set(p, values[p])
	}
	b, err := json.Marshal(root)
	if err != nil {
		panic(err)
	}
	return b
}

// harness is one pipeline over stub recipes.
type harness struct {
	ev      *events
	launch  *fakeLauncher
	decoder *fakeDecoder
	recipes []recipes.Recipe
	p       pipeline
}

// newHarness makes the pipeline of the three seeds with captures that work, unless runs says otherwise.
func newHarness(runs map[string]func(ctx context.Context, protocol int) (recipes.Capture, error)) *harness {
	ev := &events{}
	h := &harness{ev: ev, launch: &fakeLauncher{ev: ev}, decoder: &fakeDecoder{ev: ev}}
	for _, r := range recipes.Registry() {
		h.recipes = append(h.recipes, stubRecipe{real: r, ev: ev, run: runs[r.ID()]})
	}
	h.p = pipeline{launch: h.launch, decoder: h.decoder, plans: protocolPlans(),
		connect: func(n *orchestration.Network) recipes.Network {
			return recipes.Network{RPC: fakeRPC{protocol: n.Protocol}, Passphrase: n.Passphrase}
		}}
	return h
}

// only returns the stub recipe of id.
func (h *harness) only(id string) []recipes.Recipe {
	for _, r := range h.recipes {
		if r.ID() == id {
			return []recipes.Recipe{r}
		}
	}
	panic(id)
}

// notApplied is a recipe failing at its transaction: the first two conditions of "cannot work" (refused, or not a
// success).
func notApplied(id string) error {
	return fmt.Errorf("wrapped: %w", &recipes.NotAppliedError{What: id + ": the shape's transaction", TxHash: "ab", Stage: "getTransaction", Status: "FAILED", ResultXdr: "AAAA"})
}

// failAt makes a recipe fail with err at the protocols listed, and work at the others.
func failAt(err error, protocols ...int) func(context.Context, int) (recipes.Capture, error) {
	return func(_ context.Context, p int) (recipes.Capture, error) {
		for _, q := range protocols {
			if p == q {
				return recipes.Capture{}, err
			}
		}
		return recipes.Capture{}, nil // replaced below
	}
}

// fixRun completes failAt: a zero capture means the good one of id.
func fixRun(id string, f func(context.Context, int) (recipes.Capture, error)) func(context.Context, int) (recipes.Capture, error) {
	return func(ctx context.Context, p int) (recipes.Capture, error) {
		c, err := f(ctx, p)
		if err == nil && c.EntryID == "" {
			return goodCapture(id, p), nil
		}
		return c, err
	}
}

var (
	e1 = model.SeedSignedPayloadSigner
	e2 = model.SeedPoolShareTrustline
	e3 = model.SeedMuxedAccount
)

// files reads every file under dir, by slash path.
func files(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return out
}

func readCat(t *testing.T, dir string) contract.Catalogue {
	t.Helper()
	cat, err := catalogue.ReadCatalogue(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func ids(cat contract.Catalogue) []string {
	var out []string
	for _, e := range cat.Entries {
		out = append(out, e.ID)
	}
	return out
}

func ok(id string, protocol int, fallback bool) contract.ShapeGenerationResult {
	return contract.ShapeGenerationResult{ID: id, ProtocolVersion: protocol, ProtocolFallback: fallback, Generated: true}
}

// assertSchema validates the written catalogue as `ltk check`'s catalogue_schema does.
func assertSchema(t *testing.T, dir string) {
	t.Helper()
	res, _ := schema.CheckCatalogue(dir)
	if !res.Passed {
		t.Errorf("the written catalogue fails catalogue_schema: %+v", res.Failures)
	}
}

// TestPipelineAtTheCurrentProtocol: with every recipe working, the
// current network starts, the shapes run in seed order, each capture is decoded, the network is removed, and only
// then are the files and the catalogue written; nothing starts at 28. Every record is at the captured protocol with
// protocolFallback false, holds the split expected values, the bytes' hash and length, and (entry 2 only) the
// refusal evidence catalogue.RefusalEvidence makes; the catalogue passes the schema.
func TestPipelineAtTheCurrentProtocol(t *testing.T) {
	h := newHarness(nil)
	out := filepath.Join(t.TempDir(), "gen") // absent: created
	got, err := h.p.run(context.Background(), h.recipes, out, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []contract.ShapeGenerationResult{ok(e1, 30, false), ok(e2, 30, false), ok(e3, 30, false)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results %+v; want %+v", got, want)
	}
	wantEvents := []string{"start 30",
		"run " + e1 + " 30", "decode xdr " + e1 + " at 30",
		"run " + e2 + " 30", "decode xdr " + e2 + " at 30",
		"run " + e3 + " 30", "decode xdr " + e3 + " at 30",
		"stop 30"}
	if ev := h.ev.all(); !reflect.DeepEqual(ev, wantEvents) {
		t.Errorf("events %q; want %q", ev, wantEvents)
	}
	if h.launch.active != 0 {
		t.Error("the pipeline returned with its quickstart running")
	}
	cat := readCat(t, out)
	if !reflect.DeepEqual(ids(cat), []string{e1, e2, e3}) {
		t.Errorf("catalogue ids %v", ids(cat))
	}
	fs := files(t, out)
	for _, e := range cat.Entries {
		seed, _ := model.SeedByID(e.ID)
		raw := fs[e.FilePath]
		sha, n := catalogue.Measure(raw)
		if e.FilePath != e.ID+"/entry.xdr" || string(raw) != "xdr "+e.ID+" at 30" || e.Sha256 != sha || e.Length != n {
			t.Errorf("%s: file %s holds %q, recorded %s/%d", e.ID, e.FilePath, raw, e.Sha256, e.Length)
		}
		if e.Cap != seed.Cap || e.XdrType != "LedgerCloseMeta" || e.Synthetic || e.ProtocolVersion != 30 || e.ProtocolFallback {
			t.Errorf("%s: record %+v", e.ID, e)
		}
		if !strings.Contains(e.Source, orchestration.QuickstartCurrent) {
			t.Errorf("%s: source %q does not name the image", e.ID, e.Source)
		}
		if len(e.ExpectedDecodedValues.ReproducedByEveryRun) != len(seedRecipe(e.ID).Intended()) || len(e.ExpectedDecodedValues.LedgerDependent) != 1 {
			t.Errorf("%s: expected values %+v", e.ID, e.ExpectedDecodedValues)
		}
		if !json.Valid(fs[e.ID+"/entry.json"]) {
			t.Errorf("%s: no decoded JSON beside the XDR file", e.ID)
		}
		switch {
		case e.ID == e2:
			ev, err := catalogue.RefusalEvidence(decidingTextOrderEnvelope, decidingErrorResult)
			if err != nil || e.NetworkRefusalEvidence == nil || *e.NetworkRefusalEvidence != ev {
				t.Errorf("entry 2's evidence %+v; want %+v (%v)", e.NetworkRefusalEvidence, ev, err)
			}
		case e.NetworkRefusalEvidence != nil:
			t.Errorf("%s records refusal evidence", e.ID)
		}
	}
	if _, has := fs[catalogue.SchemaCopyFileName]; !has {
		t.Error("no schema copy was written with the catalogue")
	}
	assertSchema(t, out)
}

func seedRecipe(id string) recipes.Recipe {
	r, _ := recipes.ByID(id)
	return r
}

// TestPipelineFallsBackTo28: a shape whose transaction is not applied at the
// current protocol is set aside; after the current network is removed, the protocol-28 network starts and the shape
// runs there alone. Its result and record carry protocolVersion 28 and protocolFallback true; the others stay at 30.
func TestPipelineFallsBackTo28(t *testing.T) {
	h := newHarness(map[string]func(context.Context, int) (recipes.Capture, error){
		e3: fixRun(e3, failAt(notApplied(e3), 30)),
	})
	out := t.TempDir()
	got, err := h.p.run(context.Background(), h.recipes, out, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []contract.ShapeGenerationResult{ok(e1, 30, false), ok(e2, 30, false), ok(e3, 28, true)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results %+v; want %+v", got, want)
	}
	ev := h.ev.all()
	tail := []string{"run " + e3 + " 30", "stop 30", "start 28", "run " + e3 + " 28", "decode xdr " + e3 + " at 28", "stop 28"}
	if len(ev) < len(tail) || !reflect.DeepEqual(ev[len(ev)-len(tail):], tail) {
		t.Errorf("events %q; want them to end with %q", ev, tail)
	}
	cat := readCat(t, out)
	if e := cat.Entries[2]; e.ID != e3 || e.ProtocolVersion != 28 || !e.ProtocolFallback || !strings.Contains(e.Source, orchestration.QuickstartProtocol28) {
		t.Errorf("entry 3's record %+v", e)
	}
	if string(files(t, out)[e3+"/entry.xdr"]) != "xdr "+e3+" at 28" {
		t.Error("entry 3's file is not its protocol-28 capture")
	}
	assertSchema(t, out)
}

// TestPipelineFailsAtBothProtocols: a shape that cannot work at 30 or at 28 has generated
// false and an error naming both attempts; its record and folder are not written, the other shapes' are, and the
// catalogue they make passes the schema.
func TestPipelineFailsAtBothProtocols(t *testing.T) {
	h := newHarness(map[string]func(context.Context, int) (recipes.Capture, error){
		e2: fixRun(e2, failAt(notApplied(e2), 30, 28)),
	})
	out := t.TempDir()
	got, err := h.p.run(context.Background(), h.recipes, out, false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != ok(e1, 30, false) || got[2] != ok(e3, 30, false) {
		t.Errorf("results %+v", got)
	}
	r := got[1]
	if r.ID != e2 || r.Generated || r.ProtocolVersion != 28 || !r.ProtocolFallback || r.Error == nil ||
		!strings.Contains(*r.Error, "at protocol 30: ") || !strings.Contains(*r.Error, "at protocol 28: ") || !strings.Contains(*r.Error, "was not applied") {
		t.Errorf("entry 2's result %+v (%v)", r, deref(r.Error))
	}
	if h.launch.active != 0 {
		t.Error("a quickstart is still running")
	}
	cat := readCat(t, out)
	if !reflect.DeepEqual(ids(cat), []string{e1, e3}) {
		t.Errorf("catalogue ids %v", ids(cat))
	}
	if _, err := os.Stat(filepath.Join(out, e2)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("entry 2's folder was written: %v", err)
	}
	assertSchema(t, out)
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestPipelineIntendedMismatchCannotWork: a capture whose reproduced value differs from Intended() cannot work at that
// protocol (the third condition) and is retried at 28, where it matches.
func TestPipelineIntendedMismatchCannotWork(t *testing.T) {
	h := newHarness(nil)
	weight := "/v2/tx_set/v1/phases/0/v0/0/txset_comp_txs_maybe_discounted_fee/txs/0/tx/tx/operations/0/body/set_options/signer/weight"
	h.decoder.change = func(raw string, values map[string]json.RawMessage) {
		if raw == "xdr "+e1+" at 30" {
			values[weight] = json.RawMessage(`2`)
		}
	}
	got, err := h.p.run(context.Background(), h.recipes, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != ok(e1, 28, true) {
		t.Errorf("entry 1's result %+v", got[0])
	}
	// At both: not generated, the mismatch named.
	h = newHarness(nil)
	h.decoder.change = func(raw string, values map[string]json.RawMessage) {
		if strings.Contains(raw, e1) {
			values[weight] = json.RawMessage(`2`)
		}
	}
	got, err = h.p.run(context.Background(), h.recipes, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Generated || !strings.Contains(deref(got[0].Error), weight+" is 2 in the capture; the recipe submitted 1") {
		t.Errorf("entry 1's result %+v (%s)", got[0], deref(got[0].Error))
	}
}

// TestCheckIntended: every intended pointer must be in the reproduced group with its value (numbers as their decimal
// text, strings once decoded), and the group must hold nothing the recipe does not submit.
func TestCheckIntended(t *testing.T) {
	intended := map[string]json.RawMessage{"/a": json.RawMessage(`"x"`), "/b": json.RawMessage(`100000000`)}
	rv := func(p, v string) contract.ReproducedValue {
		return contract.ReproducedValue{Pointer: p, Value: json.RawMessage(v)}
	}
	if err := checkIntended("e", intended, []contract.ReproducedValue{rv("/b", ` 100000000`), rv("/a", `"x"`)}); err != nil {
		t.Errorf("equal values: %v", err)
	}
	for name, c := range map[string]struct {
		got  []contract.ReproducedValue
		want string
	}{
		"missing": {[]contract.ReproducedValue{rv("/a", `"x"`)}, "/b is not in the capture (want 100000000)"},
		"differs": {[]contract.ReproducedValue{rv("/a", `"y"`), rv("/b", `100000000`)}, `/a is "y" in the capture; the recipe submitted "x"`},
		"extra":   {[]contract.ReproducedValue{rv("/a", `"x"`), rv("/b", `100000000`), rv("/c", `1`)}, "/c is a reproduced value of the capture that the recipe does not submit"},
	} {
		err := checkIntended("e", intended, c.got)
		var m *IntendedMismatchError
		if !errors.As(err, &m) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an IntendedMismatchError saying %q", name, err, c.want)
		}
	}
}

// TestPipelineOtherFailuresAreNotRetried: a failure outside the three conditions of "cannot work" (here the
// friendbot) is not the protocol's: the shape is not generated at 30 and no protocol-28 network starts.
func TestPipelineOtherFailuresAreNotRetried(t *testing.T) {
	h := newHarness(map[string]func(context.Context, int) (recipes.Capture, error){
		e1: failAt(errors.New(e1+": funding: friendbot funding G…: HTTP 500"), 30),
	})
	h.decoder.err = nil
	got, err := h.p.run(context.Background(), h.recipes, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if r := got[0]; r.Generated || r.ProtocolVersion != 30 || r.ProtocolFallback || !strings.Contains(deref(r.Error), "at protocol 30: "+e1+": funding") {
		t.Errorf("entry 1's result %+v (%s)", r, deref(r.Error))
	}
	for _, e := range h.ev.all() {
		if e == "start 28" {
			t.Error("a protocol-28 network started for a failure that is not the protocol's")
		}
	}
	// A decoder that fails is not the protocol's either.
	h = newHarness(nil)
	h.decoder.err = errors.New("stellar-xdr decode --type LedgerCloseMeta failed: boom")
	got, err = h.p.run(context.Background(), h.recipes, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Generated || r.ProtocolFallback || !strings.Contains(deref(r.Error), "decoding the capture of ledger 7 with the Rust XDR tool") {
			t.Errorf("%+v (%s)", r, deref(r.Error))
		}
	}
}

// TestPipelineEntry2Refusal: entry 2's evidence goes through catalogue.RefusalEvidence. The
// text-order form accepted (recipes.NotRefusedError), or refused for another reason, leaves the shape's recorded value
// unproduced: it cannot work at that protocol and is retried at 28.
func TestPipelineEntry2Refusal(t *testing.T) {
	for name, at30 := range map[string]func(t *testing.T) (recipes.Capture, error){
		"accepted": func(*testing.T) (recipes.Capture, error) {
			return recipes.Capture{}, &recipes.NotRefusedError{ID: e2, TxHash: "cd", Ledger: 9}
		},
		"another code": func(t *testing.T) (recipes.Capture, error) {
			c := goodCapture(e2, 30)
			c.Refusal.ResultXdr = lowReserveResult(t)
			return c, nil
		},
	} {
		h := newHarness(map[string]func(context.Context, int) (recipes.Capture, error){
			e2: func(_ context.Context, p int) (recipes.Capture, error) {
				if p == 30 {
					return at30(t)
				}
				return goodCapture(e2, p), nil
			},
		})
		out := t.TempDir()
		got, err := h.p.run(context.Background(), h.recipes, out, false)
		if err != nil {
			t.Fatal(err)
		}
		if got[1] != ok(e2, 28, true) {
			t.Errorf("%s: entry 2's result %+v", name, got[1])
		}
		if e := readCat(t, out).Entries[1]; e.NetworkRefusalEvidence == nil || e.NetworkRefusalEvidence.ResultCode != "CHANGE_TRUST_MALFORMED" {
			t.Errorf("%s: entry 2's evidence %+v", name, e.NetworkRefusalEvidence)
		}
	}
}

// TestPipelineNothingGenerated: when no shape is generated nothing is written, not even the folder.
func TestPipelineNothingGenerated(t *testing.T) {
	runs := map[string]func(context.Context, int) (recipes.Capture, error){}
	for _, id := range []string{e1, e2, e3} {
		runs[id] = failAt(notApplied(id), 30, 28)
	}
	h := newHarness(runs)
	out := filepath.Join(t.TempDir(), "gen")
	got, err := h.p.run(context.Background(), h.recipes, out, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Generated || r.Error == nil {
			t.Errorf("%+v", r)
		}
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the folder was created: %v", err)
	}
}

// TestPipelineInterrupt: an interrupt during a shape stops the run at once with the context's
// error in the chain (exit 1 by the mapper), no later shape runs, nothing is written; the network is left to the
// command's reaper, which removes it on every path.
func TestPipelineInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHarness(map[string]func(context.Context, int) (recipes.Capture, error){
		e2: func(ctx context.Context, p int) (recipes.Capture, error) {
			cancel()
			return recipes.Capture{}, fmt.Errorf("getTransaction ab: %w", ctx.Err())
		},
	})
	out := filepath.Join(t.TempDir(), "gen")
	got, err := h.p.run(ctx, h.recipes, out, false)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("got %+v, %v; want no results and the cancellation", got, err)
	}
	for _, e := range h.ev.all() {
		if strings.HasPrefix(e, "run "+e3) || e == "start 28" {
			t.Errorf("%q after the interrupt", e)
		}
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the folder was written: %v", err)
	}
	// A recipe whose error does not wrap the cancellation is still reported as the interrupt.
	ctx2, cancel2 := context.WithCancel(context.Background())
	h = newHarness(map[string]func(context.Context, int) (recipes.Capture, error){
		e1: func(context.Context, int) (recipes.Capture, error) {
			cancel2()
			return recipes.Capture{}, errors.New("docker exec: signal: killed")
		},
	})
	if _, err := h.p.run(ctx2, h.recipes, t.TempDir(), false); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "killed") {
		t.Errorf("%v", err)
	}
}

// TestPipelineNetworks: a network that does not start fails every shape that was to run on it (no fallback, nothing
// written); a network that cannot be removed stops the run with an error before anything is written.
func TestPipelineNetworks(t *testing.T) {
	h := newHarness(nil)
	h.launch.startErr = map[int]error{30: errors.New("quickstart ltk-1-core was not ready after 5m0s (waiting for friendbot)")}
	out := filepath.Join(t.TempDir(), "gen")
	got, err := h.p.run(context.Background(), h.recipes, out, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Generated || r.ProtocolVersion != 30 || r.ProtocolFallback || !strings.Contains(deref(r.Error), "at protocol 30: the network did not start: quickstart") {
			t.Errorf("%+v (%s)", r, deref(r.Error))
		}
	}
	if ev := h.ev.all(); !reflect.DeepEqual(ev, []string{"start 30"}) {
		t.Errorf("events %q", ev)
	}
	// The fallback network does not start: the set-aside shape fails with both reasons.
	h = newHarness(map[string]func(context.Context, int) (recipes.Capture, error){e3: fixRun(e3, failAt(notApplied(e3), 30))})
	h.launch.startErr = map[int]error{28: errors.New("not ready")}
	got, err = h.p.run(context.Background(), h.recipes, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if r := got[2]; r.Generated || r.ProtocolVersion != 28 || !r.ProtocolFallback || !strings.Contains(deref(r.Error), "at protocol 30: ") || !strings.Contains(deref(r.Error), "at protocol 28: the network did not start: not ready") {
		t.Errorf("%+v (%s)", r, deref(r.Error))
	}
	if !got[0].Generated || !got[1].Generated {
		t.Errorf("%+v", got)
	}

	h = newHarness(nil)
	h.launch.stopErr = errors.New("docker network rm ltk-1-net: network has active endpoints")
	got, err = h.p.run(context.Background(), h.recipes, out, false)
	if err == nil || got != nil || !strings.Contains(err.Error(), "removing the protocol-30 generation network before the checks") {
		t.Errorf("%+v %v", got, err)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the folder was written: %v", err)
	}
}

// TestPipelineWriteFailures (the catalogue writer's failures at the pipeline's level; a directory put in a file's place
// makes a write fail even for root): an --out that is a file stops the run before any network starts; a directory where
// entry 2's decoded JSON goes stops it after the networks were removed, with an error naming the path and no catalogue
// written; a directory where the catalogue goes leaves the entries and no catalogue. A second run into the repaired
// folder writes it whole.
func TestPipelineWriteFailures(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(nil)
	if _, err := h.p.run(context.Background(), h.recipes, file, false); err == nil || len(h.ev.all()) != 0 {
		t.Errorf("an --out that is a file: %v, events %q", err, h.ev.all())
	}

	out := filepath.Join(dir, "gen")
	if err := os.MkdirAll(filepath.Join(out, e2, "entry.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	h = newHarness(nil)
	got, err := h.p.run(context.Background(), h.recipes, out, false)
	if err == nil || !strings.Contains(err.Error(), e2+"/entry.json") || len(got) != 3 {
		t.Errorf("%v (%d results)", err, len(got))
	}
	if h.launch.active != 0 || h.ev.all()[len(h.ev.all())-1] != "stop 30" {
		t.Error("the write failed before the network was removed")
	}
	if _, err := os.Stat(filepath.Join(out, catalogue.CatalogueFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a catalogue was written after a failed entry write: %v", err)
	}
	if err := os.Remove(filepath.Join(out, e2, "entry.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := newHarness(nil).p.run(context.Background(), h.recipes, out, false); err != nil {
		t.Fatalf("the second run: %v", err)
	}
	assertSchema(t, out)

	out2 := filepath.Join(dir, "gen2")
	if err := os.MkdirAll(filepath.Join(out2, catalogue.CatalogueFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	h = newHarness(nil)
	if _, err := h.p.run(context.Background(), h.recipes, out2, false); err == nil || !strings.Contains(err.Error(), catalogue.CatalogueFileName) {
		t.Errorf("a directory where the catalogue goes: %v", err)
	}
	if fs := files(t, out2); len(fs[e1+"/entry.xdr"]) == 0 {
		t.Error("the entries were not written before the catalogue")
	}
}

// TestRunShapeMergesOneRecord: into a folder that holds a catalogue,
// --shape writes only its entry's two files and replaces only its record; every other file is byte for byte as it
// was, the catalogue's other records included, and no other shape runs.
func TestRunShapeMergesOneRecord(t *testing.T) {
	out := t.TempDir()
	h := newHarness(nil)
	if _, err := h.p.run(context.Background(), h.recipes, out, false); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(out, e3, "notes.txt") // a file of someone else's in the entry's folder
	if err := os.WriteFile(extra, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := files(t, out)
	catBefore := readCat(t, out)

	h = newHarness(map[string]func(context.Context, int) (recipes.Capture, error){e3: fixRun(e3, failAt(notApplied(e3), 30))})
	got, err := h.p.run(context.Background(), h.only(e3), out, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []contract.ShapeGenerationResult{ok(e3, 28, true)}) {
		t.Errorf("results %+v", got)
	}
	for _, e := range h.ev.all() {
		if strings.HasPrefix(e, "run ") && !strings.HasPrefix(e, "run "+e3) {
			t.Errorf("another shape ran: %q", e)
		}
	}
	after := files(t, out)
	changed := map[string]bool{}
	for name, b := range after {
		if !bytes.Equal(before[name], b) {
			changed[name] = true
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			changed[name] = true
		}
	}
	want := map[string]bool{e3 + "/entry.xdr": true, e3 + "/entry.json": true, catalogue.CatalogueFileName: true}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("changed files %v; want %v", changed, want)
	}
	catAfter := readCat(t, out)
	if !reflect.DeepEqual(catAfter.Entries[:2], catBefore.Entries[:2]) || catAfter.Entries[2].ProtocolVersion != 28 || !catAfter.Entries[2].ProtocolFallback {
		t.Errorf("catalogue after the merge %+v", catAfter.Entries)
	}
	// The other records' bytes: the catalogue is the layout writer's form of the merged records.
	again, _ := catalogue.EncodeCatalogue(catAfter)
	if !bytes.Equal(again, after[catalogue.CatalogueFileName]) {
		t.Error("the merged catalogue is not in the layout writer's form")
	}
	for _, rec := range catBefore.Entries[:2] {
		b, _ := json.MarshalIndent(rec, "    ", "  ")
		if !bytes.Contains(after[catalogue.CatalogueFileName], b) {
			t.Errorf("record %s's bytes changed", rec.ID)
		}
	}
	assertSchema(t, out)
}

// TestRunShapeNewAndAddedRecords: into an absent folder --shape writes a catalogue of that one record; into a folder
// whose catalogue lacks the id, the record is added in seed order and the others are kept.
func TestRunShapeNewAndAddedRecords(t *testing.T) {
	out := filepath.Join(t.TempDir(), "new")
	h := newHarness(nil)
	if _, err := h.p.run(context.Background(), h.only(e2), out, true); err != nil {
		t.Fatal(err)
	}
	if cat := readCat(t, out); !reflect.DeepEqual(ids(cat), []string{e2}) {
		t.Errorf("ids %v", ids(cat))
	}
	assertSchema(t, out)
	if _, err := h.p.run(context.Background(), h.only(e3), out, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.run(context.Background(), h.only(e1), out, true); err != nil {
		t.Fatal(err)
	}
	if cat := readCat(t, out); !reflect.DeepEqual(ids(cat), []string{e1, e2, e3}) {
		t.Errorf("ids %v", ids(cat))
	}
	// A record of an id that is not a seed's stays where it is; a seed is inserted before the later seeds.
	cat := mergeRecords(contract.Catalogue{Entries: []contract.CatalogueEntry{{ID: e3}, {ID: "other"}}}, []contract.CatalogueEntry{{ID: e1}})
	if !reflect.DeepEqual(ids(cat), []string{e1, e3, "other"}) {
		t.Errorf("ids %v", ids(cat))
	}
}

// TestRunShapeRefusesAForeignCatalogue: a catalogue the merge would have to re-shape
// (not readable, an id twice, another form) is refused before any network starts, and nothing is written.
func TestRunShapeRefusesAForeignCatalogue(t *testing.T) {
	good := t.TempDir()
	if _, err := newHarness(nil).p.run(context.Background(), newHarness(nil).recipes, good, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(good, catalogue.CatalogueFileName))
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	cat := readCat(t, good)
	cat.Entries = append(cat.Entries, cat.Entries[0])
	twice, _ := catalogue.EncodeCatalogue(cat)
	for name, content := range map[string][]byte{
		"not JSON":      []byte("{not json"),
		"another form":  compact.Bytes(),
		"an id twice":   twice,
		"CRLF endings":  bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n")),
		"unknown field": bytes.Replace(raw, []byte(`"entries"`), []byte(`"x": 1, "entries"`), 1),
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, catalogue.CatalogueFileName), content, 0o644); err != nil {
			t.Fatal(err)
		}
		h := newHarness(nil)
		_, err := h.p.run(context.Background(), h.only(e3), dir, true)
		var coded *model.CodedError
		if !errors.As(err, &coded) || coded.Code != contract.ErrorCodeFailure || !strings.Contains(err.Error(), "nothing was started") {
			t.Errorf("%s: %v", name, err)
		}
		if len(h.ev.all()) != 0 {
			t.Errorf("%s: events %q", name, h.ev.all())
		}
		if fs := files(t, dir); len(fs) != 1 {
			t.Errorf("%s: files %v", name, fs)
		}
	}
}

// TestRunShapeFailedWritesNothing: a --shape run whose shape is not generated leaves the folder as it
// was.
func TestRunShapeFailedWritesNothing(t *testing.T) {
	out := t.TempDir()
	if _, err := newHarness(nil).p.run(context.Background(), newHarness(nil).recipes, out, false); err != nil {
		t.Fatal(err)
	}
	before := files(t, out)
	h := newHarness(map[string]func(context.Context, int) (recipes.Capture, error){e1: failAt(notApplied(e1), 30, 28)})
	got, err := h.p.run(context.Background(), h.only(e1), out, true)
	if err != nil || len(got) != 1 || got[0].Generated {
		t.Fatalf("%+v %v", got, err)
	}
	if !reflect.DeepEqual(files(t, out), before) {
		t.Error("the folder changed")
	}
}

// TestRunShapeUnknownID (the pipeline's guard): an id no recipe has is a usage error; nothing is
// started (no runner is even needed) and nothing is written.
func TestRunShapeUnknownID(t *testing.T) {
	out := filepath.Join(t.TempDir(), "gen")
	_, err := RunShape(context.Background(), nil, nil, recipes.Registry(), "cap-0099-unknown", out)
	var coded *model.CodedError
	if !errors.As(err, &coded) || coded.Code != contract.ErrorCodeUsageError || !strings.Contains(err.Error(), "cap-0099-unknown") {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%v", err)
	}
}

// TestProtocolPlans: the selector tries the current quickstart at CurrentProtocol, then the tested quickstart at 28,
// the fallback; the production pipeline uses them and connects the recipes to the network's RPC and
// friendbot on its passphrase.
func TestProtocolPlans(t *testing.T) {
	want := []protocolPlan{
		{image: orchestration.QuickstartCurrent, protocol: orchestration.CurrentProtocol},
		{image: orchestration.QuickstartProtocol28, protocol: contract.FALLBACK_PROTOCOL_VERSION, fallback: true},
	}
	if got := protocolPlans(); !reflect.DeepEqual(got, want) {
		t.Errorf("%+v", got)
	}
	p := newPipeline(nil, nil)
	if !reflect.DeepEqual(p.plans, want) {
		t.Errorf("%+v", p.plans)
	}
	n := p.connect(&orchestration.Network{RPCURL: "http://127.0.0.1:1/rpc", FriendbotURL: "http://127.0.0.1:1/friendbot", Passphrase: orchestration.StandalonePassphrase})
	if rpc, ok := n.RPC.(*RPCClient); !ok || rpc.url != "http://127.0.0.1:1/rpc" {
		t.Errorf("RPC %+v", n.RPC)
	}
	if fb, ok := n.Friendbot.(*Friendbot); !ok || fb.url != "http://127.0.0.1:1/friendbot" || n.Passphrase != orchestration.StandalonePassphrase {
		t.Errorf("friendbot %+v, passphrase %q", n.Friendbot, n.Passphrase)
	}
}
