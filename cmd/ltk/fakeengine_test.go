package main

// The fake Docker engine of the unit tier: the fake docker of testdata/fakedocker, its call log and its state, for a
// test that calls the environment check or the interrupt handler in process, or runs the program.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEngine is one test's fake docker: its folder (the PATH), its call log and its state file.
type fakeEngine struct {
	t     *testing.T
	dir   string
	log   string
	state string
	env   []string
}

// fakeContainer is a container of the fake engine's state.
type fakeContainer struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

// newFakeEngine returns the fake docker with modes (FAKE_DOCKER_MODE) and, when corpus is not "", the corpus folder its
// stellar-xdr reads; it skips the test, saying why, when there is no fake (no go tool on this machine to build it).
func newFakeEngine(t *testing.T, modes, corpus string) *fakeEngine {
	t.Helper()
	dir := requireFakeDocker(t)
	work := t.TempDir()
	f := &fakeEngine{t: t, dir: dir, log: filepath.Join(work, "calls.jsonl"), state: filepath.Join(work, "state.json")}
	f.env = []string{"PATH=" + dir, "FAKE_DOCKER_LOG=" + f.log, "FAKE_DOCKER_STATE=" + f.state, "FAKE_DOCKER_MODE=" + modes}
	if corpus != "" {
		abs, err := filepath.Abs(corpus)
		if err != nil {
			t.Fatal(err)
		}
		f.env = append(f.env, "FAKE_DOCKER_CORPUS="+abs)
	}
	return f
}

// inProcess puts the fake on this process's PATH, with its variables, for the rest of the test.
func (f *fakeEngine) inProcess() {
	for _, kv := range f.env {
		k, v, _ := strings.Cut(kv, "=")
		f.t.Setenv(k, v)
	}
}

// seed puts containers in the fake engine before the run (another project's container, say).
func (f *fakeEngine) seed(cs ...fakeContainer) {
	f.t.Helper()
	b, err := json.Marshal(map[string]any{"containers": cs, "next": 0})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.state, b, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// calls returns the fake's calls, in order, each as its arguments.
func (f *fakeEngine) calls() [][]string {
	f.t.Helper()
	file, err := os.Open(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	var out [][]string
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, e.Args)
	}
	return out
}

// subcommands returns each call's leading words: "version", "image inspect", "pull", "run", "exec", "rm", "ps", ...
func (f *fakeEngine) subcommands() []string {
	var out []string
	for _, c := range f.calls() {
		switch {
		case len(c) == 0:
			out = append(out, "")
		case (c[0] == "image" || c[0] == "network" || c[0] == "volume") && len(c) > 1:
			out = append(out, c[0]+" "+c[1])
		default:
			out = append(out, c[0])
		}
	}
	return out
}

// containers returns the fake engine's containers now.
func (f *fakeEngine) containers() []fakeContainer {
	f.t.Helper()
	b, err := os.ReadFile(f.state)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var st struct {
		Containers []fakeContainer `json:"containers"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		f.t.Fatal(err)
	}
	return st.Containers
}
