package main

// `ltk repro js-sdk-1766` and `ltk repro wallet-backend-729` at the process boundary in the unit tier.
//
//   - The arguments and the environment check are run on the built program with the fake docker of
//     testdata/fakedocker: nothing reaches a runner.
//   - Everything after the environment check is run in a helper process: this test binary, started as the program
//     with LTK_TEST_REPRO_SCENARIO set, puts a fake reproduction runner in the package's seam (jsSdkRunner,
//     walletBackendRunner) and calls main. Everything else is the program's own code: the router, the interrupt
//     handler with its signals, the environment check, the snapshots and the reaper against the fake docker, the
//     report writer, the stdout writer and the mapper's one exit. The fake runner starts one labelled container through
//     the run's runner, so the teardown has something to remove. Each helper process runs as the test does, as root in
//     the build's container, and nothing it asserts depends on permissions.
//
// The reproductions against real Docker are in repro_integration_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
	"github.com/verticalhorizonai/ltk/internal/orchestration"
	"github.com/verticalhorizonai/ltk/internal/repro/walletbackend"
)

const (
	// helperScenarioVar names the fake runner a helper process installs (fakeRepros).
	helperScenarioVar = "LTK_TEST_REPRO_SCENARIO"
	// helperRecordVar is the file the fake runner writes once its container is started: what it was given.
	helperRecordVar = "LTK_TEST_REPRO_RECORD"
)

// helperRecord is what a fake runner was given, written once its container is started.
type helperRecord struct {
	Deadline time.Duration `json:"deadline"`
	WorkDir  string        `json:"workDir"`
	Entry1   string        `json:"entry1"`
}

func strp(s string) *string { return &s }

// jsSdkReport is the command contract's success example of `ltk repro js-sdk-1766`.
func jsSdkReport() contract.JsSdk1766Report {
	return contract.JsSdk1766Report{
		Target: contract.ReproTargetJsSdk1766,
		Versions: []contract.SdkVersionResult{
			{Version: contract.SdkVersion17_2_0, Role: contract.FixRoleBeforeFix, Steps: []contract.SdkStepResult{
				{Step: contract.SdkReproStepBuild, Passed: false, Error: strp("Assets are not in lexicographic order")},
			}},
			{Version: contract.SdkVersion17_2_1, Role: contract.FixRoleFix, Steps: []contract.SdkStepResult{
				{Step: contract.SdkReproStepBuild, Passed: true},
				{Step: contract.SdkReproStepDecode, Passed: true},
				{Step: contract.SdkReproStepReEncode, Passed: true},
			}, RoundTripIdentical: true},
		},
		Passed: true,
	}
}

// walletBackendReport is a report of a successful run (exit 0) with its evidence under workDir.
func walletBackendReport(workDir string) contract.WalletBackend729Report {
	row := contract.JsonObject(json.RawMessage(`{"ledger_number":14}`))
	commit := func(c contract.WalletBackendCommit, role contract.FixRole) contract.WalletBackendCommitResult {
		return contract.WalletBackendCommitResult{Commit: c, Role: role, Ledger: 14,
			LogPath:        filepath.Join(workDir, string(c), "wallet-backend.log"),
			StoreStatePath: filepath.Join(workDir, string(c), "store-state.sql")}
	}
	pre, fix := commit(contract.WalletBackendCommit2a0a84ea, contract.FixRoleBeforeFix), commit(contract.WalletBackendCommit2c873353, contract.FixRoleFix)
	pre.Error, pre.Retries, pre.Restarts = strp("processing ledger 14: unsupported signer key type ed25519SignedPayload"), 5, 1
	fix.Ingested, fix.StoredRow = true, &row
	return contract.WalletBackend729Report{Target: contract.ReproTargetWalletBackend729,
		ProtocolVersion: contract.WALLET_BACKEND_729_PROTOCOL_VERSION, Commits: []contract.WalletBackendCommitResult{pre, fix}, Passed: true}
}

// saveEvidence writes each commit's log and store state where the report names them, as the runner does.
func saveEvidence(rep contract.WalletBackend729Report) error {
	for _, c := range rep.Commits {
		for _, p := range []string{c.LogPath, c.StoreStatePath} {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte("saved\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// fakeRun is what each fake runner does first: start one labelled container of image through the run's runner, then
// write the record (the test waits for it before an interrupt).
func fakeRun(ctx context.Context, r *orchestration.Runner, image string, rec helperRecord) error {
	if _, err := r.Run(ctx, orchestration.ContainerSpec{Role: "repro", Image: image, Network: "none"}); err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(os.Getenv(helperRecordVar), b, 0o644)
}

// fakeJsSdk installs a fake js-sdk runner returning rep (after mutate) or err, with reasons; hang waits for the
// interrupt instead.
func fakeJsSdk(mutate func(*contract.JsSdk1766Report), reasons []string, err error, hang bool) {
	jsSdkRunner.run = func(ctx context.Context, r *orchestration.Runner) (contract.JsSdk1766Report, error) {
		if ferr := fakeRun(ctx, r, orchestration.NodeImage, helperRecord{}); ferr != nil {
			return contract.JsSdk1766Report{}, ferr
		}
		if hang {
			<-ctx.Done()
			return contract.JsSdk1766Report{}, fmt.Errorf("npm ci for 17.2.0: %w", ctx.Err())
		}
		rep := jsSdkReport()
		if mutate != nil {
			mutate(&rep)
		}
		return rep, err
	}
	jsSdkRunner.reasons = func(contract.JsSdk1766Report) []string { return reasons }
}

// fakeWalletBackend installs a fake wallet-backend runner: it records its options, saves the evidence (unless
// noEvidence) and returns the report in opt.WorkDir (after mutate) or err, with reasons; hang waits for the interrupt.
func fakeWalletBackend(mutate func(*contract.WalletBackend729Report), reasons []string, err error, hang, noEvidence bool) {
	walletBackendRunner.run = func(ctx context.Context, r *orchestration.Runner, opt walletbackend.Options) (contract.WalletBackend729Report, error) {
		rec := helperRecord{Deadline: opt.Deadline, WorkDir: opt.WorkDir}
		if opt.Entry1 != nil {
			rec.Entry1 = opt.Entry1.ID()
		}
		if ferr := fakeRun(ctx, r, orchestration.QuickstartProtocol28, rec); ferr != nil {
			return contract.WalletBackend729Report{}, ferr
		}
		if hang {
			<-ctx.Done()
			return contract.WalletBackend729Report{}, fmt.Errorf("waiting for both cursors: %w", ctx.Err())
		}
		rep := walletBackendReport(opt.WorkDir)
		if mutate != nil {
			mutate(&rep)
		}
		if !noEvidence {
			if serr := saveEvidence(rep); serr != nil {
				return contract.WalletBackend729Report{}, serr
			}
		}
		return rep, err
	}
	walletBackendRunner.reasons = func(contract.WalletBackend729Report) []string { return reasons }
}

// fakeRepros are the scenarios of the helper process, by name.
var fakeRepros = map[string]func(){
	"js-passed": func() { fakeJsSdk(nil, nil, nil, false) },
	"js-not-reproduced": func() {
		fakeJsSdk(func(r *contract.JsSdk1766Report) { r.Versions[1].RoundTripIdentical, r.Passed = false, false },
			[]string{"17.2.1 re-encoded bytes differ from the built bytes"}, nil, false)
	},
	"js-passed-against-the-rule": func() { // the runner says passed, but 17.2.0 built the trustline
		fakeJsSdk(func(r *contract.JsSdk1766Report) {
			r.Versions[0].Steps = []contract.SdkStepResult{{Step: contract.SdkReproStepBuild, Passed: true}}
		}, nil, nil, false)
	},
	"js-error":  func() { fakeJsSdk(nil, nil, model.Failure("npm ci for 17.2.0 failed: exit status 1"), false) },
	"js-hang":   func() { fakeJsSdk(nil, nil, nil, true) },
	"wb-passed": func() { fakeWalletBackend(nil, nil, nil, false, false) },
	"wb-deadline": func() { // a pre-fix run reached the deadline after two failures: the runner says not reproduced
		fakeWalletBackend(func(r *contract.WalletBackend729Report) {
			r.Commits[0].Retries, r.Commits[0].Restarts, r.Passed = 2, 0, false
			r.Commits[0].Error = strp("run 1 reached the safety deadline after 2 of 5 failures")
		}, []string{"commit 2a0a84ea (before_fix) did not show its failure before the deadline"}, nil, false, false)
	},
	"wb-short-but-passed": func() { // the runner says passed with four failures: the guard maps it to not reproduced
		fakeWalletBackend(func(r *contract.WalletBackend729Report) { r.Commits[0].Retries = 4 }, nil, nil, false, false)
	},
	"wb-fix-not-ingested": func() {
		fakeWalletBackend(func(r *contract.WalletBackend729Report) {
			r.Commits[1].Ingested, r.Commits[1].StoredRow, r.Passed = false, nil, false
			r.Commits[1].Error = strp("cursor 13 below ledger 14 at the deadline")
		}, []string{"commit 2c873353 (fix) did not ingest ledger 14"}, nil, false, false)
	},
	"wb-no-evidence": func() { fakeWalletBackend(nil, nil, nil, false, true) },
	"wb-error": func() {
		fakeWalletBackend(nil, nil, model.Failure("building 2a0a84ea: go mod verify failed"), false, false)
	},
	"wb-hang": func() { fakeWalletBackend(nil, nil, nil, true, false) },
}

// runHelperMain is the helper process: it installs the scenario's fake runner and runs the program, which exits.
func runHelperMain(scenario string) {
	install, ok := fakeRepros[scenario]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown helper scenario %q\n", scenario)
		os.Exit(99)
	}
	install()
	main()
}

// reproHelper is one helper-process run of `ltk repro <target>`: its fake engine, working directory and record file.
type reproHelper struct {
	t        *testing.T
	f        *fakeEngine
	dir, tmp string
	record   string
	scenario string
}

// newReproHelper prepares a helper run of scenario with the fake docker in modes. The program's temporary directory is
// the test's own, so the wallet-backend working folder is removed with it.
func newReproHelper(t *testing.T, scenario, modes string) *reproHelper {
	t.Helper()
	f := newFakeEngine(t, modes, "")
	return &reproHelper{t: t, f: f, dir: t.TempDir(), tmp: t.TempDir(), record: filepath.Join(t.TempDir(), "record.json"), scenario: scenario}
}

// command is the helper process as `ltk repro target`.
func (h *reproHelper) command(target contract.ReproTarget) *exec.Cmd {
	h.t.Helper()
	self, err := os.Executable()
	if err != nil {
		h.t.Fatal(err)
	}
	cmd := exec.Command(self, "repro", string(target))
	cmd.Dir = h.dir
	cmd.Env = childEnv(append(append([]string{}, h.f.env...),
		helperScenarioVar+"="+h.scenario, helperRecordVar+"="+h.record, "TMPDIR="+h.tmp, "TMP="+h.tmp))
	return cmd
}

// run runs the helper process to its end.
func (h *reproHelper) run(target contract.ReproTarget) (code int, stdout, stderr []byte) {
	h.t.Helper()
	cmd := h.command(target)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	return exitCodeOfRun(h.t, cmd.Run()), out.Bytes(), errOut.Bytes()
}

// reproFiles returns the names under the working directory's repro/, nil when it does not exist.
func (h *reproHelper) reproFiles() []string {
	es, err := os.ReadDir(filepath.Join(h.dir, catalogue.ReproFolder))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// runID is the run id of the helper's run: the ltk.run label of the container the fake runner started.
func (h *reproHelper) runID() string {
	h.t.Helper()
	for _, c := range h.f.calls() {
		if len(c) > 0 && c[0] == "run" {
			for i, a := range c {
				if a == "--label" && i+1 < len(c) && strings.HasPrefix(c[i+1], orchestration.LabelRun+"=") {
					return strings.TrimPrefix(c[i+1], orchestration.LabelRun+"=")
				}
			}
		}
	}
	h.t.Fatalf("no labelled container was run: %v", h.f.subcommands())
	return ""
}

// assertTornDownRepro: nothing of the run is left in the fake engine and the reaper ran after the work.
func (h *reproHelper) assertTornDownRepro() {
	h.t.Helper()
	if left := h.f.containers(); len(left) != 0 {
		h.t.Errorf("containers left: %v", left)
	}
	reapedAfterWork(h.t, h.f)
}

// assertReport asserts a written report: stdout byte-identical to repro/<target>.json, which validates against its
// schema, and repro/ holding that report only.
func (h *reproHelper) assertReport(target contract.ReproTarget, stdout []byte) {
	h.t.Helper()
	file, err := os.ReadFile(catalogue.ReproReportPath(h.dir, target))
	if err != nil {
		h.t.Fatalf("the report was not written: %v", err)
	}
	if !bytes.Equal(stdout, file) {
		h.t.Errorf("stdout is not byte-identical to repro/%s.json:\n%s\n%s", target, stdout, file)
	}
	validateOutput(h.t, string(target)+"-report.schema.json", stdout)
	if !bytes.HasPrefix(stdout, []byte("{\n  \"target\": ")) || !bytes.HasSuffix(stdout, []byte("}\n")) || bytes.HasSuffix(stdout, []byte("\n\n")) {
		h.t.Errorf("not one two-space indented document with one trailing newline: %q", stdout)
	}
	if got := h.reproFiles(); !slices.Equal(got, []string{string(target) + ".json"}) {
		h.t.Errorf("repro/ holds %v; want the report only", got)
	}
}

// requireHelper skips the test, saying why, when this test binary cannot be started as the program (the fake docker
// is also needed).
func requireHelper(t *testing.T) {
	t.Helper()
	requireFakeDocker(t)
	if _, err := os.Executable(); err != nil {
		t.Skipf("skipped: the test binary's own path is unknown: %v", err)
	}
}

// TestProcessReproUsage (the contracts' exit-2 rows): both commands are registered; an extra
// argument or any flag exits 2 with nothing on stdout, before the environment check (the fake docker is never called)
// and nothing is written under the working directory; --help prints the synopsis and exits 0.
func TestProcessReproUsage(t *testing.T) {
	for _, target := range []string{"js-sdk-1766", "wallet-backend-729"} {
		for _, extra := range [][]string{{"extra"}, {"--out", "x"}, {"--deadline=1"}, {"-v"}} {
			t.Run(target+" "+strings.Join(extra, " "), func(t *testing.T) {
				// Without the fake docker (a native run) PATH holds no docker at all: a usage error comes first.
				var f *fakeEngine
				env := []string{"PATH=" + t.TempDir()}
				if fakeDockerDir != "" {
					f = newFakeEngine(t, "", "")
					env = f.env
				}
				dir := t.TempDir()
				code, stdout, stderr := runLTKIn(t, dir, env, append([]string{"repro", target}, extra...)...)
				if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
					t.Fatalf("exit %d stdout %q stderr %s; want 2 and nothing", code, stdout, stderr)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeUsageError)
				if !strings.HasSuffix(line.Message, "; usage: ltk repro "+target) {
					t.Errorf("message %q", line.Message)
				}
				if f != nil && len(f.calls()) != 0 {
					t.Errorf("the environment was touched: %v", f.calls())
				}
				if exists(filepath.Join(dir, catalogue.ReproFolder)) {
					t.Errorf("repro/ was created")
				}
			})
		}
		t.Run(target+" --help", func(t *testing.T) {
			code, stdout, stderr := runLTKIn(t, t.TempDir(), nil, "repro", target, "--help")
			if code != 0 || string(stdout) != "ltk repro "+target+"\n" || len(stderr) != 0 {
				t.Errorf("exit %d stdout %q stderr %q", code, stdout, stderr)
			}
		})
	}
}

// TestProcessReproEnvironmentMissing (the contracts' exit-3 rows): Docker missing, a daemon
// that is not reachable, Windows containers, or an image that cannot be pulled exits 3 with nothing on stdout and one
// environment_missing ErrorLine naming what is missing; nothing is started and nothing is written.
func TestProcessReproEnvironmentMissing(t *testing.T) {
	images := map[string][]string{
		"js-sdk-1766":        orchestration.JsSdkImages,
		"wallet-backend-729": orchestration.WalletBackendImages,
	}
	for _, target := range []string{"js-sdk-1766", "wallet-backend-729"} {
		for _, c := range []struct{ mode, want string }{
			{"no-docker", "Docker with Linux containers is not available: docker CLI not found on PATH"},
			{"no-daemon", "Docker with Linux containers is not available: Docker daemon not reachable"},
			{"windows", "Docker with Linux containers is not available: Docker is not running Linux containers"},
			{"no-image", images[target][0]},
		} {
			t.Run(target+" "+c.mode, func(t *testing.T) {
				f := newFakeEngine(t, c.mode, "")
				env := f.env
				if c.mode == "no-docker" {
					env = []string{"PATH=" + t.TempDir()}
				}
				dir := t.TempDir()
				code, stdout, stderr := runLTKIn(t, dir, env, "repro", target)
				if code != int(contract.EXIT_CODES.EnvironmentMissing) || len(stdout) != 0 {
					t.Fatalf("exit %d stdout %q stderr %s; want 3 and nothing", code, stdout, stderr)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeEnvironmentMissing)
				if !strings.Contains(line.Message, c.want) {
					t.Errorf("message %q; want it to contain %q", line.Message, c.want)
				}
				if slices.Contains(f.subcommands(), "run") {
					t.Errorf("a container was started: %v", f.subcommands())
				}
				if exists(filepath.Join(dir, catalogue.ReproFolder)) {
					t.Errorf("repro/ was created")
				}
			})
		}
	}
}

// TestProcessReproJsSdk:
//   - passed: exit 0, nothing on stderr, the report on stdout byte-identical to repro/js-sdk-1766.json under the
//     working directory, which validates against its schema and carries 17.2.0's "Assets are not in lexicographic
//     order" and 17.2.1's round trip; repro/ holds only it; the run's container is removed;
//   - not reproduced: exit 1, the report (passed false) on stdout and in the file, one failure ErrorLine carrying the
//     runner's reasons;
//   - a report the runner marked passed that breaks the report's rule for passed (17.2.0 built the trustline): passed
//     false in the report and the file, exit 1, the reason named.
func TestProcessReproJsSdk(t *testing.T) {
	requireHelper(t)
	t.Run("passed", func(t *testing.T) {
		h := newReproHelper(t, "js-passed", "")
		code, stdout, stderr := h.run(contract.ReproTargetJsSdk1766)
		if code != 0 || len(stderr) != 0 {
			t.Fatalf("exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
		}
		h.assertReport(contract.ReproTargetJsSdk1766, stdout)
		if !bytes.Contains(stdout, []byte(`"error": "Assets are not in lexicographic order"`)) ||
			!bytes.Contains(stdout, []byte(`"roundTripIdentical": true`)) || !bytes.HasSuffix(stdout, []byte("  \"passed\": true\n}\n")) {
			t.Errorf("%s", stdout)
		}
		h.assertTornDownRepro()
		for _, c := range h.f.calls() {
			if c[0] == "run" && !slices.Contains(c, orchestration.NodeImage) {
				t.Errorf("a container of another image: %v", c)
			}
		}
	})
	for _, c := range []struct{ scenario, reason string }{
		{"js-not-reproduced", "17.2.1 re-encoded bytes differ from the built bytes"},
		{"js-passed-against-the-rule", "17.2.0 (before_fix) did not reject entry 2's pool-share trustline"},
	} {
		t.Run(c.scenario, func(t *testing.T) {
			h := newReproHelper(t, c.scenario, "")
			code, stdout, stderr := h.run(contract.ReproTargetJsSdk1766)
			if code != int(contract.EXIT_CODES.Failure) {
				t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
			}
			h.assertReport(contract.ReproTargetJsSdk1766, stdout)
			if !bytes.HasSuffix(stdout, []byte("  \"passed\": false\n}\n")) {
				t.Errorf("the report is not passed false:\n%s", stdout)
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			if line.Message != "ltk repro js-sdk-1766: not reproduced: "+c.reason {
				t.Errorf("message %q", line.Message)
			}
			h.assertTornDownRepro()
		})
	}
}

// TestProcessReproWalletBackend: the runner is given the 15-minute deadline of
// each commit run (never waited for), entry 1's recipe and the working folder <TMPDIR>/ltk-<runID>/wallet-backend-729,
// outside the working directory; on passed the program exits 0 with the report on stdout byte-identical to
// repro/wallet-backend-729.json, repro/ holds only it, the evidence files stay in the working folder and nothing is
// left in the engine.
func TestProcessReproWalletBackend(t *testing.T) {
	requireHelper(t)
	h := newReproHelper(t, "wb-passed", "")
	code, stdout, stderr := h.run(contract.ReproTargetWalletBackend729)
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("exit %d stderr %s; want 0 and nothing\n%s", code, stderr, stdout)
	}
	h.assertReport(contract.ReproTargetWalletBackend729, stdout)
	h.assertTornDownRepro()
	b, err := os.ReadFile(h.record)
	if err != nil {
		t.Fatal(err)
	}
	var rec helperRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(h.tmp, "ltk-"+h.runID(), "wallet-backend-729")
	if rec.Deadline != 15*time.Minute || rec.WorkDir != wantDir || rec.Entry1 != model.SeedSignedPayloadSigner {
		t.Errorf("the runner was given %+v; want the 15-minute deadline, %s and entry 1's recipe", rec, wantDir)
	}
	var rep contract.WalletBackend729Report
	if err := json.Unmarshal(stdout, &rep); err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Commits {
		for _, p := range []string{c.LogPath, c.StoreStatePath} {
			if !strings.HasPrefix(p, wantDir+string(filepath.Separator)+string(c.Commit)+string(filepath.Separator)) || !exists(p) {
				t.Errorf("commit %s: evidence %s is not kept in the working folder", c.Commit, p)
			}
		}
	}
	for _, c := range h.f.calls() {
		if c[0] == "run" && !slices.Contains(c, orchestration.QuickstartProtocol28) {
			t.Errorf("a container of another image: %v", c)
		}
	}
}

// TestProcessReproWalletBackendNotReproduced: a report not passed exits 1 with the report on stdout and in repro/,
// passed false in both, and one failure ErrorLine carrying the reasons and the saved logs' paths:
//   - a pre-fix run that reached the deadline after two failures (the runner's verdict);
//   - a report the runner marked passed with four failures: the guard maps it to not reproduced (retries
//     must be PRE_FIX_RETRY_LIMIT, restarts PRE_FIX_RESTART_COUNT);
//   - the fix commit not ingesting;
//   - a report whose log and store state were not saved (the evidence backs the verdict).
func TestProcessReproWalletBackendNotReproduced(t *testing.T) {
	requireHelper(t)
	for _, c := range []struct {
		scenario string
		want     []string
	}{
		{"wb-deadline", []string{"reproduction failed: commit 2a0a84ea (before_fix) did not show its failure before the deadline; the logs"}},
		{"wb-short-but-passed", []string{"reproduction failed: commit 2a0a84ea (before_fix) showed 4 of 5 failures and 1 of 1 restarts, each run bounded by the safety deadline of 15 minutes: not reproduced"}},
		{"wb-fix-not-ingested", []string{"reproduction failed: commit 2c873353 (fix) did not ingest ledger 14; the logs"}},
		{"wb-no-evidence", []string{"the log of commit 2a0a84ea is not saved", "the store state of commit 2c873353 is not saved"}},
	} {
		t.Run(c.scenario, func(t *testing.T) {
			h := newReproHelper(t, c.scenario, "")
			code, stdout, stderr := h.run(contract.ReproTargetWalletBackend729)
			if code != int(contract.EXIT_CODES.Failure) {
				t.Fatalf("exit %d stderr %s; want 1\n%s", code, stderr, stdout)
			}
			h.assertReport(contract.ReproTargetWalletBackend729, stdout)
			var rep contract.WalletBackend729Report
			if err := json.Unmarshal(stdout, &rep); err != nil || rep.Passed {
				t.Errorf("the report is not passed false (%v):\n%s", err, stdout)
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			if !strings.HasPrefix(line.Message, "ltk repro wallet-backend-729: reproduction failed: ") {
				t.Errorf("message %q", line.Message)
			}
			for _, w := range append(c.want, "the logs are saved at "+filepath.ToSlash(rep.Commits[0].LogPath)+" (2a0a84ea) and "+filepath.ToSlash(rep.Commits[1].LogPath)+" (2c873353)") {
				if !strings.Contains(line.Message, w) {
					t.Errorf("message %q\nwant it to contain %q", line.Message, w)
				}
			}
			h.assertTornDownRepro()
		})
	}
}

// TestProcessReproRunnerError (the runners' error rows): an error the runner returns ends the run
// with exit 1, one failure ErrorLine carrying it, no report on stdout and nothing under repro/; everything is removed.
func TestProcessReproRunnerError(t *testing.T) {
	requireHelper(t)
	for _, c := range []struct {
		scenario string
		target   contract.ReproTarget
		want     string
	}{
		{"js-error", contract.ReproTargetJsSdk1766, "ltk repro js-sdk-1766: npm ci for 17.2.0 failed: exit status 1"},
		{"wb-error", contract.ReproTargetWalletBackend729, "ltk repro wallet-backend-729: building 2a0a84ea: go mod verify failed"},
	} {
		t.Run(c.scenario, func(t *testing.T) {
			h := newReproHelper(t, c.scenario, "")
			code, stdout, stderr := h.run(c.target)
			if code != int(contract.EXIT_CODES.Failure) || len(stdout) != 0 {
				t.Fatalf("exit %d stdout %q stderr %s; want 1 and no report", code, stdout, stderr)
			}
			if line := assertErrorLine(t, stderr, contract.ErrorCodeFailure); line.Message != c.want {
				t.Errorf("message %q, want %q", line.Message, c.want)
			}
			if exists(filepath.Join(h.dir, catalogue.ReproFolder)) {
				t.Errorf("repro/ was created")
			}
			h.assertTornDownRepro()
		})
	}
}

// TestProcessReproWriteFails (a report is written whole or not at all): run as root in the build's
// container, the report's write fails in a way root cannot override:
//   - a directory in the place of repro/<target>.json: exit 1, no report on stdout, one failure ErrorLine naming the
//     path; repro/ holds only that directory (no temporary file is left) and the directory is unchanged; every
//     container is removed. A second run does exactly the same. Once the directory is removed, a run writes the report
//     and exits 0, and repro/ holds only the report.
//   - a file in the place of repro/: the same, the message naming the folder; the file is unchanged.
func TestProcessReproWriteFails(t *testing.T) {
	requireHelper(t)
	for _, c := range []struct {
		scenario string
		target   contract.ReproTarget
	}{
		{"js-passed", contract.ReproTargetJsSdk1766},
		{"wb-passed", contract.ReproTargetWalletBackend729},
	} {
		t.Run(string(c.target)+" a directory in the report's place", func(t *testing.T) {
			h := newReproHelper(t, c.scenario, "")
			blocked := catalogue.ReproReportPath(h.dir, c.target)
			if err := os.MkdirAll(filepath.Join(blocked, "inside"), 0o755); err != nil {
				t.Fatal(err)
			}
			for run := 1; run <= 2; run++ {
				code, stdout, stderr := h.run(c.target)
				if code != int(contract.EXIT_CODES.Failure) || len(stdout) != 0 {
					t.Fatalf("run %d: exit %d stdout %q stderr %s; want 1 and no report", run, code, stdout, stderr)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
				want := "ltk repro " + string(c.target) + ": write repro/" + string(c.target) + ".json: something other than a file is in its place"
				if line.Message != want {
					t.Errorf("run %d: message %q, want %q", run, line.Message, want)
				}
				if got := h.reproFiles(); !slices.Equal(got, []string{string(c.target) + ".json"}) {
					t.Errorf("run %d: repro/ holds %v; want the directory in the way only", run, got)
				}
				if es, err := os.ReadDir(blocked); err != nil || len(es) != 1 || es[0].Name() != "inside" {
					t.Errorf("run %d: the directory in the way was changed", run)
				}
				if left := h.f.containers(); len(left) != 0 {
					t.Errorf("run %d: containers left %v", run, left)
				}
			}
			if err := os.RemoveAll(blocked); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := h.run(c.target)
			if code != 0 || len(stderr) != 0 {
				t.Fatalf("the run after the directory was removed: exit %d stderr %s; want 0", code, stderr)
			}
			h.assertReport(c.target, stdout)
		})
		t.Run(string(c.target)+" a file in repro/'s place", func(t *testing.T) {
			h := newReproHelper(t, c.scenario, "")
			if err := os.WriteFile(filepath.Join(h.dir, catalogue.ReproFolder), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := h.run(c.target)
			if code != int(contract.EXIT_CODES.Failure) || len(stdout) != 0 {
				t.Fatalf("exit %d stdout %q stderr %s; want 1 and no report", code, stdout, stderr)
			}
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			if !strings.HasPrefix(line.Message, "ltk repro "+string(c.target)+": create the folder repro: ") {
				t.Errorf("message %q", line.Message)
			}
			if b, err := os.ReadFile(filepath.Join(h.dir, catalogue.ReproFolder)); err != nil || string(b) != "x" {
				t.Errorf("the file in repro/'s place was changed")
			}
		})
	}
}

// TestProcessReproInterrupt: an interrupted run exits 1 after its teardown, with no report. SIGINT to a running
// reproduction, once its runner has started its container, stops it: exit 1, nothing on stdout, one ErrorLine
// saying the command was interrupted, nothing under repro/, every container removed with the reaper run after the work.
// The signal is sent once the container is seen; nothing is timed.
func TestProcessReproInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: os.Process.Signal cannot send an interrupt there")
	}
	requireHelper(t)
	for _, c := range []struct {
		scenario string
		target   contract.ReproTarget
	}{
		{"js-hang", contract.ReproTargetJsSdk1766},
		{"wb-hang", contract.ReproTargetWalletBackend729},
	} {
		t.Run(string(c.target), func(t *testing.T) {
			h := newReproHelper(t, c.scenario, "")
			cmd := h.command(c.target)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = cmd.Wait(); close(done) }()
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			deadline := time.Now().Add(2 * time.Minute) // an upper bound for a loaded shared runner; the calls before it take milliseconds
			for !exists(h.record) {
				select {
				case <-done:
					t.Fatalf("the program ended before its runner started: stderr %s", stderr.Bytes())
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("the runner did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			<-done
			code := exitCodeOfRun(t, waitErr)
			if code != int(contract.EXIT_CODES.Failure) || stdout.Len() != 0 {
				t.Fatalf("exit %d stdout %q stderr %s; want 1 and no report", code, stdout.Bytes(), stderr.Bytes())
			}
			line := assertErrorLine(t, stderr.Bytes(), contract.ErrorCodeFailure)
			if !strings.HasPrefix(line.Message, "interrupted before the command finished: ltk repro "+string(c.target)+": ") {
				t.Errorf("message %q", line.Message)
			}
			if exists(filepath.Join(h.dir, catalogue.ReproFolder)) {
				t.Errorf("an interrupted run wrote under repro/")
			}
			h.assertTornDownRepro()
		})
	}
}

// TestProcessReproTeardownLeftover: when the run's container cannot be
// removed, the reproduction fails (exit 1) with an ErrorLine naming the container the teardown left, though its
// verdict passed; the report was already printed and written.
func TestProcessReproTeardownLeftover(t *testing.T) {
	requireHelper(t)
	for _, c := range []struct {
		scenario string
		target   contract.ReproTarget
	}{
		{"js-passed", contract.ReproTargetJsSdk1766},
		{"wb-passed", contract.ReproTargetWalletBackend729},
	} {
		t.Run(string(c.target), func(t *testing.T) {
			h := newReproHelper(t, c.scenario, "rm-fails")
			code, stdout, stderr := h.run(c.target)
			if code != int(contract.EXIT_CODES.Failure) {
				t.Fatalf("exit %d stderr %s; want 1", code, stderr)
			}
			h.assertReport(c.target, stdout)
			line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			name := "ltk-" + h.runID() + "-repro"
			if !strings.HasPrefix(line.Message, "ltk repro "+string(c.target)+": ") || !strings.Contains(line.Message, " left container "+name) {
				t.Errorf("message %q; want it to name the container left, %s", line.Message, name)
			}
		})
	}
}

// TestCommitRunDeadline: the deadline the guard gives the runner is
// COMMIT_RUN_DEADLINE_MINUTES, 15 minutes, with entry 1's recipe and the working folder of the run.
func TestCommitRunDeadline(t *testing.T) {
	if commitRunDeadline != 15*time.Minute || contract.COMMIT_RUN_DEADLINE_MINUTES != 15 {
		t.Fatalf("deadline %s", commitRunDeadline)
	}
	opt, err := walletBackendOptions("0a1b2c3d")
	if err != nil {
		t.Fatal(err)
	}
	if opt.Deadline != 15*time.Minute || opt.Entry1 == nil || opt.Entry1.ID() != model.SeedSignedPayloadSigner ||
		opt.WorkDir != filepath.Join(os.TempDir(), "ltk-0a1b2c3d", "wallet-backend-729") {
		t.Errorf("options %+v", opt)
	}
}

// TestGuardWalletBackend: in process, the guard's reading of a
// report: passed only when the runner says so, the pre-fix commit did not ingest with exactly 5 failures and 1 restart,
// the fix commit ingested and the four evidence files exist; otherwise passed false with the reasons and the logs'
// paths.
func TestGuardWalletBackend(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name   string
		mutate func(*contract.WalletBackend729Report)
		want   string // "" when passed
	}{
		{"passed", func(*contract.WalletBackend729Report) {}, ""},
		{"no restart", func(r *contract.WalletBackend729Report) { r.Commits[0].Restarts = 0 }, "showed 5 of 5 failures and 0 of 1 restarts"},
		{"six failures", func(r *contract.WalletBackend729Report) { r.Commits[0].Retries = 6 }, "showed 6 of 5 failures"},
		{"two restarts", func(r *contract.WalletBackend729Report) { r.Commits[0].Restarts = 2 }, "and 2 of 1 restarts"},
		{"pre-fix ingested", func(r *contract.WalletBackend729Report) { r.Commits[0].Ingested = true }, "commit 2a0a84ea (before_fix) ingested ledger 14: the failure did not show"},
		{"fix not ingested", func(r *contract.WalletBackend729Report) { r.Commits[1].Ingested = false }, "commit 2c873353 (fix) did not ingest ledger 14"},
		{"runner says not passed", func(r *contract.WalletBackend729Report) { r.Passed = false }, "the logs are saved at"},
		{"log missing", func(r *contract.WalletBackend729Report) { r.Commits[0].LogPath = filepath.Join(dir, "absent.log") }, "the log of commit 2a0a84ea is not saved"},
		{"log a folder", func(r *contract.WalletBackend729Report) { r.Commits[1].LogPath = dir }, "is not a file"},
		{"no path", func(r *contract.WalletBackend729Report) { r.Commits[1].StoreStatePath = "" }, "the report names no path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := walletBackendReport(dir)
			if err := saveEvidence(rep); err != nil {
				t.Fatal(err)
			}
			c.mutate(&rep)
			reasons := guardWalletBackend(&rep, nil)
			if c.want == "" {
				if !rep.Passed || reasons != nil {
					t.Errorf("passed %v reasons %v", rep.Passed, reasons)
				}
				return
			}
			if msg := joinReasons(reasons); rep.Passed || !strings.Contains(msg, c.want) {
				t.Errorf("passed %v message %q; want %q", rep.Passed, msg, c.want)
			}
		})
	}
	rep := walletBackendReport(dir)
	rep.Passed, rep.Commits[0].Retries = false, 2
	got := guardWalletBackend(&rep, []string{"runner's reason", "runner's reason"})
	if len(got) != 2 || got[0] != "runner's reason" || !strings.HasPrefix(got[1], "the logs are saved at ") {
		t.Errorf("reasons %q: the runner's verdict, once, then the logs", got)
	}
	rep = walletBackendReport(dir)
	rep.Commits[0].Retries, rep.Commits[1].LogPath = 2, filepath.Join(dir, "absent.log")
	got = guardWalletBackend(&rep, []string{"runner's reason"})
	if rep.Passed || len(got) != 4 || got[0] != "runner's reason" || !strings.Contains(got[1], "showed 2 of 5 failures") ||
		!strings.Contains(got[2], "the log of commit 2c873353 is not saved") {
		t.Errorf("reasons %q: a report the runner passed gives its reasons, then the guard's", got)
	}
}

// TestJsSdkVerdict: in process, the report's rule for passed: 17.2.0
// with a step not passed, 17.2.1 with its three steps passed and roundTripIdentical true; the runner's reasons first.
func TestJsSdkVerdict(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*contract.JsSdk1766Report)
		want   string
	}{
		{"passed", func(*contract.JsSdk1766Report) {}, ""},
		{"17.2.0 built it", func(r *contract.JsSdk1766Report) { r.Versions[0].Steps[0].Passed = true }, "17.2.0 (before_fix) did not reject"},
		{"17.2.1 stopped", func(r *contract.JsSdk1766Report) { r.Versions[1].Steps = r.Versions[1].Steps[:1] }, "17.2.1 (fix) did not pass its three steps"},
		{"17.2.1 decode failed", func(r *contract.JsSdk1766Report) { r.Versions[1].Steps[1].Passed = false }, "17.2.1 (fix) did not pass its three steps"},
		{"bytes differ", func(r *contract.JsSdk1766Report) { r.Versions[1].RoundTripIdentical = false }, "17.2.1 (fix) re-encoded bytes differ from the built bytes"},
		{"runner says not passed", func(r *contract.JsSdk1766Report) { r.Passed = false }, "the report's passed is false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := jsSdkReport()
			c.mutate(&rep)
			reasons := jsSdkVerdict(&rep, nil)
			if c.want == "" {
				if !rep.Passed || reasons != nil {
					t.Errorf("passed %v reasons %v", rep.Passed, reasons)
				}
				return
			}
			if msg := joinReasons(reasons); rep.Passed || msg != c.want && !strings.HasPrefix(msg, c.want) {
				t.Errorf("passed %v message %q; want %q", rep.Passed, msg, c.want)
			}
		})
	}
	rep := jsSdkReport()
	rep.Versions[1].RoundTripIdentical = false
	rep.Passed = false
	if got := jsSdkVerdict(&rep, []string{"17.2.1 differs", "17.2.1 differs"}); len(got) != 1 || got[0] != "17.2.1 differs" {
		t.Errorf("reasons %q: the runner's, once", got)
	}
	rep = jsSdkReport()
	rep.Versions[1].RoundTripIdentical = false
	if got := jsSdkVerdict(&rep, []string{"runner"}); rep.Passed || len(got) != 2 || got[0] != "runner" {
		t.Errorf("reasons %q: a report marked passed against the rule gives the runner's reasons, then the rule's", got)
	}
}

// The script's lines for the fake docker's FAKE_DOCKER_SDK folder: what repro.mjs prints for each release.
const (
	sdkLineRejected  = `{"sdkVersion":"17.2.0","steps":[{"step":"build","passed":false,"error":"Assets are not in lexicographic order"}],"roundTripIdentical":false,"builtXdr":null,"reEncodedXdr":null}`
	sdkLineRoundTrip = `{"sdkVersion":"17.2.1","steps":[{"step":"build","passed":true,"error":null},{"step":"decode","passed":true,"error":null},{"step":"re_encode","passed":true,"error":null}],"roundTripIdentical":true,"builtXdr":"AAAABgAAAAM=","reEncodedXdr":"AAAABgAAAAM="}`
	sdkLineDiffers   = `{"sdkVersion":"17.2.1","steps":[{"step":"build","passed":true,"error":null},{"step":"decode","passed":true,"error":null},{"step":"re_encode","passed":true,"error":null}],"roundTripIdentical":false,"builtXdr":"AAAABgAAAAM=","reEncodedXdr":"AAAABgAAAAQ="}`
)

// TestProcessReproJsSdkScript: the built program, its real js-sdk runner and verdict, and the fake docker answering the
// two releases' containers with the script's lines:
//   - 17.2.0 rejects, 17.2.1 round-trips: exit 0, nothing on stderr, the report on stdout and in repro/ byte-identical
//     to the expected success report; each release ran in its own labelled Node container, given the files with
//     `docker cp`, and both were removed;
//   - 17.2.1's bytes differ: exit 1, the report with passed false, one ErrorLine carrying the runner's reasons;
//   - npm ci fails: exit 1, no report, nothing under repro/, every container removed.
func TestProcessReproJsSdkScript(t *testing.T) {
	cases := []struct {
		name, modes, fixLine string
		code                 int
	}{
		{"passed", "", sdkLineRoundTrip, 0},
		{"17.2.1 bytes differ", "", sdkLineDiffers, 1},
		{"npm ci fails", "npm-fails", sdkLineRoundTrip, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeEngine(t, c.modes, "")
			lines := t.TempDir()
			for role, line := range map[string]string{"sdk-17-2-0": sdkLineRejected, "sdk-17-2-1": c.fixLine} {
				if err := os.WriteFile(filepath.Join(lines, role+".json"), []byte(line+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			code, stdout, stderr := runLTKIn(t, dir, append(append([]string{}, f.env...), "FAKE_DOCKER_SDK="+lines), "repro", "js-sdk-1766")
			if code != c.code {
				t.Fatalf("exit %d stderr %s; want %d\n%s", code, stderr, c.code, stdout)
			}
			if left := f.containers(); len(left) != 0 {
				t.Errorf("containers left: %v", left)
			}
			switch c.name {
			case "passed":
				if len(stderr) != 0 {
					t.Errorf("stderr %s", stderr)
				}
				want, err := catalogue.EncodeReproReport(jsSdkReport())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(stdout, want) {
					t.Errorf("stdout\n%s\nwant the expected success report\n%s", stdout, want)
				}
				file, _ := os.ReadFile(catalogue.ReproReportPath(dir, contract.ReproTargetJsSdk1766))
				if !bytes.Equal(file, stdout) {
					t.Errorf("repro/js-sdk-1766.json differs from stdout")
				}
				var runs, copies []string
				for _, call := range f.calls() {
					switch call[0] {
					case "run":
						runs = append(runs, call[slices.Index(call, "--name")+1])
						if !slices.Contains(call, orchestration.NodeImage) || !slices.Contains(call, "ltk.managed=true") {
							t.Errorf("run %v: want the pinned Node image, labelled", call)
						}
					case "cp":
						copies = append(copies, call[len(call)-1])
					}
				}
				if len(runs) != 2 || !strings.HasSuffix(runs[0], "-sdk-17-2-0") || !strings.HasSuffix(runs[1], "-sdk-17-2-1") || len(copies) != 2 {
					t.Errorf("containers %v, copies %v: want one per release, 17.2.0 first, each given the files", runs, copies)
				}
			case "17.2.1 bytes differ":
				validateOutput(t, "js-sdk-1766-report.schema.json", stdout)
				if !bytes.HasSuffix(stdout, []byte("  \"passed\": false\n}\n")) {
					t.Errorf("%s", stdout)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
				if !strings.HasPrefix(line.Message, "ltk repro js-sdk-1766: not reproduced: ") || !strings.Contains(line.Message, "17.2.1") {
					t.Errorf("message %q", line.Message)
				}
			default:
				if len(stdout) != 0 || exists(filepath.Join(dir, catalogue.ReproFolder)) {
					t.Errorf("a report was written: %q", stdout)
				}
				line := assertErrorLine(t, stderr, contract.ErrorCodeFailure)
				if !strings.Contains(line.Message, "npm ci --ignore-scripts failed") {
					t.Errorf("message %q", line.Message)
				}
			}
		})
	}
}
