package orchestrator

// The seed corpus verification, unit tier. Read-only: the committed corpus/ is data, committed as
// `ltk generate --all --out corpus` wrote it, and no test writes it.
//
//   - The recipe registry, internal/model/seeds.go, each recipe's document and corpus/catalogue.json name the same
//     three entry ids, in seed order, with the same CAPs and XDR types, synthetic false (every entry is captured).
//   - The committed catalogue validates against schemas/catalogue.schema.json, and its schema copy is that file.
//   - The folder is in the corpus layout: the catalogue, its schema copy and, per entry, <id>/entry.xdr and
//     <id>/entry.json, nothing else; each record's sha256 and length are its file's.
//   - Each decoded JSON is the Rust XDR tool's decoding of its file. The tool cannot run without Docker, so this tier
//     holds what it can from outside: the file decodes with Stellar's Go SDK as a LedgerCloseMeta with no byte left
//     over and re-encodes byte for byte; the decoded JSON's ledger version, ledger number, ledger hash and transaction
//     hash are the SDK's; its ledger_version is the record's protocolVersion; every expected value of both groups
//     resolves in it to its recorded value. The integration tier runs the tool (seeds_integration_test.go).
//   - Entry 2 alone carries networkRefusalEvidence, CHANGE_TRUST_MALFORMED, whose submittedEnvelopeXdr decodes as a
//     ChangeTrust of entry 2's pool with its two assets in strkey-text order: the reverse of the key-byte order the
//     captured transaction carries.
//   - The lengths are the catalogue's: 1928, 2824 and 2244 at protocol 30, as the committed catalogue
//     records them; at protocol 28 entries 1 and 2 would be 1920 and 2816.
//   - Through the built program, container-free: `ltk list corpus/ --json` run from the module root lists the three
//     entries as the catalogue records them, and `ltk compare` of the corpus with itself passes.
//
// The readers here use the standard library, the JSON Schema library (santhosh-tekuri/jsonschema) and Stellar's Go
// SDK's xdr and strkey packages, never the program's own reading code, so that a passing row is the contract's verdict.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/generate/recipes"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// committedCorpus is the committed corpus folder, from this package's folder.
var committedCorpus = filepath.Join(moduleRoot, "corpus")

// seedCAPs are the CAPs of the three entries.
var seedCAPs = map[string]string{entry1ID: "CAP-0040", entry2ID: "CAP-0038", entry3ID: "CAP-0027"}

// seedLengths are the entries' lengths by the protocol the catalogue records: at protocol 30 the committed capture's;
// at protocol 28 those the feasibility run made before this tool measured for entries 1 and 2, the only ones known
// (0: none known).
var seedLengths = map[int]map[string]int{
	30: {entry1ID: 1928, entry2ID: 2824, entry3ID: 2244},
	28: {entry1ID: 1920, entry2ID: 2816, entry3ID: 0},
}

// seedValue is one expected value of a catalogue record, the value kept as its JSON text.
type seedValue struct {
	Field   string          `json:"field"`
	Pointer string          `json:"pointer"`
	Value   json.RawMessage `json:"value"`
}

// seedRecord is a catalogue entry as a reader reads it.
type seedRecord struct {
	ID                    string `json:"id"`
	Cap                   string `json:"cap"`
	ProtocolVersion       int    `json:"protocolVersion"`
	ProtocolFallback      bool   `json:"protocolFallback"`
	Source                string `json:"source"`
	Synthetic             bool   `json:"synthetic"`
	ExpectedDecodedValues struct {
		ReproducedByEveryRun []seedValue `json:"reproducedByEveryRun"`
		LedgerDependent      []seedValue `json:"ledgerDependent"`
	} `json:"expectedDecodedValues"`
	FilePath               string `json:"filePath"`
	XdrType                string `json:"xdrType"`
	Sha256                 string `json:"sha256"`
	Length                 int    `json:"length"`
	NetworkRefusalEvidence *struct {
		SubmittedEnvelopeXdr string `json:"submittedEnvelopeXdr"`
		ResultCode           string `json:"resultCode"`
	} `json:"networkRefusalEvidence"`
}

// readCommittedCatalogue reads corpus/catalogue.json's bytes and its entries.
func readCommittedCatalogue(t *testing.T) ([]byte, []seedRecord) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(committedCorpus, "catalogue.json"))
	if err != nil {
		t.Fatalf("the committed corpus has no catalogue (the repository commits it): %v", err)
	}
	var cat struct {
		Entries []seedRecord `json:"entries"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatalf("corpus/catalogue.json: %v", err)
	}
	return b, cat.Entries
}

// recipeDocumentRow reads the value of one row of a recipe document's identity table: the first `code` span of the
// row whose first cell is name.
func recipeDocumentRow(t *testing.T, id, name string) string {
	t.Helper()
	path := filepath.Join("..", "generate", "recipes", id+".md")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the recipe document of %s (every recipe has one beside its code): %v", id, err)
	}
	defer f.Close()
	re := regexp.MustCompile("^\\|\\s*" + regexp.QuoteMeta(name) + "\\s*\\|\\s*`([^`]+)`")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := re.FindStringSubmatch(sc.Text()); m != nil {
			return m[1]
		}
	}
	t.Fatalf("%s states no %q row", path, name)
	return ""
}

// TestSeedSetRegistryModelCatalogue: the recipe registry, the seed definitions, each recipe's document and the
// committed catalogue name the same three entry ids in seed order, with the same CAPs and XDR types; every entry is
// captured (synthetic false); the one seed that records the network's refusal is entry 2 in the definitions and in the
// catalogue.
func TestSeedSetRegistryModelCatalogue(t *testing.T) {
	var registryIDs []string
	for _, r := range recipes.Registry() {
		registryIDs = append(registryIDs, r.ID())
		if got, ok := recipes.ByID(r.ID()); !ok || got.ID() != r.ID() {
			t.Errorf("recipes.ByID(%q) does not return its recipe", r.ID())
		}
	}
	var modelIDs []string
	for _, s := range model.Seeds() {
		modelIDs = append(modelIDs, s.ID)
	}
	_, entries := readCommittedCatalogue(t)
	var catalogueIDs []string
	for _, e := range entries {
		catalogueIDs = append(catalogueIDs, e.ID)
	}
	for name, ids := range map[string][]string{"the recipe registry": registryIDs, "internal/model/seeds.go": modelIDs, "corpus/catalogue.json": catalogueIDs} {
		if !slices.Equal(ids, seedIDs) {
			t.Errorf("%s names %q; want the contract's three in seed order %q", name, ids, seedIDs)
		}
	}
	if t.Failed() {
		return
	}
	for i, id := range seedIDs {
		s, _ := model.SeedByID(id)
		e := entries[i]
		docID, docCap, docType := recipeDocumentRow(t, id, "Entry id"), recipeDocumentRow(t, id, "CAP"), recipeDocumentRow(t, id, "XDR type of the file")
		caps := map[string]string{"the contract": seedCAPs[id], "seeds.go": s.Cap, "the recipe document": docCap, "the catalogue": e.Cap}
		types := map[string]string{"seeds.go": s.XdrType, "the recipe document": docType, "the catalogue": e.XdrType}
		for who, c := range caps {
			if c != seedCAPs[id] {
				t.Errorf("%s: %s says CAP %q, want %q", id, who, c, seedCAPs[id])
			}
		}
		for who, x := range types {
			if x != "LedgerCloseMeta" {
				t.Errorf("%s: %s says XDR type %q, want LedgerCloseMeta (every entry is a captured ledger)", id, who, x)
			}
		}
		if docID != id {
			t.Errorf("the recipe document %s.md names the entry %q", id, docID)
		}
		if s.Synthetic || e.Synthetic {
			t.Errorf("%s: synthetic %v in seeds.go, %v in the catalogue; every seed is captured", id, s.Synthetic, e.Synthetic)
		}
		if s.RecordsNetworkRefusal != (id == entry2ID) || (e.NetworkRefusalEvidence != nil) != (id == entry2ID) {
			t.Errorf("%s: RecordsNetworkRefusal %v, catalogue evidence present %v; only entry 2 records it",
				id, s.RecordsNetworkRefusal, e.NetworkRefusalEvidence != nil)
		}
		t.Logf("%s: %s, %s, in the registry, seeds.go, its recipe document and the catalogue", id, e.Cap, e.XdrType)
	}
}

// TestSeedCorpusSchemaAndLayout: the committed catalogue validates against schemas/catalogue.schema.json; the corpus
// folder holds exactly the catalogue, its schema copy (byte-equal to the source) and each entry's entry.xdr and
// entry.json; each record's filePath is <id>/entry.xdr, its sha256 and length are its file's, and the lengths are 1928,
// 2824 and 2244 at protocol 30 (1920 and 2816 for entries 1 and 2 at protocol 28).
func TestSeedCorpusSchemaAndLayout(t *testing.T) {
	catB, entries := readCommittedCatalogue(t)
	if err := validationError(t, compileSchemaFile(t, catalogueSchemaPath), catB); err != nil {
		t.Errorf("corpus/catalogue.json does not validate against schemas/catalogue.schema.json: %v", err)
	}
	src, err := os.ReadFile(catalogueSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if cp, err := os.ReadFile(filepath.Join(committedCorpus, "catalogue.schema.json")); err != nil || !bytes.Equal(cp, src) {
		t.Errorf("corpus/catalogue.schema.json is not byte-equal to schemas/catalogue.schema.json (%v)", err)
	}

	var got []string
	err = filepath.WalkDir(committedCorpus, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(committedCorpus, p)
		got = append(got, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"catalogue.json", "catalogue.schema.json"}
	for _, id := range seedIDs {
		want = append(want, id+"/entry.json", id+"/entry.xdr")
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("corpus/ holds %q; want the corpus layout %q", got, want)
	}

	for _, e := range entries {
		if e.FilePath != e.ID+"/entry.xdr" {
			t.Errorf("%s: filePath %q, want %s/entry.xdr", e.ID, e.FilePath, e.ID)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(committedCorpus, filepath.FromSlash(e.FilePath)))
		if err != nil {
			t.Errorf("%s: %v", e.ID, err)
			continue
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != e.Sha256 || len(raw) != e.Length {
			t.Errorf("%s: the file is %d bytes, sha256 %x; the record says %d, %s", e.ID, len(raw), sum, e.Length, e.Sha256)
		}
		lengths, ok := seedLengths[e.ProtocolVersion]
		switch {
		case !ok:
			t.Errorf("%s: captured at protocol %d; this test knows the lengths at 30 and 28 only: record the new capture's lengths here", e.ID, e.ProtocolVersion)
		case lengths[e.ID] != 0 && e.Length != lengths[e.ID]:
			t.Errorf("%s: length %d at protocol %d, want %d", e.ID, e.Length, e.ProtocolVersion, lengths[e.ID])
		}
		t.Logf("%s: protocol %d (fallback %v), %d bytes, sha256 %s", e.ID, e.ProtocolVersion, e.ProtocolFallback, e.Length, e.Sha256)
	}
}

// TestSeedCorpusDecodedJSON: each entry's XDR file decodes with the Go SDK as a LedgerCloseMeta with nothing left over
// and re-encodes to the same bytes; it holds one transaction; the decoded JSON is strict JSON whose ledger_version is
// the record's protocolVersion and the SDK's, whose ledger number, ledger hash and transaction hash are the SDK's;
// every expected value of both groups resolves in it to its recorded value, and neither group is empty.
func TestSeedCorpusDecodedJSON(t *testing.T) {
	_, entries := readCommittedCatalogue(t)
	for _, e := range entries {
		t.Run(e.ID, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(committedCorpus, filepath.FromSlash(e.FilePath)))
			if err != nil {
				t.Fatal(err)
			}
			var lcm xdr.LedgerCloseMeta
			if err := xdr.SafeUnmarshal(raw, &lcm); err != nil {
				t.Fatalf("entry.xdr does not decode as a LedgerCloseMeta with the Go SDK: %v", err)
			}
			again, err := lcm.MarshalBinary()
			if err != nil || !bytes.Equal(again, raw) {
				t.Errorf("the Go SDK's re-encoding is not the file (%d bytes, %v)", len(again), err)
			}
			if n := lcm.CountTransactions(); n != 1 {
				t.Errorf("the capture holds %d transactions; the shape's is alone in its ledger", n)
			}

			jsonPath := filepath.Join(committedCorpus, e.ID, "entry.json")
			b, err := os.ReadFile(jsonPath)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(b) {
				t.Fatalf("entry.json is not JSON")
			}
			doc := decodeJSON(t, b)
			ledgerHash := lcm.LedgerHash()
			txHash := lcm.TransactionHash(0)
			sdk := map[string]string{
				"/v2/ledger_header/header/ledger_version":     strconv.FormatUint(uint64(lcm.ProtocolVersion()), 10),
				"/v2/ledger_header/header/ledger_seq":         strconv.FormatUint(uint64(lcm.LedgerSequence()), 10),
				"/v2/ledger_header/hash":                      hex.EncodeToString(ledgerHash[:]),
				"/v2/tx_processing/0/result/transaction_hash": hex.EncodeToString(txHash[:]),
			}
			for p, want := range sdk {
				if v, ok := resolvePointer(doc, p); !ok || fmt.Sprint(v) != want {
					t.Errorf("entry.json %s reads %v (%v); the Go SDK decodes %s from entry.xdr", p, v, ok, want)
				}
			}
			if int(lcm.ProtocolVersion()) != e.ProtocolVersion {
				t.Errorf("protocolVersion %d; the captured ledger header says %d (the recorded version is read from the capture)", e.ProtocolVersion, lcm.ProtocolVersion())
			}

			groups := map[string][]seedValue{"reproducedByEveryRun": e.ExpectedDecodedValues.ReproducedByEveryRun, "ledgerDependent": e.ExpectedDecodedValues.LedgerDependent}
			for name, group := range groups {
				if len(group) == 0 {
					t.Errorf("the %s group is empty", name)
				}
				for _, v := range group {
					got, ok := resolvePointer(doc, v.Pointer)
					if !ok || !sameJSON(got, decodeJSON(t, v.Value)) {
						t.Errorf("%s %s records %s; entry.json reads %v (%v)", name, v.Pointer, v.Value, got, ok)
					}
				}
			}
			t.Logf("ledger %d at protocol %d; %d reproduced and %d ledger-dependent values read again from entry.json",
				lcm.LedgerSequence(), lcm.ProtocolVersion(), len(e.ExpectedDecodedValues.ReproducedByEveryRun), len(e.ExpectedDecodedValues.LedgerDependent))
		})
	}
}

// assetCode4 is a four-byte asset code as text, without its zero padding.
func assetCode4(c xdr.AssetCode4) string { return strings.TrimRight(string(c[:]), "\x00") }

// accountBytes is an account strkey's key bytes.
func accountBytes(t *testing.T, address string) []byte {
	t.Helper()
	b, err := strkey.Decode(strkey.VersionByteAccountID, address)
	if err != nil {
		t.Fatalf("%s is not an account strkey: %v", address, err)
	}
	return b
}

// TestSeedEntry2RefusalEvidence: entry 2 alone carries networkRefusalEvidence, with resultCode CHANGE_TRUST_MALFORMED;
// its submittedEnvelopeXdr decodes as a one-operation transaction envelope from entry 2's source account whose
// operation is a ChangeTrust of the constant-product pool of the two USD assets of entry 2's issuers, fee 30, the
// assets in strkey-text order (asset A's issuer lower as text and higher as key bytes): the reverse of the order the
// captured, accepted transaction carries.
func TestSeedEntry2RefusalEvidence(t *testing.T) {
	_, entries := readCommittedCatalogue(t)
	var e2 *seedRecord
	for i := range entries {
		switch {
		case entries[i].ID == entry2ID:
			e2 = &entries[i]
		case entries[i].NetworkRefusalEvidence != nil:
			t.Errorf("%s carries networkRefusalEvidence; only entry 2 does", entries[i].ID)
		}
	}
	if e2 == nil || e2.NetworkRefusalEvidence == nil {
		t.Fatalf("entry 2 (%s) carries no networkRefusalEvidence", entry2ID)
	}
	ev := e2.NetworkRefusalEvidence
	if ev.ResultCode != "CHANGE_TRUST_MALFORMED" {
		t.Errorf("resultCode %q, want CHANGE_TRUST_MALFORMED", ev.ResultCode)
	}
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(ev.SubmittedEnvelopeXdr, &env); err != nil {
		t.Fatalf("submittedEnvelopeXdr does not decode as a TransactionEnvelope: %v", err)
	}
	source, err := model.AccountKey(entry2ID, "source")
	if err != nil {
		t.Fatal(err)
	}
	src := env.SourceAccount().ToAccountId()
	if got := src.Address(); got != source.Address() {
		t.Errorf("the envelope's source is %s; entry 2's source account is %s", got, source.Address())
	}
	if len(env.Signatures()) == 0 {
		t.Errorf("the submitted envelope carries no signature")
	}
	ops := env.Operations()
	if len(ops) != 1 {
		t.Fatalf("the envelope holds %d operations, want the one ChangeTrust", len(ops))
	}
	ct, ok := ops[0].Body.GetChangeTrustOp()
	if !ok || ct.Line.Type != xdr.AssetTypeAssetTypePoolShare || ct.Line.LiquidityPool == nil ||
		ct.Line.LiquidityPool.Type != xdr.LiquidityPoolTypeLiquidityPoolConstantProduct || ct.Line.LiquidityPool.ConstantProduct == nil {
		t.Fatalf("the operation is not a ChangeTrust of a constant-product pool share: %+v", ops[0].Body)
	}
	p := ct.Line.LiquidityPool.ConstantProduct
	if p.AssetA.Type != xdr.AssetTypeAssetTypeCreditAlphanum4 || p.AssetB.Type != xdr.AssetTypeAssetTypeCreditAlphanum4 {
		t.Fatalf("the pool's assets are of types %v and %v, want two alphanum-4 assets", p.AssetA.Type, p.AssetB.Type)
	}
	if p.Fee != 30 {
		t.Errorf("the pool's fee is %d, want 30", p.Fee)
	}
	codeA, codeB := assetCode4(p.AssetA.AlphaNum4.AssetCode), assetCode4(p.AssetB.AlphaNum4.AssetCode)
	if codeA != model.PoolAssetCode || codeB != model.PoolAssetCode {
		t.Errorf("the asset codes are %q and %q, want %q twice", codeA, codeB, model.PoolAssetCode)
	}
	issuerA, issuerB := p.AssetA.AlphaNum4.Issuer.Address(), p.AssetB.AlphaNum4.Issuer.Address()
	lower, err := model.AccountKey(entry2ID, model.PoolIssuerLowerBytesRole)
	if err != nil {
		t.Fatal(err)
	}
	higher, err := model.AccountKey(entry2ID, model.PoolIssuerHigherBytesRole)
	if err != nil {
		t.Fatal(err)
	}
	if issuerA != higher.Address() || issuerB != lower.Address() {
		t.Errorf("the envelope's issuers are %s, %s; entry 2's issuers in text order are %s, %s", issuerA, issuerB, higher.Address(), lower.Address())
	}
	if !(issuerA < issuerB) || bytes.Compare(accountBytes(t, issuerA), accountBytes(t, issuerB)) <= 0 {
		t.Errorf("the envelope's assets are not in text order against key-byte order: %s, %s", issuerA, issuerB)
	}

	recorded := map[string]string{}
	for _, v := range e2.ExpectedDecodedValues.ReproducedByEveryRun {
		for _, side := range []string{"asset_a", "asset_b"} {
			if strings.HasSuffix(v.Pointer, "/liquidity_pool_constant_product/"+side+"/credit_alphanum4/issuer") {
				var s string
				if err := json.Unmarshal(v.Value, &s); err != nil {
					t.Fatal(err)
				}
				recorded[side] = s
			}
		}
	}
	if recorded["asset_a"] != issuerB || recorded["asset_b"] != issuerA {
		t.Errorf("the accepted transaction's issuers are %q, %q (the catalogue's reproduced values); want the refused order reversed: %s, %s",
			recorded["asset_a"], recorded["asset_b"], issuerB, issuerA)
	}
	t.Logf("entry 2's refused envelope: ChangeTrust of USD/%s and USD/%s, fee %d, refused %s; the accepted one carries them in key-byte order",
		issuerA, issuerB, p.Fee, ev.ResultCode)
}

// TestSeedCorpusThroughTheProgram: run from the module root as the CI runs it, with no program on PATH,
// `ltk list corpus/ --json` exits 0 with nothing on standard error and lists the three entries in seed order, each
// record (CAP, protocol, XDR type, length, synthetic) the catalogue's; `ltk compare` of the committed corpus with
// itself exits 0 and passes, every entry's leaves equal.
func TestSeedCorpusThroughTheProgram(t *testing.T) {
	code, stdout, stderr := runLTKIn(t, moduleRoot, noDockerEnv(t), "list", "corpus/", "--json")
	if code != int(contract.EXIT_CODES.Success) || len(stderr) != 0 {
		t.Fatalf("ltk list corpus/ --json: exit %d stderr %q; want 0 and nothing", code, stderr)
	}
	requireValid(t, "list-output.schema.json", stdout)
	requireOneDocument(t, stdout)
	var listed []listRecord // ListOutput is the array of ListEntry
	if err := json.Unmarshal(stdout, &listed); err != nil {
		t.Fatalf("the list output: %v", err)
	}
	_, entries := readCommittedCatalogue(t)
	if len(listed) != len(entries) {
		t.Fatalf("ltk list lists %d entries; the catalogue has %d", len(listed), len(entries))
	}
	for i, r := range listed {
		e := entries[i]
		if r.ID != seedIDs[i] || r.Cap != e.Cap || r.ProtocolVersion.String() != strconv.Itoa(e.ProtocolVersion) ||
			r.XdrType != e.XdrType || r.Length.String() != strconv.Itoa(e.Length) || r.Synthetic != e.Synthetic {
			t.Errorf("ltk list's record %d is %+v; the catalogue records %s %s %d %s %d %v", i, r, e.ID, e.Cap, e.ProtocolVersion, e.XdrType, e.Length, e.Synthetic)
		}
	}

	abs := absFixture(t, committedCorpus)
	out, _ := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.Success, "compare", abs, abs)
	rep, unlisted := verifyCompareReport(t, out, abs, abs)
	if !rep.Passed || len(unlisted) != 0 {
		t.Errorf("ltk compare corpus corpus: passed %v, unlisted %q", rep.Passed, unlisted)
	}
	for _, e := range rep.Entries {
		if len(e.Differ) != 0 {
			t.Errorf("%s: %d differences between the corpus and itself", e.ID, len(e.Differ))
		}
	}
}

// twoEntryCorpus copies the committed corpus into a temporary folder and removes entry 3, its record and its files: the
// corpus that would ship if entry 3 could not be generated. The committed corpus is never touched.
func twoEntryCorpus(t *testing.T) string {
	t.Helper()
	dir := copyTree(t, committedCorpus)
	if err := os.RemoveAll(filepath.Join(dir, entry3ID)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "catalogue.json")
	cat, ok := decodeJSONFile(t, path).(map[string]any)
	if !ok {
		t.Fatal("corpus/catalogue.json is not an object")
	}
	list, _ := cat["entries"].([]any)
	var kept []any
	for _, e := range list {
		if m, ok := e.(map[string]any); ok && m["id"] == entry3ID {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) != 2 {
		t.Fatalf("%d entries kept, want 2", len(kept))
	}
	cat["entries"] = kept
	writeJSONFile(t, path, cat)
	return dir
}

// TestSeedMissingEntry3Propagates: a corpus without entry 3 (the two-entry fallback's shape) is still a corpus: its
// catalogue validates and `ltk list --json` lists entries 1 and 2 only, exit 0. Compared with the committed corpus it
// is the contract's usage error: `ltk compare` exits 2, nothing on standard output, one usage_error line naming the
// missing entry, in either order. The Docker tier holds `ltk check`'s row (seeds_integration_test.go).
func TestSeedMissingEntry3Propagates(t *testing.T) {
	two := twoEntryCorpus(t)
	catB, err := os.ReadFile(filepath.Join(two, "catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validationError(t, compileSchemaFile(t, catalogueSchemaPath), catB); err != nil {
		t.Errorf("the two-entry catalogue does not validate: %v", err)
	}
	stdout, _ := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.Success, "list", two, "--json")
	var listed []listRecord
	if err := json.Unmarshal(stdout, &listed); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range listed {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, seedIDs[:2]) {
		t.Errorf("ltk list lists %q, want %q", ids, seedIDs[:2])
	}

	full := absFixture(t, committedCorpus)
	for _, order := range []struct{ name, a, b string }{{"committed, two-entry", full, two}, {"two-entry, committed", two, full}} {
		_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.UsageError, "compare", order.a, order.b)
		if !strings.Contains(line.Message, entry3ID) {
			t.Errorf("ltk compare (%s): message %q does not name %s", order.name, line.Message, entry3ID)
		}
		t.Logf("ltk compare (%s): exit 2, %q", order.name, line.Message)
	}
}
