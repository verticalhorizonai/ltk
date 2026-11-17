package main

// The CI runner constraint test: no container engine is installed on the macOS or Windows runners, and no Windows
// image of any tool is built. It reads every workflow under .github/workflows/, not only ci.yml, and holds exactly that
// constraint:
//
//   - a job, or matrix leg, that runs on macOS or Windows carries no `container:` or `services:`, and none of its steps
//     that is not restricted to Linux by its `if:` uses a container-engine setup action (an action of docker/,
//     crazy-max/ghaction-setup-docker, douglascamata/setup-docker-macos-action, abiosoft/setup-colima) or runs a line
//     naming docker, podman, colima, nerdctl or buildah;
//   - no step of any job builds a Windows image: no `run:` line that builds with `--platform windows`, and no build
//     action given a Windows platform.
//
// It also holds two rules every workflow keeps (tokenPermissionFindings), and one of ci.yml (pullRequestFindings):
//
//   - the workflow's token may only read: a top-level `permissions` whose `contents` is `read`, and no permission
//     granted `write` (nor `write-all`) at the top or in any job;
//   - ci.yml runs on every pull request, with no filter, so a pull request from a fork is tested as a push is.
//
// It does not forbid Docker in the Linux job, whose `ltk check corpus/` runs the round trip in the quickstart container,
// a Dockerfile in the module, or any runner's name: the constraint is about engines on macOS and Windows, not about
// Docker as such. That the hosted runners indeed have no engine installed by the workflow, and that the jobs pass, is
// shown by the repository's own Actions runs.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	reEngineSetupAction = regexp.MustCompile(`^(docker/|crazy-max/ghaction-setup-docker|douglascamata/setup-docker-macos-action|abiosoft/setup-colima)`)
	reEngineName        = regexp.MustCompile(`(?i)\b(docker|podman|colima|nerdctl|buildah)\b`)
	reWindowsImageBuild = regexp.MustCompile(`(?i)\b(build|buildx)\b.*--platform[= ]+\S*windows`)
)

// jobSystems are the runner labels a job runs on: its matrix's os list when runs-on names it, else runs-on itself.
func jobSystems(job *yNode) []string {
	runsOn := job.text("runs-on")
	if strings.Contains(runsOn, "matrix.os") {
		return job.child("strategy").child("matrix").child("os").list()
	}
	if c := job.child("runs-on"); c != nil {
		return c.list()
	}
	return nil
}

// onMacOrWindows says whether a runner label is a macOS or Windows runner.
func onMacOrWindows(label string) bool {
	l := strings.ToLower(label)
	return strings.HasPrefix(l, "macos") || strings.HasPrefix(l, "windows") || l == "macos" || l == "windows"
}

// runnerConstraintFindings holds one workflow to the runner constraint; one finding per breach.
func runnerConstraintFindings(w workflow) []string {
	var f []string
	jobs := w.root.child("jobs")
	if jobs == nil {
		return nil
	}
	for _, job := range jobs.children() {
		macWin := slices.ContainsFunc(jobSystems(job), onMacOrWindows)
		if macWin {
			for _, k := range []string{"container", "services"} {
				if job.child(k) != nil {
					f = append(f, "job "+job.key+": `"+k+":` starts a container engine on macOS or Windows")
				}
			}
		}
		for _, s := range stepsOfJob(job) {
			if macWin && !reLinuxOnlyStep.MatchString(s.cond) {
				if reEngineSetupAction.MatchString(s.uses) {
					f = append(f, "job "+job.key+": uses "+s.uses+", a container engine on macOS or Windows")
				}
				for _, l := range s.run {
					if !strings.HasPrefix(l, "#") && reEngineName.MatchString(l) {
						f = append(f, "job "+job.key+": "+l+": names a container engine on macOS or Windows")
					}
				}
			}
			for _, l := range s.run {
				if reWindowsImageBuild.MatchString(l) {
					f = append(f, "job "+job.key+": "+l+": builds a Windows image")
				}
			}
			for _, k := range []string{"platform", "platforms"} {
				if strings.Contains(strings.ToLower(s.with[k]), "windows") {
					f = append(f, "job "+job.key+": "+k+" "+s.with[k]+": builds a Windows image")
				}
			}
		}
	}
	return f
}

// tokenPermissionFindings holds a workflow's token to read access: a top-level `permissions` with `contents: read`, and
// no `write` or `write-all` at the top or in a job.
func tokenPermissionFindings(w workflow) []string {
	var f []string
	top := w.root.child("permissions")
	switch {
	case top == nil:
		f = append(f, "no top-level permissions: the token gets the repository's default, which may be write")
	case top.value != "" || top.text("contents") != "read":
		f = append(f, "the top-level permissions are not `contents: read`")
	}
	perms := []*yNode{top}
	for _, job := range w.root.child("jobs").children() {
		perms = append(perms, job.child("permissions"))
	}
	for _, p := range perms {
		if p == nil {
			continue
		}
		if strings.Contains(p.value, "write") {
			f = append(f, fmt.Sprintf("line %d: permissions %s grants write", p.line, p.value))
		}
		for _, k := range p.kids {
			if unquoteYAML(k.value) == "write" {
				f = append(f, fmt.Sprintf("line %d: permissions %s: write grants write", k.line, k.key))
			}
		}
	}
	return f
}

// pullRequestFindings holds a workflow to running on every pull request, with no filter.
func pullRequestFindings(w workflow) []string {
	if pr := w.root.child("on").child("pull_request"); pr == nil || len(pr.kids) != 0 || pr.value != "" {
		return []string{"the workflow does not run on every pull request with no filter"}
	}
	return nil
}

// TestCIRunnersHaveNoContainerEngine, as far as the workflow files show it: no workflow
// installs or starts a container engine on macOS or Windows, and none builds a Windows image; the macOS and Windows
// legs build the tool and run its container-free tests and commands, and only Linux runs `ltk check corpus/`.
func TestCIRunnersHaveNoContainerEngine(t *testing.T) {
	dir := filepath.Join(readmeModuleRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		n++
		w := parseWorkflowFile(t, filepath.Join(dir, e.Name()))
		for _, finding := range append(runnerConstraintFindings(w), tokenPermissionFindings(w)...) {
			t.Errorf("%s: %s", e.Name(), finding)
		}
	}
	if n == 0 {
		t.Fatal("no workflow under .github/workflows")
	}
	// The macOS and Windows legs do the container-free work (the matrix and its steps, held by TestCIWorkflow).
	w := readWorkflow(t)
	for _, finding := range pullRequestFindings(w) {
		t.Errorf("ci.yml: %s", finding)
	}
	if systems := jobSystems(w.root.child("jobs").child("build-and-test")); !slices.Contains(systems, "macos-latest") || !slices.Contains(systems, "windows-latest") {
		t.Errorf("the build-and-test job runs on %q: macOS and Windows build and run the container-free tests", systems)
	}
}

// TestRunnerConstraintFindingsAreNotVacuous: each way of breaking the sentence is found, and Docker on Linux alone is
// not.
func TestRunnerConstraintFindingsAreNotVacuous(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	const unit = "      - name: Unit tests\n        run: go test ./...\n"
	if !strings.Contains(src, unit) {
		t.Fatal("ci.yml has no unit-test step to mutate")
	}
	for _, m := range []struct{ name, to, finding string }{
		{"brew install docker", unit + "      - run: brew install docker\n", "names a container engine"},
		{"colima start", unit + "      - run: colima start\n", "names a container engine"},
		{"setup action", unit + "      - uses: docker/setup-buildx-action@v3\n", "a container engine on macOS or Windows"},
		{"windows image", unit + "      - if: runner.os == 'Linux'\n        run: docker build --platform windows/amd64 .\n", "builds a Windows image"},
		{"windows platform", unit + "      - if: runner.os == 'Linux'\n        uses: docker/build-push-action@v6\n        with:\n          platforms: windows/amd64\n", "builds a Windows image"},
		{"services", "    runs-on: ${{ matrix.os }}\n    services:\n      db:\n        image: postgres\n", "`services:`"},
	} {
		from := unit
		if m.name == "services" {
			from = "    runs-on: ${{ matrix.os }}\n"
		}
		root, err := parseYAMLSubset(strings.Replace(src, from, m.to, 1))
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		f := runnerConstraintFindings(workflow{root: root})
		if !slices.ContainsFunc(f, func(s string) bool { return strings.Contains(s, m.finding) }) {
			t.Errorf("%s: findings %q, want one naming %q", m.name, f, m.finding)
		}
	}
	linux := unit + "      - if: runner.os == 'Linux'\n        run: docker version\n"
	root, err := parseYAMLSubset(strings.Replace(src, unit, linux, 1))
	if err != nil {
		t.Fatal(err)
	}
	if f := runnerConstraintFindings(workflow{root: root}); len(f) != 0 {
		t.Errorf("Docker in a Linux-only step is found: %q (Docker on Linux is allowed)", f)
	}
}

// TestTokenAndTriggerFindingsAreNotVacuous: a workflow without its top-level permissions, one granting write at the top
// or in a job, and ci.yml without its pull_request trigger are each found; the committed ci.yml has no finding.
func TestTokenAndTriggerFindingsAreNotVacuous(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	find := func(w workflow) []string { return append(tokenPermissionFindings(w), pullRequestFindings(w)...) }
	for _, m := range []struct{ name, from, to, finding string }{
		{"no permissions", "permissions:\n  contents: read\n", "", "no top-level permissions"},
		{"contents: write", "  contents: read\n", "  contents: write\n", "grants write"},
		{"write-all", "permissions:\n  contents: read\n", "permissions: write-all\n", "grants write"},
		{"a job granted write", "    runs-on: ${{ matrix.os }}\n", "    runs-on: ${{ matrix.os }}\n    permissions:\n      packages: write\n", "grants write"},
		{"no pull_request", "  pull_request:\n", "", "every pull request"},
		{"pull_request on one branch", "  pull_request:\n", "  pull_request:\n    branches: [main]\n", "every pull request"},
	} {
		if !strings.Contains(src, m.from) {
			t.Fatalf("%s: ci.yml has no %q to mutate", m.name, m.from)
		}
		root, err := parseYAMLSubset(strings.Replace(src, m.from, m.to, 1))
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		if f := find(workflow{root: root}); !slices.ContainsFunc(f, func(s string) bool { return strings.Contains(s, m.finding) }) {
			t.Errorf("%s: findings %q, want one naming %q", m.name, f, m.finding)
		}
	}
	root, err := parseYAMLSubset(src)
	if err != nil {
		t.Fatal(err)
	}
	if f := find(workflow{root: root}); len(f) != 0 {
		t.Errorf("the committed ci.yml: %q", f)
	}
}
