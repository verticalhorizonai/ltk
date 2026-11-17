package main

// The No-container command test: `ltk list` and `ltk compare` need no container and work on Linux, macOS and
// Windows.
//
// It runs in the unit tier, so it runs in every job of the CI matrix (Linux, macOS and Windows) with the program built
// for that system and named through exeName (with .exe on Windows):
//
//   - `ltk list corpus/`, `ltk list corpus/ --json`, `ltk compare corpus/ corpus/` and the ledger-only fixture pair exit
//     0, and the reproduced-changed pair exits 1 with `failure`, from the module root, first with a PATH that holds no
//     program at all, then with the fake docker on PATH, whose call log must stay empty: neither command starts docker;
//   - the two command handlers' files import only container-free packages of the module (its pure layer), which
//     the Dependency guard holds free of Docker code.
//
// That the three systems build the tool and pass these tests is shown by the continuous integration's jobs on hosted
// runners; on one machine it is shown by cross-compiling for the three systems and running this test on the systems
// at hand.

import (
	"bytes"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// containerFreeRuns are the container-free runs, from the module root, with the exit code each must give.
var containerFreeRuns = []struct {
	args []string
	exit int
}{
	{[]string{"list", "corpus/"}, 0},
	{[]string{"list", "corpus/", "--json"}, 0},
	{[]string{"compare", "corpus/", "corpus/"}, 0},
	{[]string{"compare", "testdata/compare/ledger-only/a", "testdata/compare/ledger-only/b"}, 0},
	{[]string{"compare", "testdata/compare/reproduced-changed/a", "testdata/compare/reproduced-changed/b"}, 1},
}

// TestListAndCompareNeedNoContainer: the container-free commands give the same exit codes and
// output with no program on PATH as with a docker on PATH, and never start docker.
func TestListAndCompareNeedNoContainer(t *testing.T) {
	for _, r := range containerFreeRuns {
		t.Run(strings.Join(r.args, " "), func(t *testing.T) {
			code, stdout, stderr := runLTKIn(t, readmeModuleRoot, noDockerEnv(t), r.args...)
			if code != r.exit || len(stdout) == 0 {
				t.Fatalf("with no program on PATH: exit %d, %d bytes of output, stderr %q; want exit %d and a report", code, len(stdout), stderr, r.exit)
			}
			if r.exit == 0 && len(stderr) != 0 {
				t.Errorf("stderr %q on exit 0", stderr)
			}
			if r.exit == int(contract.EXIT_CODES.Failure) {
				assertErrorLine(t, stderr, contract.ErrorCodeFailure)
			}
			f := newFakeEngine(t, "", "")
			code2, stdout2, stderr2 := runLTKIn(t, readmeModuleRoot, f.env, r.args...)
			if code2 != code || !bytes.Equal(stdout2, stdout) || !bytes.Equal(stderr2, stderr) {
				t.Errorf("with a docker on PATH the run differs: exit %d (%d), stderr %q (%q)", code2, code, stderr2, stderr)
			}
			if calls := f.calls(); len(calls) != 0 {
				t.Errorf("docker was started: %q", calls)
			}
		})
	}
}

// TestListAndCompareHandlersAreContainerFree: cmd/ltk/list.go and cmd/ltk/compare.go import
// the standard library and the container-free packages only (what those packages reach is held by
// TestDependencyGuardSource).
func TestListAndCompareHandlersAreContainerFree(t *testing.T) {
	module := readGoMod(t).module
	for _, name := range []string{"list.go", "compare.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			ip, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(ip, "/")
			switch {
			case !strings.Contains(first, ".") && ip != "os/exec":
			case strings.HasPrefix(ip, module+"/") && slices.Contains(containerFreePackages, strings.TrimPrefix(ip, module+"/")):
			default:
				t.Errorf("%s imports %s, which is not a container-free package", filepath.Join("cmd", "ltk", name), ip)
			}
		}
	}
}
