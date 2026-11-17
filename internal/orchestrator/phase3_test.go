package orchestrator

// The verification of the commands that start containers, unit tier: `ltk generate --all`, `ltk generate --shape`,
// `ltk repro js-sdk-1766`, `ltk repro wallet-backend-729` and `ltk check --also-go`, through the built program, from
// outside, with no Docker:
//
//   - all six of ltk's commands are registered, none is pending, and the generate and repro commands' --help prints
//     exactly their synopsis;
//   - each of these command forms' usage errors exit 2 and its missing environment (no docker on PATH) exits 3, with
//     nothing on standard output, one ErrorLine valid against its schema, and nothing written;
//   - the report schemas the integration tier validates against accept the contract's own examples and reject what
//     the data model excludes, so a passing integration row is the schema's verdict;
//   - the deadline path of `ltk repro wallet-backend-729` (a commit's run that reaches the 15-minute safety deadline is
//     reported, never an error), which no run of the real program can reach (a test injects the deadline, never waits
//     for it), is shown by running the committed tests that inject the deadline and reading their results.
//
// The Docker rows are in phase3_integration_test.go (tag integration), run natively.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// The three entry ids of the seed corpus, in seed order.
const (
	entry1ID = "cap-0040-signed-payload-signer"
	entry2ID = "cap-0038-pool-share-trustline"
	entry3ID = "cap-0027-muxed-account"
)

// seedIDs are the three entries in seed (catalogue) order.
var seedIDs = []string{entry1ID, entry2ID, entry3ID}

// phase3Commands are the generate and repro commands, the ones that start containers.
var phase3Commands = []string{"generate", "repro wallet-backend-729", "repro js-sdk-1766"}

// TestPhase3AllCommandsRegistered: `ltk --help` lists all six of ltk's commands, in their documented order, each with
// its documented synopsis exactly; nothing is pending; the generate and repro commands answer --help and -h with their
// synopsis, exit 0 and nothing on standard error.
func TestPhase3AllCommandsRegistered(t *testing.T) {
	registered := registeredCommands(t)
	var pending []string
	for _, c := range tableCommands {
		if !registered[c.name] {
			pending = append(pending, c.name)
		}
	}
	if len(pending) != 0 {
		t.Fatalf("pending commands %q; the program registers all six of ltk's commands", pending)
	}
	for _, c := range tableCommands {
		if !contains(phase3Commands, c.name) {
			continue
		}
		for _, h := range []string{"--help", "-h"} {
			args := append(strings.Fields(c.name), h)
			stdout, _ := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.Success, args...)
			if want := strings.Join(c.synopsis, "\n") + "\n"; string(stdout) != want {
				t.Errorf("ltk %q: stdout %q, want the synopsis %q", args, stdout, want)
			}
		}
	}
	t.Logf("registered: %d of %d; pending: none", len(registered), len(tableCommands))
}

// contains reports whether s holds v.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// requireNothingWritten fails the test unless dir holds nothing (or, when absent is true, does not exist).
func requireNothingWritten(t *testing.T, dir string, absent bool) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	switch {
	case absent && errors.Is(err, fs.ErrNotExist):
		return
	case absent && err == nil:
		t.Errorf("%s was created; the contract says nothing is written", dir)
	case err != nil:
		t.Fatal(err)
	case len(entries) != 0:
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%s holds %q; the contract says nothing is written", dir, names)
	}
}

// TestPhase3GenerateUsageErrors: with no docker on PATH, so that the environment check would exit 3, every invalid
// invocation exits 2 first: nothing on standard output, one usage_error line, and the
// --out folder is not created ("Nothing is started and nothing is written"). An unknown --shape id is named in the
// message and no nearby id is substituted.
func TestPhase3GenerateUsageErrors(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "corpus-out")
	file := filepath.Join(base, "a-file")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, inMessage string
		args            []string
	}{
		{"neither --all nor --shape", "", []string{"generate", "--out", out}},
		{"both --all and --shape", "", []string{"generate", "--all", "--shape", entry1ID, "--out", out}},
		{"--all=false and no --shape", "", []string{"generate", "--all=false", "--out", out}},
		{"--all without --out", "--out", []string{"generate", "--all"}},
		{"--shape without --out", "--out", []string{"generate", "--shape", entry1ID}},
		{"an empty --out", "--out", []string{"generate", "--all", "--out", ""}},
		{"an unknown --shape id", "not-a-real-entry-id", []string{"generate", "--shape", "not-a-real-entry-id", "--out", out}},
		{"an id one letter off", "cap-0040-signed-payload-signers", []string{"generate", "--shape", entry1ID + "s", "--out", out}},
		{"an empty --shape id", "", []string{"generate", "--shape", "", "--out", out}},
		{"--shape with no value", "", []string{"generate", "--out", out, "--shape"}},
		{"an --out that is a file", "", []string{"generate", "--all", "--out", file}},
		{"a positional argument", "", []string{"generate", "--all", "--out", out, "extra"}},
		{"an unknown flag", "", []string{"generate", "--all", "--out", out, "--protocol-version", "28"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.UsageError, c.args...)
			if line.Code != contract.ErrorCodeUsageError || !strings.Contains(line.Message, c.inMessage) {
				t.Errorf("error line %+v; want usage_error containing %q", line, c.inMessage)
			}
			requireNothingWritten(t, out, true)
		})
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "x\n" {
		t.Errorf("the file given as --out was changed: %q %v", b, err)
	}
}

// TestPhase3GenerateEnvironmentMissing: with no docker on PATH, `ltk generate --all` and `--shape` exit 3 before any
// shape: nothing on standard output, one environment_missing line naming Docker with Linux containers, and nothing
// written under the
// --out folder, whether it is absent (not created) or exists empty (left empty).
func TestPhase3GenerateEnvironmentMissing(t *testing.T) {
	for _, form := range [][]string{{"--all"}, {"--shape", entry2ID}} {
		for _, exists := range []bool{false, true} {
			name := strings.Join(form, " ")
			if exists {
				name += ", existing empty --out"
			}
			t.Run(name, func(t *testing.T) {
				out := filepath.Join(t.TempDir(), "corpus-out")
				if exists {
					if err := os.Mkdir(out, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				args := append(append([]string{"generate"}, form...), "--out", out)
				_, line := requireRun(t, noDockerEnv(t), contract.EXIT_CODES.EnvironmentMissing, args...)
				if line.Code != contract.ErrorCodeEnvironmentMissing || !strings.Contains(line.Message, "Docker with Linux containers") {
					t.Errorf("error line %+v; want environment_missing naming Docker with Linux containers", line)
				}
				requireNothingWritten(t, out, !exists)
			})
		}
	}
}

// reproEnv is an environment with no docker (PATH an empty folder) whose system temporary directory is tmp, so a test
// can see whether the wallet-backend working folder (<the system's temporary directory>/ltk-<runID>/…) was created.
func reproEnv(t *testing.T, tmp string) []string {
	t.Helper()
	return append(noDockerEnv(t), "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
}

// TestPhase3ReproUsageErrors: for `ltk repro wallet-backend-729` and `ltk repro js-sdk-1766`, an extra argument, an
// unknown flag, a target outside ReproTarget and a missing target exit 2 with nothing on standard output and one
// usage_error line, before the environment check (no docker on PATH, never 3); nothing is written: no repro/ under the
// working folder, nothing in the system's temporary directory.
func TestPhase3ReproUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"js-sdk-1766 with an extra argument", []string{"repro", "js-sdk-1766", "extra"}},
		{"js-sdk-1766 with an unknown flag", []string{"repro", "js-sdk-1766", "--json"}},
		{"js-sdk-1766 with a version flag", []string{"repro", "js-sdk-1766", "--version=17.2.1"}},
		{"wallet-backend-729 with an extra argument", []string{"repro", "wallet-backend-729", "2a0a84ea"}},
		{"wallet-backend-729 with a deadline flag", []string{"repro", "wallet-backend-729", "--deadline", "1m"}},
		{"both targets", []string{"repro", "js-sdk-1766", "wallet-backend-729"}},
		{"a target outside ReproTarget", []string{"repro", "js-sdk-17"}},
		{"a target in another case", []string{"repro", "JS-SDK-1766"}},
		{"no target", []string{"repro"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cwd, tmp := t.TempDir(), t.TempDir()
			code, stdout, stderr := runLTKIn(t, cwd, reproEnv(t, tmp), c.args...)
			if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
				t.Fatalf("ltk %q: exit %d stdout %q stderr %s; want 2 and nothing", c.args, code, tail(stdout), stderr)
			}
			if line := requireErrorLine(t, code, stderr); line.Code != contract.ErrorCodeUsageError {
				t.Errorf("code %q", line.Code)
			}
			requireNothingWritten(t, cwd, false)
			requireNothingWritten(t, tmp, false)
		})
	}
}

// TestPhase3ReproEnvironmentMissing: with no docker on PATH each target exits 3 before any work, and no report is
// written: nothing on standard output, one environment_missing line naming Docker with Linux containers, no repro/
// under the working folder and no working folder in the system's temporary directory.
func TestPhase3ReproEnvironmentMissing(t *testing.T) {
	for _, target := range []string{"wallet-backend-729", "js-sdk-1766"} {
		t.Run(target, func(t *testing.T) {
			cwd, tmp := t.TempDir(), t.TempDir()
			code, stdout, stderr := runLTKIn(t, cwd, reproEnv(t, tmp), "repro", target)
			if code != int(contract.EXIT_CODES.EnvironmentMissing) || len(stdout) != 0 {
				t.Fatalf("exit %d stdout %q stderr %s; want 3 and nothing", code, tail(stdout), stderr)
			}
			line := requireErrorLine(t, code, stderr)
			if line.Code != contract.ErrorCodeEnvironmentMissing || !strings.Contains(line.Message, "Docker with Linux containers") {
				t.Errorf("error line %+v; want environment_missing naming Docker with Linux containers", line)
			}
			requireNothingWritten(t, cwd, false)
			requireNothingWritten(t, tmp, false)
		})
	}
}

// apiContractsPath is the command contract of record, when present beside the module, from this package's folder.
const apiContractsPath = "../../specs/api-contracts.md"

// contractExamples returns the ```json blocks of the contract's section whose heading line is heading, up to the next
// heading of the same level.
func contractExamples(t *testing.T, heading string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(apiContractsPath)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s is absent (the repository does not carry it): no contract examples to read", apiContractsPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	start := strings.Index(doc, "\n"+heading+"\n")
	if start < 0 {
		t.Fatalf("%s has no heading %q", apiContractsPath, heading)
	}
	section := doc[start+1+len(heading):]
	if end := strings.Index(section, "\n### "); end >= 0 {
		section = section[:end]
	}
	var blocks [][]byte
	for _, part := range strings.Split(section, "\n```json\n")[1:] {
		end := strings.Index(part, "\n```")
		if end < 0 {
			t.Fatalf("%s: a json block of %q is not closed", apiContractsPath, heading)
		}
		blocks = append(blocks, []byte(part[:end+1]))
	}
	return blocks
}

// mutated returns doc decoded, changed by change, and encoded again.
func mutated(t *testing.T, doc []byte, change func(m map[string]any)) []byte {
	t.Helper()
	m, ok := decodeJSON(t, doc).(map[string]any)
	if !ok {
		t.Fatalf("not an object: %s", doc)
	}
	change(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// item is the i-th object of the array m[key].
func item(m map[string]any, key string, i int) map[string]any {
	return m[key].([]any)[i].(map[string]any)
}

// TestPhase3ReportSchemasAreNotVacuous: the schemas the integration tier validates the generate report and the two
// repro reports against accept every example the contract gives for them, and reject each shape the data model's field
// tables exclude, so that a report the integration tier passes is the schema's verdict.
func TestPhase3ReportSchemasAreNotVacuous(t *testing.T) {
	sections := []struct {
		heading, schema string
		examples        int
		broken          map[string]func(map[string]any)
	}{
		{"### `ltk generate`", "generate-report.schema.json", 3, map[string]func(map[string]any){
			"no shape":                    func(m map[string]any) { m["shapes"] = []any{} },
			"an empty out":                func(m map[string]any) { m["out"] = "" },
			"check omitted":               func(m map[string]any) { delete(m, "check") },
			"an extra field":              func(m map[string]any) { m["runId"] = "3f9a1c2e" },
			"fallback at 29":              func(m map[string]any) { item(m, "shapes", 0)["protocolFallback"] = true },
			"not generated with no error": func(m map[string]any) { item(m, "shapes", 0)["generated"] = false },
			"generated with an error":     func(m map[string]any) { item(m, "shapes", 0)["error"] = "x" },
			"a check report missing its entries": func(m map[string]any) {
				delete(m["check"].(map[string]any), "entries")
			},
		}},
		{"### `ltk repro wallet-backend-729`", "wallet-backend-729-report.schema.json", 1, map[string]func(map[string]any){
			"protocol 30":                  func(m map[string]any) { m["protocolVersion"] = 30 },
			"one commit":                   func(m map[string]any) { m["commits"] = m["commits"].([]any)[:1] },
			"the commits in reverse order": func(m map[string]any) { c := m["commits"].([]any); c[0], c[1] = c[1], c[0] },
			"passed with four retries":     func(m map[string]any) { item(m, "commits", 0)["retries"] = 4 },
			"passed with no restart":       func(m map[string]any) { item(m, "commits", 0)["restarts"] = 0 },
			"passed with the fix not ingested": func(m map[string]any) {
				c := item(m, "commits", 1)
				c["ingested"], c["storedRow"], c["error"] = false, nil, "did not ingest"
			},
			"ingested with no stored row":    func(m map[string]any) { item(m, "commits", 1)["storedRow"] = nil },
			"not ingested with no error":     func(m map[string]any) { item(m, "commits", 0)["error"] = nil },
			"an empty log path":              func(m map[string]any) { item(m, "commits", 0)["logPath"] = "" },
			"ledger 0":                       func(m map[string]any) { item(m, "commits", 0)["ledger"] = 0 },
			"an extra field on a commit":     func(m map[string]any) { item(m, "commits", 1)["deadlineReached"] = false },
			"the target of the other report": func(m map[string]any) { m["target"] = "js-sdk-1766" },
		}},
		{"### `ltk repro js-sdk-1766`", "js-sdk-1766-report.schema.json", 2, map[string]func(map[string]any){
			"one version":                   func(m map[string]any) { m["versions"] = m["versions"].([]any)[:1] },
			"the versions in reverse order": func(m map[string]any) { v := m["versions"].([]any); v[0], v[1] = v[1], v[0] },
			"a failed step with no error": func(m map[string]any) {
				item(item(m, "versions", 0), "steps", 0)["error"] = nil
			},
			"a passed step with an error": func(m map[string]any) {
				item(item(m, "versions", 1), "steps", 0)["error"] = "x"
			},
			"identical with two steps": func(m map[string]any) {
				v := item(m, "versions", 1)
				v["steps"] = v["steps"].([]any)[:2]
			},
			"steps out of order": func(m map[string]any) {
				s := item(m, "versions", 1)["steps"].([]any)
				s[0], s[1] = s[1], s[0]
			},
			"passed with 17.2.0 rejecting nothing": func(m map[string]any) {
				item(item(m, "versions", 0), "steps", 0)["passed"] = true
				item(item(m, "versions", 0), "steps", 0)["error"] = nil
			},
		}},
	}
	for _, s := range sections {
		t.Run(s.schema, func(t *testing.T) {
			sch := outputSchema(t, s.schema)
			examples := contractExamples(t, s.heading)
			if len(examples) != s.examples {
				t.Fatalf("%s holds %d json examples under %s, want %d", apiContractsPath, len(examples), s.heading, s.examples)
			}
			for i, ex := range examples {
				if err := validationError(t, sch, ex); err != nil {
					t.Errorf("the contract's example %d under %s does not validate against %s: %v", i+1, s.heading, s.schema, err)
				}
			}
			for name, change := range s.broken {
				if err := validationError(t, sch, mutated(t, examples[0], change)); err == nil {
					t.Errorf("%s accepts the contract's first example with %s; the data model excludes it", s.schema, name)
				}
			}
		})
	}
}

// deadlineEvidence are the committed tests that prove the deadline path of `ltk repro wallet-backend-729`: the runner,
// with the deadline injected on a fake clock, ends a pre-fix run that keeps retrying as not reproduced, without a
// restart, its log saved (and the fix commit the same way); the runner on the logs recorded by the feasibility run made
// before this tool counts five failures, one restart and one log file with the marker; the command gives the runner
// COMMIT_RUN_DEADLINE_MINUTES, its guard maps a short count to not reproduced, and through the built program a deadline
// report exits 1 with the report on standard output and under repro/ and one ErrorLine carrying the reasons and the
// saved logs' paths.
var deadlineEvidence = []struct{ pkg, test string }{
	{"./internal/repro/walletbackend", "TestPreFixRunReachingTheDeadlineIsNotReproduced"},
	{"./internal/repro/walletbackend", "TestFixCommitReachingTheDeadlineIsNotReproduced"},
	{"./internal/repro/walletbackend", "TestRunReproducesOnTheRecordedLogs"},
	{"./internal/repro/walletbackend", "TestFailuresCountsTheFiveAttemptsOfARecordedRun"},
	{"./internal/repro/walletbackend", "TestReasonsOfReports"},
	{"./cmd/ltk", "TestCommitRunDeadline"},
	{"./cmd/ltk", "TestGuardWalletBackend"},
	{"./cmd/ltk", "TestProcessReproWalletBackendNotReproduced/wb-deadline"},
	{"./cmd/ltk", "TestProcessReproWalletBackendNotReproduced/wb-short-but-passed"},
}

// testEvent is one line of `go test -json`.
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// reTopTest is a top-level test name of a deadlineEvidence row.
var reTopTest = regexp.MustCompile(`^[A-Za-z0-9_]+`)

// TestPhase3DeadlinePathEvidence: the safety deadline of COMMIT_RUN_DEADLINE_MINUTES (15) is a parameter of the runner
// and the built program never takes another (a test injects a deadline, never waits for one), so no run of the program
// from outside can reach it. This row reads the committed tests' own evidence instead: it runs each test of
// deadlineEvidence with the go tool, from the module root, and requires that each ran and passed (a skip is a failure
// here). It needs the go tool; without one it skips, saying so, and the committed tests themselves stand as the
// evidence.
func TestPhase3DeadlinePathEvidence(t *testing.T) {
	goBin := goTool()
	if goBin == "" {
		t.Skip("no go tool on PATH or under runtime.GOROOT(): the committed deadline tests cannot be run from here (they run in `go test ./...`)")
	}
	pkgs := map[string][]string{}
	var order []string
	for _, e := range deadlineEvidence {
		if _, ok := pkgs[e.pkg]; !ok {
			order = append(order, e.pkg)
		}
		top := reTopTest.FindString(e.test)
		if !contains(pkgs[e.pkg], top) {
			pkgs[e.pkg] = append(pkgs[e.pkg], top)
		}
	}
	results := map[string]string{} // "<pkg> <test>" → last action
	for _, pkg := range order {
		args := []string{"test", "-json", "-run", "^(" + strings.Join(pkgs[pkg], "|") + ")$", pkg}
		cmd := exec.Command(goBin, args...)
		cmd.Dir = moduleRoot
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		sc := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
		sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
		for sc.Scan() {
			var ev testEvent
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Test == "" {
				continue
			}
			switch ev.Action {
			case "pass", "fail", "skip":
				results[pkg+" "+ev.Test] = ev.Action
			}
		}
		if runErr != nil {
			t.Errorf("go %s: %v\n%s", strings.Join(args, " "), runErr, stderr.Bytes())
		}
	}
	for _, e := range deadlineEvidence {
		got := results[e.pkg+" "+e.test]
		if got != "pass" {
			t.Errorf("%s %s: %q; want it run and passed", e.pkg, e.test, got)
			continue
		}
		t.Logf("%s %s: pass", e.pkg, e.test)
	}
}

// TestPhase3ReaderMatchesChangeListsByKey: the reader that holds a compare report to the decoded files pairs the
// elements of the four change lists by (change kind, ledger key) as a multiset, as `ltk compare` must, and every other
// list by position. Two generations of entry 2 emit its changes in different orders (as recorded runs of the generator
// showed), so a reader that read B at A's index would misjudge a correct report.
func TestPhase3ReaderMatchesChangeListsByKey(t *testing.T) {
	a := decodeJSON(t, []byte(`{"ops":[{"changes":[
		{"state":{"last_modified_ledger_seq":7,"data":{"account":{"account_id":"GA","balance":1}}}},
		{"updated":{"last_modified_ledger_seq":9,"data":{"account":{"account_id":"GA","balance":2}}}},
		{"created":{"last_modified_ledger_seq":9,"data":{"trustline":{"account_id":"GA","asset":"P","balance":0}}}},
		{"state":{"last_modified_ledger_seq":7,"data":{"account":{"account_id":"GB","balance":5}}}},
		{"removed":{"offer":{"seller_id":"GA","offer_id":3}}},
		{"odd":1}
	],"other":[10,20]}]}`))
	b := decodeJSON(t, []byte(`{"ops":[{"changes":[
		{"created":{"last_modified_ledger_seq":12,"data":{"trustline":{"account_id":"GA","asset":"P","balance":0}}}},
		{"odd":2},
		{"state":{"last_modified_ledger_seq":8,"data":{"account":{"account_id":"GB","balance":5}}}},
		{"state":{"last_modified_ledger_seq":8,"data":{"account":{"account_id":"GA","balance":1}}}},
		{"updated":{"last_modified_ledger_seq":12,"data":{"account":{"account_id":"GA","balance":2}}}}
	],"other":[20,10]}]}`))
	cases := []struct {
		p    string
		want any // nil: no partner
	}{
		{"/ops/0/changes/0/state/data/account/account_id", "GA"},
		{"/ops/0/changes/0/state/last_modified_ledger_seq", json.Number("8")},
		{"/ops/0/changes/1/updated/data/account/balance", json.Number("2")},
		{"/ops/0/changes/2/created/last_modified_ledger_seq", json.Number("12")},
		{"/ops/0/changes/3/state/data/account/account_id", "GB"},
		{"/ops/0/changes/4/removed/offer/offer_id", nil},
		{"/ops/0/changes/5/odd", json.Number("2")},
		{"/ops/0/other/0", json.Number("20")},
	}
	for _, c := range cases {
		got, ok := resolveMatched(a, b, c.p)
		if c.want == nil {
			if ok {
				t.Errorf("%s: read %v in B; the element is on one side only", c.p, got)
			}
			continue
		}
		if !ok || !sameJSON(got, c.want) {
			t.Errorf("%s: read %v (%v) in B, want %v", c.p, got, ok, c.want)
		}
	}
}
