package main

// A fake quickstart for `ltk generate` at the process boundary in the unit tier: the RPC and the friendbot the program
// reaches on the address the fake docker's `port` reports (FAKE_DOCKER_PORTS), served by the test process. It answers
// the readiness probe (getHealth, getNetwork at its protocol on the standalone passphrase, the friendbot) and the four
// RPC methods the recipes call. It applies only entry 1's shape, a SetOptions: its capture is the real protocol-30
// capture of testdata/real with the submitted envelope and its hash put in place of the probe's, and its decoded JSON
// is the Rust XDR tool's decoding of that capture with the probe's source account, signer key and hash replaced by the
// submitted ones. Both are written as a corpus entry the fake `stellar-xdr` reads (FAKE_DOCKER_CORPUS), so the pipeline
// decodes the capture, finds entry 1's intended values in it and writes it, and the checks pass on it. Every other
// transaction is refused (sendTransaction ERROR): entries 2 and 3 cannot work on it, at either protocol. A fallback
// test (fallback_test.go) makes the current network refuse entry 1 as well and the protocol-28 one apply it, its
// capture's header then stating protocol 28.

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

const (
	realCaptureXDR  = "../../testdata/real/capture-p30.xdr"
	realCaptureJSON = "../../testdata/real/capture-p30.json"
	// fakeSequence is the sequence number every funded account has.
	fakeSequence = 3 << 32
)

// fakeStellar is one fake quickstart network.
type fakeStellar struct {
	t        *testing.T
	protocol int
	srv      *httptest.Server
	// corpus is the folder the fake stellar-xdr reads the capture's decoding from.
	corpus string

	mu     sync.Mutex
	funded map[string]bool
	ledger uint32            // the ledger that applies entry 1's shape: the real capture's
	txs    map[string]uint32 // applied transaction hash → ledger
	meta   map[uint32]string // ledger → metadataXdr

	// sends receives one value per sendTransaction, before it is answered.
	sends chan string
	// hangSend makes sendTransaction answer only when its request is cancelled (the program interrupted).
	hangSend bool
	// applies is whether the network applies entry 1's shape: by default only the network at the current protocol
	// does; a test of the fallback (fallback_test.go) refuses it there and applies it at 28 instead.
	applies bool
}

// newFakeStellar starts a fake quickstart at protocol, writing its captures under corpus.
func newFakeStellar(t *testing.T, protocol int, corpus string) *fakeStellar {
	t.Helper()
	f := &fakeStellar{t: t, protocol: protocol, corpus: corpus, funded: map[string]bool{}, txs: map[string]uint32{},
		meta: map[uint32]string{}, sends: make(chan string, 64), applies: protocol == orchestration.CurrentProtocol}
	raw, err := os.ReadFile(realCaptureXDR)
	if err != nil {
		t.Fatal(err)
	}
	var lcm xdr.LedgerCloseMeta
	if err := xdr.SafeUnmarshal(raw, &lcm); err != nil {
		t.Fatal(err)
	}
	f.ledger = lcm.LedgerSequence()
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// addr is the server's host:port, as `docker port` prints it.
func (f *fakeStellar) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }

func (f *fakeStellar) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/friendbot":
		addr := r.URL.Query().Get("addr")
		if addr == "" {
			http.Error(w, "missing addr", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.funded[addr] = true
		f.mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	case "/rpc":
		f.rpc(w, r)
	default:
		http.NotFound(w, r)
	}
}

// rpc answers one JSON-RPC 2.0 call.
func (f *fakeStellar) rpc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := f.call(r.Context(), req.Method, req.Params)
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if err != nil {
		resp["error"] = map[string]any{"code": -32600, "message": err.Error()}
	} else {
		resp["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeStellar) call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "getHealth":
		return map[string]any{"status": "healthy"}, nil
	case "getNetwork":
		return map[string]any{"passphrase": orchestration.StandalonePassphrase, "protocolVersion": f.protocol}, nil
	case "getLedgerEntries":
		var p struct {
			Keys []string `json:"keys"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		entries := []map[string]any{}
		for _, k := range p.Keys {
			var lk xdr.LedgerKey
			if err := xdr.SafeUnmarshalBase64(k, &lk); err != nil {
				return nil, err
			}
			acc, ok := lk.GetAccount()
			if !ok || !f.funded[acc.AccountId.Address()] {
				continue
			}
			data := xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeAccount, Account: &xdr.AccountEntry{
				AccountId: acc.AccountId, Balance: 100_000_000_000, SeqNum: fakeSequence}}
			b64, err := xdr.MarshalBase64(data)
			if err != nil {
				return nil, err
			}
			entries = append(entries, map[string]any{"key": k, "xdr": b64, "lastModifiedLedgerSeq": f.ledger - 1})
		}
		return map[string]any{"entries": entries, "latestLedger": f.ledger}, nil
	case "sendTransaction":
		var p struct {
			Transaction string `json:"transaction"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		f.sends <- p.Transaction
		if f.hangSend {
			f.mu.Unlock()
			<-ctx.Done()
			f.mu.Lock()
			return nil, ctx.Err()
		}
		return f.send(p.Transaction)
	case "getTransaction":
		var p struct {
			Hash string `json:"hash"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if l, ok := f.txs[p.Hash]; ok {
			return map[string]any{"status": "SUCCESS", "ledger": l, "latestLedger": f.ledger}, nil
		}
		return map[string]any{"status": "NOT_FOUND", "latestLedger": f.ledger}, nil
	case "getLedgers":
		var p struct {
			StartLedger uint32 `json:"startLedger"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		ledgers := []map[string]any{}
		if m, ok := f.meta[p.StartLedger]; ok {
			ledgers = append(ledgers, map[string]any{"sequence": p.StartLedger, "metadataXdr": m})
		}
		return map[string]any{"ledgers": ledgers, "latestLedger": f.ledger, "oldestLedger": 1}, nil
	}
	return nil, fmt.Errorf("method %s not served by the fake", method)
}

// refused is the TransactionResult of a refused transaction: txFAILED, its one operation failing.
func refused() string {
	ops := []xdr.OperationResult{{Code: xdr.OperationResultCodeOpNoAccount}}
	r := xdr.TransactionResult{FeeCharged: 100, Result: xdr.TransactionResultResult{Code: xdr.TransactionResultCodeTxFailed, Results: &ops}}
	b64, err := xdr.MarshalBase64(r)
	if err != nil {
		panic(err)
	}
	return b64
}

// send applies entry 1's SetOptions and refuses anything else.
func (f *fakeStellar) send(envB64 string) (any, error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envB64, &env); err != nil {
		return nil, err
	}
	hash, err := network.HashTransactionInEnvelope(env, orchestration.StandalonePassphrase)
	if err != nil {
		return nil, err
	}
	h := hex.EncodeToString(hash[:])
	ops := env.Operations()
	if len(ops) != 1 || ops[0].Body.Type != xdr.OperationTypeSetOptions || !f.applies {
		return map[string]any{"status": "ERROR", "hash": h, "latestLedger": f.ledger, "errorResultXdr": refused()}, nil
	}
	meta, err := f.capture(env, hash)
	if err != nil {
		return nil, err
	}
	f.txs[h] = f.ledger
	f.meta[f.ledger] = meta
	return map[string]any{"status": "PENDING", "hash": h, "latestLedger": f.ledger}, nil
}

// capture builds entry 1's capture of env from the real capture and writes it, with its decoding, as a corpus entry the
// fake stellar-xdr reads.
func (f *fakeStellar) capture(env xdr.TransactionEnvelope, hash [32]byte) (string, error) {
	raw, err := os.ReadFile(realCaptureXDR)
	if err != nil {
		return "", err
	}
	decoded, err := os.ReadFile(realCaptureJSON)
	if err != nil {
		return "", err
	}
	var lcm xdr.LedgerCloseMeta
	if err := xdr.SafeUnmarshal(raw, &lcm); err != nil {
		return "", err
	}
	probeEnv := lcm.TransactionEnvelopes()[0]
	probeHash := lcm.TransactionHash(0)
	txs := (*lcm.V2.TxSet.V1TxSet.Phases[0].V0Components)[0].TxsMaybeDiscountedFee.Txs
	txs[0] = env
	lcm.V2.TxProcessing[0].Result.TransactionHash = hash
	// The header states the network's protocol: the pipeline reads the protocol from the capture.
	realVersion := int(lcm.V2.LedgerHeader.Header.LedgerVersion)
	lcm.V2.LedgerHeader.Header.LedgerVersion = xdr.Uint32(f.protocol)

	probeSigner, err := signerKey(probeEnv)
	if err != nil {
		return "", err
	}
	newSigner, err := signerKey(env)
	if err != nil {
		return "", err
	}
	js := string(decoded)
	for old, repl := range map[string]string{
		probeEnv.SourceAccount().ToAccountId().Address(): env.SourceAccount().ToAccountId().Address(),
		probeSigner:                      newSigner,
		hex.EncodeToString(probeHash[:]): hex.EncodeToString(hash[:]),
	} {
		if !strings.Contains(js, old) {
			return "", fmt.Errorf("the real capture's decoding does not hold %s", old)
		}
		js = strings.ReplaceAll(js, old, repl)
	}
	if f.protocol != realVersion {
		old := fmt.Sprintf(`"ledger_version": %d`, realVersion)
		if strings.Count(js, old) != 1 {
			return "", fmt.Errorf("the real capture's decoding does not hold %s once", old)
		}
		js = strings.Replace(js, old, fmt.Sprintf(`"ledger_version": %d`, f.protocol), 1)
	}
	b, err := lcm.MarshalBinary()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(f.corpus, model.SeedSignedPayloadSigner)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "entry.xdr"), b, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "entry.json"), []byte(js), 0o644); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// signerKey is the P... strkey of the signer a one-SetOptions envelope adds.
func signerKey(env xdr.TransactionEnvelope) (string, error) {
	ops := env.Operations()
	if len(ops) != 1 {
		return "", fmt.Errorf("%d operations", len(ops))
	}
	so, ok := ops[0].Body.GetSetOptionsOp()
	if !ok || so.Signer == nil {
		return "", fmt.Errorf("not a SetOptions adding a signer")
	}
	return so.Signer.Key.GetAddress()
}

// generateEngine is the fake docker of a generation: the current quickstart served by a fake at the current protocol,
// the protocol-28 quickstart by a fake at 28, and the fake stellar-xdr reading their captures, then extra corpora.
func generateEngine(t *testing.T, modes string, extraCorpora ...string) (*fakeEngine, *fakeStellar) {
	t.Helper()
	f := newFakeEngine(t, modes, "")
	captures := t.TempDir()
	current := newFakeStellar(t, orchestration.CurrentProtocol, captures)
	p28 := newFakeStellar(t, orchestration.FallbackProtocol, captures)
	corpora := []string{captures}
	for _, c := range extraCorpora {
		abs, err := filepath.Abs(c)
		if err != nil {
			t.Fatal(err)
		}
		corpora = append(corpora, abs)
	}
	f.env = append(f.env,
		"FAKE_DOCKER_PORTS="+orchestration.QuickstartCurrent+"="+current.addr()+";"+orchestration.QuickstartProtocol28+"="+p28.addr(),
		"FAKE_DOCKER_CORPUS="+strings.Join(corpora, string(os.PathListSeparator)))
	return f, current
}
