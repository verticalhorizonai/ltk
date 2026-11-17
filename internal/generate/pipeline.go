package generate

// The Generation pipeline. It is what `ltk generate` (cmd/ltk/generate.go) calls inside its withContainers scope, after
// the arguments and the environment were checked, and before the checks, which the command runs on the folder this
// writes:
//
//  1. start the current quickstart (CurrentProtocol) and run each selected recipe on it, in seed order; decode each
//     capture with the Rust XDR tool, read its expected values through the grouping table, and assert that the
//     reproduced values are the recipe's Intended() (and, for entry 2, record the refusal through
//     catalogue.RefusalEvidence);
//  2. remove that network; if a shape could not work there (one of the three conditions in protocol.go), start the
//     protocol-28 quickstart and run those shapes again there, then remove it (the Protocol selector, protocol.go);
//  3. write each generated entry's XDR file and decoded JSON, then the catalogue and its schema copy, through
//     internal/catalogue's layout writer; a shape that was not generated has no file and no record, and the shapes
//     that were generated are still written.
//
// It returns only after its quickstart networks are removed, so the checks never run beside a generation network; the
// Rust XDR tool's container is the command's, shared with the checks, and the command's reaper removes it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// Decoder is the primary decoder of a capture: Stellar's Rust XDR tool, run in the current quickstart image. The
// command passes the run's *rustxdr.Adapter (internal/check/rustxdr), the same value it then gives check.Run, so one
// command run starts one Rust XDR tool container.
type Decoder interface {
	// Decode decodes raw XDR bytes of xdrType to the tool's JSON form.
	Decode(ctx context.Context, xdrType string, raw []byte) ([]byte, error)
}

// RunAll is the pipeline of `ltk generate --all --out <folder>`: every recipe of
// rs (production passes recipes.Registry(), the seed order), in order, on the networks of the run whose Docker CLI
// runner is r, decoded by dec, written to out (created if absent) as a whole catalogue. It starts no container but the
// quickstart networks; the Rust XDR tool's container is dec's.
//
// The results are one per shape, in seed order, each with the protocol it was generated (or last attempted) at. A
// shape that could not be generated has generated false and its error, no file and no record; the others are
// written. When no shape was generated, nothing is written. The command prints the results in its
// GenerateReport and runs the checks on out only when every shape was generated.
//
// The error is non-nil only when the run must stop without a report (exit 1, one ErrorLine): an interrupt (the
// context's cancellation is in its chain); a write of the folder that failed (WriteEntryFiles or WriteCatalogue; the
// message names the path), or an --out that is a file, found before any network starts; and a quickstart network that
// could not be removed, which would leave the checks running beside it. It is returned after the
// networks it started were removed, except on an interrupt or a failed removal, where the command's reaper removes
// them.
func RunAll(ctx context.Context, r *orchestration.Runner, dec Decoder, rs []recipes.Recipe, out string) ([]contract.ShapeGenerationResult, error) {
	return newPipeline(r, dec).run(ctx, rs, out, false)
}

// launcher starts and removes a quickstart network: *orchestration.Runner, or a fake in the unit tests.
type launcher interface {
	StartQuickstart(ctx context.Context, image string, protocol int) (*orchestration.Network, error)
	StopQuickstart(ctx context.Context, n *orchestration.Network) error
}

// pipeline is one generation: its networks, its decoder and the protocols it tries, in order.
type pipeline struct {
	launch  launcher
	decoder Decoder
	// plans are the networks of the Protocol selector, the current protocol first (protocol.go).
	plans []protocolPlan
	// connect makes the recipes' view of a started network.
	connect func(n *orchestration.Network) recipes.Network
}

// newPipeline is the production pipeline of the run whose runner is r.
func newPipeline(r *orchestration.Runner, dec Decoder) pipeline {
	return pipeline{launch: r, decoder: dec, plans: protocolPlans(), connect: connectNetwork}
}

// connectNetwork is the recipes' view of a started quickstart: its RPC through the RPC client, its friendbot, its
// passphrase. The recipes declare the small interface they use, so they import no RPC client.
func connectNetwork(n *orchestration.Network) recipes.Network {
	return recipes.Network{RPC: NewRPCClient(n.RPCURL), Friendbot: NewFriendbot(n.FriendbotURL), Passphrase: n.Passphrase}
}

// shape is one selected shape's outcome: generated, with its capture, its decoded JSON and its catalogue record; or
// not, with the reason of each attempt.
type shape struct {
	recipe recipes.Recipe
	// plan is the network the shape was generated on, or last attempted on.
	plan protocolPlan
	// capture, decoded and record are set when the shape was generated.
	capture recipes.Capture
	decoded []byte
	record  contract.CatalogueEntry
	// attempts are the failures, one per network tried, each naming its protocol; empty when generated.
	attempts []string
	// generated is true when the shape's record and files are ready to be written.
	generated bool
}

// result is the shape's ShapeGenerationResult, as the generate report holds it: the record's protocol fields when it
// was generated; otherwise the protocol of the last network tried, and the attempts' reasons.
func (s shape) result() contract.ShapeGenerationResult {
	if s.generated {
		return contract.ShapeGenerationResult{ID: s.recipe.ID(), ProtocolVersion: s.record.ProtocolVersion,
			ProtocolFallback: s.record.ProtocolFallback, Generated: true}
	}
	msg := strings.Join(s.attempts, "; ")
	return contract.ShapeGenerationResult{ID: s.recipe.ID(), ProtocolVersion: s.plan.protocol, ProtocolFallback: s.plan.fallback, Error: &msg}
}

// run generates the selected recipes into out: the whole catalogue, or, with merge, one record merged into the
// catalogue out already holds (single.go).
func (p pipeline) run(ctx context.Context, selected []recipes.Recipe, out string, merge bool) ([]contract.ShapeGenerationResult, error) {
	if st, err := os.Stat(out); err == nil && !st.IsDir() {
		return nil, model.Failure("the --out folder %s is a file; nothing was started", filepath.ToSlash(out))
	}
	var base *contract.Catalogue
	if merge {
		b, err := readCatalogueToMerge(out)
		if err != nil {
			return nil, err
		}
		base = b
	}
	shapes, err := p.selectProtocols(ctx, selected)
	if err != nil {
		return nil, err
	}
	results := make([]contract.ShapeGenerationResult, len(shapes))
	var generated []shape
	for i, s := range shapes {
		results[i] = s.result()
		if s.generated {
			generated = append(generated, s)
		}
	}
	if err := writeFolder(out, generated, base); err != nil {
		return results, err
	}
	return results, nil
}

// failure is a shape that did not work on one network; cannotWork is true for the three conditions of "cannot work"
// (protocol.go), the only failures the Protocol selector retries at protocol 28.
type failure struct {
	err        error
	cannotWork bool
}

// produce runs one recipe on one network and turns its capture into the entry: the capture,
// decoded by the Rust XDR tool; the expected values read from the decoding and split by the grouping table
// (catalogue.SplitExpectedValues); the reproduced ones asserted equal to Intended(); entry 2's refusal recorded through
// catalogue.RefusalEvidence; and the catalogue record, whose protocol fields the fallback recorder sets. An interrupt
// is returned as the error; a shape that does not work there is the failure.
func (p pipeline) produce(ctx context.Context, rec recipes.Recipe, net recipes.Network, plan protocolPlan) (shape, *failure, error) {
	id := rec.ID()
	seed, ok := model.SeedByID(id)
	if !ok {
		return shape{}, &failure{err: fmt.Errorf("%s is not a seed entry", id)}, nil
	}
	c, err := rec.Run(ctx, net)
	if err != nil {
		if ctx.Err() != nil {
			return shape{}, nil, interrupted(ctx, id, err)
		}
		return shape{}, &failure{err: err, cannotWork: recipeCannotWork(err)}, nil
	}
	decoded, err := p.decoder.Decode(ctx, c.XdrType, c.XDR)
	if err != nil {
		if ctx.Err() != nil {
			return shape{}, nil, interrupted(ctx, id, err)
		}
		return shape{}, &failure{err: fmt.Errorf("%s: decoding the capture of ledger %d with the Rust XDR tool: %w", id, c.Ledger, err)}, nil
	}
	expected, err := catalogue.SplitExpectedValues(id, decoded)
	if err != nil {
		return shape{}, &failure{err: err}, nil
	}
	if err := checkIntended(id, rec.Intended(), expected.ReproducedByEveryRun); err != nil {
		return shape{}, &failure{err: err, cannotWork: true}, nil // the third condition of "cannot work"
	}
	record := contract.CatalogueEntry{
		ID:                    id,
		Cap:                   seed.Cap,
		Source:                sourceOf(plan),
		Synthetic:             seed.Synthetic,
		ExpectedDecodedValues: expected,
		FilePath:              catalogue.EntryFilePath(id),
		XdrType:               c.XdrType,
	}
	record.Sha256, record.Length = catalogue.Measure(c.XDR)
	switch {
	case seed.RecordsNetworkRefusal && c.Refusal == nil:
		return shape{}, &failure{err: fmt.Errorf("%s: the recipe returned no refusal of the text-order form", id)}, nil
	case seed.RecordsNetworkRefusal:
		ev, err := catalogue.RefusalEvidence(c.Refusal.EnvelopeXdr, c.Refusal.ResultXdr)
		if err != nil {
			// The refusal's result code is a reproduced value of entry 2: a network that refuses the text-order form
			// otherwise does not produce the shape's intended value (condition 3 of "cannot work").
			return shape{}, &failure{err: fmt.Errorf("%s: %w", id, err), cannotWork: true}, nil
		}
		record.NetworkRefusalEvidence = &ev
	case c.Refusal != nil:
		return shape{}, &failure{err: fmt.Errorf("%s: the recipe returned a refusal, which only entry 2 records", id)}, nil
	}
	if err := catalogue.RecordProtocol(&record, c.Protocol, plan.fallback); err != nil {
		return shape{}, &failure{err: err}, nil
	}
	return shape{recipe: rec, plan: plan, capture: c, decoded: decoded, record: record, generated: true}, nil, nil
}

// interrupted is the error of a shape cut short by the command's interrupt: the context's error stays in the chain, so
// the command maps it to the interrupt's exit (1, with a message saying the command was interrupted).
func interrupted(ctx context.Context, id string, err error) error {
	if errors.Is(err, ctx.Err()) {
		return fmt.Errorf("generating %s: %w", id, err)
	}
	return fmt.Errorf("generating %s: %w (%v)", id, ctx.Err(), err)
}

// recipeCannotWork reports whether a recipe's error is one of the first two conditions: a transaction of the shape
// refused, or its getTransaction not a success (recipes.NotAppliedError); or entry 2's text-order form accepted
// (recipes.NotRefusedError), which leaves the refusal the shape records unproduced (condition 3).
func recipeCannotWork(err error) bool {
	var na *recipes.NotAppliedError
	var nr *recipes.NotRefusedError
	return errors.As(err, &na) || errors.As(err, &nr)
}

// IntendedMismatchError is a capture whose reproduced values are not the ones its recipe submitted (condition 3 of
// "cannot work", and the guard that keeps a generation from reproducing anything but what was submitted): an intended
// pointer absent from the capture's reproduced group, a value that differs, or a reproduced value the recipe does not
// submit.
type IntendedMismatchError struct {
	// ID is the entry id.
	ID string
	// Problems are the differences, one per pointer, in pointer order.
	Problems []string
}

// Error names the entry and every difference.
func (e *IntendedMismatchError) Error() string {
	return fmt.Sprintf("%s: the capture's reproduced values are not the recipe's intended values: %s", e.ID, strings.Join(e.Problems, "; "))
}

// checkIntended asserts that the reproduced values read from the capture are exactly the recipe's Intended(), pointer
// for pointer, values compared as JSON values.
func checkIntended(id string, intended map[string]json.RawMessage, reproduced []contract.ReproducedValue) error {
	got := map[string]json.RawMessage{}
	for _, v := range reproduced {
		got[v.Pointer] = json.RawMessage(v.Value)
	}
	var problems []string
	for _, ptr := range sortedKeys(intended) {
		v, ok := got[ptr]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is not in the capture (want %s)", ptr, intended[ptr]))
		case !sameJSON(v, intended[ptr]):
			problems = append(problems, fmt.Sprintf("%s is %s in the capture; the recipe submitted %s", ptr, v, intended[ptr]))
		}
	}
	for _, ptr := range sortedKeys(got) {
		if _, ok := intended[ptr]; !ok {
			problems = append(problems, fmt.Sprintf("%s is a reproduced value of the capture that the recipe does not submit (%s)", ptr, got[ptr]))
		}
	}
	if len(problems) > 0 {
		return &IntendedMismatchError{ID: id, Problems: problems}
	}
	return nil
}

// sameJSON compares two JSON values as values (numbers as their decimal text); a value that is not JSON
// differs from everything.
func sameJSON(a, b []byte) bool {
	na, err := catalogue.Parse(a)
	if err != nil {
		return false
	}
	nb, err := catalogue.Parse(b)
	if err != nil {
		return false
	}
	return catalogue.Equal(na, nb)
}

// sortedKeys returns m's keys in order.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sourceOf is the record's source: where its bytes come from. It names the image, never the
// run, so two generations on the same image record the same text.
func sourceOf(plan protocolPlan) string {
	return "captured by ltk generate from a local stellar-core network (" + plan.image + ", local mode, core and RPC): " +
		"the LedgerCloseMeta that RPC getLedgers returned as metadataXdr for the ledger holding the shape's transaction"
}

// writeFolder writes the generated shapes to out, in the corpus layout: each entry's XDR file and decoded JSON
// (catalogue.WriteEntryFiles), then the catalogue with its schema copy (catalogue.WriteCatalogue), last, so the
// catalogue never names a file that was not written. Without base, the catalogue holds the generated records in seed
// order; with base, each generated record replaces the record of its id, or is added (single.go). Nothing is written
// when no shape was generated: a catalogue holds at least one entry (its schema's minItems).
func writeFolder(out string, generated []shape, base *contract.Catalogue) error {
	if len(generated) == 0 {
		return nil
	}
	for _, s := range generated {
		if err := catalogue.WriteEntryFiles(out, s.record.FilePath, s.capture.XDR, s.decoded); err != nil {
			return fmt.Errorf("writing entry %s to the corpus folder: %w", s.record.ID, err)
		}
	}
	cat := contract.Catalogue{}
	for _, s := range generated {
		cat.Entries = append(cat.Entries, s.record)
	}
	if base != nil {
		cat = mergeRecords(*base, cat.Entries)
	}
	if err := catalogue.WriteCatalogue(out, cat); err != nil {
		return fmt.Errorf("writing the catalogue of the corpus folder: %w", err)
	}
	return nil
}
