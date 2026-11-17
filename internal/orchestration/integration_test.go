//go:build integration

package orchestration_test

// The integration tier of internal/orchestration, selected by the `integration` build tag: real Docker, real quickstart
// images. Run serially (`-p 1`), natively. Each test refuses to start when any ltk.managed resource exists (a stale run
// is cleaned by hand), and ends with the reaper, then asserts that Leftovers of a full before/after Snapshot is empty
// and that the three label-filtered listings are empty.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os/exec"
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

// dockerOut runs a read-only docker command of the test and returns its trimmed standard output.
func dockerOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// managedListings are the three label-filtered listings of every ltk.managed resource.
func managedListings(t *testing.T) []string {
	t.Helper()
	return []string{
		dockerOut(t, "ps", "-a", "-q", "--filter", "label=ltk.managed=true"),
		dockerOut(t, "network", "ls", "-q", "--filter", "label=ltk.managed=true"),
		dockerOut(t, "volume", "ls", "-q", "--filter", "label=ltk.managed=true"),
	}
}

// cleanRun returns a runner of a fresh run and registers its teardown: the reaper, then the clean-stop assertions
// (the before/after difference of every container, network and volume is empty).
func cleanRun(t *testing.T) (*orchestration.Runner, string, context.Context) {
	t.Helper()
	for _, l := range managedListings(t) {
		if l != "" {
			t.Fatalf("ltk.managed resources exist before the test; clean them by hand:\n"+
				"  docker rm -f -v $(docker ps -a -q --filter label=ltk.managed=true)\n"+
				"  docker network rm $(docker network ls -q --filter label=ltk.managed=true)\n"+
				"  docker volume rm $(docker volume ls -q --filter label=ltk.managed=true)\nfound: %q", l)
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
	t.Logf("run id %s", runID)
	t.Cleanup(func() {
		start := time.Now()
		if err := r.Reap(ctx); err != nil {
			t.Errorf("Reap: %v", err)
		}
		t.Logf("teardown took %s", time.Since(start).Round(time.Millisecond))
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
	return r, runID, ctx
}

// probeKey is the fixed-seed key of the probe: keypair.FromRawSeed(sha256("ltk/probe/role")) (the form of the recipes'
// keys, `ltk/<shape>/<role>`).
func probeKey(t *testing.T) *keypair.Full {
	t.Helper()
	k, err := keypair.FromRawSeed(sha256.Sum256([]byte("ltk/probe/role")))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// accountKey is the base64 LedgerKey of an account.
func accountKey(t *testing.T, address string) string {
	t.Helper()
	var key xdr.LedgerKey
	if err := key.SetAccount(xdr.MustAddress(address)); err != nil {
		t.Fatal(err)
	}
	b, err := key.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return xdrcodec.ToBase64(b)
}

// startAndProbe brings up image at protocol and proves the local network's isolation on it: its own labelled network,
// the one published port on 127.0.0.1, the protocol and passphrase, and a fixed-seed account funded by the local
// friendbot and read back with getLedgerEntries.
func startAndProbe(t *testing.T, image string, protocol int) {
	r, runID, ctx := cleanRun(t)
	if err := r.EnsureImages(ctx, []string{image}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := r.StartQuickstart(ctx, image, protocol)
	if err != nil {
		t.Fatalf("StartQuickstart: %v", err)
	}
	t.Logf("ready after %s: %+v", time.Since(start).Round(time.Millisecond), *n)
	if n.Protocol != protocol || n.Passphrase != orchestration.StandalonePassphrase {
		t.Fatalf("protocol %d, passphrase %q", n.Protocol, n.Passphrase)
	}

	// The container's labels and its one network.
	for obj, format := range map[string]string{n.Container: "{{json .Config.Labels}}", n.Name: "{{json .Labels}}"} {
		labels := dockerOut(t, "inspect", "--format", format, obj)
		if !strings.Contains(labels, `"ltk.managed":"true"`) || !strings.Contains(labels, `"ltk.run":"`+runID+`"`) {
			t.Errorf("%s labels %s", obj, labels)
		}
	}
	var nets map[string]any
	if err := json.Unmarshal([]byte(dockerOut(t, "inspect", "--format", "{{json .NetworkSettings.Networks}}", n.Container)), &nets); err != nil {
		t.Fatal(err)
	}
	if _, ok := nets[n.Name]; !ok || len(nets) != 1 {
		t.Errorf("the container's networks are %v; want only %s", nets, n.Name)
	}
	// Every published port is bound to 127.0.0.1, and only 8000 is published.
	var ports map[string][]struct{ HostIp, HostPort string }
	if err := json.Unmarshal([]byte(dockerOut(t, "inspect", "--format", "{{json .NetworkSettings.Ports}}", n.Container)), &ports); err != nil {
		t.Fatal(err)
	}
	published := 0
	for p, bindings := range ports {
		for _, b := range bindings {
			published++
			if b.HostIp != "127.0.0.1" || p != "8000/tcp" {
				t.Errorf("published %s on %s:%s", p, b.HostIp, b.HostPort)
			}
		}
	}
	if published != 1 {
		t.Errorf("%d published bindings: %v", published, ports)
	}
	t.Logf("docker port: %s", dockerOut(t, "port", n.Container))
	t.Logf("arguments: %s", dockerOut(t, "inspect", "--format", "{{json .Config.Entrypoint}} {{json .Args}} {{.Config.Image}}", n.Container))

	// The friendbot funds a fixed-seed account; getLedgerEntries reads it back.
	k := probeKey(t)
	fundStart := time.Now()
	if err := generate.NewFriendbot(n.FriendbotURL).Fund(ctx, k.Address()); err != nil {
		t.Fatalf("Fund: %v", err)
	}
	t.Logf("friendbot funded %s in %s", k.Address(), time.Since(fundStart).Round(time.Millisecond))
	rpc := generate.NewRPCClient(n.RPCURL)
	var entries generate.LedgerEntries
	for i := 0; i < 30; i++ {
		if entries, err = rpc.GetLedgerEntries(ctx, []string{accountKey(t, k.Address())}); err == nil && len(entries.Entries) == 1 {
			break
		}
		time.Sleep(time.Second)
	}
	if len(entries.Entries) != 1 {
		t.Fatalf("getLedgerEntries never returned the funded account: %v", err)
	}
	var data xdr.LedgerEntryData
	if err := xdr.SafeUnmarshalBase64(entries.Entries[0].XDR, &data); err != nil {
		t.Fatal(err)
	}
	acct := data.MustAccount()
	if acct.AccountId.Address() != k.Address() || acct.Balance <= 0 {
		t.Fatalf("account %s balance %d", acct.AccountId.Address(), acct.Balance)
	}
	t.Logf("account %s: balance %d stroops, seq %d, last modified in ledger %d", k.Address(), acct.Balance, acct.SeqNum, entries.Entries[0].LastModifiedLedger)

	// What the log says about protocol upgrades and the friendbot.
	logs, _ := exec.Command("docker", "logs", n.Container).CombinedOutput()
	for _, line := range strings.Split(string(logs), "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "protocol version") || strings.Contains(l, "upgrades:") || strings.Contains(l, "friendbot") {
			t.Logf("log: %s", strings.TrimSpace(line))
		}
	}
}

// The current quickstart at protocol 30: isolated, and funding through its local friendbot.
func TestIntegrationQuickstartCurrent(t *testing.T) {
	startAndProbe(t, orchestration.QuickstartCurrent, orchestration.CurrentProtocol)
}

// The protocol-28 quickstart at protocol 28 with no flag (the fallback and the wallet-backend 729 network): isolated,
// and funding through its local friendbot.
func TestIntegrationQuickstartProtocol28(t *testing.T) {
	startAndProbe(t, orchestration.QuickstartProtocol28, orchestration.FallbackProtocol)
}

// sdkEnvelope builds a signed SetOptions adding a 29-byte signed-payload signer (entry 1's shape) with the SDK.
func sdkEnvelope(t *testing.T) []byte {
	t.Helper()
	src := probeKey(t)
	signer, err := keypair.FromRawSeed(sha256.Sum256([]byte("ltk/probe/signer")))
	if err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("ltk/probe/payload"))
	sp, err := strkey.NewSignedPayload(signer.Address(), payload[:29])
	if err != nil {
		t.Fatal(err)
	}
	p, err := sp.Encode()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: src.Address(), Sequence: 1 << 32},
		IncrementSequenceNum: true,
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
		Operations:           []txnbuild.Operation{&txnbuild.SetOptions{Signer: &txnbuild.Signer{Address: p, Weight: 1}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx, err = tx.Sign(orchestration.StandalonePassphrase, src); err != nil {
		t.Fatal(err)
	}
	b, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The Rust XDR tool of the current quickstart round-trips a real XDR value, an SDK-built TransactionEnvelope, byte for
// byte, agreeing with the Go SDK codec, and fails cleanly, carrying its message, on bytes that are not the declared
// type.
func TestIntegrationXDRToolRoundTrip(t *testing.T) {
	r, _, ctx := cleanRun(t)
	if err := r.EnsureImages(ctx, []string{orchestration.QuickstartCurrent}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	tool, err := r.StartXDRTool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the XDR tool's container started in %s", time.Since(start).Round(time.Millisecond))
	ver, _, err := r.Exec(ctx, r.Name("xdr"), nil, "stellar-xdr", "version")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("stellar-xdr version: %s", strings.TrimSpace(string(ver)))

	raw := sdkEnvelope(t)
	start = time.Now()
	jsonForm, err := tool.Decode(ctx, xdrcodec.TypeTransactionEnvelope, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	re, err := tool.Encode(ctx, xdrcodec.TypeTransactionEnvelope, jsonForm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	t.Logf("one decode and encode took %s", time.Since(start).Round(time.Millisecond))
	if !bytes.Equal(re, raw) {
		t.Fatalf("the Rust round trip differs: %d bytes in, %d out", len(raw), len(re))
	}
	goRT, err := xdrcodec.RoundTrip(xdrcodec.TypeTransactionEnvelope, raw)
	if err != nil || !bytes.Equal(goRT, re) {
		t.Fatalf("the Go SDK round trip disagrees with the Rust one: %v", err)
	}
	t.Logf("TransactionEnvelope: %d bytes round-tripped identically; decoded JSON is %d bytes", len(raw), len(jsonForm))

	for name, c := range map[string]struct {
		typ string
		b   []byte
	}{
		"text, not XDR":                  {xdrcodec.TypeTransactionEnvelope, []byte("not xdr at all\n")},
		"an envelope as LedgerCloseMeta": {xdrcodec.TypeLedgerCloseMeta, raw},
		"a truncated envelope":           {xdrcodec.TypeTransactionEnvelope, raw[:len(raw)-5]},
		"an envelope with bytes over":    {xdrcodec.TypeTransactionEnvelope, append(append([]byte{}, raw...), 0, 0, 0, 0)},
		"an unknown type":                {"NoSuchType", raw},
	} {
		_, err := tool.Decode(ctx, c.typ, c.b)
		var te *orchestration.XDRToolError
		if !errors.As(err, &te) || te.Stderr == "" {
			t.Errorf("%s: err = %v; want an XDRToolError carrying the tool's message", name, err)
			continue
		}
		t.Logf("%s: exit %d: %s", name, te.ExitCode, te.Error())
	}
	_, err = tool.Encode(ctx, xdrcodec.TypeTransactionEnvelope, []byte(`{"not":"an envelope"}`))
	var te *orchestration.XDRToolError
	if !errors.As(err, &te) || te.Op != "encode" {
		t.Errorf("Encode of JSON that is not an envelope: %v", err)
	} else {
		t.Logf("encode of bad JSON: exit %d: %s", te.ExitCode, te.Stderr)
	}
}
