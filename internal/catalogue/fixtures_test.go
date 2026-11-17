package catalogue

// The fixture corpora under testdata/: every one is a complete corpus folder written by the Corpus layout writer, from
// synthetic decoded JSON shaped like the Rust XDR tool's decoding of a LedgerCloseMeta (the captures of the deciding
// test, the feasibility run made before this tool, show its pointers) and deterministic bytes that are not XDR.
// TestFixtureCorpora holds the committed fixtures to this generator byte for byte; run with `-args -update-fixtures` it
// rewrites them first.
//
//	testdata/corpus/valid               three entries, each file matching its recorded hash and length
//	testdata/corpus/schema-pointer      the catalogue misses /entries/1/sha256 and has a string at /entries/0/length
//	testdata/corpus/hash                one byte of entry 1's XDR file altered: the hash differs, the length does not
//	testdata/corpus/length              the catalogue records entry 2's length one byte longer: only the length differs
//	testdata/corpus/round-trip          entry 3's file carries 8 bytes after its value, recorded by hash and length: the
//	                                    file passes integrity and its re-encoding is shorter (the round-trip check)
//	testdata/corpus/expected-value      the catalogue records entry 1's operation result as "malformed"; the decoded
//	                                    form says "success" (the expected-values check)
//	testdata/corpus/schema-copy         the schema copy differs from schemas/ by one value
//	testdata/compare/ledger-only/{a,b}  two generations that differ only in ledger-dependent values (exit 0)
//	testdata/compare/reproduced-changed/{a,b}  b's entry 2 operation result differs, a reproduced value (exit 1)

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// updateFixtures rewrites testdata/corpus and testdata/compare from the generator.
var updateFixtures = flag.Bool("update-fixtures", false, "rewrite the fixture corpora under testdata/")

// testdataRoot is the module's testdata/ folder.
const testdataRoot = "../../testdata"

// fixtureDirs are the folders of testdata/ the generator owns.
var fixtureDirs = []string{"corpus", "compare"}

// fixtureRun describes one generation of the three seeds.
type fixtureRun struct {
	run int
	// alter, when set, changes one reproduced value: entry id → the operation result's new text.
	alter map[string]string
}

// fixtureLengths are the files' lengths: entries 1 and 2 as the deciding test measured them at protocol 28, entry 3 a
// sample value of the kind `ltk list` shows.
var fixtureLengths = map[string]int{
	model.SeedSignedPayloadSigner: 1920,
	model.SeedPoolShareTrustline:  2816,
	model.SeedMuxedAccount:        2048,
}

// fixtureProtocol is each fixture entry's protocol and fallback flag: entry 2 at the fallback protocol 28, the
// others at protocol 30.
var fixtureProtocol = map[string]struct {
	version  int
	fallback bool
}{
	model.SeedSignedPayloadSigner: {30, false},
	model.SeedPoolShareTrustline:  {contract.FALLBACK_PROTOCOL_VERSION, true},
	model.SeedMuxedAccount:        {30, false},
}

const fixtureSource = "test fixture of internal/catalogue/fixtures_test.go: decoded JSON shaped like the Rust XDR tool's decoding of a LedgerCloseMeta, and bytes that are not XDR"

// hexOf is a deterministic 64-hex-character value.
func hexOf(parts ...any) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(parts...)))
	return hex.EncodeToString(sum[:])
}

// fixtureBytes are n deterministic bytes, the first a NUL so every tool treats the file as binary.
func fixtureBytes(id string, run, n int) []byte {
	var out []byte
	for i := 0; len(out) < n; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("ltk fixture %s %d %d", id, run, i)))
		out = append(out, sum[:]...)
	}
	out = out[:n]
	out[0] = 0
	return out
}

// address is a seed role's account address, derived as the recipes derive it.
func address(t *testing.T, id, role string) string {
	t.Helper()
	k, err := model.AccountKey(id, role)
	if err != nil {
		t.Fatal(err)
	}
	return k.Address()
}

// fixtureDoc is the synthetic decoded JSON of one seed in one run: a ledger header, the transaction set holding the
// shape's transaction, and its processing. Every ledger-dependent value changes with the run, the ledger header's own
// hash and its tx_set_result_hash included.
func fixtureDoc(t *testing.T, id string, fr fixtureRun) []byte {
	t.Helper()
	ledger := 29 + fr.run*3
	closeTime := 1794696789 + fr.run*153
	seq := fmt.Sprint((uint64(ledger-1) << 32) + 1)
	h := func(name string) string { return hexOf(id, "/", fr.run, "/", name) }
	src := address(t, id, "source")
	var op, opResult, change string
	switch id {
	case model.SeedSignedPayloadSigner:
		signer := "PDUII3XUZ7DKZRPOVBDBE2PLIWHL26EXSEMNMNPNT5VI4UQYJKGGUAAAAAOX4GUACND5JH3IRHOCPMGLCWFCBOK74MUWYUSRPYZ4ZAIFOYAAAAG2RQ"
		op = fmt.Sprintf(`{"set_options":{"inflation_dest":null,"home_domain":null,"signer":{"key":%q,"weight":1}}}`, signer)
		opResult = `{"set_options":%q}`
		change = fmt.Sprintf(`{"updated":{"last_modified_ledger_seq":%d,"data":{"account":{"account_id":%q,"balance":"99999999900","seq_num":%q,"signers":[{"key":%q,"weight":1}],"ext":{"v1":{"liabilities":{"buying":"0","selling":"0"},"ext":{"v2":{"num_sponsored":0,"num_sponsoring":0,"signer_sponsoring_i_ds":[null],"ext":{"v3":{"ext":"v0","seq_ledger":%d,"seq_time":"%d"}}}}}}}},"ext":"v0"}}`,
			ledger, src, seq, signer, ledger, closeTime)
	case model.SeedPoolShareTrustline:
		asset := func(issuer string) string {
			return fmt.Sprintf(`{"credit_alphanum4":{"asset_code":%q,"issuer":%q}}`, model.PoolAssetCode, issuer)
		}
		op = fmt.Sprintf(`{"change_trust":{"line":{"pool_share":{"liquidity_pool_constant_product":{"asset_a":%s,"asset_b":%s,"fee":30}}},"limit":"9223372036854775807"}}`,
			asset(model.PoolIssuerLowerBytes), asset(model.PoolIssuerHigherBytes))
		opResult = `{"change_trust":%q}`
		change = fmt.Sprintf(`{"created":{"last_modified_ledger_seq":%d,"data":{"trustline":{"account_id":%q,"asset":{"pool_share":"LCCUJUDDCBXZYWOGOLKQKGXH4ST24YQ4HDC5GTH7U4NS2UC6UJJOCT4O"},"balance":"0","limit":"9223372036854775807","flags":0,"ext":"v0"}},"ext":"v0"}}`,
			ledger, src)
	case model.SeedMuxedAccount:
		muxedSrc := "MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAGZFQ"
		muxedDst := "MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAAAJLK"
		src = muxedSrc
		op = fmt.Sprintf(`{"payment":{"destination":%q,"asset":"native","amount":"10000000"}}`, muxedDst)
		opResult = `{"payment":%q}`
		change = fmt.Sprintf(`{"updated":{"last_modified_ledger_seq":%d,"data":{"account":{"account_id":%q,"balance":"99989999900","seq_num":%q}},"ext":"v0"}}`,
			ledger, address(t, id, "source"), seq)
	}
	result := "success"
	if r, ok := fr.alter[id]; ok {
		result = r
	}
	doc := fmt.Sprintf(`{"v2":{"ext":"v0",
"ledger_header":{"hash":%q,"header":{"ledger_version":28,"previous_ledger_hash":%q,"scp_value":{"tx_set_hash":%q,"close_time":"%d","upgrades":[],"ext":{"signed":{"node_id":"GCTI6HMWRH2QGMFKWVU5M5ZSOTKL7P7JAHZDMJJBKDHGWTEC4CJ7O3DU","signature":%q}}},"tx_set_result_hash":%q,"bucket_list_hash":%q,"ledger_seq":%d,"total_coins":"1000000000000000000","fee_pool":"%d","base_fee":100,"ext":"v0"},"ext":"v0"},
"tx_set":{"v1":{"previous_ledger_hash":%q,"phases":[{"v0":[{"txset_comp_txs_maybe_discounted_fee":{"base_fee":"100","txs":[{"tx":{"tx":{"source_account":%q,"fee":100,"seq_num":%q,"memo":"none","operations":[{"source_account":null,"body":%s}],"ext":"v0"},"signatures":[{"hint":"3a964e6a","signature":%q}]}}]}}]},{"v1":{"base_fee":null,"execution_stages":[]}}]}},
"tx_processing":[{"ext":"v0","result":{"transaction_hash":%q,"result":{"fee_charged":"100","result":{"tx_success":[{"op_inner":%s}]},"ext":"v0"}},
"fee_processing":[{"state":{"last_modified_ledger_seq":%d,"data":{"account":{"account_id":%q,"balance":"100000000000","seq_num":%q}},"ext":"v0"}}],
"tx_apply_processing":{"v4":{"ext":"v0","tx_changes_before":[],"operations":[{"ext":"v0","changes":[%s],"events":[]}],"tx_changes_after":[],"soroban_meta":null,"events":[],"diagnostic_events":[]}},"post_tx_apply_fee_processing":[]}],
"upgrades_processing":[],"scp_info":[],"total_byte_size_of_live_soroban_state":"31984","evicted_keys":[]}}`,
		h("ledger_hash"), h("previous_ledger_hash"), h("tx_set_hash"), closeTime, h("consensus_signature")+h("signature-2"),
		h("tx_set_result_hash"), h("bucket_list_hash"), ledger, 10036710+fr.run*7207,
		h("previous_ledger_hash"), src, seq, op, h("transaction_signature")+h("signature-2"),
		h("transaction_hash"), fmt.Sprintf(opResult, result),
		ledger-1, address(t, id, "source"), fmt.Sprint(uint64(ledger-1)<<32), change)
	return []byte(doc)
}

// writeFixtureRun writes one generation of the three seeds into folder through the layout writer, as ltk generate
// does: per entry its XDR file and its decoded JSON beside it, then the schema copy and the catalogue.
func writeFixtureRun(t *testing.T, folder string, fr fixtureRun) contract.Catalogue {
	t.Helper()
	var cat contract.Catalogue
	for _, s := range model.Seeds() {
		doc := fixtureDoc(t, s.ID, fr)
		xdrBytes := fixtureBytes(s.ID, fr.run, fixtureLengths[s.ID])
		filePath := EntryFilePath(s.ID)
		if err := WriteEntryFiles(folder, filePath, xdrBytes, doc); err != nil {
			t.Fatal(err)
		}
		expected, err := SplitExpectedValues(s.ID, doc)
		if err != nil {
			t.Fatal(err)
		}
		sha, length := Measure(xdrBytes)
		e := contract.CatalogueEntry{
			ID:                    s.ID,
			Cap:                   s.Cap,
			ProtocolVersion:       fixtureProtocol[s.ID].version,
			ProtocolFallback:      fixtureProtocol[s.ID].fallback,
			Source:                fixtureSource,
			Synthetic:             s.Synthetic,
			ExpectedDecodedValues: expected,
			FilePath:              filePath,
			XdrType:               s.XdrType,
			Sha256:                sha,
			Length:                length,
		}
		if s.RecordsNetworkRefusal {
			e.NetworkRefusalEvidence = &contract.NetworkRefusalEvidence{
				// The deciding test's text-order submission, from the first of its two final runs.
				SubmittedEnvelopeXdr: "AAAAAgAAAABHkNor3ig51PdlHkIM4Y2NM3dLEk4QZcsGgutgDbA4bAAAAGQAAAAiAAAAAwAAAAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAABgAAAAMAAAAAAAAAAVVTRAAAAAAAP8C+p0Z0AnwzxH9Rb0Dno52w/xg/BHtR0MEYc8n+2CwAAAABVVNEAAAAAAAeFJFtRyqAPNR7qoSWtFvKor9CHvlxnFd0g4dQ3SPlhQAAAB5//////////wAAAAAAAAABDbA4bAAAAED90nNIEQOzH4TiUGJfNzNZ7PSNqdXjYxwTV+jQvUczLKmFmf24VwmitXzRrlWJY0qSkUiwZED7UK/MhRBOr9MN",
				ResultCode:           contract.NetworkRefusalResultCodeChangeTrustMalformed,
			}
		}
		cat.Entries = append(cat.Entries, e)
	}
	if err := WriteCatalogue(folder, cat); err != nil {
		t.Fatal(err)
	}
	return cat
}

// mustEdit replaces the n-th (1-based) occurrence of old in the file at name with new.
func mustEdit(t *testing.T, name, old, new string, n int) {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	at := -1
	for i := 0; i < n; i++ {
		next := strings.Index(s[at+1:], old)
		if next < 0 {
			t.Fatalf("%s: occurrence %d of %q not found", name, n, old)
		}
		at += 1 + next
	}
	if err := os.WriteFile(name, []byte(s[:at]+new+s[at+len(old):]), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildFixtures writes every fixture folder under root.
func buildFixtures(t *testing.T, root string) {
	t.Helper()
	corpus := func(kind string) string { return filepath.Join(root, "corpus", kind) }

	writeFixtureRun(t, corpus("valid"), fixtureRun{})

	writeFixtureRun(t, corpus("schema-pointer"), fixtureRun{})
	cat := filepath.Join(corpus("schema-pointer"), CatalogueFileName)
	sha1 := ""
	if c, err := ReadCatalogue(corpus("schema-pointer")); err == nil {
		sha1 = c.Entries[1].Sha256
	}
	mustEdit(t, cat, fmt.Sprintf("      \"sha256\": %q,\n", sha1), "", 1)
	mustEdit(t, cat, "\"length\": 1920", "\"length\": \"1920\"", 1)

	writeFixtureRun(t, corpus("hash"), fixtureRun{})
	x := filepath.Join(corpus("hash"), filepath.FromSlash(EntryFilePath(model.SeedSignedPayloadSigner)))
	xb, _ := os.ReadFile(x)
	xb[100] ^= 0xFF
	if err := os.WriteFile(x, xb, 0o644); err != nil {
		t.Fatal(err)
	}

	writeFixtureRun(t, corpus("length"), fixtureRun{})
	mustEdit(t, filepath.Join(corpus("length"), CatalogueFileName), "\"length\": 2816", "\"length\": 2817", 1)

	c := writeFixtureRun(t, corpus("round-trip"), fixtureRun{})
	x = filepath.Join(corpus("round-trip"), filepath.FromSlash(EntryFilePath(model.SeedMuxedAccount)))
	xb, _ = os.ReadFile(x)
	longer := append(xb, make([]byte, 8)...)
	if err := os.WriteFile(x, longer, 0o644); err != nil {
		t.Fatal(err)
	}
	c.Entries[2].Sha256, c.Entries[2].Length = Measure(longer)
	if err := WriteCatalogue(corpus("round-trip"), c); err != nil {
		t.Fatal(err)
	}

	writeFixtureRun(t, corpus("expected-value"), fixtureRun{})
	mustEdit(t, filepath.Join(corpus("expected-value"), CatalogueFileName),
		"op_inner/set_options\",\n            \"value\": \"success\"", "op_inner/set_options\",\n            \"value\": \"malformed\"", 1)

	writeFixtureRun(t, corpus("schema-copy"), fixtureRun{})
	mustEdit(t, filepath.Join(corpus("schema-copy"), SchemaCopyFileName), "\"minItems\": 1,", "\"minItems\": 2,", 1)

	writeFixtureRun(t, filepath.Join(root, "compare", "ledger-only", "a"), fixtureRun{run: 0})
	writeFixtureRun(t, filepath.Join(root, "compare", "ledger-only", "b"), fixtureRun{run: 1})
	writeFixtureRun(t, filepath.Join(root, "compare", "reproduced-changed", "a"), fixtureRun{run: 0})
	writeFixtureRun(t, filepath.Join(root, "compare", "reproduced-changed", "b"),
		fixtureRun{run: 1, alter: map[string]string{model.SeedPoolShareTrustline: "malformed"}})
}

// treeFiles maps each file under root (forward-slash paths relative to root) to its bytes.
func treeFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestFixtureCorpora holds testdata/corpus and testdata/compare to the generator, file by file and byte for byte
// (-update-fixtures rewrites them).
func TestFixtureCorpora(t *testing.T) {
	if *updateFixtures {
		for _, d := range fixtureDirs {
			if err := os.RemoveAll(filepath.Join(testdataRoot, d)); err != nil {
				t.Fatal(err)
			}
		}
		buildFixtures(t, testdataRoot)
	}
	want := t.TempDir()
	buildFixtures(t, want)
	for _, d := range fixtureDirs {
		got, exp := treeFiles(t, filepath.Join(testdataRoot, d)), treeFiles(t, filepath.Join(want, d))
		var names []string
		for n := range exp {
			names = append(names, n)
		}
		for n := range got {
			if _, ok := exp[n]; !ok {
				t.Errorf("testdata/%s/%s is not a fixture the generator writes", d, n)
			}
		}
		slices.Sort(names)
		for _, n := range names {
			if g, ok := got[n]; !ok {
				t.Errorf("testdata/%s/%s is missing; run go test ./internal/catalogue -run TestFixtureCorpora -args -update-fixtures", d, n)
			} else if !bytes.Equal(g, exp[n]) {
				t.Errorf("testdata/%s/%s differs from the generator's; run go test ./internal/catalogue -run TestFixtureCorpora -args -update-fixtures", d, n)
			}
		}
	}
}
