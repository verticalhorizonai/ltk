package main

// The registry test: the registry against the commands of the command contract of record, when present beside the
// module.

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

const apiContractsPath = "../../specs/api-contracts.md"

// pending lists the contract's commands that are not registered yet, each with the work that registers it. A command's
// line is removed when it is registered; the list is empty when every command is.
var pending = map[string]string{}

var (
	reTableRow   = regexp.MustCompile("(?m)^\\| `ltk ([a-z0-9 -]+)` \\|")
	reCmdSection = regexp.MustCompile("(?m)^### `ltk ([a-z0-9 -]+)`\\s*$")
)

// readContract reads the contract: the command names of the Master Command Table, and each command definition
// section's synopsis block (the lines between the ``` fences after **Synopsis**).
func readContract(t *testing.T) (table []string, synopses map[string][]string) {
	t.Helper()
	b, err := os.ReadFile(apiContractsPath)
	if os.IsNotExist(err) {
		t.Skipf("%s is absent (the repository is exported without it): no contract to hold the registry to", apiContractsPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(b), "\r\n", "\n")
	head, tableText, ok := strings.Cut(doc, "## Master Command Table")
	if !ok {
		t.Fatalf("%s has no Master Command Table", apiContractsPath)
	}
	for _, m := range reTableRow.FindAllStringSubmatch(tableText, -1) {
		table = append(table, m[1])
	}
	synopses = map[string][]string{}
	locs := reCmdSection.FindAllStringSubmatchIndex(head, -1)
	for i, loc := range locs {
		name := head[loc[2]:loc[3]]
		end := len(head)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		section := head[loc[1]:end]
		_, afterLabel, ok := strings.Cut(section, "**Synopsis**\n")
		if !ok {
			t.Fatalf("%s: the section of ltk %s has no **Synopsis**", apiContractsPath, name)
		}
		_, block, ok := strings.Cut(afterLabel, "```\n")
		if !ok {
			t.Fatalf("%s: the synopsis of ltk %s has no code block", apiContractsPath, name)
		}
		body, _, ok := strings.Cut(block, "```")
		if !ok {
			t.Fatalf("%s: the synopsis block of ltk %s is not closed", apiContractsPath, name)
		}
		if _, dup := synopses[name]; dup {
			t.Fatalf("%s: two sections define ltk %s", apiContractsPath, name)
		}
		synopses[name] = strings.Split(strings.TrimRight(body, "\n"), "\n")
	}
	return table, synopses
}

// TestRegistryMatchesContract: every command of the Master Command Table is registered or pending;
// nothing outside the table is registered; each registered command's synopsis is the contract's, exactly; the
// router's list of contract commands is the table's.
func TestRegistryMatchesContract(t *testing.T) {
	table, synopses := readContract(t)
	if len(table) != 6 || len(synopses) != 6 {
		t.Fatalf("the contract reader found %d table rows %v and %d command sections; want 6 and 6", len(table), table, len(synopses))
	}
	for _, n := range table {
		if _, ok := synopses[n]; !ok {
			t.Errorf("the table's command ltk %s has no definition section with a synopsis", n)
		}
	}
	all := slices.Clone(table)
	sort.Strings(all)
	var sections []string
	for n := range synopses {
		sections = append(sections, n)
	}
	sort.Strings(sections)
	if !slices.Equal(sections, all) {
		t.Errorf("the command sections %v differ from the table's commands %v", sections, all)
	}
	router := slices.Clone(contractCommands)
	sort.Strings(router)
	if !slices.Equal(router, all) {
		t.Errorf("the router's contract commands %v, the contract's %v", router, all)
	}

	reg := newRegistry()
	registered := map[string]bool{}
	for _, n := range reg.names() {
		registered[n] = true
		want, inContract := synopses[n]
		if !inContract || !slices.Contains(table, n) {
			t.Errorf("ltk %s is registered but the Master Command Table has no such command", n)
			continue
		}
		if got := reg.commands[n].synopsis; !slices.Equal(got, want) {
			t.Errorf("ltk %s: synopsis %q, the contract's %q", n, got, want)
		}
	}
	for _, n := range all {
		task, isPending := pending[n]
		switch {
		case registered[n] && isPending:
			t.Errorf("ltk %s is registered: remove it from the pending list (task %s)", n, task)
		case !registered[n] && !isPending:
			t.Errorf("ltk %s of the contract is neither registered nor pending on its task", n)
		}
	}
	for n := range pending {
		if !slices.Contains(table, n) {
			t.Errorf("pending command ltk %s is not a command of the Master Command Table", n)
		}
	}
}
