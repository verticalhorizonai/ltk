package recipes

// The Shape recipe registry's interface and its network seam. A recipe reaches the network only through the small
// interfaces declared here: internal/generate's RPC client and friendbot satisfy them, so this package imports neither
// internal/generate nor net/http (no import cycle, and net/http stays in the two packages that talk to a network).

import (
	"context"
	"encoding/json"
	"time"

	"github.com/verticalhorizonai/ltk/internal/model"
)

// Recipe is one shape: it submits the shape's transactions to a network and captures the ledger that holds the
// shape's transaction, and it states the reproduced values it submits. A recipe value holds no state: the same value
// runs against any number of networks, one after the other or at once (the wallet-backend 729 reproduction runs entry
// 1's Run against its own network).
type Recipe interface {
	// Run funds the recipe's accounts through the network's friendbot, submits the shape's transaction in a later
	// ledger, polls getTransaction until it is applied, and returns the capture of its ledger.
	Run(ctx context.Context, net Network) (Capture, error)
	// Intended maps each reproduced-group JSON pointer of the capture's decoded JSON (the Rust XDR tool's form) to the
	// value the recipe submits. It is computed from the recipe's constants and its deterministic keys only, never from
	// a network.
	Intended() map[string]json.RawMessage
	// ID is the recipe's seed entry id (internal/model's seed definitions).
	ID() string
}

// RPC is the part of Stellar RPC the recipes use: internal/generate's *RPCClient satisfies it.
type RPC interface {
	// GetLedgerEntries reads ledger entries by their base64 LedgerKey XDR.
	GetLedgerEntries(ctx context.Context, keys []string) (LedgerEntries, error)
	// SendTransaction submits a signed base64 TransactionEnvelope.
	SendTransaction(ctx context.Context, envelope string) (SentTransaction, error)
	// GetTransaction reads a transaction by its hex hash.
	GetTransaction(ctx context.Context, hash string) (Transaction, error)
	// GetLedgers reads up to limit ledgers from start on.
	GetLedgers(ctx context.Context, start uint32, limit int) (Ledgers, error)
}

// Funder funds an account on the local network: internal/generate's *Friendbot satisfies it.
type Funder interface {
	// Fund creates and funds the account address (a G... strkey).
	Fund(ctx context.Context, address string) error
}

// The waits of a recipe (injected through Network, so a unit test never waits them out).
const (
	// PollInterval is the pause between two polls of RPC.
	PollInterval = time.Second
	// WaitTimeout bounds each wait of a recipe: an account to appear after funding, a transaction to be applied,
	// RPC to serve a ledger. The deciding test, the feasibility run made before this tool, waited at most 60 polls
	// of a second for each; two minutes covers a loaded machine with a wide margin. The spec states no value.
	WaitTimeout = 2 * time.Minute
)

// Network is the network a recipe runs against: its RPC, its friendbot and its passphrase, which must be the local
// standalone network's: the tool never runs against a public network.
type Network struct {
	// RPC is the network's Stellar RPC.
	RPC RPC
	// Friendbot is the network's local friendbot.
	Friendbot Funder
	// Passphrase is the network's passphrase.
	Passphrase string
	// Poll is the pause between two polls; zero means PollInterval.
	Poll time.Duration
	// Timeout bounds each wait; zero means WaitTimeout.
	Timeout time.Duration
}

// Capture is what a recipe returns: the LedgerCloseMeta of the ledger that holds the shape's transaction, as RPC's
// getLedgers served it, and where it came from.
type Capture struct {
	// EntryID is the seed entry id.
	EntryID string
	// XdrType is the type of XDR: LedgerCloseMeta for every seed.
	XdrType string
	// XDR is the capture's raw bytes: getLedgers' metadataXdr, base64-decoded.
	XDR []byte
	// Ledger is the ledger that holds the shape's transaction.
	Ledger uint32
	// Protocol is the protocol version read from the captured ledger header, never from the flag requested, so the
	// catalogue states what the ledger ran.
	Protocol int
	// TxHash is the shape's transaction hash, hex.
	TxHash string
	// Refusal is the network's refusal of the text-order form: entry 2 only, nil for the others.
	Refusal *Refusal
}

// Refusal is the raw evidence of the network's refusal of entry 2's text-order ChangeTrust. The Refusal
// evidence recorder (internal/catalogue/refusal.go) records it in the catalogue's networkRefusalEvidence.
type Refusal struct {
	// EnvelopeXdr is the submitted envelope, base64 XDR, its pool assets in strkey-text order.
	EnvelopeXdr string
	// ResultXdr is the network's TransactionResult, base64 XDR: sendTransaction's errorResultXdr, or the failed
	// getTransaction's resultXdr.
	ResultXdr string
	// Stage is where the network refused it: "sendTransaction" (status ERROR) or "getTransaction" (status FAILED).
	Stage string
	// Status is the status the network answered at that stage.
	Status string
	// TxHash is the refused transaction's hash, hex.
	TxHash string
}

// registry is the Shape recipe registry, in seed order.
var registry = [...]Recipe{signedPayloadSigner{}, poolShareTrustline{}, muxedPayment{}}

// Registry returns the three recipes in seed order (internal/model's Seeds order).
func Registry() []Recipe {
	return append([]Recipe(nil), registry[:]...)
}

// ByID returns the recipe of the seed entry id; false when no recipe has that id.
func ByID(id string) (Recipe, bool) {
	for _, r := range registry {
		if r.ID() == id {
			return r, true
		}
	}
	return nil, false
}

// seedOf is the seed definition of a recipe's entry id; the ids are model's constants, so it is always found.
func seedOf(id string) model.Seed {
	s, _ := model.SeedByID(id)
	return s
}
