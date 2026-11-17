package recipes

// Entry 3, cap-0027-muxed-account: a payment from a muxed source account (an M... address, the transaction's source)
// to a muxed destination, both underlying accounts funded by the friendbot. Unlike entries 1 and 2, this shape was not
// run before this tool: its first runs were the recipe's own on a local network.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/model"
)

// The muxed ids, constants of the recipe. The source's id has its top bit set, so a decoder that reads the
// 64-bit id as signed, or truncates it, shows a different value; the destination's is the number of the
// wallet-backend issue the shape is tied to (716).
const (
	// muxedSourceID is the muxed id of the source: 2^63 + 716.
	muxedSourceID uint64 = 1<<63 + 716
	// muxedDestinationID is the muxed id of the destination.
	muxedDestinationID uint64 = 716
)

// paymentAmount is the payment's amount in lumens, as txnbuild takes it; paymentStroops is the same in stroops, as
// the XDR (and the Rust tool's decoded JSON, a 64-bit integer written as a string) carries it.
const (
	paymentAmount  = "10"
	paymentStroops = "100000000"
)

// muxedPayment is entry 3's recipe. Its roles are "source" and "destination", the underlying G accounts of the two
// muxed addresses.
type muxedPayment struct{}

// ID is entry 3's id.
func (muxedPayment) ID() string { return model.SeedMuxedAccount }

// muxed returns the M... address of role's account with id.
func (r muxedPayment) muxed(role string, id uint64) string {
	m, err := xdr.MuxedAccountFromAccountId(key(r.ID(), role).Address(), id)
	if err != nil {
		panic(err) // a G address derived from a key is always valid
	}
	return m.Address()
}

// Run funds both underlying accounts, submits the payment from the muxed source to the muxed destination in a later
// ledger, and captures its ledger.
func (r muxedPayment) Run(ctx context.Context, net Network) (Capture, error) {
	s, err := newSession(net)
	if err != nil {
		return Capture{}, err
	}
	source := key(r.ID(), "source")
	destination := key(r.ID(), "destination")
	if err := s.fund(ctx, source.Address(), destination.Address()); err != nil {
		return Capture{}, fmt.Errorf("%s: funding: %w", r.ID(), err)
	}
	acc, err := s.account(ctx, r.muxed("source", muxedSourceID), source.Address())
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	tx, err := s.build(acc, source, &txnbuild.Payment{
		Destination: r.muxed("destination", muxedDestinationID),
		Amount:      paymentAmount,
		Asset:       txnbuild.NativeAsset{},
	})
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	st, err := s.encode(tx)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	ledger, err := s.apply(ctx, r.ID()+": the muxed-to-muxed payment", st)
	if err != nil {
		return Capture{}, err
	}
	c, err := s.capture(ctx, r.ID(), ledger, st.hash)
	if err != nil {
		return Capture{}, fmt.Errorf("%s: %w", r.ID(), err)
	}
	return c, nil
}

// Intended is entry 3's reproduced values: the muxed source, the muxed destination, the asset and the amount of the
// payment, and the operation's result.
func (r muxedPayment) Intended() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		txSourcePath:                           jsonValue(r.muxed("source", muxedSourceID)),
		operationPath + "/payment/destination": jsonValue(r.muxed("destination", muxedDestinationID)),
		operationPath + "/payment/asset":       jsonValue("native"),
		operationPath + "/payment/amount":      jsonValue(paymentStroops),
		opResultPath + "/payment":              jsonValue("success"),
	}
}
