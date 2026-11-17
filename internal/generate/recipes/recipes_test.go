package recipes

// The recipes' unit tests: each recipe's transactions, its declared seed
// (id, CAP, XDR type) and the protocol its capture records, on a fake network with no container. The native runs are
// integration_test.go's.

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// TestRegistryIsTheSeedsInOrder: one recipe per seed, in seed order, with the seed's id.
func TestRegistryIsTheSeedsInOrder(t *testing.T) {
	seeds := model.Seeds()
	reg := Registry()
	if len(reg) != len(seeds) || len(reg) != 3 {
		t.Fatalf("Registry has %d recipes; the seeds are %d", len(reg), len(seeds))
	}
	for i, r := range reg {
		if r.ID() != seeds[i].ID {
			t.Errorf("recipe %d is %q; seed %d is %q", i, r.ID(), i, seeds[i].ID)
		}
		got, ok := ByID(seeds[i].ID)
		if !ok || got.ID() != seeds[i].ID {
			t.Errorf("ByID(%q) = %v, %v", seeds[i].ID, got, ok)
		}
	}
	if _, ok := ByID("cap-0000-unknown"); ok {
		t.Error("ByID found a recipe for an unknown id")
	}
	reg[0] = nil
	if Registry()[0] == nil {
		t.Error("Registry returned the registry itself, not a copy")
	}
}

// run runs the recipe id on f and fails the test on an error.
func run(t *testing.T, id string, f *fakeNet) Capture {
	t.Helper()
	r, _ := ByID(id)
	c, err := r.Run(context.Background(), f.network())
	if err != nil {
		t.Fatalf("%s.Run: %v", id, err)
	}
	return c
}

// checkCapture holds what every capture states: its seed's id and XDR type, the ledger of the shape's transaction,
// that transaction's hash, and the protocol read from the captured header.
func checkCapture(t *testing.T, f *fakeNet, c Capture, id string, shape xdr.TransactionEnvelope) {
	t.Helper()
	seed, _ := model.SeedByID(id)
	if c.EntryID != id || c.XdrType != seed.XdrType || c.XdrType != "LedgerCloseMeta" {
		t.Errorf("capture is %q of type %q; want %q of type %q", c.EntryID, c.XdrType, id, seed.XdrType)
	}
	h, err := network.HashTransactionInEnvelope(shape, f.network().Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if want := f.txs[c.TxHash]; c.TxHash != hexOf(h) || want.ledger != c.Ledger {
		t.Errorf("capture holds tx %s in ledger %d; the shape is %s in ledger %d", c.TxHash, c.Ledger, hexOf(h), want.ledger)
	}
	if !bytes.Equal(c.XDR, f.ledgers[c.Ledger]) {
		t.Error("the capture's bytes are not getLedgers' metadataXdr of its ledger")
	}
	if c.Protocol != int(f.protocol) {
		t.Errorf("capture records protocol %d; the ledger header says %d", c.Protocol, f.protocol)
	}
	if _, err := xdrcodec.Decode(c.XdrType, c.XDR); err != nil {
		t.Errorf("the capture does not decode: %v", err)
	}
}

func hexOf(h [32]byte) string { return hex.EncodeToString(h[:]) }

// TestSignedPayloadSignerTransactions is entry 1's shape: one SetOptions from the
// funded source adding a signed-payload signer of the signer role's key and a 29-byte payload, weight 1.
func TestSignedPayloadSignerTransactions(t *testing.T) {
	f := newFakeNet(30)
	c := run(t, model.SeedSignedPayloadSigner, f)
	if len(f.sent) != 1 {
		t.Fatalf("sent %d transactions; want 1", len(f.sent))
	}
	env := f.sent[0]
	source := key(model.SeedSignedPayloadSigner, "source").Address()
	if src := env.SourceAccount(); src.Address() != source {
		t.Errorf("source %s; want %s", src.Address(), source)
	}
	ops := env.Operations()
	if len(ops) != 1 || ops[0].Body.SetOptionsOp == nil || ops[0].Body.SetOptionsOp.Signer == nil {
		t.Fatalf("operations %+v; want one SetOptions with a signer", ops)
	}
	signer := ops[0].Body.SetOptionsOp.Signer
	if signer.Weight != 1 || signer.Key.Type != xdr.SignerKeyTypeSignerKeyTypeEd25519SignedPayload {
		t.Fatalf("signer %+v; want a signed-payload signer of weight 1", signer)
	}
	sp := signer.Key.Ed25519SignedPayload
	if len(sp.Payload) != 29 || model.SignedPayloadLength != 29 {
		t.Errorf("payload of %d bytes; want 29", len(sp.Payload))
	}
	if got, _ := strkey.Encode(strkey.VersionByteAccountID, sp.Ed25519[:]); got != key(model.SeedSignedPayloadSigner, "signer").Address() {
		t.Errorf("payload's key %s; want the signer role's", got)
	}
	checkCapture(t, f, c, model.SeedSignedPayloadSigner, env)
	if c.Refusal != nil {
		t.Error("entry 1 returned refusal evidence")
	}
}

// poolParams is the pool of env's one ChangeTrust.
func poolParams(t *testing.T, env xdr.TransactionEnvelope) *xdr.LiquidityPoolConstantProductParameters {
	t.Helper()
	ops := env.Operations()
	if len(ops) != 1 || ops[0].Body.ChangeTrustOp == nil || ops[0].Body.ChangeTrustOp.Line.LiquidityPool == nil {
		t.Fatalf("operations %+v; want one pool-share ChangeTrust", ops)
	}
	return ops[0].Body.ChangeTrustOp.Line.LiquidityPool.ConstantProduct
}

// TestPoolShareTrustlineTransactions is entry 2's shape: the holder trusts
// both USD assets, opens the pool-share trustline in key-byte order (the capture), then submits the text-order form,
// whose refusal is returned verbatim with the envelope submitted.
func TestPoolShareTrustlineTransactions(t *testing.T) {
	f := newFakeNet(30)
	c := run(t, model.SeedPoolShareTrustline, f)
	if len(f.sent) != 3 {
		t.Fatalf("sent %d transactions; want 3 (trust, pool in key-byte order, pool in text order)", len(f.sent))
	}
	holder := key(model.SeedPoolShareTrustline, "source").Address()
	for i, env := range f.sent {
		if src := env.SourceAccount(); src.Address() != holder {
			t.Errorf("transaction %d's source %s; want the holder %s", i, src.Address(), holder)
		}
	}
	trust := f.sent[0].Operations()
	if len(trust) != 2 {
		t.Fatalf("the setup has %d operations; want 2", len(trust))
	}
	for i, issuer := range []string{model.PoolIssuerLowerBytes, model.PoolIssuerHigherBytes} {
		a := trust[i].Body.ChangeTrustOp.Line.AlphaNum4
		if a == nil || a.Issuer.Address() != issuer || string(bytes.TrimRight(a.AssetCode[:], "\x00")) != model.PoolAssetCode {
			t.Errorf("setup operation %d trusts %+v; want USD of %s", i, trust[i].Body.ChangeTrustOp.Line, issuer)
		}
	}
	p := poolParams(t, f.sent[1])
	if p.AssetA.AlphaNum4.Issuer.Address() != model.PoolIssuerLowerBytes || p.AssetB.AlphaNum4.Issuer.Address() != model.PoolIssuerHigherBytes || p.Fee != 30 {
		t.Errorf("the entry's pool is %s, %s, fee %d; want key-byte order %s, %s, fee 30", p.AssetA.AlphaNum4.Issuer.Address(),
			p.AssetB.AlphaNum4.Issuer.Address(), p.Fee, model.PoolIssuerLowerBytes, model.PoolIssuerHigherBytes)
	}
	checkCapture(t, f, c, model.SeedPoolShareTrustline, f.sent[1])

	if c.Refusal == nil {
		t.Fatal("entry 2 returned no refusal evidence")
	}
	var refused xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(c.Refusal.EnvelopeXdr, &refused); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustMarshal(t, refused), mustMarshal(t, f.sent[2])) {
		t.Error("the evidence's envelope is not the envelope submitted")
	}
	q := poolParams(t, refused)
	if q.AssetA.AlphaNum4.Issuer.Address() != model.PoolIssuerHigherBytes || q.AssetB.AlphaNum4.Issuer.Address() != model.PoolIssuerLowerBytes {
		t.Error("the refused envelope's assets are not in text order")
	}
	if model.PoolIssuerHigherBytes >= model.PoolIssuerLowerBytes {
		t.Error("text order is not the reverse of key-byte order for the pair")
	}
	if len(refused.Signatures()) != 1 {
		t.Fatal("the text-order form is not signed once")
	}
	h, _ := network.HashTransactionInEnvelope(refused, f.network().Passphrase)
	kp := key(model.SeedPoolShareTrustline, "source")
	if err := kp.Verify(h[:], refused.Signatures()[0].Signature); err != nil {
		t.Errorf("the text-order form is not signed by the holder over its own hash: %v", err)
	}
	if c.Refusal.ResultXdr != changeTrustMalformed || c.Refusal.Stage != "sendTransaction" || c.Refusal.Status != "ERROR" || c.Refusal.TxHash != hexOf(h) {
		t.Errorf("refusal %+v; want sendTransaction's ERROR with its errorResultXdr verbatim", c.Refusal)
	}
	// The evidence is what the Refusal evidence recorder (internal/catalogue) takes: it records CHANGE_TRUST_MALFORMED
	// from it.
	ev, err := catalogue.RefusalEvidence(c.Refusal.EnvelopeXdr, c.Refusal.ResultXdr)
	if err != nil || ev.ResultCode != contract.NetworkRefusalResultCodeChangeTrustMalformed || ev.SubmittedEnvelopeXdr != c.Refusal.EnvelopeXdr {
		t.Errorf("RefusalEvidence(evidence) = %+v, %v; want CHANGE_TRUST_MALFORMED with the envelope verbatim", ev, err)
	}
	if refused.SeqNum() != f.sent[1].SeqNum()+1 {
		t.Errorf("the text-order form has sequence %d; want the next one, %d", refused.SeqNum(), f.sent[1].SeqNum()+1)
	}
}

func mustMarshal(t *testing.T, v xdr.TransactionEnvelope) []byte {
	t.Helper()
	b, err := v.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPoolShareRefusedWhenApplied: a refusal can come from getTransaction (FAILED) instead of sendTransaction.
func TestPoolShareRefusedWhenApplied(t *testing.T) {
	f := newFakeNet(30)
	f.textOrder = "FAILED"
	c := run(t, model.SeedPoolShareTrustline, f)
	if c.Refusal == nil || c.Refusal.Stage != "getTransaction" || c.Refusal.Status != "FAILED" || c.Refusal.ResultXdr != changeTrustMalformed {
		t.Errorf("refusal %+v; want getTransaction's FAILED with its resultXdr", c.Refusal)
	}
}

// TestPoolShareTextOrderAcceptedFailsGeneration: the shape fails when the network accepts the text-order form.
func TestPoolShareTextOrderAcceptedFailsGeneration(t *testing.T) {
	f := newFakeNet(30)
	f.textOrder = "SUCCESS"
	r, _ := ByID(model.SeedPoolShareTrustline)
	_, err := r.Run(context.Background(), f.network())
	var nr *NotRefusedError
	if !errors.As(err, &nr) || nr.ID != model.SeedPoolShareTrustline || nr.TxHash == "" ||
		!strings.Contains(err.Error(), "accepted the pool-share trustline with its assets in text order") {
		t.Fatalf("Run = %v; want the text order's acceptance as a *NotRefusedError", err)
	}
}

// TestMuxedPaymentTransactions is entry 3's shape: one payment whose transaction
// source is the source role's account muxed with its id, to the destination role's account muxed with its id.
func TestMuxedPaymentTransactions(t *testing.T) {
	f := newFakeNet(30)
	c := run(t, model.SeedMuxedAccount, f)
	if len(f.sent) != 1 {
		t.Fatalf("sent %d transactions; want 1", len(f.sent))
	}
	env := f.sent[0]
	src := env.SourceAccount()
	if src.Type != xdr.CryptoKeyTypeKeyTypeMuxedEd25519 || uint64(src.Med25519.Id) != muxedSourceID ||
		src.ToAccountId().Address() != key(model.SeedMuxedAccount, "source").Address() {
		t.Errorf("source %s; want the source role muxed with id %d", src.Address(), muxedSourceID)
	}
	ops := env.Operations()
	if len(ops) != 1 || ops[0].Body.PaymentOp == nil {
		t.Fatalf("operations %+v; want one payment", ops)
	}
	pay := ops[0].Body.PaymentOp
	if pay.Destination.Type != xdr.CryptoKeyTypeKeyTypeMuxedEd25519 || uint64(pay.Destination.Med25519.Id) != muxedDestinationID ||
		pay.Destination.ToAccountId().Address() != key(model.SeedMuxedAccount, "destination").Address() {
		t.Errorf("destination %s; want the destination role muxed with id %d", pay.Destination.Address(), muxedDestinationID)
	}
	if pay.Asset.Type != xdr.AssetTypeAssetTypeNative || pay.Amount != 100000000 || paymentStroops != "100000000" {
		t.Errorf("payment of %d of %s; want 10 XLM (100000000 stroops) native", pay.Amount, pay.Asset.Type)
	}
	if ops[0].SourceAccount != nil {
		t.Error("the operation has its own source; the transaction's muxed source is the payment's source")
	}
	checkCapture(t, f, c, model.SeedMuxedAccount, env)
}

// TestCaptureRecordsTheHeaderProtocol: the protocol is read from the captured ledger header, never from the protocol
// asked.
func TestCaptureRecordsTheHeaderProtocol(t *testing.T) {
	for _, p := range []uint32{28, 30} {
		f := newFakeNet(p)
		if c := run(t, model.SeedSignedPayloadSigner, f); c.Protocol != int(p) {
			t.Errorf("protocol %d recorded; the header says %d", c.Protocol, p)
		}
	}
}

// TestRecipeRefusesAnotherPassphrase: a recipe signs only for the local standalone network.
func TestRecipeRefusesAnotherPassphrase(t *testing.T) {
	for _, r := range Registry() {
		f := newFakeNet(30)
		n := f.network()
		n.Passphrase = network.TestNetworkPassphrase
		if _, err := r.Run(context.Background(), n); err == nil || !strings.Contains(err.Error(), "local standalone network") {
			t.Errorf("%s: Run = %v; want a refusal of the passphrase", r.ID(), err)
		}
		if f.calls != 0 {
			t.Errorf("%s: %d calls reached the network", r.ID(), f.calls)
		}
	}
}

// TestShapeNotAppliedIsAnError: a transaction the network does not apply successfully fails the recipe with a
// *NotAppliedError (the first two conditions of "cannot work", which the Protocol selector retries at 28), naming the
// transaction.
func TestShapeNotAppliedIsAnError(t *testing.T) {
	for _, r := range Registry() {
		f := newFakeNet(30)
		f.failAll = true
		_, err := r.Run(context.Background(), f.network())
		var na *NotAppliedError
		if !errors.As(err, &na) || na.Stage != "getTransaction" || na.Status != "FAILED" || !strings.Contains(err.Error(), "was not applied") {
			t.Errorf("%s: Run = %v; want the failed transaction as a *NotAppliedError", r.ID(), err)
		}
	}
}

// TestCaptureMustHoldTheShapeAlone: a captured ledger with another transaction in it fails the recipe.
func TestCaptureMustHoldTheShapeAlone(t *testing.T) {
	f := newFakeNet(30)
	f.extraTx = true
	r, _ := ByID(model.SeedSignedPayloadSigner)
	if _, err := r.Run(context.Background(), f.network()); err == nil || !strings.Contains(err.Error(), "must be alone") {
		t.Fatalf("Run = %v; want the second transaction as an error", err)
	}
}

// TestWaitsEndAtTheInjectedTimeout: a transaction RPC never finds ends the recipe at the network's timeout (the
// timeout is injected, so the test never waits it out).
func TestWaitsEndAtTheInjectedTimeout(t *testing.T) {
	f := newFakeNet(30)
	f.notFound = -1
	n := f.network()
	n.Timeout = 20 * time.Millisecond
	r, _ := ByID(model.SeedSignedPayloadSigner)
	if _, err := r.Run(context.Background(), n); err == nil || !strings.Contains(err.Error(), "not done after 20ms") {
		t.Fatalf("Run = %v; want the timeout", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f = newFakeNet(30)
	f.notFound = -1
	if _, err := r.Run(ctx, f.network()); err == nil {
		t.Fatal("Run with a cancelled context succeeded")
	}
}

// TestRecipesHoldNoState: the same recipe value runs against two networks at once and each capture is its own network's
// (the wallet-backend 729 reproduction runs entry 1's Run against its own network).
func TestRecipesHoldNoState(t *testing.T) {
	for _, r := range Registry() {
		nets := []*fakeNet{newFakeNet(30), newFakeNet(28)}
		caps := make([]Capture, len(nets))
		errs := make([]error, len(nets))
		var wg sync.WaitGroup
		for i, f := range nets {
			wg.Add(1)
			go func() {
				defer wg.Done()
				caps[i], errs[i] = r.Run(context.Background(), f.network())
			}()
		}
		wg.Wait()
		for i, f := range nets {
			if errs[i] != nil {
				t.Fatalf("%s on network %d: %v", r.ID(), i, errs[i])
			}
			if !bytes.Equal(caps[i].XDR, f.ledgers[caps[i].Ledger]) || caps[i].Protocol != int(f.protocol) {
				t.Errorf("%s: network %d's capture is not its own", r.ID(), i)
			}
		}
		// A second run on a fresh network gives the same transactions: the same keys, the same shape.
		again := newFakeNet(30)
		if _, err := r.Run(context.Background(), again.network()); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(mustMarshal(t, again.sent[len(again.sent)-1]), mustMarshal(t, nets[0].sent[len(nets[0].sent)-1])) {
			t.Errorf("%s: two runs on two fresh networks submitted different last envelopes", r.ID())
		}
	}
}
