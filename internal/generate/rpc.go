package generate

// The RPC client: a minimal JSON-RPC 2.0 client over net/http with exactly the four methods generation uses,
// `getLedgerEntries`, `sendTransaction`,
// `getTransaction` and `getLedgers`, whose `metadataXdr` is the capture. Values stay base64 as RPC sends them;
// internal/generate/xdrcodec decodes them. The requests are the ones the deciding test, the feasibility run made
// before this tool, sent and saw answered.
// The recipes use it through the small interface they declare (internal/generate/recipes.RPC); its results are the
// types that interface declares, named here through aliases.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
)

// rpcRequestTimeout bounds one RPC request; the callers poll.
const rpcRequestTimeout = 30 * time.Second

// RPCClient is a JSON-RPC 2.0 client of one Stellar RPC endpoint.
type RPCClient struct {
	url  string
	http *http.Client
	id   atomic.Int64
}

// NewRPCClient returns the client of the RPC at url (a Network's RPCURL).
func NewRPCClient(url string) *RPCClient {
	return &RPCClient{url: url, http: &http.Client{Timeout: rpcRequestTimeout}}
}

// The four methods' results, declared beside the recipes' RPC interface, so the recipes import no RPC client.
type (
	// LedgerEntry is one entry of getLedgerEntries.
	LedgerEntry = recipes.LedgerEntry
	// LedgerEntries is getLedgerEntries' result.
	LedgerEntries = recipes.LedgerEntries
	// SentTransaction is sendTransaction's result.
	SentTransaction = recipes.SentTransaction
	// Transaction is getTransaction's result.
	Transaction = recipes.Transaction
	// LedgerInfo is one ledger of getLedgers.
	LedgerInfo = recipes.LedgerInfo
	// Ledgers is getLedgers' result.
	Ledgers = recipes.Ledgers
)

// The client is the recipes' RPC.
var _ recipes.RPC = (*RPCClient)(nil)

// RPCError is an error object RPC answered with (JSON-RPC 2.0 §5.1).
type RPCError struct {
	// Method is the method called.
	Method string
	// Code is the error's code.
	Code int
	// Message is the error's message.
	Message string
	// Data is the error's data, when there is any.
	Data json.RawMessage
}

// Error names the method, the code and the message.
func (e *RPCError) Error() string {
	return fmt.Sprintf("RPC %s: error %d: %s", e.Method, e.Code, e.Message)
}

// call makes one JSON-RPC call of method with params (nil for none) and decodes its result into out.
func (c *RPCClient) call(ctx context.Context, method string, params, out any) error {
	req := map[string]any{"jsonrpc": "2.0", "id": c.id.Add(1), "method": method}
	if params != nil {
		req["params"] = params
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("RPC %s: %w", method, err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("RPC %s: %w", method, err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(hreq)
	if err != nil {
		return fmt.Errorf("RPC %s: %w", method, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("RPC %s: %w", method, err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("RPC %s: HTTP %d, not a JSON-RPC answer: %.200s", method, resp.StatusCode, b)
	}
	if env.Error != nil {
		return &RPCError{Method: method, Code: env.Error.Code, Message: env.Error.Message, Data: env.Error.Data}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC %s: HTTP %d", method, resp.StatusCode)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("RPC %s: the answer has no result", method)
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("RPC %s: reading the result: %w", method, err)
	}
	return nil
}

// GetLedgerEntries reads the ledger entries of keys (base64 LedgerKey XDR): a recipe reads its funded account and its
// sequence number with it.
func (c *RPCClient) GetLedgerEntries(ctx context.Context, keys []string) (LedgerEntries, error) {
	var out LedgerEntries
	err := c.call(ctx, "getLedgerEntries", map[string]any{"keys": keys}, &out)
	return out, err
}

// SendTransaction submits a signed envelope (base64 TransactionEnvelope XDR).
func (c *RPCClient) SendTransaction(ctx context.Context, envelope string) (SentTransaction, error) {
	var out SentTransaction
	err := c.call(ctx, "sendTransaction", map[string]any{"transaction": envelope}, &out)
	return out, err
}

// GetTransaction reads the transaction of hash (hex); a recipe polls it until it is no longer NOT_FOUND.
func (c *RPCClient) GetTransaction(ctx context.Context, hash string) (Transaction, error) {
	var out Transaction
	err := c.call(ctx, "getTransaction", map[string]any{"hash": hash}, &out)
	return out, err
}

// GetLedgers reads up to limit ledgers from start on; the recipe captures the ledger holding its transaction with
// limit 1 and keeps its MetadataXdr, the LedgerCloseMeta that is the capture.
func (c *RPCClient) GetLedgers(ctx context.Context, start uint32, limit int) (Ledgers, error) {
	var out Ledgers
	err := c.call(ctx, "getLedgers", map[string]any{"startLedger": start, "pagination": map[string]any{"limit": limit}}, &out)
	return out, err
}
