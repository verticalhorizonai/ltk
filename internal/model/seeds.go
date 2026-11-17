package model

// The Seed entry definitions: the three seed entries' declared identity, their account roles and the derivation of
// every account key, and entry 2's issuer pair. The recipes (internal/generate/recipes), the catalogue check (which
// entry carries networkRefusalEvidence) and the SDK reproduction (repro.mjs, which reads no corpus file and builds its
// pool from entry 2's issuer pair and asset codes) read them from here and declare none of their own.

import (
	"crypto/sha256"
	"fmt"

	"github.com/stellar/go-stellar-sdk/keypair"
)

// The three seed entry ids, in seed order.
const (
	// SeedSignedPayloadSigner is entry 1: a SetOptions that adds a signed-payload signer with a 29-byte payload.
	SeedSignedPayloadSigner = "cap-0040-signed-payload-signer"
	// SeedPoolShareTrustline is entry 2: a pool-share ChangeTrust whose two issuers sort one way as text and the other
	// way as key bytes.
	SeedPoolShareTrustline = "cap-0038-pool-share-trustline"
	// SeedMuxedAccount is entry 3: a payment from a muxed source account to a muxed destination.
	SeedMuxedAccount = "cap-0027-muxed-account"
)

// Seed is one seed entry's declared identity.
type Seed struct {
	// ID is the entry id, CatalogueEntry.id and the --shape argument.
	ID string
	// Cap is the CAP the shape demonstrates (CatalogueEntry.cap).
	Cap string
	// XdrType is the XDR type of the captured file (CatalogueEntry.xdrType): the LedgerCloseMeta of the ledger that
	// holds the shape's transaction.
	XdrType string
	// Synthetic is the synthetic flag: false, every seed is captured from a local network.
	Synthetic bool
	// Shape is what the entry demonstrates.
	Shape string
	// Roles are the entry's account roles; each role's key is AccountKey(ID, role).
	Roles []string
	// RecordsNetworkRefusal is true for the one entry whose catalogue record carries networkRefusalEvidence (entry 2).
	// The catalogue check fails the record of that entry without it and of any other entry with it.
	RecordsNetworkRefusal bool
}

// SignedPayloadLength is the length in bytes of entry 1's signed payload: 29, so the XDR carries padding.
const SignedPayloadLength = 29

// PoolAssetCode is the asset code of both assets of entry 2's pool, as in the deciding test, the feasibility run made
// before this tool.
const PoolAssetCode = "USD"

// Entry 2's two issuer roles, found by a deterministic search over the roles issuer-<n> (pairs (i, j), j < i, in
// order), the deciding test's own search, and fixed here: the first pair whose public keys sort one way as strkey text
// and the other way as key bytes. seeds_test.go runs the search again and holds the property.
const (
	// PoolIssuerLowerBytesRole is the issuer whose public key is lower as key bytes (the protocol's order).
	PoolIssuerLowerBytesRole = "issuer-6"
	// PoolIssuerHigherBytesRole is the issuer whose public key is higher as key bytes.
	PoolIssuerHigherBytesRole = "issuer-3"
)

// The issuers' public keys, AccountKey(SeedPoolShareTrustline, role).Address() for the two roles above, in key-byte
// order: what repro.mjs builds its pool from. seeds_test.go derives them again.
const (
	// PoolIssuerLowerBytes is assetA's issuer: lower as key bytes, higher as strkey text.
	PoolIssuerLowerBytes = "GCG2JCL34VF4QCBY7EDPFQY54QAQ7QK62SB564FXQDIAL25LE5KCPEX4"
	// PoolIssuerHigherBytes is assetB's issuer: higher as key bytes, lower as strkey text.
	PoolIssuerHigherBytes = "GC7P7N7UYLS63YRQTZDAWBGZJ4F5EIWCBIJADQT4RSCXO6DZOORVFUZC"
)

// seeds are the three seeds in seed order.
var seeds = [...]Seed{
	{
		ID:        SeedSignedPayloadSigner,
		Cap:       "CAP-0040",
		XdrType:   "LedgerCloseMeta",
		Synthetic: false,
		Shape:     "SetOptions that adds a signed-payload signer with a 29-byte payload",
		Roles:     []string{"source", "signer"},
	},
	{
		ID:                    SeedPoolShareTrustline,
		Cap:                   "CAP-0038",
		XdrType:               "LedgerCloseMeta",
		Synthetic:             false,
		Shape:                 "pool-share ChangeTrust whose two issuers sort one way as text and the other way as key bytes",
		Roles:                 []string{"source", PoolIssuerLowerBytesRole, PoolIssuerHigherBytesRole},
		RecordsNetworkRefusal: true,
	},
	{
		ID:        SeedMuxedAccount,
		Cap:       "CAP-0027",
		XdrType:   "LedgerCloseMeta",
		Synthetic: false,
		Shape:     "payment from a muxed source account to a muxed destination",
		Roles:     []string{"source", "destination"},
	},
}

// Seeds returns the three seeds in seed order, each a copy.
func Seeds() []Seed {
	out := make([]Seed, len(seeds))
	for i, s := range seeds {
		s.Roles = append([]string(nil), s.Roles...)
		out[i] = s
	}
	return out
}

// SeedByID returns the seed whose id is id.
func SeedByID(id string) (Seed, bool) {
	for _, s := range Seeds() {
		if s.ID == id {
			return s, true
		}
	}
	return Seed{}, false
}

// AccountKey is the key of an entry's account role: keypair.FromRawSeed(sha256("ltk/<entry id>/<role>")). The keys
// are throwaway keys of the local network, derived from public text, so every run of a recipe uses the same accounts.
func AccountKey(entryID, role string) (*keypair.Full, error) {
	kp, err := keypair.FromRawSeed(sha256.Sum256([]byte("ltk/" + entryID + "/" + role)))
	if err != nil {
		return nil, fmt.Errorf("the key of %s/%s: %w", entryID, role, err)
	}
	return kp, nil
}
