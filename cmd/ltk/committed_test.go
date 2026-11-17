package main

// The committed data in the unit tier: corpus/ and repro/, written by the tool's own commands, read here and never
// written. No Docker: the integration tier's acceptance tests (accept_*_test.go) run the same commands against real
// containers.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/schema"
)

const (
	// committedCorpusDir and committedReproDir are the committed data, relative to this package (read only).
	committedCorpusDir = "../../corpus"
	committedReproDir  = "../../repro"
	// corpusArg is the folder argument the acceptance targets give, run from the module root.
	corpusArg = "corpus/"
)

// acceptRoot is the module root, absolute: the working directory of the runs that name `corpus/`.
func acceptRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// seedIDs are the three seed entry ids in seed order (internal/model/seeds.go).
func seedIDs() []string {
	var ids []string
	for _, s := range model.Seeds() {
		ids = append(ids, s.ID)
	}
	return ids
}

// TestCommittedCorpusLayout, without Docker: corpus/ is the three seed entries in the layout writer's form and nothing
// else: the catalogue and its schema copy at the top, one folder per entry holding entry.xdr and entry.json. The
// catalogue passes the catalogue_schema check (the schema, the two rules it cannot express, unique ids and the refusal
// evidence on entry 2 only, and the copy equal to schemas/), lists the seeds in seed order, and records each file's
// SHA-256 and length; each decoded JSON is strict JSON; an entry generated at protocol 28 with entries 1 and 2 has
// their protocol-28 lengths (1920 and 2816, as the deciding test, the feasibility run made before this tool, measured
// them).
func TestCommittedCorpusLayout(t *testing.T) {
	res, cat := schema.CheckCatalogue(committedCorpusDir)
	if !res.Passed || cat == nil {
		t.Fatalf("corpus/ fails catalogue_schema: %+v", res.Failures)
	}
	var ids []string
	for _, e := range cat.Entries {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, seedIDs()) {
		t.Fatalf("corpus/ lists %v; want the three seeds in seed order %v", ids, seedIDs())
	}
	want := []string{catalogue.CatalogueFileName, catalogue.SchemaCopyFileName}
	p28 := map[string]int{model.SeedSignedPayloadSigner: 1920, model.SeedPoolShareTrustline: 2816}
	for _, e := range cat.Entries {
		seed, _ := model.SeedByID(e.ID)
		if e.FilePath != catalogue.EntryFilePath(e.ID) || e.XdrType != seed.XdrType || e.Cap != seed.Cap || e.Synthetic {
			t.Errorf("%s: filePath %q xdrType %q cap %q synthetic %v", e.ID, e.FilePath, e.XdrType, e.Cap, e.Synthetic)
		}
		raw, err := catalogue.ReadEntryFile(committedCorpusDir, e.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		if sha, n := catalogue.Measure(raw); sha != e.Sha256 || n != e.Length {
			t.Errorf("%s: the file is %s, %d bytes; the catalogue records %s, %d", e.ID, sha, n, e.Sha256, e.Length)
		}
		if n, ok := p28[e.ID]; ok && e.ProtocolVersion == contract.FALLBACK_PROTOCOL_VERSION && e.Length != n {
			t.Errorf("%s at protocol 28 is %d bytes; the protocol-28 length is %d", e.ID, e.Length, n)
		}
		decoded, err := catalogue.ReadDecodedJSON(committedCorpusDir, e.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := catalogue.Parse(decoded); err != nil {
			t.Errorf("%s: the decoded JSON is not strict JSON: %v", e.ID, err)
		}
		want = append(want, e.FilePath, catalogue.DecodedJSONPath(e.FilePath))
	}
	var got []string
	for p := range treeOf(t, committedCorpusDir) {
		rel, err := filepath.Rel(committedCorpusDir, p)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("corpus/ holds %v\nwant exactly %v", got, want)
	}
}

// TestProcessCheckCommittedCorpusFake: `ltk check corpus/` generates nothing. Run from the module root as a clean clone
// runs it, with the fake docker, whose stellar-xdr reads the committed corpus: exit 0, nothing on stderr, the report
// for folder "corpus/" with the three entries validated, hashed, round-tripped and read back. The only container is the
// Rust XDR tool's: one `docker run` of the current quickstart with `--network none` and `sleep` as its entrypoint, no
// published port, no network or volume created; every exec is a stellar-xdr decode or encode. No generation path runs:
// no quickstart network, no RPC, no recipe. No file under corpus/ is written.
func TestProcessCheckCommittedCorpusFake(t *testing.T) {
	root := acceptRoot(t)
	corpus := filepath.Join(root, "corpus")
	f := newFakeEngine(t, "", corpus)
	before := treeOf(t, corpus)
	code, stdout, stderr := runLTKIn(t, root, f.env, "check", corpusArg)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %q; want 0 and nothing\n%s", code, stderr, stdout)
	}
	assertCommittedCheckReport(t, decodeReport(t, stdout))
	runs := 0
	for _, c := range f.calls() {
		switch {
		case c[0] == "run":
			runs++
			if !slices.Contains(c, orchestration.QuickstartCurrent) || !slices.Contains(c, "none") || !slices.Contains(c, "sleep") ||
				slices.Contains(c, "-p") || !slices.Contains(c, orchestration.LabelManaged+"=true") {
				t.Errorf("container %v: want the Rust XDR tool's only (the current quickstart, no network, sleep, labelled)", c)
			}
		case c[0] == "exec":
			if !slices.Contains(c, "stellar-xdr") || !(slices.Contains(c, "decode") || slices.Contains(c, "encode")) {
				t.Errorf("exec %v: a stellar-xdr decode or encode only", c)
			}
		case (c[0] == "network" || c[0] == "volume") && len(c) > 1 && c[1] == "create",
			c[0] == "port":
			t.Errorf("a generation path ran: %v", c)
		}
	}
	if runs != 1 {
		t.Errorf("%d containers started; want 1, the round trip's: %v", runs, f.subcommands())
	}
	if left := f.containers(); len(left) != 0 {
		t.Errorf("containers left: %v", left)
	}
	assertTreeUnchanged(t, "corpus/", before, treeOf(t, corpus))
}

// assertCommittedCheckReport holds `ltk check corpus/`'s report to its pass condition: folder "corpus/",
// catalogue_schema passed with no failure, the three seed entries in catalogue (seed) order, each with file_integrity
// (the file's hash and length equal to the recorded ones), round_trip (every byte compared), expected_values (no
// mismatch) passed and goSdkRoundTrip null, in CHECK_ORDER, and passed true.
func assertCommittedCheckReport(t *testing.T, r contract.CheckReport) {
	t.Helper()
	cs := r.CatalogueSchema
	if r.Folder != corpusArg || cs.Check != contract.CheckNameCatalogueSchema || !cs.Passed || len(cs.Failures) != 0 || !r.Passed {
		t.Errorf("folder %q catalogueSchema %+v passed %v; want %q, passed, no failure", r.Folder, cs, r.Passed, corpusArg)
	}
	var ids []string
	for _, e := range r.Entries {
		ids = append(ids, e.ID)
		fi, rt, ev := e.FileIntegrity, e.RoundTrip, e.ExpectedValues
		if fi.Check != contract.CheckNameFileIntegrity || !fi.Passed || fi.Sha256 != fi.RecordedSha256 || fi.Length != fi.RecordedLength || fi.Length < 1 {
			t.Errorf("%s: fileIntegrity %+v", e.ID, fi)
		}
		if rt.Check != contract.CheckNameRoundTrip || !rt.Passed || rt.BytesCompared != fi.Length {
			t.Errorf("%s: roundTrip %+v, the file is %d bytes", e.ID, rt, fi.Length)
		}
		if ev.Check != contract.CheckNameExpectedValues || !ev.Passed || len(ev.Mismatches) != 0 {
			t.Errorf("%s: expectedValues %+v", e.ID, ev)
		}
		if e.GoSdkRoundTrip != nil {
			t.Errorf("%s: goSdkRoundTrip %+v; want null without --also-go", e.ID, e.GoSdkRoundTrip)
		}
	}
	if !slices.Equal(ids, seedIDs()) {
		t.Errorf("entries %v; want the three seeds in catalogue order %v", ids, seedIDs())
	}
	cat, err := catalogue.ReadCatalogue(committedCorpusDir)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range r.Entries {
		if i < len(cat.Entries) && (e.FileIntegrity.RecordedSha256 != cat.Entries[i].Sha256 || e.FileIntegrity.RecordedLength != cat.Entries[i].Length) {
			t.Errorf("%s: the report's recorded values are not the catalogue's", e.ID)
		}
	}
}

// assertTreeUnchanged fails when after differs from before: a file added, removed or changed (the committed
// data is never written).
func assertTreeUnchanged(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for p, b := range before {
		if a, ok := after[p]; !ok {
			t.Errorf("%s: %s was removed", what, p)
		} else if a != b {
			t.Errorf("%s: %s was changed", what, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("%s: %s was written", what, p)
		}
	}
}

// readCommittedReport reads repro/<target>.json, validates it against its schema, decodes it strictly into v and
// holds its bytes to the report writer's form: the bytes the writer makes of v (two-space indentation, one trailing
// newline).
func readCommittedReport(t *testing.T, target contract.ReproTarget, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(committedReproDir, string(target)+".json"))
	if err != nil {
		t.Fatalf("the committed report: %v", err)
	}
	validateOutput(t, string(target)+"-report.schema.json", b)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("repro/%s.json: %v", target, err)
	}
	again, err := catalogue.EncodeReproReport(v)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, b) {
		t.Errorf("repro/%s.json is not in the report writer's form", target)
	}
}

// TestCommittedReproReports, on the committed reports, read only: repro/ holds exactly the two reports, as `ltk repro`
// wrote them, each valid against its schema and recording the reproduction's pass condition:
//   - js-sdk-1766: 17.2.0 (before_fix) fails its build step with "Assets are not in lexicographic order",
//     roundTripIdentical false; 17.2.1 (fix) passes build, decode and re_encode with roundTripIdentical true; passed
//     true, and the command's rule for passed (jsSdkVerdict) finds nothing against it;
//   - wallet-backend-729: protocolVersion 28; 2a0a84ea (before_fix) not ingested, its error the ingestion failure of
//     ledger L, storedRow null, retries 5 and restarts 1 (attempts 1 to 4 are logged and the fifth is in the exit
//     line); 2c873353 (fix) ingested, error null, storedRow the SIGNER row of ledger L; both at the same ledger L;
//     each commit's log and store state named in its own folder <…>/ltk-<runID>/wallet-backend-729/<commit>/ outside
//     repro/; passed true.
func TestCommittedReproReports(t *testing.T) {
	es, err := os.ReadDir(committedReproDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	if want := []string{"js-sdk-1766.json", "wallet-backend-729.json"}; !slices.Equal(names, want) {
		t.Errorf("repro/ holds %v; want exactly %v", names, want)
	}

	t.Run("js-sdk-1766", func(t *testing.T) {
		var rep contract.JsSdk1766Report
		readCommittedReport(t, contract.ReproTargetJsSdk1766, &rep)
		if rep.Target != contract.ReproTargetJsSdk1766 || !rep.Passed || len(rep.Versions) != 2 {
			t.Fatalf("%+v", rep)
		}
		before, fix := rep.Versions[0], rep.Versions[1]
		if before.Version != contract.SdkVersion17_2_0 || before.Role != contract.FixRoleBeforeFix || before.RoundTripIdentical ||
			len(before.Steps) != 1 || before.Steps[0].Step != contract.SdkReproStepBuild || before.Steps[0].Passed ||
			before.Steps[0].Error == nil || *before.Steps[0].Error != "Assets are not in lexicographic order" {
			t.Errorf("17.2.0: %+v", before)
		}
		steps := []contract.SdkReproStep{contract.SdkReproStepBuild, contract.SdkReproStepDecode, contract.SdkReproStepReEncode}
		if fix.Version != contract.SdkVersion17_2_1 || fix.Role != contract.FixRoleFix || !fix.RoundTripIdentical || len(fix.Steps) != len(steps) {
			t.Fatalf("17.2.1: %+v", fix)
		}
		for i, s := range fix.Steps {
			if s.Step != steps[i] || !s.Passed || s.Error != nil {
				t.Errorf("17.2.1 step %d: %+v", i, s)
			}
		}
		if reasons := jsSdkVerdict(&rep, nil); len(reasons) != 0 || !rep.Passed {
			t.Errorf("the rule for passed finds %v", reasons)
		}
	})

	t.Run("wallet-backend-729", func(t *testing.T) {
		var rep contract.WalletBackend729Report
		readCommittedReport(t, contract.ReproTargetWalletBackend729, &rep)
		if rep.Target != contract.ReproTargetWalletBackend729 || !rep.Passed || rep.ProtocolVersion != contract.WALLET_BACKEND_729_PROTOCOL_VERSION || len(rep.Commits) != 2 {
			t.Fatalf("target %s passed %v protocolVersion %d commits %d", rep.Target, rep.Passed, rep.ProtocolVersion, len(rep.Commits))
		}
		pre, fix := rep.Commits[0], rep.Commits[1]
		if pre.Commit != contract.WalletBackendCommit2a0a84ea || pre.Role != contract.FixRoleBeforeFix || pre.Ingested || pre.StoredRow != nil ||
			pre.Error == nil || !strings.Contains(*pre.Error, fmt.Sprintf("Error ingesting data for ledger %d", pre.Ledger)) ||
			pre.Retries != contract.PRE_FIX_RETRY_LIMIT || pre.Restarts != contract.PRE_FIX_RESTART_COUNT {
			t.Errorf("before_fix: commit %s role %s ingested %v storedRow-set %v retries %d restarts %d error %v",
				pre.Commit, pre.Role, pre.Ingested, pre.StoredRow != nil, pre.Retries, pre.Restarts, pre.Error)
		}
		var row map[string]json.RawMessage
		if fix.Commit != contract.WalletBackendCommit2c873353 || fix.Role != contract.FixRoleFix || !fix.Ingested || fix.Error != nil ||
			fix.StoredRow == nil || json.Unmarshal(*fix.StoredRow, &row) != nil {
			t.Fatalf("fix: %+v", fix)
		}
		if string(row["state_change_category"]) != `"SIGNER"` || string(row["ledger_number"]) != fmt.Sprint(fix.Ledger) {
			t.Errorf("fix: the stored row is not the SIGNER row of ledger %d: %s", fix.Ledger, *fix.StoredRow)
		}
		if pre.Ledger < 1 || pre.Ledger != fix.Ledger {
			t.Errorf("ledgers %d and %d; want entry 1's ledger, the same for both", pre.Ledger, fix.Ledger)
		}
		for _, c := range rep.Commits {
			for _, p := range []string{c.LogPath, c.StoreStatePath} {
				s := filepath.ToSlash(p)
				if !strings.Contains(s, "/ltk-") || !strings.Contains(s, "/"+string(contract.ReproTargetWalletBackend729)+"/"+string(c.Commit)+"/") ||
					strings.Contains(s, "/repro/") {
					t.Errorf("commit %s: %s is not in <temporary directory>/ltk-<runID>/wallet-backend-729/%s/ outside repro/", c.Commit, p, c.Commit)
				}
			}
		}
		if pre.LogPath == fix.LogPath || pre.StoreStatePath == fix.StoreStatePath {
			t.Errorf("the two commits share an evidence file")
		}
	})
}
