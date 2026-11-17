package recipes

// The results of the four RPC methods the recipes use, as Stellar RPC writes them.
// They are declared here, beside the RPC interface, so that internal/generate's client returns them and satisfies the
// interface without an import cycle; internal/generate names them through type aliases. Values stay base64 as RPC
// sends them; internal/generate/xdrcodec decodes them.

// LedgerEntry is one entry of getLedgerEntries.
type LedgerEntry struct {
	// Key is the LedgerKey, base64 XDR.
	Key string `json:"key"`
	// XDR is the entry's LedgerEntryData, base64 XDR.
	XDR string `json:"xdr"`
	// LastModifiedLedger is the ledger that last changed the entry.
	LastModifiedLedger uint32 `json:"lastModifiedLedgerSeq"`
}

// LedgerEntries is getLedgerEntries' result.
type LedgerEntries struct {
	// Entries are the entries found; a key with no entry is absent.
	Entries []LedgerEntry `json:"entries"`
	// LatestLedger is the latest ledger RPC knows.
	LatestLedger uint32 `json:"latestLedger"`
}

// SentTransaction is sendTransaction's result.
type SentTransaction struct {
	// Status is PENDING, DUPLICATE, TRY_AGAIN_LATER or ERROR.
	Status string `json:"status"`
	// Hash is the transaction's hash, hex.
	Hash string `json:"hash"`
	// LatestLedger is the latest ledger RPC knows.
	LatestLedger uint32 `json:"latestLedger"`
	// ErrorResultXdr is the TransactionResult, base64 XDR, when Status is ERROR: entry 2's refusal.
	ErrorResultXdr string `json:"errorResultXdr,omitempty"`
}

// Transaction is getTransaction's result.
type Transaction struct {
	// Status is SUCCESS, FAILED or NOT_FOUND.
	Status string `json:"status"`
	// Ledger is the ledger that holds the transaction, when it was applied.
	Ledger uint32 `json:"ledger"`
	// EnvelopeXdr is the transaction's envelope, base64 XDR.
	EnvelopeXdr string `json:"envelopeXdr,omitempty"`
	// ResultXdr is its TransactionResult, base64 XDR.
	ResultXdr string `json:"resultXdr,omitempty"`
	// LatestLedger is the latest ledger RPC knows.
	LatestLedger uint32 `json:"latestLedger"`
}

// LedgerInfo is one ledger of getLedgers.
type LedgerInfo struct {
	// Hash is the ledger's hash, hex.
	Hash string `json:"hash"`
	// Sequence is the ledger's sequence number.
	Sequence uint32 `json:"sequence"`
	// LedgerCloseTime is the ledger's close time, Unix seconds as RPC writes it.
	LedgerCloseTime string `json:"ledgerCloseTime"`
	// HeaderXdr is the LedgerHeaderHistoryEntry, base64 XDR.
	HeaderXdr string `json:"headerXdr"`
	// MetadataXdr is the LedgerCloseMeta, base64 XDR: the capture.
	MetadataXdr string `json:"metadataXdr"`
}

// Ledgers is getLedgers' result.
type Ledgers struct {
	// Ledgers are the ledgers from the start ledger on.
	Ledgers []LedgerInfo `json:"ledgers"`
	// LatestLedger is the latest ledger RPC knows.
	LatestLedger uint32 `json:"latestLedger"`
	// OldestLedger is the oldest ledger RPC keeps.
	OldestLedger uint32 `json:"oldestLedger"`
}
