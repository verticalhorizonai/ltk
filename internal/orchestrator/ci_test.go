package orchestrator

// The CI verification: matrix steps, line endings, the aggregate job, no cancellation, and no container engine on the
// macOS and Windows runners. Unit tier: it reads `.github/workflows/ci.yml` from outside and holds it to what CI must
// prove:
//
//   - it runs on every push and on every pull request (a `push` and a `pull_request` trigger, each with no branch, tag
//     or path filter), so a pull request from a fork is tested as a push is;
//   - its token may only read: a top-level `permissions` with `contents: read`, and no permission granted `write` (nor
//     `write-all`) at the top or in any job, in any workflow;
//   - one matrix job on ubuntu-latest, macos-latest and windows-latest, `fail-fast: false`, unconditional;
//   - `git config --global core.autocrlf false` before `actions/checkout`, then `actions/setup-go` at Go 1.25
//     (`go-version-file: go.mod`, whose go directive is 1.25.x, or `go-version: 1.25…`), then `go build ./...` and
//     `go test ./...`, every one on all three systems;
//   - the "Container-free commands" step on all three systems, in bash, building the program into $RUNNER_TEMP (with
//     .exe on Windows) and running `ltk list corpus/ --json`, `ltk compare corpus/ corpus/`, the ledger-only pair
//     (expected to pass) and the reproduced-changed pair (expected to fail, so its success fails the step);
//   - `ltk check corpus/` on Linux only, built from ./cmd/ltk in that job;
//   - no step of a macOS or Windows job (or leg) installs or starts a container engine, and the job carries no
//     `container:` or `services:`; no step of any workflow builds a Windows image (Docker in the Linux job is allowed);
//   - no integration-tier test in CI; no `continue-on-error: true`;
//   - an aggregate job `ci-green` that needs the matrix job, runs `if: always()` and fails unless its result is
//     success; no `cancel-in-progress` anywhere.
//
// Parsing is line-based (no YAML library: the dependency guard allows exactly two direct requirements): a small
// reader of the indentation, the list items and the block scalars a GitHub workflow uses. The checks are proved not
// to be vacuous: a workflow written from the contract passes them, and each mutation of it that breaks one rule fails
// with that rule's finding.
//
// What this test cannot show: that the workflow passes on GitHub's hosted runners. The latest run's status, the three
// jobs passing and `ltk check corpus/` passing on Linux are shown by the repository's own Actions runs, not here.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The workflow folder and the CI workflow, from this package's folder.
var (
	workflowFolder = filepath.Join(moduleRoot, ".github", "workflows")
	ciWorkflowPath = filepath.Join(workflowFolder, "ci.yml")
)

// ciMatrixOSes are the three systems of the matrix: the three platforms ltk supports.
var ciMatrixOSes = []string{"macos-latest", "ubuntu-latest", "windows-latest"}

// wfNode is one line of a workflow as the line-based reader reads it: a mapping key with its inline value, or a list
// item (whose mapping, when it has one, is its children), with the lines of a block scalar (| or >) when the value is
// one.
type wfNode struct {
	line   int
	indent int
	item   bool
	key    string
	value  string
	block  []string
	kids   []*wfNode
	parent *wfNode
}

// reWorkflowKey is a mapping line: a plain or quoted key, a colon, and an optional inline value.
var reWorkflowKey = regexp.MustCompile(`^("[^"]*"|'[^']*'|[A-Za-z0-9_.${}\- ]+?):(?:\s+(.*))?$`)

// unquote strips one pair of matching quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// stripComment removes a trailing comment from an unquoted inline value.
func stripComment(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v[0] == '"' || v[0] == '\'' {
		return v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// parseWorkflow reads a workflow's text into a tree of wfNode, the root at indent -1.
func parseWorkflow(src string) *wfNode {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	root := &wfNode{indent: -1}
	stack := []*wfNode{root}
	attach := func(n *wfNode, asItem bool) {
		for len(stack) > 1 {
			top := stack[len(stack)-1]
			if top.indent > n.indent || top.indent == n.indent && (!asItem || top.item) {
				stack = stack[:len(stack)-1]
				continue
			}
			break
		}
		p := stack[len(stack)-1]
		n.parent = p
		p.kids = append(p.kids, n)
		stack = append(stack, n)
	}
	var mapping func(i, indent int, text string) int
	mapping = func(i, indent int, text string) int {
		m := reWorkflowKey.FindStringSubmatch(text)
		if m == nil {
			n := &wfNode{line: i + 1, indent: indent, value: stripComment(text)}
			attach(n, false)
			return i
		}
		n := &wfNode{line: i + 1, indent: indent, key: unquote(m[1]), value: stripComment(m[2])}
		attach(n, false)
		if v := n.value; v == "|" || v == ">" || strings.HasPrefix(v, "|-") || strings.HasPrefix(v, "|+") || strings.HasPrefix(v, ">-") || strings.HasPrefix(v, ">+") {
			n.value = ""
			j := i + 1
			blockIndent := -1
			for ; j < len(lines); j++ {
				raw := lines[j]
				if strings.TrimSpace(raw) == "" {
					n.block = append(n.block, "")
					continue
				}
				ind := len(raw) - len(strings.TrimLeft(raw, " "))
				if ind <= indent {
					break
				}
				if blockIndent < 0 {
					blockIndent = ind
				}
				n.block = append(n.block, strings.TrimRight(raw[min(ind, blockIndent):], " "))
			}
			for len(n.block) > 0 && n.block[len(n.block)-1] == "" {
				n.block = n.block[:len(n.block)-1]
			}
			return j - 1
		}
		return i
	}
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if trimmed == "-" || strings.HasPrefix(trimmed, "- ") {
			item := &wfNode{line: i + 1, indent: indent, item: true}
			attach(item, true)
			rest := strings.TrimLeft(trimmed[1:], " ")
			if rest == "" {
				continue
			}
			col := indent + len(trimmed) - len(rest)
			if reWorkflowKey.MatchString(rest) {
				i = mapping(i, col, rest)
			} else {
				item.value = stripComment(rest)
			}
			continue
		}
		i = mapping(i, indent, trimmed)
	}
	return root
}

// child is the mapping child named key, nil when there is none.
func (n *wfNode) child(key string) *wfNode {
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

// get is the inline value of the child named key, unquoted; "" when there is none.
func (n *wfNode) get(key string) string {
	if c := n.child(key); c != nil {
		return unquote(c.value)
	}
	return ""
}

// items are the list items under n.
func (n *wfNode) items() []*wfNode {
	if n == nil {
		return nil
	}
	var out []*wfNode
	for _, k := range n.kids {
		if k.item {
			out = append(out, k)
		}
	}
	return out
}

// list is n's value read as a list: an inline [a, b], one scalar, or the scalar items under it.
func (n *wfNode) list() []string {
	if n == nil {
		return nil
	}
	if v := strings.TrimSpace(n.value); v != "" {
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			var out []string
			for _, s := range strings.Split(v[1:len(v)-1], ",") {
				if s = unquote(s); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		return []string{unquote(v)}
	}
	var out []string
	for _, it := range n.items() {
		if it.value != "" {
			out = append(out, unquote(it.value))
		}
	}
	return out
}

// lines are the lines of n's value: the block scalar's, or the inline value.
func (n *wfNode) lines() []string {
	if n == nil {
		return nil
	}
	if len(n.block) > 0 {
		return n.block
	}
	if n.value != "" {
		return []string{unquote(n.value)}
	}
	return nil
}

// walk calls f on n and every node below it.
func (n *wfNode) walk(f func(*wfNode)) {
	f(n)
	for _, k := range n.kids {
		k.walk(f)
	}
}

// wfStep is one step of a job as the checks read it.
type wfStep struct {
	index int
	node  *wfNode
	name  string
	uses  string
	cond  string
	shell string
	run   []string
}

// stepsOf are a job's steps in order.
func stepsOf(job *wfNode) []wfStep {
	var out []wfStep
	for i, it := range job.child("steps").items() {
		out = append(out, wfStep{
			index: i, node: it, name: it.get("name"), uses: it.get("uses"), cond: strings.TrimSpace(it.child("if").valueOr()),
			shell: it.get("shell"), run: it.child("run").lines(),
		})
	}
	return out
}

// valueOr is n's raw value, "" for a missing node.
func (n *wfNode) valueOr() string {
	if n == nil {
		return ""
	}
	return n.value
}

// runText is a step's run lines joined.
func (s wfStep) runText() string { return strings.Join(s.run, "\n") }

// reLinuxOnly is an `if:` that restricts a step to the Linux leg.
var reLinuxOnly = regexp.MustCompile(`runner\.os\s*==\s*['"]Linux['"]|matrix\.os\s*==\s*['"]ubuntu-latest['"]|startsWith\(\s*matrix\.os\s*,\s*['"]ubuntu`)

// linuxOnly reports whether the condition cond restricts its step to Linux: it names Linux and offers no other way in.
func linuxOnly(cond string) bool {
	return reLinuxOnly.MatchString(cond) && !strings.Contains(cond, "||") && !strings.Contains(cond, "!=")
}

// The rules for the macOS and Windows runners: a container-engine setup action, a run line that names a container
// engine, a Windows image build.
var (
	reEngineAction = regexp.MustCompile(`^(docker/|crazy-max/ghaction-setup-docker|douglascamata/setup-docker-macos-action|abiosoft/setup-colima)`)
	reEngineWord   = regexp.MustCompile(`(?i)\b(docker|podman|colima|nerdctl|buildah)\b`)
	reWindowsImage = regexp.MustCompile(`(?i)--platform[= ]+["']?windows`)
	reCheckCorpus  = regexp.MustCompile(`(^|\s|["'])check\s+corpus/(\s|$|["'])`)
	reListJSON     = regexp.MustCompile(`(^|\s|["'])list\s+(corpus/\s+--json|--json\s+corpus/)(\s|$)`)
	reBuildLTK     = regexp.MustCompile(`go build\s+(-o\s+\S+\s+)?\./cmd/ltk\b`)
	reBuildTarget  = regexp.MustCompile(`go build\s+-o\s+(\S+)\s+\./cmd/ltk\b`)
	reCheckProgram = regexp.MustCompile(`(\S+)\s+check\s+corpus/`)
	reIntegration  = regexp.MustCompile(`-tags[= ]+["']?integration`)
)

// jobOSes are the systems a job runs on: its matrix's os list when runs-on names matrix.os, else its runs-on.
func jobOSes(job *wfNode) []string {
	runsOn := job.get("runs-on")
	if strings.Contains(runsOn, "matrix.os") {
		return job.child("strategy").child("matrix").child("os").list()
	}
	return job.child("runs-on").list()
}

// reachesMacOrWindows reports whether a job runs on macOS or Windows.
func reachesMacOrWindows(job *wfNode) bool {
	for _, o := range jobOSes(job) {
		if strings.HasPrefix(o, "macos") || strings.HasPrefix(o, "windows") {
			return true
		}
	}
	return false
}

// everyWorkflowFindings are the rules that hold in every workflow file: no container engine on a macOS or
// Windows job, no Windows image built anywhere, no cancel-in-progress, no continue-on-error: true.
func everyWorkflowFindings(root *wfNode) []string {
	var f []string
	add := func(format string, a ...any) { f = append(f, fmt.Sprintf(format, a...)) }
	root.walk(func(n *wfNode) {
		switch n.key {
		case "permissions":
			if strings.Contains(n.value, "write") {
				add("line %d: permissions: %s grants write; the token may only read", n.line, n.value)
			}
			for _, k := range n.kids {
				if unquote(k.value) == "write" {
					add("line %d: permissions %s: write grants write; the token may only read", k.line, k.key)
				}
			}
		case "cancel-in-progress":
			if unquote(n.value) != "false" {
				add("line %d: cancel-in-progress: %s; the workflow declares no cancellation", n.line, n.value)
			}
		case "continue-on-error":
			if unquote(n.value) != "false" {
				add("line %d: continue-on-error: %s; a step that may fail silently proves nothing", n.line, n.value)
			}
		}
	})
	jobs := root.child("jobs")
	if jobs == nil {
		return f
	}
	for _, job := range jobs.kids {
		macWin := reachesMacOrWindows(job)
		if macWin {
			for _, k := range []string{"container", "services"} {
				if c := job.child(k); c != nil {
					add("job %s line %d: `%s:` on a job that runs on macOS or Windows starts a container engine", job.key, c.line, k)
				}
			}
		}
		for _, s := range stepsOf(job) {
			if macWin && !linuxOnly(s.cond) {
				if reEngineAction.MatchString(s.uses) {
					add("job %s step %d: uses %s, a container engine on macOS or Windows", job.key, s.index+1, s.uses)
				}
				for _, l := range s.run {
					if !strings.HasPrefix(strings.TrimSpace(l), "#") && reEngineWord.MatchString(l) {
						add("job %s step %d: %q names a container engine on macOS or Windows", job.key, s.index+1, l)
					}
				}
			}
			for _, l := range s.run {
				if reWindowsImage.MatchString(l) {
					add("job %s step %d: %q builds a Windows image", job.key, s.index+1, l)
				}
			}
			with := s.node.child("with")
			for _, k := range []string{"platforms", "platform"} {
				if strings.Contains(strings.ToLower(with.get(k)), "windows") {
					add("job %s step %d: %s %s builds a Windows image", job.key, s.index+1, k, with.get(k))
				}
			}
		}
	}
	return f
}

// ciFindings holds the CI workflow's text to the contract; goDirective is go.mod's go version. It returns one finding
// per broken rule, none for a conforming workflow.
func ciFindings(src, goDirective string) []string {
	var f []string
	add := func(format string, a ...any) { f = append(f, fmt.Sprintf(format, a...)) }
	root := parseWorkflow(src)
	f = append(f, everyWorkflowFindings(root)...)

	// On every push: a push trigger with no filter.
	on := root.child("on")
	var triggers []string
	var push *wfNode
	if on != nil && on.value != "" {
		triggers = on.list()
	} else if on != nil {
		for _, k := range on.kids {
			triggers = append(triggers, k.key)
		}
		push = on.child("push")
	}
	if !slices.Contains(triggers, "push") {
		add("no push trigger: the workflow runs on %q; it must run on every push", triggers)
	}
	for _, k := range push.kidsKeys() {
		add("the push trigger is filtered by %s: not every push runs it", k)
	}

	// On every pull request: a pull_request trigger with no filter.
	if !slices.Contains(triggers, "pull_request") {
		add("no pull_request trigger: the workflow runs on %q; it must run on every pull request", triggers)
	}
	for _, k := range on.child("pull_request").kidsKeys() {
		add("the pull_request trigger is filtered by %s: not every pull request runs it", k)
	}

	// The token may only read the repository's contents (a write anywhere is everyWorkflowFindings').
	if p := root.child("permissions"); p == nil {
		add("no top-level permissions: the token gets the repository's default, which may be write")
	} else if p.value != "" || p.get("contents") != "read" {
		add("the top-level permissions are not contents: read")
	}

	jobs := root.child("jobs")
	if jobs == nil {
		add("no jobs")
		return f
	}
	var matrixJobs []*wfNode
	for _, j := range jobs.kids {
		if j.child("strategy").child("matrix").child("os") != nil {
			matrixJobs = append(matrixJobs, j)
		}
	}
	if len(matrixJobs) != 1 {
		add("%d jobs with an os matrix, want the one three-system matrix job", len(matrixJobs))
		return f
	}
	mj := matrixJobs[0]
	oses := mj.child("strategy").child("matrix").child("os").list()
	sorted := slices.Clone(oses)
	slices.Sort(sorted)
	if !slices.Equal(sorted, ciMatrixOSes) {
		add("the matrix is %q, want %q", oses, ciMatrixOSes)
	}
	if !strings.Contains(mj.get("runs-on"), "matrix.os") {
		add("the matrix job runs on %q, not on matrix.os", mj.get("runs-on"))
	}
	if ff := mj.child("strategy").get("fail-fast"); ff != "false" {
		add("strategy.fail-fast is %q, want false: each system reports its own result", ff)
	}
	if c := mj.child("if"); c != nil {
		add("the matrix job is conditional (if: %s): not every push builds and tests", c.value)
	}

	steps := stepsOf(mj)
	find := func(pred func(wfStep) bool) int {
		for _, s := range steps {
			if pred(s) {
				return s.index
			}
		}
		return -1
	}
	runHas := func(sub string) func(wfStep) bool {
		return func(s wfStep) bool {
			for _, l := range s.run {
				if strings.Contains(l, sub) {
					return true
				}
			}
			return false
		}
	}
	order := []struct {
		what string
		idx  int
	}{
		{"`git config --global core.autocrlf false`", find(runHas("git config --global core.autocrlf false"))},
		{"actions/checkout", find(func(s wfStep) bool { return strings.HasPrefix(s.uses, "actions/checkout@") })},
		{"actions/setup-go", find(func(s wfStep) bool { return strings.HasPrefix(s.uses, "actions/setup-go@") })},
		{"`go build ./...`", find(runHas("go build ./..."))},
		{"`go test ./...`", find(runHas("go test ./..."))},
	}
	prev := -1
	for _, o := range order {
		switch {
		case o.idx < 0:
			add("the matrix job has no %s step", o.what)
		case steps[o.idx].cond != "":
			add("the %s step is conditional (if: %s): it must run on all three systems", o.what, steps[o.idx].cond)
		case o.idx <= prev:
			add("the %s step (step %d) comes before the step it must follow (the order is autocrlf, checkout, setup-go, build, test)", o.what, o.idx+1)
		}
		if o.idx >= 0 {
			prev = o.idx
		}
	}
	if i := order[2].idx; i >= 0 {
		with := steps[i].node.child("with")
		switch gvf, gv := with.get("go-version-file"), with.get("go-version"); {
		case gvf == "go.mod":
			if !strings.HasPrefix(goDirective, "1.25") {
				add("setup-go reads go.mod, whose go directive is %q, not 1.25", goDirective)
			}
		case gvf != "":
			add("setup-go reads %s, want go.mod", gvf)
		case !strings.HasPrefix(gv, "1.25"):
			add("setup-go installs Go %q, want 1.25", gv)
		}
	}

	// The container-free commands, on all three systems.
	cf := find(func(s wfStep) bool { return s.name == "Container-free commands" })
	if cf < 0 {
		add("the matrix job has no step named \"Container-free commands\"")
	} else {
		s := steps[cf]
		text := s.runText()
		if s.cond != "" {
			add("the Container-free commands step is conditional (if: %s): it runs on all three systems", s.cond)
		}
		if s.shell != "bash" {
			add("the Container-free commands step runs in %q, want shell: bash (its script is bash on Windows too)", s.shell)
		}
		if !reBuildLTK.MatchString(text) || !strings.Contains(text, "RUNNER_TEMP") || !strings.Contains(text, ".exe") {
			add("the Container-free commands step does not build ./cmd/ltk into $RUNNER_TEMP with .exe on Windows")
		}
		type want struct {
			what   string
			match  func(string) bool
			negate bool
		}
		wants := []want{
			{"`ltk list corpus/ --json`", reListJSON.MatchString, false},
			{"`ltk compare corpus/ corpus/`", func(l string) bool { return strings.Contains(l, "compare corpus/ corpus/") }, false},
			{"the ledger-only compare pair", func(l string) bool {
				return strings.Contains(l, "compare testdata/compare/ledger-only/a testdata/compare/ledger-only/b")
			}, false},
			{"the reproduced-changed compare pair", func(l string) bool {
				return strings.Contains(l, "compare testdata/compare/reproduced-changed/a testdata/compare/reproduced-changed/b")
			}, true},
		}
		for _, w := range wants {
			at := -1
			for i, l := range s.run {
				if w.match(l) {
					at = i
					break
				}
			}
			if at < 0 {
				add("the Container-free commands step does not run %s", w.what)
				continue
			}
			l := strings.TrimSpace(s.run[at])
			negated := strings.HasPrefix(l, "if ") && strings.Contains(l, "then exit 1") ||
				strings.HasPrefix(l, "! ") && at == len(s.run)-1
			switch {
			case w.negate && !negated:
				add("%s is run as %q: its exit 1 must be expected, its success failing the step", w.what, l)
			case !w.negate && (strings.Contains(l, "||") || strings.HasPrefix(l, "!") || strings.HasPrefix(l, "if ")):
				add("%s is run as %q: its failure must fail the step", w.what, l)
			}
		}
	}

	// `ltk check corpus/` on Linux only: it runs the Rust XDR tool's Linux container, which no macOS or Windows
	// runner can run.
	linuxChecks := 0
	for _, job := range jobs.kids {
		js := stepsOf(job)
		for _, s := range js {
			if !reCheckCorpus.MatchString(s.runText()) {
				continue
			}
			onLinux := job == mj && linuxOnly(s.cond) || job != mj && !reachesMacOrWindows(job) && slices.ContainsFunc(jobOSes(job), func(o string) bool { return strings.HasPrefix(o, "ubuntu") })
			if !onLinux {
				add("job %s step %d runs `ltk check corpus/` (if: %q) on a system other than Linux; it runs on Linux only", job.key, s.index+1, s.cond)
				continue
			}
			if s.node.child("working-directory") != nil {
				add("job %s step %d runs `ltk check corpus/` outside the module root", job.key, s.index+1)
			}
			// The program it runs is built in the same step, or is the -o target of an earlier step's build.
			built := reBuildLTK.MatchString(s.runText())
			if m := reCheckProgram.FindStringSubmatch(s.runText()); m != nil && !built {
				for _, b := range js[:s.index] {
					for _, tm := range reBuildTarget.FindAllStringSubmatch(b.runText(), -1) {
						built = built || unquote(tm[1]) == unquote(m[1])
					}
				}
			}
			if !built {
				add("job %s step %d runs `ltk check corpus/` with no program built from ./cmd/ltk in that job", job.key, s.index+1)
			}
			linuxChecks++
		}
	}
	if linuxChecks == 0 {
		add("no step runs `ltk check corpus/` on Linux")
	}

	// The integration tier never runs in CI.
	root.walk(func(n *wfNode) {
		for _, l := range n.lines() {
			if n.key == "run" && reIntegration.MatchString(l) {
				add("line %d: %q runs the integration tier, which never runs in CI", n.line, l)
			}
		}
	})

	// The aggregate job.
	agg := jobs.child("ci-green")
	if agg == nil {
		add("no aggregate job ci-green")
		return f
	}
	if !slices.Contains(agg.child("needs").list(), mj.key) {
		add("ci-green needs %q, not the matrix job %s", agg.child("needs").list(), mj.key)
	}
	if !strings.Contains(agg.get("if"), "always()") {
		add("ci-green runs if %q, want always(): it must report a failed or cancelled leg", agg.get("if"))
	}
	judged := false
	for _, s := range stepsOf(agg) {
		t := s.runText()
		judged = judged || strings.Contains(t, "needs."+mj.key+".result") && strings.Contains(t, "success")
	}
	if !judged {
		add("no step of ci-green fails unless needs.%s.result is success", mj.key)
	}
	return f
}

// kidsKeys are the mapping keys under n.
func (n *wfNode) kidsKeys() []string {
	if n == nil {
		return nil
	}
	var out []string
	for _, k := range n.kids {
		if !k.item {
			out = append(out, k.key)
		}
	}
	return out
}

// goDirective is the go directive of the module's go.mod.
func goDirective(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go\s+(\S+)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("go.mod has no go directive")
	}
	return string(m[1])
}

// contractWorkflow is a workflow written from the rules above: the checks' known-good input.
const contractWorkflow = `name: ci

on:
  push:
  pull_request:

permissions:
  contents: read

jobs:
  build-and-test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - run: git config --global core.autocrlf false
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go build ./...
      - run: go test ./...
      - name: Container-free commands
        shell: bash
        run: |
          BIN="$RUNNER_TEMP/ltk${{ runner.os == 'Windows' && '.exe' || '' }}"
          go build -o "$BIN" ./cmd/ltk
          "$BIN" list corpus/ --json
          "$BIN" compare corpus/ corpus/
          "$BIN" compare testdata/compare/ledger-only/a testdata/compare/ledger-only/b
          if "$BIN" compare testdata/compare/reproduced-changed/a testdata/compare/reproduced-changed/b; then exit 1; fi
      - name: ltk check corpus/ (Linux)
        if: runner.os == 'Linux'
        run: go build -o "$RUNNER_TEMP/ltk" ./cmd/ltk && "$RUNNER_TEMP/ltk" check corpus/

  ci-green:
    needs: build-and-test
    if: always()
    runs-on: ubuntu-latest
    steps:
      - run: test "${{ needs.build-and-test.result }}" = success
`

// TestCIChecksAreNotVacuous: the workflow written from the contract passes every check, with LF or CRLF
// line endings; each mutation that breaks one rule fails with that rule's finding.
func TestCIChecksAreNotVacuous(t *testing.T) {
	for name, src := range map[string]string{"LF": contractWorkflow, "CRLF": strings.ReplaceAll(contractWorkflow, "\n", "\r\n")} {
		if f := ciFindings(src, "1.25.0"); len(f) != 0 {
			t.Errorf("the contract's workflow (%s) has findings: %q", name, f)
		}
	}
	checkStep := "      - name: ltk check corpus/ (Linux)\n"
	beforeAgg := "\n  ci-green:\n"
	mutations := []struct {
		name, old, new, finding string
	}{
		{"no push", "on:\n  push:\n", "on:\n  pull_request:\n", "no push trigger"},
		{"push on one branch", "  push:\n", "  push:\n    branches: [main]\n", "filtered by branches"},
		{"inline trigger without push", "on:\n  push:\n", "on: [pull_request]\n", "no push trigger"},
		{"no pull_request", "  pull_request:\n", "", "no pull_request trigger"},
		{"pull_request on one branch", "  pull_request:\n", "  pull_request:\n    branches: [main]\n", "the pull_request trigger is filtered by branches"},
		{"no permissions", "permissions:\n  contents: read\n", "", "no top-level permissions"},
		{"a write permission", "  contents: read\n", "  contents: write\n", "grants write"},
		{"write-all", "permissions:\n  contents: read\n", "permissions: write-all\n", "grants write"},
		{"a job granted write", "    runs-on: ${{ matrix.os }}\n", "    runs-on: ${{ matrix.os }}\n    permissions:\n      packages: write\n", "grants write"},
		{"two systems", "[ubuntu-latest, macos-latest, windows-latest]", "[ubuntu-latest, macos-latest]", "the matrix is"},
		{"fail-fast", "fail-fast: false", "fail-fast: true", "fail-fast"},
		{"conditional matrix job", "    runs-on: ${{ matrix.os }}\n", "    runs-on: ${{ matrix.os }}\n    if: github.ref == 'refs/heads/main'\n", "the matrix job is conditional"},
		{"autocrlf after checkout", "      - run: git config --global core.autocrlf false\n      - uses: actions/checkout@v4\n",
			"      - uses: actions/checkout@v4\n      - run: git config --global core.autocrlf false\n", "comes before the step it must follow"},
		{"no autocrlf", "      - run: git config --global core.autocrlf false\n", "", "core.autocrlf false"},
		{"autocrlf on Windows only", "      - run: git config --global core.autocrlf false\n",
			"      - if: runner.os == 'Windows'\n        run: git config --global core.autocrlf false\n", "is conditional"},
		{"Go 1.24", "go-version-file: go.mod", "go-version: '1.24'", "want 1.25"},
		{"no setup-go", "      - uses: actions/setup-go@v5\n        with:\n          go-version-file: go.mod\n", "", "no actions/setup-go"},
		{"no go build", "      - run: go build ./...\n", "", "no `go build ./...`"},
		{"no go test", "      - run: go test ./...\n", "", "no `go test ./...`"},
		{"go test on Linux only", "      - run: go test ./...\n", "      - if: runner.os == 'Linux'\n        run: go test ./...\n", "is conditional"},
		{"integration tier in CI", "      - run: go test ./...\n", "      - run: go test ./...\n      - run: go test -tags integration ./...\n", "integration tier"},
		{"no container-free step", "Container-free commands", "Commands", "no step named"},
		{"container-free step on Linux only", "      - name: Container-free commands\n", "      - name: Container-free commands\n        if: runner.os == 'Linux'\n", "is conditional"},
		{"container-free step not bash", "        shell: bash\n", "", "shell: bash"},
		{"binary in the repository", `BIN="$RUNNER_TEMP/ltk${{ runner.os == 'Windows' && '.exe' || '' }}"`, `BIN="./ltk"`, "into $RUNNER_TEMP"},
		{"list without --json", `"$BIN" list corpus/ --json`, `"$BIN" list corpus/`, "list corpus/ --json"},
		{"no self-compare", `          "$BIN" compare corpus/ corpus/` + "\n", "", "compare corpus/ corpus/"},
		{"ledger-only allowed to fail", `"$BIN" compare testdata/compare/ledger-only/a testdata/compare/ledger-only/b`,
			`"$BIN" compare testdata/compare/ledger-only/a testdata/compare/ledger-only/b || true`, "its failure must fail the step"},
		{"reproduced-changed expected to pass", `if "$BIN" compare testdata/compare/reproduced-changed/a testdata/compare/reproduced-changed/b; then exit 1; fi`,
			`"$BIN" compare testdata/compare/reproduced-changed/a testdata/compare/reproduced-changed/b`, "its exit 1 must be expected"},
		{"check on all three", "        if: runner.os == 'Linux'\n        run: go build -o \"$RUNNER_TEMP/ltk\" ./cmd/ltk && \"$RUNNER_TEMP/ltk\" check corpus/\n",
			"        run: go build -o \"$RUNNER_TEMP/ltk\" ./cmd/ltk && \"$RUNNER_TEMP/ltk\" check corpus/\n", "on a system other than Linux"},
		{"check on a Linux-or-macOS condition", "        if: runner.os == 'Linux'\n", "        if: runner.os == 'Linux' || runner.os == 'macOS'\n", "on a system other than Linux"},
		{"no check", `"$RUNNER_TEMP/ltk" check corpus/`, `"$RUNNER_TEMP/ltk" list corpus/`, "no step runs `ltk check corpus/` on Linux"},
		{"check without a build", `go build -o "$RUNNER_TEMP/ltk" ./cmd/ltk && "$RUNNER_TEMP/ltk" check corpus/`, `ltk check corpus/`, "no program built"},
		{"check allowed to fail", checkStep, checkStep + "        continue-on-error: true\n", "continue-on-error"},
		{"colima on macOS", beforeAgg, "\n      - run: brew install colima && colima start\n" + beforeAgg, "names a container engine"},
		{"docker setup action", beforeAgg, "\n      - uses: docker/setup-buildx-action@v3\n" + beforeAgg, "a container engine on macOS or Windows"},
		{"services on the matrix job", "    runs-on: ${{ matrix.os }}\n", "    runs-on: ${{ matrix.os }}\n    services:\n      db:\n        image: postgres\n", "`services:`"},
		{"a macOS job with docker", "\n  ci-green:\n", "\n  mac:\n    runs-on: macos-latest\n    steps:\n      - run: docker info\n\n  ci-green:\n", "names a container engine"},
		{"a Windows image on Linux", beforeAgg, "\n      - if: runner.os == 'Linux'\n        run: docker build --platform windows/amd64 .\n" + beforeAgg, "builds a Windows image"},
		{"no aggregate job", "  ci-green:\n", "  other:\n", "no aggregate job ci-green"},
		{"aggregate needs nothing", "    needs: build-and-test\n", "", "not the matrix job"},
		{"aggregate without always()", "    if: always()\n", "", "want always()"},
		{"aggregate judges nothing", `      - run: test "${{ needs.build-and-test.result }}" = success`, `      - run: echo done`, "fails unless"},
		{"cancel-in-progress", "name: ci\n", "name: ci\nconcurrency:\n  group: ci\n  cancel-in-progress: true\n", "cancel-in-progress"},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			src := strings.Replace(contractWorkflow, m.old, m.new, 1)
			if src == contractWorkflow {
				t.Fatalf("the mutation changed nothing: %q not found", m.old)
			}
			f := ciFindings(src, "1.25.0")
			if !slices.ContainsFunc(f, func(s string) bool { return strings.Contains(s, m.finding) }) {
				t.Errorf("no finding containing %q; findings %q", m.finding, f)
			}
		})
	}
	if f := ciFindings(contractWorkflow, "1.24.0"); len(f) != 1 || !strings.Contains(f[0], "not 1.25") {
		t.Errorf("go.mod at 1.24: findings %q, want the setup-go version", f)
	}
}

// TestCIWorkflow: the committed `.github/workflows/ci.yml` passes every check of ciFindings, as far as the file shows
// them, and every other workflow file the rules that hold in every file. The fixture pairs the workflow compares
// exist. Its run on GitHub's hosted runners is shown by the repository's own Actions runs, not here.
func TestCIWorkflow(t *testing.T) {
	b, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		t.Fatalf("the CI workflow: %v", err)
	}
	for _, finding := range ciFindings(string(b), goDirective(t)) {
		t.Errorf(".github/workflows/ci.yml: %s", finding)
	}
	entries, err := os.ReadDir(workflowFolder)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "ci.yml" || !(strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		other, err := os.ReadFile(filepath.Join(workflowFolder, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range everyWorkflowFindings(parseWorkflow(string(other))) {
			t.Errorf(".github/workflows/%s: %s", e.Name(), finding)
		}
	}
	for _, p := range []string{"ledger-only/a", "ledger-only/b", "reproduced-changed/a", "reproduced-changed/b"} {
		if _, err := os.Stat(filepath.Join(testdataFolder, "compare", filepath.FromSlash(p), "catalogue.json")); err != nil {
			t.Errorf("the workflow compares testdata/compare/%s, which has no catalogue: %v", p, err)
		}
	}
	if !t.Failed() {
		mj := ""
		for _, j := range parseWorkflow(string(b)).child("jobs").kids {
			if oses := j.child("strategy").child("matrix").child("os").list(); len(oses) != 0 {
				mj = fmt.Sprintf("%s on %q", j.key, oses)
			}
		}
		t.Logf("ci.yml: on every push and pull request, the token read-only, %s, fail-fast false, autocrlf before checkout, setup-go 1.25, build, test, the container-free commands on all three, ltk check corpus/ on Linux only, ci-green; no container engine on macOS or Windows, no Windows image, no cancel-in-progress. Its run on hosted runners is shown by the repository's own Actions runs.", mj)
	}
}
