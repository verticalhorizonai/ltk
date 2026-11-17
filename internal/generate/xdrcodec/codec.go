package xdrcodec

// The Go SDK XDR codec, on Stellar's Go SDK github.com/stellar/go-stellar-sdk and its xdr package.

import (
	"encoding"
	"encoding/base64"
	"fmt"
	"sort"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// The XDR type names the codec knows, as an entry or an RPC value declares them.
const (
	// TypeLedgerCloseMeta is every seed entry's type: the capture of RPC getLedgers' metadataXdr.
	TypeLedgerCloseMeta = "LedgerCloseMeta"
	// TypeTransactionEnvelope is a submitted transaction's envelope.
	TypeTransactionEnvelope = "TransactionEnvelope"
	// TypeTransactionResult is a refused transaction's result, read for entry 2's result code.
	TypeTransactionResult = "TransactionResult"
)

// value is what the codec decodes into and encodes from: an SDK XDR type.
type value interface {
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}

// constructors maps each known type name to a constructor of a fresh SDK value.
var constructors = map[string]func() value{
	TypeLedgerCloseMeta:     func() value { return new(xdr.LedgerCloseMeta) },
	TypeTransactionEnvelope: func() value { return new(xdr.TransactionEnvelope) },
	TypeTransactionResult:   func() value { return new(xdr.TransactionResult) },
}

// Types returns the type names the codec knows, sorted.
func Types() []string {
	out := make([]string, 0, len(constructors))
	for t := range constructors {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// UnknownTypeError is a type name the codec does not map to a Go SDK type. The cross-check reports it as a failure
// naming the type; nothing panics.
type UnknownTypeError struct {
	// Type is the name asked for.
	Type string
}

// Error names the type and the types the codec knows.
func (e *UnknownTypeError) Error() string {
	return fmt.Sprintf("the Go SDK codec does not know the XDR type %q (it knows %v)", e.Type, Types())
}

// Decode decodes b as xdrType with the SDK's xdr.SafeUnmarshal, which also fails when bytes are left over, and returns
// a pointer to the SDK value (*xdr.LedgerCloseMeta, *xdr.TransactionEnvelope or *xdr.TransactionResult).
func Decode(xdrType string, b []byte) (any, error) {
	mk, ok := constructors[xdrType]
	if !ok {
		return nil, &UnknownTypeError{Type: xdrType}
	}
	v := mk()
	if err := xdr.SafeUnmarshal(b, v); err != nil {
		return nil, fmt.Errorf("decoding %d bytes as %s with the Go SDK: %w", len(b), xdrType, err)
	}
	return v, nil
}

// Encode encodes an SDK XDR value with its MarshalBinary.
func Encode(v any) ([]byte, error) {
	m, ok := v.(encoding.BinaryMarshaler)
	if !ok {
		return nil, fmt.Errorf("the Go SDK codec cannot encode a %T: it is not an XDR value", v)
	}
	b, err := m.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("encoding a %T with the Go SDK: %w", v, err)
	}
	return b, nil
}

// RoundTrip decodes b as xdrType and re-encodes the value, returning the re-encoded bytes. The cross-check compares
// them with the file and with the Rust tool's.
func RoundTrip(xdrType string, b []byte) ([]byte, error) {
	v, err := Decode(xdrType, b)
	if err != nil {
		return nil, err
	}
	return Encode(v)
}

// FromBase64 returns the raw bytes of a base64 XDR value, as RPC returns them (metadataXdr, errorResultXdr).
func FromBase64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("the RPC value is not base64: %w", err)
	}
	return b, nil
}

// ToBase64 returns the base64 form of raw XDR bytes, as RPC takes them (sendTransaction's transaction,
// getLedgerEntries' keys).
func ToBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// DecodeBase64 decodes a base64 RPC value as xdrType.
func DecodeBase64(xdrType, s string) (any, error) {
	b, err := FromBase64(s)
	if err != nil {
		return nil, err
	}
	return Decode(xdrType, b)
}
