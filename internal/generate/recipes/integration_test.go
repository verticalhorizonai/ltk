//go:build integration

package recipes_test

// The recipes' native runs, in the integration tier (the `integration` build tag): for each recipe, one quickstart
// network at a time, started by the launcher, the recipe runs through internal/generate's RPC client and friendbot; its
// capture is decoded by the Go SDK codec and by the Rust XDR tool, both round trips must be byte-identical, and every
// pointer of Intended() must hold its intended value in the Rust tool's decoded JSON. Each run records the protocol,
// the ledger, the byte length, the sha256 and the transaction hash, and tears its network down.
//
// LTK_RECIPE_PROTOCOL=28 runs the protocol-28 image (the generation fallback); the default is the current image at the
// current protocol. LTK_RECIPES (comma-separated entry ids) selects recipes. LTK_CAPTURE_DIR keeps each capture as
// <entry id>-p<protocol><LTK_CAPTURE_SUFFIX>.xdr and .json there (and entry 2's refusal as -refusal.json).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/generate"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
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

// resolve returns the value at the JSON pointer ptr of doc, or nil.
func resolve(doc *catalogue.Node, ptr string) *catalogue.Node {
	n := doc
	for _, tok := range strings.Split(ptr, "/")[1:] {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch n.Kind {
		case catalogue.KindObject:
			n = n.Member(tok)
		case catalogue.KindArray:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(n.Elems) {
				return nil
			}
			n = n.Elems[i]
		default:
			return nil
		}
		if n == nil {
			return nil
		}
	}
	return n
}

// TestIntegrationRecipes runs every selected recipe on its own quickstart network, one after the other.
func TestIntegrationRecipes(t *testing.T) {
	image, protocol := orchestration.QuickstartCurrent, orchestration.CurrentProtocol
	if os.Getenv("LTK_RECIPE_PROTOCOL") == strconv.Itoa(orchestration.FallbackProtocol) {
		image, protocol = orchestration.QuickstartProtocol28, orchestration.FallbackProtocol
	}
	selected := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("LTK_RECIPES"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = true
		}
	}
	for _, l := range managedListings(t) {
		if l != "" {
			t.Fatalf("ltk.managed resources exist before the test; clean them by hand (the test never cleans them itself, which would mask a leak): %q", l)
		}
	}
	ctx := context.Background()
	t.Cleanup(func() {
		for i, l := range managedListings(t) {
			if l != "" {
				t.Errorf("label-filtered listing %d not empty after the runs: %q", i, l)
			}
		}
	})
	for _, r := range recipes.Registry() {
		if len(selected) > 0 && !selected[r.ID()] {
			continue
		}
		t.Run(r.ID(), func(t *testing.T) { runRecipe(ctx, t, r, image, protocol) })
	}
}

func runRecipe(ctx context.Context, t *testing.T, r recipes.Recipe, image string, protocol int) {
	runID, err := orchestration.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := orchestration.NewRunner(runID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := run.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := run.Reap(ctx); err != nil {
			t.Errorf("Reap: %v", err)
		}
		after, err := run.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if left := orchestration.Leftovers(before, after); len(left) != 0 {
			t.Errorf("Leftovers after teardown: %q", left)
		}
		t.Logf("run %s torn down", runID)
	})
	if err := run.EnsureImages(ctx, []string{image, orchestration.QuickstartCurrent}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := run.StartQuickstart(ctx, image, protocol)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("run %s: %s ready at protocol %d after %s", runID, image, n.Protocol, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	c, err := r.Run(ctx, recipes.Network{
		RPC:        generate.NewRPCClient(n.RPCURL),
		Friendbot:  generate.NewFriendbot(n.FriendbotURL),
		Passphrase: n.Passphrase,
	})
	if err != nil {
		t.Fatalf("%s at protocol %d: %v", r.ID(), protocol, err)
	}
	t.Logf("recipe ran in %s", time.Since(start).Round(time.Millisecond))
	if c.Protocol != protocol {
		t.Errorf("the capture's header says protocol %d; the network was started at %d", c.Protocol, protocol)
	}

	// The Go SDK codec: decode and a byte-identical round trip.
	v, err := xdrcodec.Decode(c.XdrType, c.XDR)
	if err != nil {
		t.Fatal(err)
	}
	lcm := v.(*xdr.LedgerCloseMeta)
	goRT, err := xdrcodec.RoundTrip(c.XdrType, c.XDR)
	if err != nil || !bytes.Equal(goRT, c.XDR) {
		t.Fatalf("the Go SDK round trip differs (%d bytes in, %d out): %v", len(c.XDR), len(goRT), err)
	}

	// The Rust XDR tool of the current image: decode and a byte-identical round trip.
	tool, err := run.StartXDRTool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := tool.Decode(ctx, c.XdrType, c.XDR)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := tool.Encode(ctx, c.XdrType, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, c.XDR) {
		t.Fatalf("the Rust round trip differs: %d bytes in, %d out", len(c.XDR), len(reencoded))
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, decoded, "", "  "); err != nil {
		t.Fatal(err)
	}

	// Every pointer of Intended() holds its value in the Rust tool's decoded JSON.
	doc, err := catalogue.Parse(decoded)
	if err != nil {
		t.Fatal(err)
	}
	intended := r.Intended()
	pointers := make([]string, 0, len(intended))
	for p := range intended {
		pointers = append(pointers, p)
	}
	sort.Strings(pointers)
	for _, p := range pointers {
		want, err := catalogue.Parse(intended[p])
		if err != nil {
			t.Fatal(err)
		}
		got := resolve(doc, p)
		switch {
		case got == nil:
			t.Errorf("INTENDED MISSING %s (intended %s)", p, intended[p])
		case !catalogue.Equal(got, want):
			t.Errorf("INTENDED DIFFERS %s: decoded %s, intended %s", p, got.Raw, intended[p])
		default:
			t.Logf("INTENDED OK %s = %s", p, intended[p])
		}
	}

	// What the grouping table records from this decoding (internal/catalogue's SplitExpectedValues): its reproduced
	// pointers against Intended(), and its ledger-dependent pointers, for the grouping table's owner.
	split, err := catalogue.SplitExpectedValues(r.ID(), decoded)
	if err != nil {
		t.Errorf("SplitExpectedValues: %v", err)
	} else {
		seen := map[string]bool{}
		for _, rv := range split.ReproducedByEveryRun {
			seen[rv.Pointer] = true
			if _, ok := intended[rv.Pointer]; !ok {
				t.Logf("GROUPING reproduced but not intended: %s = %s", rv.Pointer, rv.Value)
			}
		}
		for _, p := range pointers {
			if !seen[p] {
				t.Logf("GROUPING intended but not recorded as reproduced: %s", p)
			}
		}
		for _, lv := range split.LedgerDependent {
			t.Logf("GROUPING ledger-dependent %s %s", lv.Field, lv.Pointer)
		}
		t.Logf("GROUPING %d reproduced, %d ledger-dependent", len(split.ReproducedByEveryRun), len(split.LedgerDependent))
	}

	sum := sha256.Sum256(c.XDR)
	t.Logf("RESULT %s protocol=%d image=%s ledger=%d lcm=v%d bytes=%d sha256=%x tx=%s decodedJSON=%d",
		r.ID(), c.Protocol, image, c.Ledger, lcm.V, len(c.XDR), sum, c.TxHash, indented.Len())

	if c.Refusal != nil {
		res, err := xdrcodec.DecodeBase64(xdrcodec.TypeTransactionResult, c.Refusal.ResultXdr)
		if err != nil {
			t.Fatal(err)
		}
		tr := res.(*xdr.TransactionResult)
		opCode := "none"
		if ops, ok := tr.Result.GetResults(); ok && len(ops) > 0 {
			if in, ok := ops[0].GetTr(); ok {
				if ct, ok := in.GetChangeTrustResult(); ok {
					opCode = ct.Code.String()
				}
			}
		}
		t.Logf("REFUSAL stage=%s status=%s tx=%s txResult=%s opResult=%s resultXdr=%s envelopeXdr=%s",
			c.Refusal.Stage, c.Refusal.Status, c.Refusal.TxHash, tr.Result.Code, opCode, c.Refusal.ResultXdr, c.Refusal.EnvelopeXdr)
		if opCode != xdr.ChangeTrustResultCodeChangeTrustMalformed.String() {
			t.Errorf("the text-order form was refused with %s/%s, not CHANGE_TRUST_MALFORMED", tr.Result.Code, opCode)
		}
	} else if r.ID() == "cap-0038-pool-share-trustline" {
		t.Error("entry 2 returned no refusal evidence")
	}

	if dir := os.Getenv("LTK_CAPTURE_DIR"); dir != "" {
		base := filepath.Join(dir, fmt.Sprintf("%s-p%d%s", r.ID(), c.Protocol, os.Getenv("LTK_CAPTURE_SUFFIX")))
		files := map[string][]byte{base + ".xdr": c.XDR, base + ".json": append(indented.Bytes(), '\n')}
		if c.Refusal != nil {
			b, err := json.MarshalIndent(c.Refusal, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			files[base+"-refusal.json"] = append(b, '\n')
		}
		for name, b := range files {
			if err := os.WriteFile(name, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("capture kept as %s.{xdr,json}", base)
	}
}
