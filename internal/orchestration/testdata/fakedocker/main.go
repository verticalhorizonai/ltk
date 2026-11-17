// Command docker is the fake `docker` of internal/orchestration's unit tests. TestMain builds it with the go tool
// found on PATH or under runtime.GOROOT(); it is under testdata/, so `go build ./...` and the Dependency guard never
// see it.
//
// It appends one JSON line per call to the file FAKE_DOCKER_LOG: {"args": [...], "stdin": "<base64>"} (stdin only for
// `exec -i` and `cp -`). It answers from the rules file FAKE_DOCKER_RULES, a JSON array of
//
//	{"prefix": ["image", "inspect"], "stdout": "...", "stderr": "...", "exit": 1, "times": 1, "echoStdin": false}
//
// The first rule whose prefix matches the call's leading arguments answers; a rule with "times" > 0 answers that many
// calls and is then spent (the count is kept in FAKE_DOCKER_RULES + ".state"). No rule: exit 0 with no output.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type rule struct {
	Prefix    []string `json:"prefix"`
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr"`
	Exit      int      `json:"exit"`
	Times     int      `json:"times"`
	EchoStdin bool     `json:"echoStdin"`
}

func main() {
	args := os.Args[1:]
	var stdin []byte
	if len(args) >= 2 && ((args[0] == "exec" && args[1] == "-i") || (args[0] == "cp" && args[1] == "-")) {
		stdin, _ = io.ReadAll(os.Stdin)
	}
	if logPath := os.Getenv("FAKE_DOCKER_LOG"); logPath != "" {
		entry := map[string]any{"args": args}
		if stdin != nil {
			entry["stdin"] = base64.StdEncoding.EncodeToString(stdin)
		}
		line, _ := json.Marshal(entry)
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.Write(append(line, '\n'))
			f.Close()
		}
	}
	rulesPath := os.Getenv("FAKE_DOCKER_RULES")
	if rulesPath == "" {
		return
	}
	b, err := os.ReadFile(rulesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake docker:", err)
		os.Exit(125)
	}
	var rules []rule
	if err := json.Unmarshal(b, &rules); err != nil {
		fmt.Fprintln(os.Stderr, "fake docker:", err)
		os.Exit(125)
	}
	statePath := rulesPath + ".state"
	used := map[int]int{}
	if sb, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(sb, &used)
	}
	for i, r := range rules {
		if !hasPrefix(args, r.Prefix) || (r.Times > 0 && used[i] >= r.Times) {
			continue
		}
		if r.Times > 0 {
			used[i]++
			sb, _ := json.Marshal(used)
			_ = os.WriteFile(statePath, sb, 0o644)
		}
		if r.EchoStdin {
			_, _ = os.Stdout.Write(stdin)
		}
		fmt.Fprint(os.Stdout, r.Stdout)
		fmt.Fprint(os.Stderr, r.Stderr)
		os.Exit(r.Exit)
	}
}

func hasPrefix(args, prefix []string) bool {
	if len(prefix) > len(args) {
		return false
	}
	for i, p := range prefix {
		if args[i] != p {
			return false
		}
	}
	return true
}
