package orchestrator

// The router's verification: the router, the exit-code mapper and the error line at the process boundary, through
// the built program. Unit tier: no Docker.
//
// Which of ltk's six commands the program registers is read from the program's own `ltk --help`, so this verification
// holds whatever is registered: a registered command's synopsis must be the documented one, and every command not
// listed must be a usage error.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// invocationLine is the documented form of an invocation, the first line of `ltk --help`.
const invocationLine = "ltk <command> [<subcommand>] [flags] [arguments]"

// contractCommand is one of ltk's six commands, with its synopsis block and an invocation that would
// run it once registered.
type contractCommand struct {
	name     string
	synopsis []string
	example  []string
}

// tableCommands are ltk's six commands in their documented order, each with its synopsis block exactly as `--help`
// must print it.
var tableCommands = []contractCommand{
	{"generate", []string{"ltk generate --all --out <folder>", "ltk generate --shape <entry id> --out <folder>"}, []string{"generate", "--all", "--out", "out"}},
	{"compare", []string{"ltk compare <folder A> <folder B>"}, []string{"compare", "a", "b"}},
	{"repro wallet-backend-729", []string{"ltk repro wallet-backend-729"}, []string{"repro", "wallet-backend-729"}},
	{"list", []string{"ltk list <folder> [--json]"}, []string{"list", "corpus/"}},
	{"check", []string{"ltk check <folder> [--also-go]"}, []string{"check", "corpus/"}},
	{"repro js-sdk-1766", []string{"ltk repro js-sdk-1766"}, []string{"repro", "js-sdk-1766"}},
}

// helpLines runs `ltk --help` and returns its standard output's lines.
func helpLines(t *testing.T) []string {
	t.Helper()
	code, stdout, stderr := runLTK(t, nil, "--help")
	if code != int(contract.EXIT_CODES.Success) || len(stderr) != 0 {
		t.Fatalf("ltk --help: exit %d stderr %q; want 0 and nothing", code, stderr)
	}
	if !bytes.HasSuffix(stdout, []byte("\n")) {
		t.Fatalf("ltk --help: stdout does not end in \\n: %q", stdout)
	}
	return strings.Split(strings.TrimSuffix(string(stdout), "\n"), "\n")
}

// registeredCommands reads which commands `ltk --help` lists: after the invocation line, the synopsis blocks of the
// registered commands in the documented order, each block whole and exact.
func registeredCommands(t *testing.T) map[string]bool {
	t.Helper()
	lines := helpLines(t)
	if lines[0] != invocationLine {
		t.Fatalf("ltk --help: first line %q, want the invocation line %q", lines[0], invocationLine)
	}
	registered := map[string]bool{}
	rest := lines[1:]
	for _, c := range tableCommands {
		if len(rest) >= len(c.synopsis) && slices.Equal(rest[:len(c.synopsis)], c.synopsis) {
			registered[c.name] = true
			rest = rest[len(c.synopsis):]
		}
	}
	if len(rest) != 0 {
		t.Fatalf("ltk --help: lines that are not a contract synopsis in the table's order: %q", rest)
	}
	return registered
}

// requireUsageError holds a run to the usage-error row: exit 2, nothing on standard output, one ErrorLine with code
// usage_error.
func requireUsageError(t *testing.T, args []string) {
	t.Helper()
	code, stdout, stderr := runLTKIn(t, t.TempDir(), nil, args...)
	if code != int(contract.EXIT_CODES.UsageError) {
		t.Fatalf("ltk %q: exit %d, want %d; stderr %q", args, code, contract.EXIT_CODES.UsageError, stderr)
	}
	if len(stdout) != 0 {
		t.Errorf("ltk %q: stdout %q, want nothing (exits 2 and 3 write nothing on standard output)", args, stdout)
	}
	if line := requireErrorLine(t, code, stderr); line.Code != contract.ErrorCodeUsageError {
		t.Errorf("ltk %q: code %q, want %q", args, line.Code, contract.ErrorCodeUsageError)
	}
}

// TestPhase1RouterUsageErrors: no command, an unknown command, and the repro family's malformed invocations are
// usage errors.
func TestPhase1RouterUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no command", nil},
		{"unknown command", []string{"frobnicate"}},
		{"unknown command with a flag", []string{"frobnicate", "--json"}},
		{"an unknown flag as the command", []string{"--version"}},
		{"repro with no target", []string{"repro"}},
		{"repro with an unknown target", []string{"repro", "wallet-backend-730"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { requireUsageError(t, c.args) })
	}
}

// TestPhase1PendingCommands: every one of the six commands that `ltk --help` does not list is pending and is a usage
// error, with its own arguments and with --help. The program registers all six, so this test has nothing left to hold
// unless a command is dropped from the registry.
func TestPhase1PendingCommands(t *testing.T) {
	registered := registeredCommands(t)
	var pending []string
	for _, c := range tableCommands {
		if registered[c.name] {
			continue
		}
		pending = append(pending, c.name)
		t.Run(c.name, func(t *testing.T) {
			requireUsageError(t, c.example)
			requireUsageError(t, append(strings.Fields(c.name), "--help"))
		})
	}
	t.Logf("registered: %d of %d; pending: %q", len(registered), len(tableCommands), pending)
}

// TestPhase1Help: `ltk --help` and its spellings (-h, -help, --h) exit 0 with the invocation line first, then only
// documented synopses, and nothing on standard error.
func TestPhase1Help(t *testing.T) {
	want := helpLines(t)
	if want[0] != invocationLine {
		t.Fatalf("ltk --help: first line %q, want %q", want[0], invocationLine)
	}
	registeredCommands(t) // every further line is a contract synopsis, in the table's order
	for _, arg := range []string{"-h", "-help", "--h"} {
		code, stdout, stderr := runLTK(t, nil, arg)
		if code != int(contract.EXIT_CODES.Success) || len(stderr) != 0 {
			t.Errorf("ltk %s: exit %d stderr %q; want 0 and nothing", arg, code, stderr)
		}
		if got := strings.Split(strings.TrimSuffix(string(stdout), "\n"), "\n"); !slices.Equal(got, want) {
			t.Errorf("ltk %s: stdout %q, want the same as ltk --help %q", arg, got, want)
		}
	}
}

// TestPhase1ExitCodes: one row per exit code the router alone can produce, at the process level, each the code of
// contract.EXIT_CODES its outcome names: 0 with nothing on standard error; 2 with one usage_error line and nothing on
// standard output. The error line's code is held to the exit code by requireErrorLine.
func TestPhase1ExitCodes(t *testing.T) {
	if contract.EXIT_CODES.Success != 0 || contract.EXIT_CODES.UsageError != 2 {
		t.Fatalf("EXIT_CODES: success %d usageError %d; the notation says 0 and 2", contract.EXIT_CODES.Success, contract.EXIT_CODES.UsageError)
	}
	rows := []struct {
		name string
		args []string
		want contract.ExitCode
	}{
		{"success: ltk --help", []string{"--help"}, contract.EXIT_CODES.Success},
		{"usageError: no command", nil, contract.EXIT_CODES.UsageError},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			code, stdout, stderr := runLTK(t, nil, r.args...)
			if code != int(r.want) {
				t.Fatalf("exit %d, want %d", code, r.want)
			}
			if code == 0 {
				if len(stderr) != 0 || len(stdout) == 0 {
					t.Errorf("exit 0: stdout %q stderr %q; want data and nothing", stdout, stderr)
				}
				return
			}
			if len(stdout) != 0 {
				t.Errorf("exit %d: stdout %q, want nothing", code, stdout)
			}
			requireErrorLine(t, code, stderr)
		})
	}
}

// TestPhase1ExplicitEnvironment: with an explicit, near-empty environment (only SystemRoot and TEMP kept, which a
// Windows process needs) the router behaves the same; nothing the router does reads the environment.
func TestPhase1ExplicitEnvironment(t *testing.T) {
	code, stdout, stderr := runLTK(t, []string{}, "--help")
	if code != 0 || len(stderr) != 0 || !bytes.HasPrefix(stdout, []byte(invocationLine+"\n")) {
		t.Errorf("ltk --help with an explicit environment: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runLTK(t, []string{}, "frobnicate")
	if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
		t.Fatalf("ltk frobnicate with an explicit environment: exit %d stdout %q", code, stdout)
	}
	requireErrorLine(t, code, stderr)
}

// TestPhase1ClosedPipe: standard output is a pipe whose reader is closed before the program starts; the program exits
// 0 with nothing on standard error, as it does whenever a reader closes the pipe early (SIGPIPE is ignored).
func TestPhase1ClosedPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	errPath := filepath.Join(t.TempDir(), "stderr")
	errFile, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := ltkCommand(t, "", nil, "--help")
	cmd.Stdout, cmd.Stderr = w, errFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	w.Close()
	runErr := cmd.Wait()
	errFile.Close()
	code := exitCodeOfRun(t, runErr)
	stderr, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || len(stderr) != 0 {
		t.Errorf("ltk --help into a closed pipe: exit %d stderr %q; want 0 and nothing", code, stderr)
	}
}

// TestErrorLineSchemaIsNotVacuous: the schema requireErrorLine validates with accepts the three codes and rejects
// every shape the error line's definition excludes, so a passing row above is the schema's verdict.
func TestErrorLineSchemaIsNotVacuous(t *testing.T) {
	sch := loadErrorLineSchema(t)
	cases := []struct {
		doc   string
		valid bool
	}{
		{`{"code":"failure","message":"m"}`, true},
		{`{"code":"usage_error","message":"m"}`, true},
		{`{"code":"environment_missing","message":"m"}`, true},
		{`{"code":"usage_error","message":""}`, false},
		{`{"code":"error","message":"m"}`, false},
		{`{"code":"usage_error"}`, false},
		{`{"message":"m"}`, false},
		{`{"code":"usage_error","message":"m","detail":"x"}`, false},
		{`["usage_error","m"]`, false},
	}
	for _, c := range cases {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(c.doc))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); (err == nil) != c.valid {
			t.Errorf("%s: valid %v, want %v (%v)", c.doc, err == nil, c.valid, err)
		}
	}
}
