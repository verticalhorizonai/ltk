package main

// The router and the exit at the process boundary: the built program, through the shared harness.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

func TestProcessUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no command", nil},
		{"unknown command", []string{"frobnicate"}},
		{"help is not a command", []string{"help"}},
		{"repro with no target", []string{"repro"}},
		{"repro with an unknown target", []string{"repro", "nope"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runLTK(t, nil, c.args...)
			if code != int(contract.EXIT_CODES.UsageError) {
				t.Errorf("exit %d, want %d; stderr %q", code, contract.EXIT_CODES.UsageError, stderr)
			}
			if len(stdout) != 0 {
				t.Errorf("stdout %q, want nothing", stdout)
			}
			assertErrorLine(t, stderr, contract.ErrorCodeUsageError)
		})
	}
}

// TestProcessPendingCommands: a known command that is not registered yet (in pending) is a usage error, exit 2.
func TestProcessPendingCommands(t *testing.T) {
	args := map[string][]string{
		"list":                     {"list", "corpus/"},
		"check":                    {"check", "corpus/"},
		"compare":                  {"compare", "a", "b"},
		"generate":                 {"generate", "--all", "--out", "x"},
		"repro wallet-backend-729": {"repro", "wallet-backend-729"},
		"repro js-sdk-1766":        {"repro", "js-sdk-1766"},
	}
	for name := range pending {
		t.Run(name, func(t *testing.T) {
			a, ok := args[name]
			if !ok {
				t.Fatalf("no invocation for the pending command %q", name)
			}
			code, stdout, stderr := runLTKIn(t, t.TempDir(), nil, a...)
			if code != int(contract.EXIT_CODES.UsageError) || len(stdout) != 0 {
				t.Errorf("exit %d stdout %q, want 2 and nothing", code, stdout)
			}
			assertErrorLine(t, stderr, contract.ErrorCodeUsageError)
		})
	}
}

func TestProcessHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		code, stdout, stderr := runLTK(t, nil, arg)
		if code != 0 || len(stdout) == 0 || len(stderr) != 0 {
			t.Errorf("ltk %s: exit %d stdout %q stderr %q; want 0, the synopses, nothing", arg, code, stdout, stderr)
		}
	}
}

// TestProcessClosedPipe: standard output is a pipe whose reader is closed before the
// program starts, so its first write fails; the program exits 0 with nothing on standard error. Without the SIGPIPE
// handling the program dies of SIGPIPE (exit -1 here) on Unix.
func TestProcessClosedPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	stderrFile := filepath.Join(t.TempDir(), "stderr")
	errOut, err := os.Create(stderrFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd := ltkCommand(t, "", nil, "--help")
	cmd.Stdout, cmd.Stderr = w, errOut
	runErr := cmd.Start()
	w.Close()
	if runErr == nil {
		runErr = cmd.Wait()
	} else {
		t.Fatalf("start: %v", runErr)
	}
	errOut.Close()
	code := exitCodeOfRun(t, runErr)
	stderr, err := os.ReadFile(stderrFile)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || len(stderr) != 0 {
		t.Errorf("ltk --help into a closed pipe: exit %d stderr %q; want 0 and nothing", code, stderr)
	}
}

// TestFakeDockerHarness: the fake docker is built when its source and a go tool exist; otherwise the tests that need
// it skip and say why.
func TestFakeDockerHarness(t *testing.T) {
	dir := requireFakeDocker(t)
	if _, err := os.Stat(filepath.Join(dir, exeName("docker"))); err != nil {
		t.Errorf("the fake docker is missing: %v", err)
	}
}
