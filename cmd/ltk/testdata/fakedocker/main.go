// Command docker is the fake `docker` of cmd/ltk's unit tests, which stands in for the Docker CLI and engine.
// TestMain builds it with the go tool into its own folder as docker (docker.exe on Windows); it is under testdata/, so
// `go build ./...`, `go list` and the Dependency guard never see it. Only the fake reads its variables; ltk never does.
//
//   - FAKE_DOCKER_LOG: a file to which every call appends one JSON line, {"args": [...]}.
//   - FAKE_DOCKER_MODE: a comma-separated set of modes (none: Docker with Linux containers and every image present):
//     no-daemon       `version` writes "Cannot connect to the Docker daemon ..." to stderr and exits 1 (no daemon)
//     hang-version    `version` never answers (the environment check's deadline)
//     windows         `version` prints "windows" (Windows containers, not Linux)
//     no-image        `image inspect` fails and `pull` fails: the image is absent and cannot be pulled
//     pull-ok         `image inspect` fails and `pull` succeeds
//     run-fails       `run` fails: a container that does not start
//     exec-hang       `exec` never answers (an interrupt test stops the program while it waits)
//     encode-hang     `exec … stellar-xdr encode` never answers: ltk generate's pipeline only decodes, its checks encode
//     rm-fails        `rm` fails and removes nothing (a teardown that leaves the container)
//     rm-wait         `rm` waits until the file FAKE_DOCKER_STATE + ".release" exists, then removes
//     npm-fails       `exec … npm ci …` fails as npm does without the registry
//   - FAKE_DOCKER_STATE: the JSON file holding the fake engine's containers between calls (default FAKE_DOCKER_LOG +
//     ".state"); a test may seed it with another project's container.
//   - FAKE_DOCKER_PORTS: for `ltk generate`'s quickstart, `image=host:port` pairs separated by ";": `port <container>`
//     prints the address of the container's image (a fake RPC and friendbot the test serves); `logs` prints
//     nothing and `inspect` says "running".
//   - FAKE_DOCKER_SDK: for `ltk repro js-sdk-1766`, a folder holding the reproduction script's line for each release's
//     container, as <role>.json (sdk-17-2-0.json, sdk-17-2-1.json): `exec <container> node …` prints the file named by
//     the container's role, or fails as node does when there is none; `exec <container> sh -c "… npm ci …"` succeeds
//     printing nothing (fails with mode npm-fails); `cp - <container>:<dir>` reads the tar stream and succeeds.
//   - FAKE_DOCKER_CORPUS: a corpus folder the fake `stellar-xdr` reads as its codec. `decode` of bytes equal to an
//     entry's <id>/entry.xdr prints that entry's <id>/entry.json; `encode` of a JSON form equal to an entry.json prints
//     its entry.xdr; anything else fails as the tool does ("error: xdr value invalid"). So a fixture corpus whose bytes
//     are not XDR round-trips, and its expected values are read from its decoded JSON. It may name several folders,
//     separated as PATH is (os.PathListSeparator).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// container is one container of the fake engine.
type container struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Image  string            `json:"image,omitempty"`
	Labels map[string]string `json:"labels"`
}

// state is the fake engine.
type state struct {
	Containers []container `json:"containers"`
	Next       int         `json:"next"`
}

var modes = map[string]bool{}

func main() {
	args := os.Args[1:]
	for _, m := range strings.Split(os.Getenv("FAKE_DOCKER_MODE"), ",") {
		if m = strings.TrimSpace(m); m != "" {
			modes[m] = true
		}
	}
	logCall(args)
	if len(args) == 0 {
		return
	}
	st := load()
	switch args[0] {
	case "version":
		switch {
		case modes["no-daemon"]:
			fail("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
		case modes["hang-version"]:
			time.Sleep(time.Hour)
		case modes["windows"]:
			fmt.Println("windows")
		default:
			fmt.Println("linux")
		}
	case "image":
		if modes["no-image"] || modes["pull-ok"] {
			fail("Error: No such image: " + last(args))
		}
		fmt.Println("sha256:fakeimage")
	case "pull":
		if modes["no-image"] {
			fail("Error response from daemon: pull access denied for " + last(args) + ", repository does not exist or may require 'docker login'")
		}
		fmt.Println("Status: Downloaded newer image for " + last(args))
	case "run":
		if modes["run-fails"] {
			fail("docker: Error response from daemon: fake create failure.")
		}
		c := container{ID: fmt.Sprintf("fakecontainer%04d", st.Next), Labels: map[string]string{}}
		st.Next++
		for i := 1; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--label" && i+1 < len(args):
				k, v, _ := strings.Cut(args[i+1], "=")
				c.Labels[k] = v
				i++
			case a == "--name" && i+1 < len(args):
				c.Name = args[i+1]
				i++
			case (a == "--network" || a == "-p" || a == "-e" || a == "--entrypoint") && i+1 < len(args):
				i++
			case strings.HasPrefix(a, "-"):
			default: // the image; what follows is the container's command
				c.Image = a
				i = len(args)
			}
		}
		st.Containers = append(st.Containers, c)
		save(st)
		fmt.Println(c.ID)
	case "ps":
		ps(st, args[1:])
	case "network", "volume":
		// The fake engine has no network or volume: every listing is empty, every removal succeeds.
	case "rm":
		rm(st, args[1:])
	case "exec":
		if sdkExec(args[1:]) {
			return
		}
		execTool(args[1:])
	case "cp":
		_, _ = io.ReadAll(os.Stdin)
	case "port":
		port(st, args[1:])
	case "logs":
	case "inspect":
		fmt.Println("running")
	default:
		fail("fake docker: unsupported call " + strings.Join(args, " "))
	}
}

// ps lists the containers: with -q and --filter label=k=v, the ids of those carrying the label; otherwise one line per
// container, "<id>\t<name>" (the Snapshot's --format).
func ps(st state, args []string) {
	quiet, filter := false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-q":
			quiet = true
		case "--filter":
			if i+1 < len(args) {
				filter = strings.TrimPrefix(args[i+1], "label=")
				i++
			}
		}
	}
	for _, c := range st.Containers {
		if filter != "" {
			k, v, _ := strings.Cut(filter, "=")
			if c.Labels[k] != v {
				continue
			}
		}
		if quiet {
			fmt.Println(c.ID)
		} else {
			fmt.Printf("%s\t%s\n", c.ID, c.Name)
		}
	}
}

// port prints the address FAKE_DOCKER_PORTS gives the image of the container named first in args
// (`port <container> 8000/tcp`).
func port(st state, args []string) {
	if len(args) == 0 {
		fail("fake docker: port needs a container")
	}
	for _, c := range st.Containers {
		if c.Name != args[0] && c.ID != args[0] {
			continue
		}
		for _, pair := range strings.Split(os.Getenv("FAKE_DOCKER_PORTS"), ";") {
			if img, addr, ok := strings.Cut(pair, "="); ok && img == c.Image {
				fmt.Println(addr)
				return
			}
		}
		fail("fake docker: no published port for the image " + c.Image)
	}
	fail("Error response from daemon: No such container: " + args[0])
}

// rm removes the named containers (`rm -f -v <id>...`).
func rm(st state, args []string) {
	if modes["rm-fails"] {
		fail("Error response from daemon: fake removal failure")
	}
	if modes["rm-wait"] {
		for {
			if _, err := os.Stat(statePath() + ".release"); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	gone := map[string]bool{}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			gone[a] = true
		}
	}
	var keep []container
	for _, c := range st.Containers {
		if !gone[c.ID] && !gone[c.Name] {
			keep = append(keep, c)
		}
	}
	st.Containers = keep
	save(st)
}

// sdkExec answers the js-sdk reproduction's two calls from FAKE_DOCKER_SDK (`exec -i <container> sh -c "… npm ci …"`
// and `exec -i <container> node …/repro.mjs …`) and reports whether it did.
func sdkExec(args []string) bool {
	dir := os.Getenv("FAKE_DOCKER_SDK")
	if dir == "" || len(args) < 3 || args[0] != "-i" {
		return false
	}
	container := args[1]
	switch {
	case args[2] == "sh" && strings.Contains(strings.Join(args, " "), "npm ci"):
		if modes["npm-fails"] {
			fail("npm error code EAI_AGAIN\nnpm error request to https://registry.npmjs.org/ failed")
		}
		return true
	case args[2] == "node":
		role := container[strings.LastIndex(container, "-sdk-")+1:]
		b, err := os.ReadFile(filepath.Join(dir, role+".json"))
		if err != nil {
			fail("Error: Cannot find module '@stellar/stellar-sdk'")
		}
		_, _ = os.Stdout.Write(b)
		return true
	}
	return false
}

// execTool answers `exec -i <container> stellar-xdr decode|encode --type <T> ...` from FAKE_DOCKER_CORPUS.
func execTool(args []string) {
	if modes["exec-hang"] || (modes["encode-hang"] && slicesContains(args, "encode")) {
		time.Sleep(time.Hour)
	}
	in, _ := io.ReadAll(os.Stdin)
	op := ""
	for i, a := range args {
		if a == "stellar-xdr" && i+1 < len(args) {
			op = args[i+1]
		}
	}
	var entries []string
	for _, corpus := range filepath.SplitList(os.Getenv("FAKE_DOCKER_CORPUS")) {
		found, _ := filepath.Glob(filepath.Join(corpus, "*", "entry.xdr"))
		entries = append(entries, found...)
	}
	for _, x := range entries {
		xb, err1 := os.ReadFile(x)
		jb, err2 := os.ReadFile(strings.TrimSuffix(x, ".xdr") + ".json")
		if err1 != nil || err2 != nil {
			continue
		}
		switch {
		case op == "decode" && bytes.Equal(in, xb):
			_, _ = os.Stdout.Write(jb)
			return
		case op == "encode" && bytes.Equal(in, jb):
			_, _ = os.Stdout.Write(xb)
			return
		}
	}
	fail("error: xdr value invalid")
}

func slicesContains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func last(args []string) string { return args[len(args)-1] }

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func statePath() string {
	if p := os.Getenv("FAKE_DOCKER_STATE"); p != "" {
		return p
	}
	if p := os.Getenv("FAKE_DOCKER_LOG"); p != "" {
		return p + ".state"
	}
	return ""
}

func load() state {
	var st state
	if p := statePath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &st)
		}
	}
	return st
}

func save(st state) {
	if p := statePath(); p != "" {
		b, _ := json.Marshal(st)
		_ = os.WriteFile(p, b, 0o644)
	}
}

func logCall(args []string) {
	p := os.Getenv("FAKE_DOCKER_LOG")
	if p == "" {
		return
	}
	line, _ := json.Marshal(map[string]any{"args": args})
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	f.Close()
}
