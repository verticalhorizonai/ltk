package orchestration

// The unit tier's harness: TestMain builds the fake docker of testdata/fakedocker with the go tool
// found on PATH or under runtime.GOROOT(); a test that needs it skips, saying why, when there is no go tool (the
// native integration binaries run where Go is not installed). The fake logs every call and answers from rules.

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var (
	// fakeDockerDir holds the fake docker; "" when it was not built (fakeDockerSkip says why).
	fakeDockerDir  string
	fakeDockerSkip string
)

// exeName is name with .exe on Windows, so that the tests run there too.
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
	dir, err := os.MkdirTemp("", "ltk-orchestration-")
	if err != nil {
		fakeDockerSkip = err.Error()
	} else {
		buildFakeDocker(dir)
	}
	code := m.Run()
	if dir != "" {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

// buildFakeDocker builds testdata/fakedocker into dir/fakedocker/docker, recording why when it cannot.
func buildFakeDocker(dir string) {
	goBin := goTool()
	if goBin == "" {
		fakeDockerSkip = "no go tool on PATH or under runtime.GOROOT() to build the fake docker"
		return
	}
	out := filepath.Join(dir, "fakedocker")
	bin := filepath.Join(out, exeName("docker"))
	cmd := exec.Command(goBin, "build", "-o", bin, "./testdata/fakedocker")
	if b, err := cmd.CombinedOutput(); err != nil {
		fakeDockerSkip = fmt.Sprintf("the fake docker did not build: %v\n%s", err, b)
		return
	}
	// A go found on PATH may be a wrapper that builds elsewhere (this build's container toolchain): the fake must exist
	// here and run here; with no rules and no log set (TestMain runs before any test sets them) it exits 0.
	if err := exec.Command(bin).Run(); err != nil {
		fakeDockerSkip = fmt.Sprintf("the go tool %s built no fake docker that runs on this system: %v", goBin, err)
		return
	}
	fakeDockerDir = out
}

// fakeRule is one answer of the fake docker (testdata/fakedocker/main.go).
type fakeRule struct {
	Prefix    []string `json:"prefix"`
	Stdout    string   `json:"stdout,omitempty"`
	Stderr    string   `json:"stderr,omitempty"`
	Exit      int      `json:"exit,omitempty"`
	Times     int      `json:"times,omitempty"`
	EchoStdin bool     `json:"echoStdin,omitempty"`
}

// fakeCall is one logged call of the fake docker.
type fakeCall struct {
	Args  []string
	Stdin []byte
}

// fake is the fake docker of one test: PATH holds only it, and its log and rules are the test's own.
type fake struct {
	t   *testing.T
	log string
}

// newFake puts the fake docker alone on PATH with rules, or skips the test saying why there is none.
func newFake(t *testing.T, rules ...fakeRule) *fake {
	t.Helper()
	if fakeDockerDir == "" {
		t.Skipf("skipped: %s", fakeDockerSkip)
	}
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.json")
	if rules == nil {
		rules = []fakeRule{}
	}
	b, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fake{t: t, log: filepath.Join(dir, "calls.jsonl")}
	t.Setenv("PATH", fakeDockerDir)
	t.Setenv("FAKE_DOCKER_LOG", f.log)
	t.Setenv("FAKE_DOCKER_RULES", rulesPath)
	return f
}

// runner returns the runner of run runID on the fake.
func (f *fake) runner(runID string) *Runner {
	f.t.Helper()
	r, err := NewRunner(runID)
	if err != nil {
		f.t.Fatalf("NewRunner: %v", err)
	}
	return r
}

// calls returns the fake's calls, in order.
func (f *fake) calls() []fakeCall {
	f.t.Helper()
	file, err := os.Open(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	var out []fakeCall
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e struct {
			Args  []string `json:"args"`
			Stdin *string  `json:"stdin"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			f.t.Fatal(err)
		}
		c := fakeCall{Args: e.Args}
		if e.Stdin != nil {
			c.Stdin, _ = base64.StdEncoding.DecodeString(*e.Stdin)
		}
		out = append(out, c)
	}
	return out
}

// argLines returns each call's arguments joined by spaces.
func (f *fake) argLines() []string {
	var out []string
	for _, c := range f.calls() {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

// flagValues returns every value of flag in args (`--flag value`).
func flagValues(args []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
		}
	}
	return out
}

// isCreate reports whether args create a resource: `run`, `create`, `network create`, `volume create`.
func isCreate(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "run", "create":
		return true
	case "network", "volume", "container":
		return len(args) > 1 && (args[1] == "create" || (args[0] == "container" && args[1] == "run"))
	}
	return false
}
