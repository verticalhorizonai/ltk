// Package xdrcodec is the Go SDK XDR codec: it maps each XDR type name an entry may declare (a catalogue record's
// xdrType) to its type in Stellar's Go SDK, github.com/stellar/go-stellar-sdk, decodes bytes of that type and
// re-encodes them. It knows LedgerCloseMeta, TransactionEnvelope and TransactionResult (codec.go); any other name is
// an UnknownTypeError naming it. `ltk check --also-go`'s cross-check (internal/check) calls it, and internal/generate
// decodes RPC's base64 XDR and entry 2's refusal result code with it.
//
// It is on the container-free side of the module: it imports only the standard library, internal/contract and the
// SDK's `xdr` package, never internal/orchestration, internal/generate or internal/repro, not even transitively; the
// Dependency guard test (cmd/ltk/deps_test.go) enforces that.
package xdrcodec
