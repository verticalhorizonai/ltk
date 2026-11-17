package generate

// Single shape generation: the Generation pipeline restricted to one recipe of the registry,
// which writes that entry's folder and merges that entry's record into the catalogue the folder already holds. Every
// other record and every other entry folder is left byte for byte as it was; with no catalogue, a catalogue holding
// that one record is written.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// RunShape is the pipeline of `ltk generate --shape <entry id> --out <folder>`: only the recipe of
// rs (production passes recipes.Registry()) whose ID() is id, on the networks of the run whose Docker CLI runner is r,
// decoded by dec, written to out. It returns one result, as RunAll does.
//
// An id that names no recipe is a usage error (exit 2) and nothing is started; the command checks the id before the
// environment, so this is its second guard. The catalogue out already holds is read before any network starts, and
// must be one this tool wrote: a catalogue that is not readable, lists an id twice, or is not in the form the layout
// writer writes (so merging would rewrite its other records) is refused (exit 1) and nothing is started or written;
// the tool never rewrites a file it did not write. A shape that was not generated writes nothing.
func RunShape(ctx context.Context, r *orchestration.Runner, dec Decoder, rs []recipes.Recipe, id, out string) ([]contract.ShapeGenerationResult, error) {
	for _, rec := range rs {
		if rec.ID() == id {
			return newPipeline(r, dec).run(ctx, []recipes.Recipe{rec}, out, true)
		}
	}
	return nil, model.Usage("unknown --shape entry id: %s", id)
}

// readCatalogueToMerge returns the catalogue out holds, or nil when out or its catalogue does not exist yet (a new
// catalogue is then written).
func readCatalogueToMerge(out string) (*contract.Catalogue, error) {
	raw, err := os.ReadFile(filepath.Join(out, catalogue.CatalogueFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	name := filepath.ToSlash(filepath.Join(out, catalogue.CatalogueFileName))
	if err != nil {
		return nil, model.Failure("reading %s to merge a record into it: %w; nothing was started", name, err)
	}
	cat, err := catalogue.ReadCatalogue(out)
	if err != nil {
		return nil, model.Failure("%s cannot take a merged record: %w; nothing was started", name, err)
	}
	seen := map[string]bool{}
	for _, e := range cat.Entries {
		if seen[e.ID] {
			return nil, model.Failure("%s cannot take a merged record: it lists entry id %s twice; nothing was started", name, e.ID)
		}
		seen[e.ID] = true
	}
	again, err := catalogue.EncodeCatalogue(cat)
	if err != nil || !bytes.Equal(again, raw) {
		return nil, model.Failure("%s cannot take a merged record: it is not in the form ltk generate writes (an unknown "+
			"field, another layout or other line endings), so writing it again would change its other records; nothing was started", name)
	}
	return &cat, nil
}

// mergeRecords returns base with each record of add in place of base's record with the same id, or, for an id base
// does not list, inserted before the first record of a later seed (after every record otherwise), so the seeds keep
// their order. base's other records are kept as they are, in their order.
func mergeRecords(base contract.Catalogue, add []contract.CatalogueEntry) contract.Catalogue {
	entries := append([]contract.CatalogueEntry(nil), base.Entries...)
	for _, rec := range add {
		replaced := false
		for i := range entries {
			if entries[i].ID == rec.ID {
				entries[i], replaced = rec, true
				break
			}
		}
		if replaced {
			continue
		}
		at := len(entries)
		for i := range entries {
			if seedIndex(entries[i].ID) > seedIndex(rec.ID) {
				at = i
				break
			}
		}
		entries = append(entries[:at], append([]contract.CatalogueEntry{rec}, entries[at:]...)...)
	}
	return contract.Catalogue{Entries: entries}
}

// seedIndex is id's position in seed order; an id that is not a seed's comes after every seed.
func seedIndex(id string) int {
	for i, s := range model.Seeds() {
		if s.ID == id {
			return i
		}
	}
	return len(model.Seeds())
}
