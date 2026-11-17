package main

// The shared test harness of cmd/ltk. Every test that runs the program uses it. A machine without a go tool runs the
// tests against a program built elsewhere (LTK_BINARY); the tests that need the fake docker then skip, saying why.
//
//   - The program under test is LTK_BINARY when that is set (nothing is built then); otherwise TestMain builds ./ with
//     the go tool found on PATH or under runtime.GOROOT(). What it built must exist here and run here: a go found on
//     PATH may be a wrapper that builds in a container, whose output is not on this system; requireLTK then skips the
//     test, saying why.
//   - The fake docker of cmd/ltk/testdata/fakedocker is built the same way when its source exists and a go tool is
//     found, and must exist and run here too; requireFakeDocker skips a test, saying why, when it does not.
//   - runLTK runs the program and returns its exit code, standard output and standard error.
//   - With LTK_TEST_REPRO_SCENARIO set, this test binary is the program itself, with a fake reproduction runner in the
//     package's seam (repro_test.go's helper process); TestMain then runs main and nothing else.
//   - Every program name gets .exe on Windows through exeName.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var (
	// ltkBin is the path of the program under test; "" when it could not be had (ltkSetupErr says why).
	ltkBin      string
	ltkSetupErr error
	// ltkSkip says why the program built here cannot run here; "" when it can.
	ltkSkip string
	// fakeDockerDir holds the fake docker program; "" when it was not built (fakeDockerSkip says why).
	fakeDockerDir  string
	fakeDockerSkip string
	// harnessDir is TestMain's temporary folder, removed when the tests end; "" when it could not be made. The
	// integration tier keeps there the working folders of the runs its tests share (sharedRun, cleanstop_test.go).
	harnessDir string
)

// exeName is name with .exe on Windows, where a program is found only by its full file name.
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
	if scenario := os.Getenv(helperScenarioVar); scenario != "" {
		runHelperMain(scenario) // this binary started as the program by repro_test.go; main exits
		return
	}
	dir, err := os.MkdirTemp("", "ltk-harness-")
	if err != nil {
		ltkSetupErr = err
	} else {
		harnessDir = dir
		setUpBinaries(dir)
	}
	code := m.Run()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}

// setUpBinaries finds or builds the program under test and the fake docker, recording why when it cannot.
func setUpBinaries(dir string) {
	goBin := goTool()
	if p := os.Getenv("LTK_BINARY"); p != "" {
		if abs, err := filepath.Abs(p); err != nil {
			ltkSetupErr = err
		} else if _, err := os.Stat(abs); err != nil {
			ltkSetupErr = fmt.Errorf("LTK_BINARY: %w", err)
		} else {
			ltkBin = abs
		}
	} else if goBin == "" {
		ltkSetupErr = errors.New("LTK_BINARY is not set and no go tool is on PATH or under runtime.GOROOT(): nothing to run")
	} else {
		ltkBin, ltkSetupErr = goBuild(goBin, ".", filepath.Join(dir, exeName("ltk")))
		if ltkSetupErr == nil {
			if reason := runsHere(goBin, ltkBin, "--help"); reason != "" {
				ltkBin, ltkSkip = "", reason
			}
		}
	}

	src := filepath.Join("testdata", "fakedocker")
	switch _, statErr := os.Stat(src); {
	case statErr != nil:
		fakeDockerSkip = "the fake docker's source (cmd/ltk/testdata/fakedocker) is absent"
	case goBin == "":
		fakeDockerSkip = "no go tool on PATH or under runtime.GOROOT() to build the fake docker"
	default:
		fakeDockerDir = filepath.Join(dir, "fakedocker")
		bin, err := goBuild(goBin, "./"+filepath.ToSlash(src), filepath.Join(fakeDockerDir, exeName("docker")))
		switch {
		case err != nil:
			fakeDockerDir, fakeDockerSkip = "", fmt.Sprintf("the fake docker did not build: %v", err)
		default:
			// With no argument and no FAKE_DOCKER_* variable the fake exits 0 and writes nothing.
			if reason := runsHere(goBin, bin); reason != "" {
				fakeDockerDir, fakeDockerSkip = "", reason
			}
		}
	}
}

// runsHere says why the program bin that goBin built does not exist or does not run on this system, or "" when it
// runs (exit 0) with args.
func runsHere(goBin, bin string, args ...string) string {
	if _, err := os.Stat(bin); err != nil {
		return fmt.Sprintf("the go tool %s built no %s on this system (a go that builds in a container?): %v", goBin, filepath.Base(bin), err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "FAKE_DOCKER_MODE=", "FAKE_DOCKER_LOG=", "FAKE_DOCKER_STATE=", "FAKE_DOCKER_CORPUS=")
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("the %s that %s built does not run on this system: %v", filepath.Base(bin), goBin, err)
	}
	return ""
}

// goBuild builds pkg into out.
func goBuild(goBin, pkg, out string) (string, error) {
	cmd := exec.Command(goBin, "build", "-o", out, pkg)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build %s: %v\n%s", pkg, err, b)
	}
	return out, nil
}

// requireLTK fails the test when there is no program to run, and skips it, saying why, when the program built here
// does not run here (a go tool that builds in a container leaves no program for this system).
func requireLTK(t *testing.T) string {
	t.Helper()
	if ltkSkip != "" {
		t.Skipf("skipped: %s; set LTK_BINARY to a program built for this system", ltkSkip)
	}
	if ltkBin == "" {
		t.Fatalf("no program under test: %v", ltkSetupErr)
	}
	return ltkBin
}

// requireFakeDocker returns the fake docker's directory, or skips the test saying why there is none.
func requireFakeDocker(t *testing.T) string {
	t.Helper()
	if fakeDockerDir == "" {
		t.Skipf("skipped: %s", fakeDockerSkip)
	}
	return fakeDockerDir
}

// childEnv is the environment of the program: the test's own when env is nil; otherwise only SystemRoot and TEMP
// (when set; Windows needs them to run a program) and env.
func childEnv(env []string) []string {
	if env == nil {
		return nil
	}
	var out []string
	for _, k := range []string{"SystemRoot", "TEMP"} {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return append(out, env...)
}

// ltkCommand is the program under test as an exec.Cmd, run in dir ("" for the test's working directory).
func ltkCommand(t *testing.T, dir string, env []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(requireLTK(t), args...)
	cmd.Dir = dir
	cmd.Env = childEnv(env)
	return cmd
}

// runLTK runs the program with args and returns its exit code, standard output and standard error. A nil env keeps
// the test's environment; a non-nil env is the child's whole environment besides SystemRoot and TEMP.
func runLTK(t *testing.T, env []string, args ...string) (code int, stdout, stderr []byte) {
	t.Helper()
	return runLTKIn(t, "", env, args...)
}

// runLTKIn is runLTK with the working directory dir.
func runLTKIn(t *testing.T, dir string, env []string, args ...string) (code int, stdout, stderr []byte) {
	t.Helper()
	cmd := ltkCommand(t, dir, env, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	return exitCodeOfRun(t, cmd.Run()), out.Bytes(), errOut.Bytes()
}

// exitCodeOfRun is the exit code of a finished run; a run killed by a signal is -1.
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
