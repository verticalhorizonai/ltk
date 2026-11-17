//go:build integration

package generate_test

// The integration tier of internal/generate's RPC client and codec, selected by the `integration` build tag: on the
// current quickstart, a fixed-seed account is funded by the friendbot, a transaction is sent with
// sendTransaction and confirmed with getTransaction, and getLedgers' metadataXdr of its ledger is the capture. The
// capture decodes with the Go SDK codec and round-trips byte for byte through the codec and through the Rust XDR tool.
// Set LTK_CAPTURE_DIR to keep the capture's bytes there.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/generate"
	"github.com/verticalhorizonai/ltk/internal/generate/xdrcodec"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// managedListings are the three label-filtered listings of every ltk.managed resource.
func managedListings(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, args := range [][]string{
		{"ps", "-a", "-q", "--filter", "label=ltk.managed=true"},
		{"network", "ls", "-q", "--filter", "label=ltk.managed=true"},
		{"volume", "ls", "-q", "--filter", "label=ltk.managed=true"},
	} {
		b, err := exec.Command("docker", args...).Output()
		if err != nil {
			t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
		}
		out = append(out, strings.TrimSpace(string(b)))
	}
	return out
}

// TestIntegrationCaptureRoundTrip captures a real ledger with the four RPC methods and holds the capture's round trip
// through both decoders: the first real LedgerCloseMeta at protocol 30. Nothing labelled ltk.managed may exist before
// it, and nothing it started is left after it.
func TestIntegrationCaptureRoundTrip(t *testing.T) {
	for _, l := range managedListings(t) {
		if l != "" {
			t.Fatalf("ltk.managed resources exist before the test; clean them by hand (the test never cleans them itself, which would mask a leak): %q", l)
		}
	}
	runID, err := orchestration.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	r, err := orchestration.NewRunner(runID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Reap(ctx); err != nil {
			t.Errorf("Reap: %v", err)
		}
		after, err := r.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if left := orchestration.Leftovers(before, after); len(left) != 0 {
			t.Errorf("Leftovers after teardown: %q", left)
		}
		for i, l := range managedListings(t) {
			if l != "" {
				t.Errorf("label-filtered listing %d not empty after teardown: %q", i, l)
			}
		}
	})
	if err := r.EnsureImages(ctx, orchestration.CheckImages); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := r.StartQuickstart(ctx, orchestration.QuickstartCurrent, orchestration.CurrentProtocol)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("run %s: quickstart ready at protocol %d after %s", runID, n.Protocol, time.Since(start).Round(time.Millisecond))

	// Fund the fixed-seed source through the local friendbot, then read its sequence with getLedgerEntries.
	src, err := keypair.FromRawSeed(sha256.Sum256([]byte("ltk/probe/role")))
	if err != nil {
		t.Fatal(err)
	}
	if err := generate.NewFriendbot(n.FriendbotURL).Fund(ctx, src.Address()); err != nil {
		t.Fatal(err)
	}
	rpc := generate.NewRPCClient(n.RPCURL)
	var key xdr.LedgerKey
	if err := key.SetAccount(xdr.MustAddress(src.Address())); err != nil {
		t.Fatal(err)
	}
	keyBytes, err := key.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	for i := 0; i < 30 && seq == 0; i++ {
		entries, err := rpc.GetLedgerEntries(ctx, []string{xdrcodec.ToBase64(keyBytes)})
		if err == nil && len(entries.Entries) == 1 {
			var data xdr.LedgerEntryData
			if err := xdr.SafeUnmarshalBase64(entries.Entries[0].XDR, &data); err != nil {
				t.Fatal(err)
			}
			seq = int64(data.MustAccount().SeqNum)
			break
		}
		time.Sleep(time.Second)
	}
	if seq == 0 {
		t.Fatal("getLedgerEntries never returned the funded source account")
	}

	// Send entry 1's shape: a SetOptions adding a 29-byte signed-payload signer.
	signer, err := keypair.FromRawSeed(sha256.Sum256([]byte("ltk/probe/signer")))
	if err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("ltk/probe/payload"))
	sp, err := strkey.NewSignedPayload(signer.Address(), payload[:29])
	if err != nil {
		t.Fatal(err)
	}
	pStrkey, err := sp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: src.Address(), Sequence: seq},
		IncrementSequenceNum: true,
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
		Operations:           []txnbuild.Operation{&txnbuild.SetOptions{Signer: &txnbuild.Signer{Address: pStrkey, Weight: 1}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx, err = tx.Sign(n.Passphrase, src); err != nil {
		t.Fatal(err)
	}
	envB64, err := tx.Base64()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := tx.HashHex(n.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := rpc.SendTransaction(ctx, envB64)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Status != "PENDING" || sent.Hash != hash {
		t.Fatalf("sendTransaction: %+v (hash %s)", sent, hash)
	}
	var got generate.Transaction
	for i := 0; i < 60; i++ {
		if got, err = rpc.GetTransaction(ctx, hash); err != nil {
			t.Fatal(err)
		}
		if got.Status != "NOT_FOUND" {
			break
		}
		time.Sleep(time.Second)
	}
	if got.Status != "SUCCESS" || got.Ledger == 0 {
		t.Fatalf("getTransaction: %+v", got)
	}

	// The capture: getLedgers' metadataXdr of that ledger.
	var info generate.LedgerInfo
	for i := 0; i < 60; i++ {
		ledgers, err := rpc.GetLedgers(ctx, got.Ledger, 1)
		if err == nil && len(ledgers.Ledgers) == 1 && ledgers.Ledgers[0].Sequence == got.Ledger {
			info = ledgers.Ledgers[0]
			break
		}
		time.Sleep(time.Second)
	}
	if info.MetadataXdr == "" {
		t.Fatalf("getLedgers never served ledger %d", got.Ledger)
	}
	capture, err := xdrcodec.FromBase64(info.MetadataXdr)
	if err != nil {
		t.Fatal(err)
	}
	v, err := xdrcodec.Decode(xdrcodec.TypeLedgerCloseMeta, capture)
	if err != nil {
		t.Fatal(err)
	}
	lcm := v.(*xdr.LedgerCloseMeta)
	if lcm.LedgerSequence() != got.Ledger || lcm.ProtocolVersion() != uint32(orchestration.CurrentProtocol) {
		t.Fatalf("the capture is ledger %d at protocol %d", lcm.LedgerSequence(), lcm.ProtocolVersion())
	}
	if lcm.CountTransactions() != 1 {
		t.Fatalf("the capture holds %d transactions; want 1", lcm.CountTransactions())
	}
	if h := lcm.TransactionHash(0); hex.EncodeToString(h[:]) != hash {
		t.Fatalf("the capture holds %d transactions; want only %s", lcm.CountTransactions(), hash)
	}
	sum := sha256.Sum256(capture)
	t.Logf("capture: ledger %d, LedgerCloseMeta v%d, protocol %d, %d bytes, sha256 %x, tx %s", got.Ledger, lcm.V,
		lcm.ProtocolVersion(), len(capture), sum, hash)

	goRT, err := xdrcodec.RoundTrip(xdrcodec.TypeLedgerCloseMeta, capture)
	if err != nil || !bytes.Equal(goRT, capture) {
		t.Fatalf("the Go SDK round trip of the capture differs: %v", err)
	}
	tool, err := r.StartXDRTool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	jsonForm, err := tool.Decode(ctx, xdrcodec.TypeLedgerCloseMeta, capture)
	if err != nil {
		t.Fatal(err)
	}
	re, err := tool.Encode(ctx, xdrcodec.TypeLedgerCloseMeta, jsonForm)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(re, capture) {
		t.Fatalf("the Rust round trip of the capture differs: %d bytes in, %d out", len(capture), len(re))
	}
	t.Logf("Rust round trip identical: %d bytes, decoded JSON %d bytes, in %s", len(capture), len(jsonForm), time.Since(start).Round(time.Millisecond))

	if dir := os.Getenv("LTK_CAPTURE_DIR"); dir != "" {
		for name, b := range map[string][]byte{"capture-p30.xdr": capture, "capture-p30.json": jsonForm} {
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("capture kept in %s", dir)
	}
}
