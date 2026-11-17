package recipes

// Entry 1, cap-0040-signed-payload-signer: a SetOptions that adds a CAP-40 signed-payload signer (a P... strkey) with a
// 29-byte payload, weight 1, so that the XDR carries padding. It is the shape the deciding test, the feasibility run
// made before this tool, submitted; wallet-backend 729's reproduction submits it again by calling Run against its own
// network.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"

	"github.com/verticalhorizonai/ltk/internal/model"
)

// signerWeight is the signed-payload signer's weight (the deciding test's).
const signerWeight = 1

// signedPayloadSigner is entry 1's recipe. Its roles are "source", the account that adds the signer, and "signer",
// the ed25519 key the signed payload belongs to.
type signedPayloadSigner struct{}

// ID is entry 1's id.
func (signedPayloadSigner) ID() string { return model.SeedSignedPayloadSigner }

// payload is the signed payload: the first SignedPayloadLength (29) bytes of sha256("ltk/<entry id>/payload"), a
// constant of the recipe derived from public text, as the deciding test derived its own.
func (signedPayloadSigner) payload() []byte {
	h := sha256.Sum256([]byte("ltk/" + model.SeedSignedPayloadSigner + "/payload"))
	return h[:model.SignedPayloadLength]
}

// signerKey is the signer's P... strkey: the signer role's public key with the payload.
func (r signedPayloadSigner) signerKey() string {
	sp, err := strkey.NewSignedPayload(key(r.ID(), "signer").Address(), r.payload())
	if err != nil {
		panic(err) // a 29-byte payload is within the 64 bytes CAP-40 allows
	}
	p, err := sp.Encode()
	if err != nil {
		panic(err)
	}
	return p
}

// Run funds the source, submits the SetOptions in a later ledger and captures its ledger.
func (r signedPayloadSigner) Run(ctx context.Context, net Network) (Capture, error) {
	s, err := newSession(net)
	if err != nil {
		return Capture{}, err
	}
	source := key(r.ID(), "source")
	if err := s.fund(ctx, source.Address()); err != nil {
		return Capture{}, fmt.Errorf("%s: funding: %w", r.ID(), err)
	}
	acc, err := s.account(ctx, source.Address(), source.Address())
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	tx, err := s.build(acc, source, &txnbuild.SetOptions{Signer: &txnbuild.Signer{Address: r.signerKey(), Weight: signerWeight}})
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	st, err := s.encode(tx)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	ledger, err := s.apply(ctx, r.ID()+": the SetOptions", st)
	if err != nil {
		return Capture{}, err
	}
	c, err := s.capture(ctx, r.ID(), ledger, st.hash)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	return c, nil
}

// Intended is entry 1's reproduced values: the source, the signer's key with its payload and its weight in the
// operation, the operation's result, and the signer in the account's new state.
func (r signedPayloadSigner) Intended() map[string]json.RawMessage {
	signer := jsonValue(r.signerKey())
	return map[string]json.RawMessage{
		txSourcePath: jsonValue(key(r.ID(), "source").Address()),
		operationPath + "/set_options/signer/key":               signer,
		operationPath + "/set_options/signer/weight":            jsonValue(signerWeight),
		opResultPath + "/set_options":                           jsonValue("success"),
		opChangesPath + "/1/updated/data/account/signers/0/key": signer,
	}
}
