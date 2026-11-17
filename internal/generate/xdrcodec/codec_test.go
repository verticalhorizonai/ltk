package xdrcodec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// sdkEnvelope builds a signed TransactionEnvelope with the SDK's txnbuild from a fixed seed: a SetOptions adding a
// signed-payload signer of 29 bytes, entry 1's shape, so the XDR carries padding.
func sdkEnvelope(t *testing.T) []byte {
	t.Helper()
	seed := sha256.Sum256([]byte("ltk/xdrcodec-test/source"))
	src, err := keypair.FromRawSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	signerSeed := sha256.Sum256([]byte("ltk/xdrcodec-test/signer"))
	signer, err := keypair.FromRawSeed(signerSeed)
	if err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("ltk/xdrcodec-test/payload"))
	sp, err := strkey.NewSignedPayload(signer.Address(), payload[:29])
	if err != nil {
		t.Fatal(err)
	}
	pStrkey, err := sp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: src.Address(), Sequence: 4294967296},
		IncrementSequenceNum: true,
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
		Operations:           []txnbuild.Operation{&txnbuild.SetOptions{Signer: &txnbuild.Signer{Address: pStrkey, Weight: 1}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx, err = tx.Sign(network.TestNetworkPassphrase, src); err != nil {
		t.Fatal(err)
	}
	b, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sdkLedgerCloseMeta builds a LedgerCloseMeta (v0, empty transaction set) with the SDK.
func sdkLedgerCloseMeta(t *testing.T) []byte {
	t.Helper()
	lcm := xdr.LedgerCloseMeta{V: 0, V0: &xdr.LedgerCloseMetaV0{
		LedgerHeader: xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{LedgerVersion: 30, LedgerSeq: 7}},
	}}
	b, err := lcm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sdkTransactionResult builds a TransactionResult with the result code txBAD_SEQ.
func sdkTransactionResult(t *testing.T) []byte {
	t.Helper()
	res := xdr.TransactionResult{FeeCharged: 100, Result: xdr.TransactionResultResult{Code: xdr.TransactionResultCodeTxBadSeq}}
	b, err := res.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Each known type decodes to its SDK type and re-encodes to the same bytes.
func TestRoundTripOfSDKBuiltValues(t *testing.T) {
	cases := []struct {
		xdrType string
		b       []byte
		check   func(v any) bool
	}{
		{TypeTransactionEnvelope, sdkEnvelope(t), func(v any) bool {
			e, ok := v.(*xdr.TransactionEnvelope)
			return ok && e.Type == xdr.EnvelopeTypeEnvelopeTypeTx && len(e.V1.Tx.Operations) == 1
		}},
		{TypeLedgerCloseMeta, sdkLedgerCloseMeta(t), func(v any) bool {
			m, ok := v.(*xdr.LedgerCloseMeta)
			return ok && m.LedgerSequence() == 7
		}},
		{TypeTransactionResult, sdkTransactionResult(t), func(v any) bool {
			r, ok := v.(*xdr.TransactionResult)
			return ok && r.Result.Code == xdr.TransactionResultCodeTxBadSeq
		}},
	}
	for _, c := range cases {
		v, err := Decode(c.xdrType, c.b)
		if err != nil {
			t.Fatalf("Decode(%s): %v", c.xdrType, err)
		}
		if !c.check(v) {
			t.Fatalf("Decode(%s) = %#v", c.xdrType, v)
		}
		re, err := Encode(v)
		if err != nil || !bytes.Equal(re, c.b) {
			t.Fatalf("Encode(Decode(%s)) differs: %v", c.xdrType, err)
		}
		rt, err := RoundTrip(c.xdrType, c.b)
		if err != nil || !bytes.Equal(rt, c.b) {
			t.Fatalf("RoundTrip(%s) differs: %v", c.xdrType, err)
		}
		b64, err := DecodeBase64(c.xdrType, ToBase64(c.b))
		if err != nil {
			t.Fatalf("DecodeBase64(%s): %v", c.xdrType, err)
		}
		if re, _ := Encode(b64); !bytes.Equal(re, c.b) {
			t.Fatalf("DecodeBase64(%s) did not decode the same value", c.xdrType)
		}
	}
}

// An unknown type is an error naming it, never a panic.
func TestUnknownTypeIsAnErrorNamingIt(t *testing.T) {
	for _, f := range []func() error{
		func() error { _, err := Decode("LedgerEntry", []byte{0, 0, 0, 0}); return err },
		func() error { _, err := RoundTrip("LedgerEntry", []byte{0, 0, 0, 0}); return err },
		func() error { _, err := DecodeBase64("LedgerEntry", "AAAAAA=="); return err },
	} {
		err := f()
		var ut *UnknownTypeError
		if !errors.As(err, &ut) || ut.Type != "LedgerEntry" || !strings.Contains(err.Error(), `"LedgerEntry"`) {
			t.Fatalf("err = %v; want an UnknownTypeError naming LedgerEntry", err)
		}
	}
	if got := Types(); !slices.Equal(got, []string{"LedgerCloseMeta", "TransactionEnvelope", "TransactionResult"}) {
		t.Fatalf("Types() = %q", got)
	}
}

// Bytes that are not the declared type, or that leave bytes over, do not decode.
func TestBytesThatAreNotTheTypeDoNotDecode(t *testing.T) {
	env := sdkEnvelope(t)
	for name, b := range map[string][]byte{
		"truncated":        env[:len(env)-3],
		"trailing bytes":   append(slices.Clone(env), 0, 0, 0, 0),
		"empty":            {},
		"bad discriminant": {0, 0, 0, 99},
	} {
		if _, err := Decode(TypeTransactionEnvelope, b); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	if _, err := FromBase64("not base64!"); err == nil {
		t.Fatal("FromBase64 accepted text that is not base64")
	}
	if _, err := Encode("a string"); err == nil {
		t.Fatal("Encode accepted a value that is not XDR")
	}
}
