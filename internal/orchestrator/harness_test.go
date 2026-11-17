package orchestrator

// The shared helper of every verification test of this package: the program under test, the run helpers, and the
// error line held to its schema.
//
//   - ltkBin is LTK_BINARY when that is set (nothing is built then); otherwise TestMain builds ../../cmd/ltk with the
//     go tool found on PATH or under runtime.GOROOT() into a temporary folder.
//   - runLTK and runLTKIn run the program and return its exit code, standard output and standard error, with an
//     optional working folder and environment; an explicit environment keeps only SystemRoot and TEMP of the test's
//     own (Windows needs them).
//   - Every program name gets .exe on Windows through exeName.
//   - requireErrorLine holds standard error to exactly one ErrorLine valid against
//     internal/contract/testdata/error-line.schema.json, whose code corresponds to the exit code.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

var (
	// ltkBin is the absolute path of the program under test; "" when it could not be had (ltkSetupErr says why).
	ltkBin      string
	ltkSetupErr error
)

// exeName is name with .exe on Windows.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// goTool finds the go tool on PATH, else under runtime.GOROOT(); "" when there is none.
func goTool() string {
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	if root := runtime.GOROOT(); root != "" {
		p := filepath.Join(root, "bin", exeName("go"))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ltk-orchestrator-")
	if err != nil {
		ltkSetupErr = err
	} else {
		ltkBin, ltkSetupErr = programUnderTest(dir)
	}
	code := m.Run()
	if dir != "" {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

// programUnderTest is LTK_BINARY made absolute, or ../../cmd/ltk built into dir when LTK_BINARY is not set.
func programUnderTest(dir string) (string, error) {
	if p := os.Getenv("LTK_BINARY"); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("LTK_BINARY: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("LTK_BINARY: %w", err)
		}
		return abs, nil
	}
	goBin := goTool()
	if goBin == "" {
		return "", errors.New("LTK_BINARY is not set and no go tool is on PATH or under runtime.GOROOT(): nothing to run")
	}
	out := filepath.Join(dir, exeName("ltk"))
	cmd := exec.Command(goBin, "build", "-o", out, "../../cmd/ltk")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ../../cmd/ltk: %v\n%s", err, b)
	}
	return out, nil
}

// requireLTK returns the program under test, or fails the test saying why there is none: a verification that did
// not run the program never passes.
func requireLTK(t *testing.T) string {
	t.Helper()
	if ltkBin == "" {
		t.Fatalf("no program under test: %v", ltkSetupErr)
	}
	return ltkBin
}

// childEnv is the program's environment: the test's own when env is nil; otherwise only SystemRoot and TEMP (when
// set; Windows needs them) and env.
func childEnv(env []string) []string {
	if env == nil {
		return nil
	}
	out := []string{}
	for _, k := range []string{"SystemRoot", "TEMP"} {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return append(out, env...)
}

// ltkCommand is the program under test as an exec.Cmd, run in dir ("" for the test's working folder) with the
// environment childEnv(env).
func ltkCommand(t *testing.T, dir string, env []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(requireLTK(t), args...)
	cmd.Dir = dir
	cmd.Env = childEnv(env)
	return cmd
}

// runLTK runs the program with args in the test's working folder and returns its exit code, standard output and
// standard error. A nil env keeps the test's environment; a non-nil env is the program's whole environment besides
// SystemRoot and TEMP.
func runLTK(t *testing.T, env []string, args ...string) (code int, stdout, stderr []byte) {
	t.Helper()
	return runLTKIn(t, "", env, args...)
}

// runLTKIn is runLTK with the working folder dir.
func runLTKIn(t *testing.T, dir string, env []string, args ...string) (code int, stdout, stderr []byte) {
	t.Helper()
	cmd := ltkCommand(t, dir, env, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	return exitCodeOfRun(t, cmd.Run()), out.Bytes(), errOut.Bytes()
}

// exitCodeOfRun is the exit code of a finished run; a run ended by a signal is -1. A program that did not start
// fails the test.
func exitCodeOfRun(t *testing.T, err error) int {
	t.Helper()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	t.Fatalf("the program did not run: %v", err)
	return -1
}

// errorLineSchemaPath is the ErrorLine's JSON Schema, a test fixture under internal/: schemas/ publishes the
// catalogue's schema only.
const errorLineSchemaPath = "../contract/testdata/error-line.schema.json"

var (
	errorLineSchemaOnce sync.Once
	errorLineSchema     *jsonschema.Schema
	errorLineSchemaErr  error
)

// loadErrorLineSchema compiles the ErrorLine's schema once, with github.com/santhosh-tekuri/jsonschema/v6 (draft
// 2020-12).
func loadErrorLineSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	errorLineSchemaOnce.Do(func() {
		f, err := os.Open(errorLineSchemaPath)
		if err != nil {
			errorLineSchemaErr = err
			return
		}
		defer f.Close()
		doc, err := jsonschema.UnmarshalJSON(f)
		if err != nil {
			errorLineSchemaErr = fmt.Errorf("%s: %w", errorLineSchemaPath, err)
			return
		}
		const url = "https://ltk.invalid/error-line.schema.json"
		c := jsonschema.NewCompiler()
		if err := c.AddResource(url, doc); err != nil {
			errorLineSchemaErr = err
			return
		}
		errorLineSchema, errorLineSchemaErr = c.Compile(url)
	})
	if errorLineSchemaErr != nil {
		t.Fatalf("the ErrorLine schema: %v", errorLineSchemaErr)
	}
	return errorLineSchema
}

// exitCodeOfErrorCode is the error codes' one-to-one map to exit codes: failure → 1, usage_error → 2,
// environment_missing → 3, through contract.EXIT_CODES.
var exitCodeOfErrorCode = map[contract.ErrorCode]contract.ExitCode{
	contract.ErrorCodeFailure:            contract.EXIT_CODES.Failure,
	contract.ErrorCodeUsageError:         contract.EXIT_CODES.UsageError,
	contract.ErrorCodeEnvironmentMissing: contract.EXIT_CODES.EnvironmentMissing,
}

// requireErrorLine holds the standard error of a run that exited exitCode (non-zero) to the error line's form: exactly
// one line, ending in \n, one JSON object valid against the ErrorLine schema, whose code corresponds to exitCode. It
// returns the line.
func requireErrorLine(t *testing.T, exitCode int, stderr []byte) contract.ErrorLine {
	t.Helper()
	if exitCode == 0 {
		t.Fatalf("requireErrorLine called for exit 0")
	}
	if !bytes.HasSuffix(stderr, []byte("\n")) || bytes.Count(stderr, []byte("\n")) != 1 {
		t.Fatalf("stderr is not exactly one line ending in \\n: %q", stderr)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(stderr))
	if err != nil {
		t.Fatalf("stderr is not one JSON value: %v: %q", err, stderr)
	}
	if err := loadErrorLineSchema(t).Validate(inst); err != nil {
		t.Fatalf("stderr does not validate against %s: %v: %q", errorLineSchemaPath, err, stderr)
	}
	var line contract.ErrorLine
	dec := json.NewDecoder(bytes.NewReader(stderr))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&line); err != nil {
		t.Fatalf("stderr does not decode as contract.ErrorLine: %v: %q", err, stderr)
	}
	want, ok := exitCodeOfErrorCode[line.Code]
	if !ok {
		t.Fatalf("code %q has no exit code in the one-to-one map of error codes to exit codes", line.Code)
	}
	if int(want) != exitCode {
		t.Fatalf("the error line's code %q corresponds to exit %d, but the program exited %d", line.Code, want, exitCode)
	}
	return line
}
