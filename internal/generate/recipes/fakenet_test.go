package recipes

// A fake network for the recipes' unit tests (no container): a friendbot that creates accounts, and an RPC that
// applies or refuses transactions the way stellar-core does for the three shapes, one transaction per ledger, and
// serves each applied transaction's ledger as a LedgerCloseMeta v2 holding it alone.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// changeTrustMalformed is the result the network returned for the text-order form in the deciding test, the
// feasibility run made before this tool (its errorResultXdr): txFAILED, its one operation CHANGE_TRUST_MALFORMED.
const changeTrustMalformed = "AAAAAAAAAGT/////AAAAAQAAAAAAAAAG/////wAAAAA="

// txResult is a TransactionResult of code with no operation result, base64.
func txResult(code xdr.TransactionResultCode) string {
	r := xdr.TransactionResult{FeeCharged: 100, Result: xdr.TransactionResultResult{Code: code}}
	if code == xdr.TransactionResultCodeTxSuccess || code == xdr.TransactionResultCodeTxFailed {
		r.Result.Results = &[]xdr.OperationResult{}
	}
	b64, err := xdr.MarshalBase64(r)
	if err != nil {
		panic(err)
	}
	return b64
}

type fakeTx struct {
	status    string
	ledger    uint32
	resultXdr string
}

type fakeNet struct {
	mu       sync.Mutex
	protocol uint32
	ledger   uint32
	accounts map[string]int64
	txs      map[string]fakeTx
	ledgers  map[uint32][]byte
	sent     []xdr.TransactionEnvelope
	calls    int

	// textOrder is what the network does with a pool-share ChangeTrust whose assets are not in key-byte order:
	// "ERROR" (refused by sendTransaction, as the deciding test saw), "FAILED" (applied and failed), "SUCCESS".
	textOrder string
	// failAll makes every transaction fail when applied.
	failAll bool
	// extraTx puts a second transaction in every served ledger.
	extraTx bool
	// notFound is how many times getTransaction answers NOT_FOUND before the outcome; -1 for always.
	notFound int
}

func newFakeNet(protocol uint32) *fakeNet {
	return &fakeNet{protocol: protocol, ledger: 2, accounts: map[string]int64{}, txs: map[string]fakeTx{},
		ledgers: map[uint32][]byte{}, textOrder: "ERROR", notFound: 1}
}

func (f *fakeNet) network() Network {
	return Network{RPC: f, Friendbot: f, Passphrase: orchestration.StandalonePassphrase, Poll: 1}
}

func (f *fakeNet) Fund(_ context.Context, address string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if _, ok := f.accounts[address]; ok {
		return fmt.Errorf("friendbot: %s already exists", address)
	}
	f.ledger++
	f.accounts[address] = int64(f.ledger) << 32
	return nil
}

func (f *fakeNet) GetLedgerEntries(_ context.Context, keys []string) (LedgerEntries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	var out LedgerEntries
	for _, k := range keys {
		var lk xdr.LedgerKey
		if err := xdr.SafeUnmarshalBase64(k, &lk); err != nil {
			return out, err
		}
		addr := lk.Account.AccountId.Address()
		seq, ok := f.accounts[addr]
		if !ok {
			continue
		}
		data := xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeAccount, Account: &xdr.AccountEntry{
			AccountId: lk.Account.AccountId, Balance: 100000000000, SeqNum: xdr.SequenceNumber(seq)}}
		b64, err := xdr.MarshalBase64(data)
		if err != nil {
			return out, err
		}
		out.Entries = append(out.Entries, LedgerEntry{Key: k, XDR: b64})
	}
	out.LatestLedger = f.ledger
	return out, nil
}

// textOrdered reports whether env is a pool-share ChangeTrust whose assets are not in key-byte order.
func textOrdered(env xdr.TransactionEnvelope) bool {
	for _, op := range env.Operations() {
		ct, ok := op.Body.GetChangeTrustOp()
		if !ok || ct.Line.LiquidityPool == nil {
			continue
		}
		p := ct.Line.LiquidityPool.ConstantProduct
		a, _ := strkey.Decode(strkey.VersionByteAccountID, p.AssetA.AlphaNum4.Issuer.Address())
		b, _ := strkey.Decode(strkey.VersionByteAccountID, p.AssetB.AlphaNum4.Issuer.Address())
		return bytes.Compare(a, b) > 0
	}
	return false
}

func (f *fakeNet) SendTransaction(_ context.Context, envelope string) (SentTransaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelope, &env); err != nil {
		return SentTransaction{}, err
	}
	f.sent = append(f.sent, env)
	h, err := network.HashTransactionInEnvelope(env, orchestration.StandalonePassphrase)
	if err != nil {
		return SentTransaction{}, err
	}
	hash := hex.EncodeToString(h[:])
	src := env.SourceAccount().ToAccountId()
	addr := src.Address()
	if seq, ok := f.accounts[addr]; !ok || int64(env.SeqNum()) != seq+1 {
		return SentTransaction{Status: "ERROR", Hash: hash, ErrorResultXdr: txResult(xdr.TransactionResultCodeTxBadSeq)}, nil
	}
	if textOrdered(env) {
		switch f.textOrder {
		case "ERROR":
			return SentTransaction{Status: "ERROR", Hash: hash, ErrorResultXdr: changeTrustMalformed}, nil
		case "FAILED":
			f.ledger++
			f.accounts[addr]++
			f.txs[hash] = fakeTx{status: "FAILED", ledger: f.ledger, resultXdr: changeTrustMalformed}
			return SentTransaction{Status: "PENDING", Hash: hash}, nil
		}
	}
	f.ledger++
	f.accounts[addr]++
	if f.failAll {
		f.txs[hash] = fakeTx{status: "FAILED", ledger: f.ledger, resultXdr: txResult(xdr.TransactionResultCodeTxFailed)}
		return SentTransaction{Status: "PENDING", Hash: hash}, nil
	}
	lcm, err := f.lcm(f.ledger, h)
	if err != nil {
		return SentTransaction{}, err
	}
	f.ledgers[f.ledger] = lcm
	f.txs[hash] = fakeTx{status: "SUCCESS", ledger: f.ledger, resultXdr: txResult(xdr.TransactionResultCodeTxSuccess)}
	return SentTransaction{Status: "PENDING", Hash: hash}, nil
}

// lcm is a LedgerCloseMeta v2 of ledger holding the transaction h (and a second one when extraTx is set).
func (f *fakeNet) lcm(ledger uint32, h xdr.Hash) ([]byte, error) {
	result := func(h xdr.Hash) xdr.TransactionResultMetaV1 {
		return xdr.TransactionResultMetaV1{
			Result: xdr.TransactionResultPair{TransactionHash: h, Result: xdr.TransactionResult{FeeCharged: 100,
				Result: xdr.TransactionResultResult{Code: xdr.TransactionResultCodeTxSuccess, Results: &[]xdr.OperationResult{}}}},
			TxApplyProcessing: xdr.TransactionMeta{V: 0, Operations: &[]xdr.OperationMeta{}},
		}
	}
	txs := []xdr.TransactionResultMetaV1{result(h)}
	if f.extraTx {
		txs = append(txs, result(xdr.Hash{1}))
	}
	m := xdr.LedgerCloseMeta{V: 2, V2: &xdr.LedgerCloseMetaV2{
		LedgerHeader: xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{LedgerVersion: xdr.Uint32(f.protocol), LedgerSeq: xdr.Uint32(ledger)}},
		TxSet:        xdr.GeneralizedTransactionSet{V: 1, V1TxSet: &xdr.TransactionSetV1{}},
		TxProcessing: txs,
	}}
	return m.MarshalBinary()
}

func (f *fakeNet) GetTransaction(_ context.Context, hash string) (Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	tx, ok := f.txs[hash]
	if !ok || f.notFound != 0 {
		if f.notFound > 0 {
			f.notFound--
		}
		return Transaction{Status: "NOT_FOUND", LatestLedger: f.ledger}, nil
	}
	f.notFound = 1
	return Transaction{Status: tx.status, Ledger: tx.ledger, ResultXdr: tx.resultXdr, LatestLedger: f.ledger}, nil
}

func (f *fakeNet) GetLedgers(_ context.Context, start uint32, limit int) (Ledgers, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	out := Ledgers{LatestLedger: f.ledger}
	if b, ok := f.ledgers[start]; ok && limit >= 1 {
		out.Ledgers = []LedgerInfo{{Sequence: start, MetadataXdr: base64.StdEncoding.EncodeToString(b)}}
	}
	return out, nil
}
