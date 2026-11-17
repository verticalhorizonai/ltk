package generate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// rpcRecorder is an httptest JSON-RPC server that records each request and answers with the next canned body.
type rpcRecorder struct {
	srv      *httptest.Server
	requests []map[string]any
	answer   func(method string) (status int, body string)
}

func newRPCRecorder(t *testing.T, answer func(method string) (int, string)) *rpcRecorder {
	t.Helper()
	r := &rpcRecorder{answer: answer}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var m map[string]any
		if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s with Content-Type %q", req.Method, req.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(req.Body).Decode(&m); err != nil {
			t.Errorf("request body: %v", err)
		}
		r.requests = append(r.requests, m)
		status, body := r.answer(m["method"].(string))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// Each of the four methods sends a JSON-RPC 2.0 request with the parameters of the deciding test, the feasibility run
// made before this tool, and reads its result.
func TestRPCClientMethods(t *testing.T) {
	results := map[string]string{
		"getLedgerEntries": `{"entries":[{"key":"AAAAAA==","xdr":"AAAAAQ==","lastModifiedLedgerSeq":12}],"latestLedger":15}`,
		"sendTransaction":  `{"status":"PENDING","hash":"ab12","latestLedger":15}`,
		"getTransaction":   `{"status":"SUCCESS","ledger":16,"envelopeXdr":"AAAA","resultXdr":"BBBB","latestLedger":17}`,
		"getLedgers":       `{"ledgers":[{"hash":"cd34","sequence":16,"ledgerCloseTime":"1763300000","headerXdr":"HHHH","metadataXdr":"MMMM"}],"latestLedger":17,"oldestLedger":2}`,
	}
	rec := newRPCRecorder(t, func(method string) (int, string) {
		return 200, `{"jsonrpc":"2.0","id":1,"result":` + results[method] + `}`
	})
	c := NewRPCClient(rec.srv.URL)
	ctx := context.Background()

	entries, err := c.GetLedgerEntries(ctx, []string{"AAAAAA=="})
	if err != nil || !reflect.DeepEqual(entries, LedgerEntries{Entries: []LedgerEntry{{Key: "AAAAAA==", XDR: "AAAAAQ==", LastModifiedLedger: 12}}, LatestLedger: 15}) {
		t.Fatalf("GetLedgerEntries = %+v, %v", entries, err)
	}
	sent, err := c.SendTransaction(ctx, "ENVELOPE")
	if err != nil || sent != (SentTransaction{Status: "PENDING", Hash: "ab12", LatestLedger: 15}) {
		t.Fatalf("SendTransaction = %+v, %v", sent, err)
	}
	tx, err := c.GetTransaction(ctx, "ab12")
	if err != nil || tx != (Transaction{Status: "SUCCESS", Ledger: 16, EnvelopeXdr: "AAAA", ResultXdr: "BBBB", LatestLedger: 17}) {
		t.Fatalf("GetTransaction = %+v, %v", tx, err)
	}
	ledgers, err := c.GetLedgers(ctx, 16, 1)
	if err != nil || len(ledgers.Ledgers) != 1 || ledgers.Ledgers[0].MetadataXdr != "MMMM" || ledgers.Ledgers[0].Sequence != 16 {
		t.Fatalf("GetLedgers = %+v, %v", ledgers, err)
	}

	wantParams := []string{
		`{"keys":["AAAAAA=="]}`,
		`{"transaction":"ENVELOPE"}`,
		`{"hash":"ab12"}`,
		`{"pagination":{"limit":1},"startLedger":16}`,
	}
	wantMethods := []string{"getLedgerEntries", "sendTransaction", "getTransaction", "getLedgers"}
	if len(rec.requests) != 4 {
		t.Fatalf("%d requests", len(rec.requests))
	}
	ids := map[float64]bool{}
	for i, req := range rec.requests {
		params, _ := json.Marshal(req["params"])
		if req["jsonrpc"] != "2.0" || req["method"] != wantMethods[i] || string(params) != wantParams[i] {
			t.Errorf("request %d = %v; want method %s params %s", i, req, wantMethods[i], wantParams[i])
		}
		ids[req["id"].(float64)] = true
	}
	if len(ids) != 4 {
		t.Errorf("request ids are not distinct: %v", ids)
	}
}

// JSON-RPC 2.0 §5.1: an error object is a *RPCError with its code and message, also on an HTTP error status.
func TestRPCClientJSONRPCError(t *testing.T) {
	for _, status := range []int{200, 400} {
		rec := newRPCRecorder(t, func(string) (int, string) {
			return status, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"startLedger must be between the oldest ledger: 2 and the latest ledger: 9","data":"x"}}`
		})
		_, err := NewRPCClient(rec.srv.URL).GetLedgers(context.Background(), 99, 1)
		var re *RPCError
		if !errors.As(err, &re) || re.Method != "getLedgers" || re.Code != -32602 || !strings.Contains(re.Message, "startLedger") {
			t.Fatalf("HTTP %d: err = %v; want an *RPCError", status, err)
		}
	}
}

// A body that is not a JSON-RPC answer, an HTTP error with no error object, or a missing result is an error.
func TestRPCClientBadAnswers(t *testing.T) {
	for _, a := range []struct {
		status int
		body   string
	}{
		{502, "<html>Bad Gateway</html>"},
		{503, `{"jsonrpc":"2.0","id":1,"result":{"status":"PENDING"}}`},
		{200, `{"jsonrpc":"2.0","id":1}`},
		{200, `{"jsonrpc":"2.0","id":1,"result":"not an object"}`},
	} {
		rec := newRPCRecorder(t, func(string) (int, string) { return a.status, a.body })
		if _, err := NewRPCClient(rec.srv.URL).SendTransaction(context.Background(), "E"); err == nil {
			t.Errorf("HTTP %d %q: no error", a.status, a.body)
		}
	}
	if _, err := NewRPCClient("http://127.0.0.1:1/rpc").GetTransaction(context.Background(), "h"); err == nil {
		t.Error("an unreachable RPC gave no error")
	}
}

// Fund asks the local friendbot for the address; an answer but 200 is an error carrying
// the friendbot's answer.
func TestFriendbotFund(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = append(got, req.Method+" "+req.URL.Path+"?"+req.URL.RawQuery)
		if req.URL.Query().Get("addr") == "GALREADY" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"account already funded to starting balance"}`))
			return
		}
		_, _ = w.Write([]byte(`{"successful":true}`))
	}))
	defer srv.Close()
	fb := NewFriendbot(srv.URL + "/friendbot")
	if err := fb.Fund(context.Background(), "GABC"); err != nil {
		t.Fatal(err)
	}
	err := fb.Fund(context.Background(), "GALREADY")
	if err == nil || !strings.Contains(err.Error(), "already funded") || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("Fund of a funded account: %v", err)
	}
	want := []string{"GET /friendbot?addr=GABC", "GET /friendbot?addr=GALREADY"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests %q; want %q", got, want)
	}
}
