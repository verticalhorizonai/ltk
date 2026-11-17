package main

// The CI workflow's test. The workflow cannot be run by a test: whether its jobs pass on Linux, macOS and Windows is
// shown by the repository's own Actions runs. What a test can show is that the workflow file asks for exactly what CI
// must prove, which this test reads:
//
//   - it runs on every push and on every pull request, each with no branch, tag or path filter;
//   - its token may only read: a top-level `permissions` with `contents: read`, and no write granted anywhere
//     (tokenPermissionFindings, in ciconstraint_test.go);
//   - one matrix job on ubuntu-latest, macos-latest and windows-latest, `fail-fast: false`, unconditional;
//   - `git config --global core.autocrlf false`, then actions/checkout, then actions/setup-go from go.mod's directive,
//     then `go build ./...` and `go test ./...`, in that order, on all three systems;
//   - a "Container-free commands" step on all three systems, in bash, that builds the program into $RUNNER_TEMP (with
//     .exe on Windows) and runs `ltk list corpus/ --json`, `ltk compare corpus/ corpus/`, the ledger-only
//     pair (which must pass) and the reproduced-changed pair (whose success fails the step);
//   - `ltk check corpus/` in one step, on Linux only, with the program built in that step;
//   - an aggregate job `ci-green` that needs the matrix job, runs `if: always()` and fails unless its result is
//     success; no `cancel-in-progress`, no `continue-on-error`, and never the integration tier.
//
// The reader is a strict subset of YAML, line based (no YAML library: the dependency guard allows two direct
// requirements): indentation, `key: value` lines, `- ` list items, flow lists `[a, b]`, block scalars `|` and `>`, and
// whole-line comments. A line it cannot read is an error, so the workflow stays within what the test can judge.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// yNode is one line of a workflow: a mapping key with its inline value, or a list item whose mapping is its children,
// with the lines of a block scalar when its value is one.
type yNode struct {
	line   int
	indent int
	item   bool
	key    string
	value  string
	block  []string
	kids   []*yNode
}

var reYAMLKey = regexp.MustCompile(`^([A-Za-z0-9_.-]+):(?:\s+(.*))?$`)

// parseYAMLSubset reads a workflow into its tree; the root's kids are the top-level keys.
func parseYAMLSubset(text string) (*yNode, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	root := &yNode{indent: -1}
	stack := []*yNode{root}
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		content := strings.TrimLeft(raw, " ")
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}
		if strings.HasPrefix(content, "\t") {
			return nil, fmt.Errorf("line %d: a tab in the indentation", i+1)
		}
		indent := len(raw) - len(content)
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		if strings.HasPrefix(content, "- ") || content == "-" {
			it := &yNode{line: i + 1, indent: indent, item: true}
			parent.kids = append(parent.kids, it)
			stack = append(stack, it)
			rest := strings.TrimSpace(strings.TrimPrefix(content, "-"))
			if rest == "" {
				continue
			}
			parent, indent, content = it, indent+2, rest
		}
		m := reYAMLKey.FindStringSubmatch(content)
		if m == nil {
			return nil, fmt.Errorf("line %d: %q is not a key: value line of the subset the test reads", i+1, content)
		}
		n := &yNode{line: i + 1, indent: indent, key: m[1], value: strings.TrimSpace(m[2])}
		if v := n.value; strings.HasPrefix(v, "#") {
			n.value = ""
		}
		parent.kids = append(parent.kids, n)
		if n.value == "|" || n.value == ">" {
			for i+1 < len(lines) {
				next := lines[i+1]
				body := strings.TrimLeft(next, " ")
				if body != "" && len(next)-len(body) <= indent {
					break
				}
				i++
				n.block = append(n.block, strings.TrimSpace(next))
			}
			for len(n.block) > 0 && n.block[len(n.block)-1] == "" {
				n.block = n.block[:len(n.block)-1]
			}
			continue
		}
		stack = append(stack, n)
	}
	return root, nil
}

// child is the kid with key, or nil.
func (n *yNode) child(key string) *yNode {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if !k.item && k.key == key {
			return k
		}
	}
	return nil
}

// children are n's kids; none when n is nil.
func (n *yNode) children() []*yNode {
	if n == nil {
		return nil
	}
	return n.kids
}

// text is the value of the kid with key, unquoted ("" when absent).
func (n *yNode) text(key string) string {
	c := n.child(key)
	if c == nil {
		return ""
	}
	return unquoteYAML(c.value)
}

// unquoteYAML is v without the quotes around it, when it is quoted whole.
func unquoteYAML(v string) string {
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// list is a flow list's or a block list's items, unquoted.
func (n *yNode) list() []string {
	if n == nil {
		return nil
	}
	var out []string
	if v := n.value; strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		for _, s := range strings.Split(v[1:len(v)-1], ",") {
			out = append(out, unquoteYAML(strings.TrimSpace(s)))
		}
		return out
	}
	if n.value != "" {
		return []string{unquoteYAML(n.value)}
	}
	for _, k := range n.kids {
		if k.item {
			out = append(out, unquoteYAML(k.value))
		}
	}
	return out
}

// lines is a run value: its block scalar's lines, or its one inline line.
func (n *yNode) lines() []string {
	if n == nil {
		return nil
	}
	if n.block != nil {
		return n.block
	}
	return []string{n.value}
}

// walk calls f on n and every node under it.
func (n *yNode) walk(f func(*yNode)) {
	f(n)
	for _, k := range n.kids {
		k.walk(f)
	}
}

// wfStep is one step of a job.
type wfStep struct {
	index                   int
	name, uses, shell, cond string
	run                     []string
	with                    map[string]string
	node                    *yNode
}

// workflow is a parsed workflow file.
type workflow struct {
	path string
	root *yNode
}

// jobs are the workflow's jobs, by key, in order.
func (w workflow) jobs() []*yNode { return w.root.child("jobs").children() }

// steps are the steps of job key (nil when there is no such job).
func (w workflow) steps(key string) []wfStep { return stepsOfJob(w.root.child("jobs").child(key)) }

// stepsOfJob reads a job's steps.
func stepsOfJob(job *yNode) []wfStep {
	var out []wfStep
	for i, it := range job.child("steps").children() {
		s := wfStep{index: i, name: it.text("name"), uses: it.text("uses"), shell: it.text("shell"), cond: it.text("if"),
			run: it.child("run").lines(), with: map[string]string{}, node: it}
		for _, w := range it.child("with").children() {
			s.with[w.key] = unquoteYAML(w.value)
		}
		out = append(out, s)
	}
	return out
}

// runText is the step's run lines joined.
func (s wfStep) runText() string { return strings.Join(s.run, "\n") }

// parseWorkflowFile reads one workflow file.
func parseWorkflowFile(t *testing.T, path string) workflow {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseYAMLSubset(string(b))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return workflow{path: path, root: root}
}

// readWorkflow reads .github/workflows/ci.yml.
func readWorkflow(t *testing.T) workflow {
	t.Helper()
	return parseWorkflowFile(t, filepath.Join(readmeModuleRoot, ".github", "workflows", "ci.yml"))
}

var (
	reLinuxOnlyStep  = regexp.MustCompile(`^runner\.os\s*==\s*'Linux'$`)
	reBuildProgram   = regexp.MustCompile(`go build -o "?\$RUNNER_TEMP/ltk[^ ]*"? \./cmd/ltk\b`)
	reCheckCorpusRun = regexp.MustCompile(`\scheck corpus/(\s|$)`)
)

// ciWorkflowFindings holds the workflow to the points of the header comment; it returns one finding per broken point.
func ciWorkflowFindings(w workflow) []string {
	var f []string
	add := func(format string, a ...any) { f = append(f, fmt.Sprintf(format, a...)) }

	on := w.root.child("on")
	if push := on.child("push"); push == nil || len(push.kids) != 0 || push.value != "" {
		add("the workflow does not run on every push with no filter")
	}
	f = append(f, pullRequestFindings(w)...)
	f = append(f, tokenPermissionFindings(w)...)
	job := w.root.child("jobs").child("build-and-test")
	if job == nil {
		return append(f, "no job build-and-test")
	}
	strategy := job.child("strategy")
	oses := strategy.child("matrix").child("os").list()
	slices.Sort(oses)
	if !slices.Equal(oses, []string{"macos-latest", "ubuntu-latest", "windows-latest"}) {
		add("the matrix is %q, want Linux, macOS and Windows, the three supported systems", oses)
	}
	if !strings.Contains(job.text("runs-on"), "matrix.os") {
		add("the job does not run on its matrix")
	}
	if strategy.text("fail-fast") != "false" {
		add("fail-fast is %q, want false: each system reports its own result", strategy.text("fail-fast"))
	}
	if job.child("if") != nil {
		add("the matrix job is conditional")
	}

	steps := w.steps("build-and-test")
	at := func(pred func(wfStep) bool) int {
		for _, s := range steps {
			if pred(s) {
				return s.index
			}
		}
		return -1
	}
	runs := func(sub string) func(wfStep) bool {
		return func(s wfStep) bool { return slices.Contains(s.run, sub) }
	}
	prev := -1
	for _, o := range []struct {
		what string
		idx  int
	}{
		{"git config --global core.autocrlf false", at(runs("git config --global core.autocrlf false"))},
		{"actions/checkout", at(func(s wfStep) bool { return strings.HasPrefix(s.uses, "actions/checkout@") })},
		{"actions/setup-go from go.mod", at(func(s wfStep) bool {
			return strings.HasPrefix(s.uses, "actions/setup-go@") && s.with["go-version-file"] == "go.mod"
		})},
		{"go build ./...", at(runs("go build ./..."))},
		{"go test ./...", at(runs("go test ./..."))},
	} {
		switch {
		case o.idx < 0:
			add("no step runs %s", o.what)
		case steps[o.idx].cond != "":
			add("the %s step is conditional", o.what)
		case o.idx < prev:
			add("the %s step comes too early (autocrlf, checkout, setup-go, build, test)", o.what)
		}
		prev = max(prev, o.idx)
	}

	cf := at(func(s wfStep) bool { return s.name == "Container-free commands" })
	if cf < 0 {
		add("no Container-free commands step")
	} else {
		s := steps[cf]
		if s.cond != "" || s.shell != "bash" {
			add("the Container-free commands step runs if %q in %q; want every system, in bash", s.cond, s.shell)
		}
		if len(s.run) < 2 || s.run[0] != `BIN="$RUNNER_TEMP/ltk${{ runner.os == 'Windows' && '.exe' || '' }}"` || s.run[1] != `go build -o "$BIN" ./cmd/ltk` {
			add("the Container-free commands step does not build ./cmd/ltk into $RUNNER_TEMP, .exe on Windows")
		}
		for _, l := range []string{
			`"$BIN" list corpus/ --json`,
			`"$BIN" compare corpus/ corpus/`,
			`"$BIN" compare testdata/compare/ledger-only/a testdata/compare/ledger-only/b`,
			`if "$BIN" compare testdata/compare/reproduced-changed/a testdata/compare/reproduced-changed/b; then exit 1; fi`,
		} {
			if !slices.Contains(s.run, l) {
				add("the Container-free commands step does not run %q", l)
			}
		}
	}

	checks := 0
	for _, j := range w.jobs() {
		for _, s := range stepsOfJob(j) {
			if !reCheckCorpusRun.MatchString(" " + s.runText()) {
				continue
			}
			checks++
			if j != job || !reLinuxOnlyStep.MatchString(s.cond) {
				add("job %s step %d runs ltk check corpus/ on a system other than Linux alone (if: %q)", j.key, s.index+1, s.cond)
			}
			if !reBuildProgram.MatchString(s.runText()) {
				add("the ltk check corpus/ step does not build the program it runs")
			}
		}
	}
	if checks != 1 {
		add("%d steps run ltk check corpus/, want one, on Linux", checks)
	}

	agg := w.root.child("jobs").child("ci-green")
	switch {
	case agg == nil:
		add("no aggregate job ci-green")
	case !slices.Contains(agg.child("needs").list(), "build-and-test"):
		add("ci-green does not need build-and-test")
	case agg.text("if") != "always()":
		add("ci-green runs if %q, want always()", agg.text("if"))
	default:
		judged := false
		for _, s := range stepsOfJob(agg) {
			judged = judged || s.runText() == `test "${{ needs.build-and-test.result }}" = success`
		}
		if !judged {
			add("ci-green does not fail unless build-and-test succeeded")
		}
	}

	w.root.walk(func(n *yNode) {
		switch {
		case n.key == "cancel-in-progress" && n.value != "false":
			add("line %d: cancel-in-progress: a cancelled run is not green", n.line)
		case n.key == "continue-on-error" && n.value != "false":
			add("line %d: continue-on-error: a step that may fail silently proves nothing", n.line)
		case n.key == "run" && strings.Contains(strings.Join(n.lines(), "\n"), "-tags integration"):
			add("line %d: the integration tier never runs in CI: it needs Docker and the network", n.line)
		}
	})
	return f
}

// TestCIWorkflow: .github/workflows/ci.yml asks for what CI must prove, as far as a file can show it; the runs
// themselves are the repository's Actions runs.
func TestCIWorkflow(t *testing.T) {
	for _, finding := range ciWorkflowFindings(readWorkflow(t)) {
		t.Error(finding)
	}
}

// TestCIWorkflowFindingsAreNotVacuous: each mutation of the committed workflow that breaks one point is found.
func TestCIWorkflowFindingsAreNotVacuous(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, m := range []struct{ name, from, to, finding string }{
		{"a branch filter", "  push:\n", "  push:\n    branches: [main]\n", "every push"},
		{"no pull_request", "  pull_request:\n", "", "every pull request"},
		{"no permissions", "permissions:\n  contents: read\n", "", "no top-level permissions"},
		{"a write permission", "  contents: read\n", "  contents: write\n", "grants write"},
		{"two systems", "[ubuntu-latest, macos-latest, windows-latest]", "[ubuntu-latest, macos-latest]", "the matrix is"},
		{"fail-fast", "fail-fast: false", "fail-fast: true", "fail-fast"},
		{"no autocrlf", "run: git config --global core.autocrlf false", "run: git config --global core.autocrlf true", "core.autocrlf false"},
		{"no --json", `"$BIN" list corpus/ --json`, `"$BIN" list corpus/`, "list corpus/ --json"},
		{"failure expected to pass", `if "$BIN" compare testdata/compare/reproduced-changed/a testdata/compare/reproduced-changed/b; then exit 1; fi`, `"$BIN" compare testdata/compare/reproduced-changed/a testdata/compare/reproduced-changed/b`, "reproduced-changed"},
		{"check on every system", "        if: runner.os == 'Linux'\n", "", "other than Linux"},
		{"aggregate without always", "    if: always()\n", "", "want always()"},
		{"cancellation", "name: ci\n", "name: ci\nconcurrency:\n  group: ci\n  cancel-in-progress: true\n", "cancel-in-progress"},
		{"integration tier", "run: go test ./...", "run: go test -tags integration ./...", "integration tier"},
	} {
		if !strings.Contains(src, m.from) {
			t.Fatalf("%s: the workflow has no %q to mutate", m.name, m.from)
		}
		root, err := parseYAMLSubset(strings.Replace(src, m.from, m.to, 1))
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		f := ciWorkflowFindings(workflow{root: root})
		if !slices.ContainsFunc(f, func(s string) bool { return strings.Contains(s, m.finding) }) {
			t.Errorf("%s: findings %q, want one naming %q", m.name, f, m.finding)
		}
	}
	if root, err := parseYAMLSubset(src); err != nil || len(ciWorkflowFindings(workflow{root: root})) != 0 {
		t.Errorf("the committed workflow: %v %q", err, ciWorkflowFindings(workflow{root: root}))
	}
}

// TestParseYAMLSubset holds the reader to the forms the workflow uses, and to refusing a line outside them.
func TestParseYAMLSubset(t *testing.T) {
	root, err := parseYAMLSubset("a:\n  b: [x, 'y']\n  # c\n  s:\n    - name: one\n      run: |\n        l1\n\n        l2\n    - uses: u@v\n      with:\n        k: 'v'\nz: 1\n")
	if err != nil {
		t.Fatal(err)
	}
	a := root.child("a")
	steps := stepsOfJob(&yNode{kids: []*yNode{{key: "steps", kids: a.child("s").kids}}})
	if got := a.child("b").list(); !slices.Equal(got, []string{"x", "y"}) || root.text("z") != "1" || len(steps) != 2 ||
		!slices.Equal(steps[0].run, []string{"l1", "", "l2"}) || steps[0].name != "one" || steps[1].with["k"] != "v" {
		t.Errorf("list %q z %q steps %+v", got, root.text("z"), steps)
	}
	if _, err := parseYAMLSubset("a: b\n{ c }\n"); err == nil {
		t.Error("a line outside the subset was read")
	}
}
