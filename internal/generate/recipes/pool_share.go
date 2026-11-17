package recipes

// Entry 2, cap-0038-pool-share-trustline: a pool-share
// ChangeTrust whose two issuers sort one way as strkey text and the other way as key bytes. The holder trusts both USD
// assets, then opens the pool-share trustline with the assets in key-byte order (the protocol's order; the captured
// ledger), then submits the same pool with the assets in text order, which the network refuses with
// CHANGE_TRUST_MALFORMED: the refusal is the entry's evidence. It is the shape the deciding test, the feasibility run
// made before this tool, submitted and saw refused, with the issuer pair of internal/model, the one source of that
// pair for this recipe and for the js-stellar-sdk reproduction.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// poolFee is the pool's fee in basis points: the only constant-product fee the protocol allows (30).
const poolFee = txnbuild.LiquidityPoolFeeV18

// poolShareTrustline is entry 2's recipe. Its roles are "source", the holder of the trustlines, and the two issuers
// of internal/model (PoolIssuerLowerBytesRole, PoolIssuerHigherBytesRole).
type poolShareTrustline struct{}

// ID is entry 2's id.
func (poolShareTrustline) ID() string { return model.SeedPoolShareTrustline }

// issuers returns the pool's issuers in key-byte order, after asserting the property the shape exists for: the pair
// sorts the other way as strkey text. They must also be internal/model's published pair, the one repro.mjs
// builds its pool from.
func (r poolShareTrustline) issuers() (lower, higher string, err error) {
	lower = key(r.ID(), model.PoolIssuerLowerBytesRole).Address()
	higher = key(r.ID(), model.PoolIssuerHigherBytesRole).Address()
	lb, err := strkey.Decode(strkey.VersionByteAccountID, lower)
	if err != nil {
		return "", "", err
	}
	hb, err := strkey.Decode(strkey.VersionByteAccountID, higher)
	if err != nil {
		return "", "", err
	}
	if !(bytes.Compare(lb, hb) < 0 && higher < lower) {
		return "", "", fmt.Errorf("%s: the issuer pair %s, %s does not sort one way as key bytes and the other way as text", r.ID(), lower, higher)
	}
	if lower != model.PoolIssuerLowerBytes || higher != model.PoolIssuerHigherBytes {
		return "", "", fmt.Errorf("%s: the issuer roles' keys %s, %s are not internal/model's published pair %s, %s", r.ID(),
			lower, higher, model.PoolIssuerLowerBytes, model.PoolIssuerHigherBytes)
	}
	return lower, higher, nil
}

// assets returns the pool's two USD assets in key-byte order.
func (r poolShareTrustline) assets() (a, b txnbuild.CreditAsset, err error) {
	lower, higher, err := r.issuers()
	if err != nil {
		return a, b, err
	}
	return txnbuild.CreditAsset{Code: model.PoolAssetCode, Issuer: lower}, txnbuild.CreditAsset{Code: model.PoolAssetCode, Issuer: higher}, nil
}

// Run funds the holder and the issuers, trusts both assets, opens the pool-share trustline in key-byte order and
// captures its ledger, then submits the text-order form and returns the network's refusal as the capture's Refusal.
// Generation fails when the text-order form is accepted: the shape would no longer show its point.
func (r poolShareTrustline) Run(ctx context.Context, net Network) (Capture, error) {
	a, b, err := r.assets() // the start-up assertion of the pair's two orders
	if err != nil {
		return Capture{}, err
	}
	s, err := newSession(net)
	if err != nil {
		return Capture{}, err
	}
	holder := key(r.ID(), "source")
	if err := s.fund(ctx, a.Issuer, b.Issuer, holder.Address()); err != nil {
		return Capture{}, fmt.Errorf("%s: funding: %w", r.ID(), err)
	}
	acc, err := s.account(ctx, holder.Address(), holder.Address())
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}

	// 1. Trust both assets: a pool-share trustline requires them.
	setup, err := s.build(acc, holder,
		&txnbuild.ChangeTrust{Line: a.MustToChangeTrustAsset()},
		&txnbuild.ChangeTrust{Line: b.MustToChangeTrustAsset()})
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	st, err := s.encode(setup)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	if _, err := s.apply(ctx, r.ID()+": trusting both assets", st); err != nil {
		return Capture{}, err
	}

	// 2. The pool-share trustline with the assets in key-byte order: the entry.
	pool := txnbuild.LiquidityPoolShareChangeTrustAsset{LiquidityPoolParameters: txnbuild.LiquidityPoolParameters{AssetA: a, AssetB: b, Fee: poolFee}}
	shape, err := s.build(acc, holder, &txnbuild.ChangeTrust{Line: pool})
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	sh, err := s.encode(shape)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	ledger, err := s.apply(ctx, r.ID()+": the pool-share trustline in key-byte order", sh)
	if err != nil {
		return Capture{}, err
	}
	c, err := s.capture(ctx, r.ID(), ledger, sh.hash)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}

	// 3. The same pool with the assets in text order. The SDK (rightly) refuses to build it, so the operation is
	// swapped in the envelope's XDR and signed again (the deciding test's way).
	wrong, err := s.build(acc, holder, &txnbuild.ChangeTrust{Line: pool})
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	ws, err := textOrder(wrong, s.net.Passphrase, holder)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	o, err := s.submit(ctx, ws)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: the text-order form: %w", r.ID(), err)
	}
	if o.accepted() {
		return Capture{}, &NotRefusedError{ID: r.ID(), TxHash: ws.hash, Ledger: o.ledger}
	}
	if o.resultXdr == "" {
		return Capture{}, fmt.Errorf("%s: the text-order form was refused (%s %s) with no result XDR", r.ID(), o.stage, o.status)
	}
	if _, err := xdrcodec.DecodeBase64(xdrcodec.TypeTransactionResult, o.resultXdr); err != nil {
		return Capture{}, fmt.Errorf("%s: the text-order form's refusal: %w", r.ID(), err)
	}
	c.Refusal = &Refusal{EnvelopeXdr: ws.envelope, ResultXdr: o.resultXdr, Stage: o.stage, Status: o.status, TxHash: ws.hash}
	return c, nil
}

// NotRefusedError is entry 2's text-order form accepted by the network: the refusal the entry records was not
// produced, so the shape cannot work at this protocol (the third condition: its intended value, the refusal's result
// code, is not in what the network returned).
type NotRefusedError struct {
	// ID is the entry id.
	ID string
	// TxHash and Ledger are the accepted text-order transaction and the ledger that applied it.
	TxHash string
	Ledger uint32
}

// Error names the accepted transaction and its ledger.
func (e *NotRefusedError) Error() string {
	return fmt.Sprintf("%s: the network accepted the pool-share trustline with its assets in text order (transaction %s, ledger %d): the shape no longer shows the sort-order issue",
		e.ID, e.TxHash, e.Ledger)
}

// textOrder returns tx's envelope with its pool-share ChangeTrust's two assets swapped into strkey-text order, hashed
// and signed again by the holder.
func textOrder(tx *txnbuild.Transaction, passphrase string, holder *keypair.Full) (signed, error) {
	env := tx.ToXDR()
	if env.V1 == nil || len(env.V1.Tx.Operations) != 1 {
		return signed{}, fmt.Errorf("the text-order form: not a one-operation v1 envelope")
	}
	ct, ok := env.V1.Tx.Operations[0].Body.GetChangeTrustOp()
	if !ok || ct.Line.LiquidityPool == nil || ct.Line.LiquidityPool.ConstantProduct == nil {
		return signed{}, fmt.Errorf("the text-order form: the operation is not a pool-share ChangeTrust")
	}
	params := ct.Line.LiquidityPool.ConstantProduct
	params.AssetA, params.AssetB = params.AssetB, params.AssetA
	hash, err := network.HashTransactionInEnvelope(env, passphrase)
	if err != nil {
		return signed{}, fmt.Errorf("the text-order form: hashing: %w", err)
	}
	sig, err := holder.SignDecorated(hash[:])
	if err != nil {
		return signed{}, fmt.Errorf("the text-order form: signing: %w", err)
	}
	env.V1.Signatures = []xdr.DecoratedSignature{sig}
	b64, err := xdr.MarshalBase64(env)
	if err != nil {
		return signed{}, fmt.Errorf("the text-order form: encoding: %w", err)
	}
	return signed{envelope: b64, hash: hex.EncodeToString(hash[:])}, nil
}

// Intended is entry 2's reproduced values: the holder, the two assets' codes and issuers in the order the operation
// carries them (key-byte order), the pool's fee and the operation's result. The pool id of the new trustline is not
// among them: it follows from the assets and the fee, which are.
func (r poolShareTrustline) Intended() map[string]json.RawMessage {
	a, b, err := r.assets()
	if err != nil {
		panic(err) // internal/model's seeds_test.go holds the pair's property; Run reports it as an error
	}
	pool := operationPath + "/change_trust/line/pool_share/liquidity_pool_constant_product"
	return map[string]json.RawMessage{
		txSourcePath: jsonValue(key(r.ID(), "source").Address()),
		pool + "/asset_a/credit_alphanum4/asset_code": jsonValue(a.Code),
		pool + "/asset_a/credit_alphanum4/issuer":     jsonValue(a.Issuer),
		pool + "/asset_b/credit_alphanum4/asset_code": jsonValue(b.Code),
		pool + "/asset_b/credit_alphanum4/issuer":     jsonValue(b.Issuer),
		pool + "/fee":                  jsonValue(poolFee),
		opResultPath + "/change_trust": jsonValue("success"),
	}
}
