package main

// The protocol fallback end to end. When a recipe cannot be made to work at the current protocol, the entry is
// generated at protocol 28, on a second network started after the first is torn down, and the catalogue and the
// README's first page say so.
//
// The failure at the current protocol is forced from outside the program, never by a flag: the fake quickstart of the
// current image refuses entry 1's SetOptions (sendTransaction ERROR), and the fake of the protocol-28 image applies it,
// its capture's header stating protocol 28. Everything else is the program's own code, built and run as a user runs it:
// the router, the environment check, the Protocol selector and the generation pipeline (internal/generate) with entry
// 1's real recipe, the Protocol fallback recorder, the layout writer, the checks and the report. Only Docker and the
// network are faked (fakedocker, fakestellar_test.go).
//
// The README rule is firstPageNamesFallbacks: the README consistency test (readme_test.go) calls it with the README's
// first page and the committed catalogue's entries.

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/schema"
)

// fallbackEngine is generateEngine with entry 1 refused at the current protocol and applied at 28.
func fallbackEngine(t *testing.T) *fakeEngine {
	t.Helper()
	f := newFakeEngine(t, "", "")
	captures := t.TempDir()
	current := newFakeStellar(t, orchestration.CurrentProtocol, captures)
	p28 := newFakeStellar(t, orchestration.FallbackProtocol, captures)
	current.applies, p28.applies = false, true
	f.env = append(f.env,
		"FAKE_DOCKER_PORTS="+orchestration.QuickstartCurrent+"="+current.addr()+";"+orchestration.QuickstartProtocol28+"="+p28.addr(),
		"FAKE_DOCKER_CORPUS="+captures)
	return f
}

// TestProcessGenerateFallbackTo28: entry 1's recipe cannot work at the current protocol, so
// `ltk generate` generates it at protocol 28 on the second network, started after the first was removed:
//   - --shape: exit 0, nothing on stderr; the report's shape is generated at 28 with protocolFallback true and error
//     null, its check passed; the catalogue record carries protocolVersion 28 and protocolFallback true, validates
//     against the catalogue schema, and names the protocol-28 image as its source; `ltk list` of the folder shows 28;
//   - --all: entry 1 is generated at 28 with the fallback recorded; entries 2 and 3, refused at both protocols, are
//     not generated, each with the error of both attempts (exit 1, the report on stdout, one ErrorLine); the catalogue
//     holds entry 1's fallback record only.
//
// Nothing is left in the engine either way.
func TestProcessGenerateFallbackTo28(t *testing.T) {
	t.Run("--shape", func(t *testing.T) {
		f := fallbackEngine(t)
		out := filepath.Join(t.TempDir(), "out")
		code, stdout, stderr := runLTK(t, f.env, "generate", "--shape", entry1, "--out", out)
		if code != 0 || len(stderr) != 0 {
			t.Fatalf("exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
		}
		r := decodeGenerateReport(t, stdout)
		if len(r.Shapes) != 1 {
			t.Fatalf("%s", stdout)
		}
		assertFellBack(t, r.Shapes[0])
		if r.Check == nil || !r.Check.Passed || len(r.Check.Entries) != 1 || r.Check.Entries[0].ID != entry1 {
			t.Errorf("the checks on the fallback entry: %+v", r.Check)
		}
		cat := assertFallbackRecord(t, out)
		if len(cat.Entries) != 1 {
			t.Errorf("catalogue of %d entries, want entry 1's only", len(cat.Entries))
		}
		if got := coreRuns(f); !slices.Equal(got, []string{orchestration.QuickstartCurrent, orchestration.QuickstartProtocol28}) {
			t.Errorf("quickstarts started %v: the current, then the protocol-28 one", got)
		}
		assertTornDown(t, f, true)

		code, stdout, stderr = runLTK(t, noDockerEnv(t), "list", out)
		want := entry1 + "\t" + model.Seeds()[0].Cap + "\t28\t"
		if code != 0 || len(stderr) != 0 || !strings.Contains(string(stdout), "\n"+want) {
			t.Errorf("ltk list of the fallback folder: exit %d stderr %q stdout %q; want a row starting %q", code, stderr, stdout, want)
		}
	})
	t.Run("--all", func(t *testing.T) {
		f := fallbackEngine(t)
		out := filepath.Join(t.TempDir(), "out")
		code, stdout, stderr := runLTK(t, f.env, "generate", "--all", "--out", out)
		if code != int(contract.EXIT_CODES.Failure) {
			t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
		}
		r := decodeGenerateReport(t, stdout)
		if len(r.Shapes) != 3 || r.Check != nil {
			t.Fatalf("%s", stdout)
		}
		assertFellBack(t, r.Shapes[0])
		for _, s := range r.Shapes[1:] {
			if s.Generated || s.Error == nil || !strings.Contains(*s.Error, "at protocol 30: ") || !strings.Contains(*s.Error, "at protocol 28: ") ||
				s.ProtocolVersion != contract.FALLBACK_PROTOCOL_VERSION || !s.ProtocolFallback {
				t.Errorf("%s: %+v; want not generated, its two attempts named, the last at 28 on the fallback network", s.ID, s)
			}
		}
		line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
		if !strings.HasPrefix(line.Message, "ltk generate: generation failed: 2 of 3 shapes not generated; ") {
			t.Errorf("message %q", line.Message)
		}
		cat := assertFallbackRecord(t, out)
		if len(cat.Entries) != 1 {
			t.Errorf("catalogue of %d entries, want entry 1's only", len(cat.Entries))
		}
		assertTornDown(t, f, false)
	})
}

// assertFellBack holds a shape result to the fallback's: generated at FALLBACK_PROTOCOL_VERSION with protocolFallback
// true and no error.
func assertFellBack(t *testing.T, s contract.ShapeGenerationResult) {
	t.Helper()
	want := contract.ShapeGenerationResult{ID: entry1, ProtocolVersion: contract.FALLBACK_PROTOCOL_VERSION, ProtocolFallback: true, Generated: true}
	if s.ID != want.ID || s.ProtocolVersion != want.ProtocolVersion || s.ProtocolFallback != want.ProtocolFallback || s.Generated != want.Generated || s.Error != nil {
		t.Errorf("shape %+v; want %+v", s, want)
	}
}

// assertFallbackRecord reads the catalogue written to out and holds entry 1's record to the fallback's: protocolVersion
// 28, protocolFallback true, the protocol-28 image as its source; the catalogue passes its schema check (the schema
// ties protocolFallback true to protocolVersion 28) and the README rule names it.
func assertFallbackRecord(t *testing.T, out string) contract.Catalogue {
	t.Helper()
	res, _ := schema.CheckCatalogue(out)
	if !res.Passed {
		t.Errorf("the catalogue fails its schema check: %+v", res.Failures)
	}
	cat, err := catalogue.ReadCatalogue(out)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(cat.Entries, func(e contract.CatalogueEntry) bool { return e.ID == entry1 })
	if i < 0 {
		t.Fatalf("entry 1 has no record: %+v", cat)
	}
	e := cat.Entries[i]
	if e.ProtocolVersion != contract.FALLBACK_PROTOCOL_VERSION || !e.ProtocolFallback {
		t.Errorf("entry 1's record: protocolVersion %d protocolFallback %v; want 28 and true", e.ProtocolVersion, e.ProtocolFallback)
	}
	if !strings.Contains(e.Source, orchestration.QuickstartProtocol28) {
		t.Errorf("entry 1's source %q does not name the protocol-28 image", e.Source)
	}
	// The README rule: a first page that does not state this fallback fails; one that does passes.
	if missing := firstPageNamesFallbacks("Three entries are a seed, not a corpus. All were generated at protocol 30.", cat.Entries); !slices.Equal(missing, []string{entry1}) {
		t.Errorf("a first page silent on the fallback: the rule reports %v, want [%s]", missing, entry1)
	}
	page := "Three entries are a seed.\n\n- `" + entry1 + "` could not be generated at protocol 30; it fell back to protocol 28.\n"
	if missing := firstPageNamesFallbacks(page, cat.Entries); len(missing) != 0 {
		t.Errorf("a first page that states the fallback: the rule reports %v", missing)
	}
	return cat
}

// firstPageNamesFallbacks is the README rule that the first page names every fallback, for the README consistency test:
// it returns, in catalogue order, the id of every entry that records protocolFallback true and that the first page does
// not state as a fallback; nil when there is none. An entry is stated when one block of the first page holds its id,
// the words "protocol 28" and "fallback" or "fell back" (case aside): a block is a paragraph between blank lines,
// except that each list item and each table row is a block of its own. The id and "protocol 28" alone are not enough:
// the first page also gives entries' lengths at protocol 28 without any fallback.
func firstPageNamesFallbacks(firstPage string, entries []contract.CatalogueEntry) []string {
	blocks := firstPageBlocks(firstPage)
	var missing []string
	for _, e := range entries {
		if !e.ProtocolFallback {
			continue
		}
		stated := slices.ContainsFunc(blocks, func(b string) bool {
			l := strings.ToLower(b)
			return strings.Contains(b, e.ID) && strings.Contains(l, "protocol "+strconv.Itoa(contract.FALLBACK_PROTOCOL_VERSION)) &&
				(strings.Contains(l, "fallback") || strings.Contains(l, "fell back") || strings.Contains(l, "falls back"))
		})
		if !stated {
			missing = append(missing, e.ID)
		}
	}
	return missing
}

// firstPageBlocks splits Markdown text into firstPageNamesFallbacks's blocks.
func firstPageBlocks(text string) []string {
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, " "))
			cur = nil
		}
	}
	sc := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(text, "\r\n", "\n")))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "|"), strings.HasPrefix(line, "#"):
			flush()
			blocks = append(blocks, line)
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "), startsNumbered(line):
			flush()
			cur = append(cur, line)
		default:
			cur = append(cur, line)
		}
	}
	flush()
	return blocks
}

// startsNumbered reports whether line starts a numbered list item ("1. ").
func startsNumbered(line string) bool {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && strings.HasPrefix(line[i:], ". ")
}

// TestFirstPageNamesFallbacks: the rule's edges: a catalogue with no fallback needs no statement; the id in one block
// and "protocol 28" in another do not state it; a list item or a table row is its own block; the words' case does not
// matter; the lengths at protocol 28 beside an id are not a fallback statement.
func TestFirstPageNamesFallbacks(t *testing.T) {
	fb := func(id string) contract.CatalogueEntry {
		return contract.CatalogueEntry{ID: id, ProtocolVersion: 28, ProtocolFallback: true}
	}
	plain := contract.CatalogueEntry{ID: model.SeedMuxedAccount, ProtocolVersion: 30}
	id1, id2 := model.SeedSignedPayloadSigner, model.SeedPoolShareTrustline
	cases := []struct {
		name, page string
		entries    []contract.CatalogueEntry
		want       []string
	}{
		{"no fallback", "nothing", []contract.CatalogueEntry{plain}, nil},
		{"stated in a paragraph", "Entry `" + id1 + "` fell back\nto Protocol 28.", []contract.CatalogueEntry{fb(id1), plain}, nil},
		{"id and protocol in two paragraphs", id1 + " is entry 1.\n\nA fallback to protocol 28 happened.", []contract.CatalogueEntry{fb(id1)}, []string{id1}},
		{"two list items", "- " + id1 + " fell back\n- to protocol 28", []contract.CatalogueEntry{fb(id1)}, []string{id1}},
		{"a table row", "| id | protocol |\n|---|---|\n| " + id1 + " | protocol 28 (fallback) |", []contract.CatalogueEntry{fb(id1)}, nil},
		{"lengths only", id1 + " is 1,920 bytes at protocol 28 and " + id2 + " 2,816.", []contract.CatalogueEntry{fb(id1), fb(id2)}, []string{id1, id2}},
		{"one of two stated", "1. " + id2 + ": protocol 28, a fallback.", []contract.CatalogueEntry{fb(id1), fb(id2)}, []string{id1}},
	}
	for _, c := range cases {
		if got := firstPageNamesFallbacks(c.page, c.entries); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestCommittedCorpusFallbacksOnREADME: every fallback the committed catalogue records is stated in README.md. Without
// a README this skips, saying why. The whole README is read here; the README consistency test (readme_test.go) applies
// the same rule to the first page alone.
func TestCommittedCorpusFallbacksOnREADME(t *testing.T) {
	cat, err := catalogue.ReadCatalogue(committedCorpusDir)
	if err != nil {
		t.Fatalf("the committed corpus: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if os.IsNotExist(err) {
		t.Skip("skipped: README.md is not written yet")
	}
	if err != nil {
		t.Fatal(err)
	}
	if missing := firstPageNamesFallbacks(string(readme), cat.Entries); len(missing) != 0 {
		t.Errorf("README.md does not state the protocol-28 fallback of %v", missing)
	}
}
