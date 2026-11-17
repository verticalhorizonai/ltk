package main

// The Repository layout guard.
//
// Nothing sits at the repository's root except the seven folders (cmd, internal, schemas, corpus, repro, testdata,
// .github), the README, the licence and the Go module files, go.mod and go.sum beside it. The guard reads the top-level
// entries of the files git tracks (`git ls-files`, run from the module root, so .git itself is never an entry) and
// fails on anything else. It ignores exactly the build package's own files, which govern the build and are not part of
// the exported repository, and nothing else. Outside a git work tree (a container that mounts the module without its
// .git) it skips, saying why.
//
// The folders' contents are held on the file system, which needs no git: corpus/ holds the catalogue, its schema copy
// and each entry's XDR file and decoded JSON; schemas/ holds the schema and its embedding file only; repro/ holds the
// two reports and nothing else; internal/repro/ holds the two reproductions' code, testdata/ small corpora and
// .github/ the CI workflow.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/catalogue"
)

// packageOwnEntries are the build package's own files at the build workspace's root, exactly. They govern the build
// and are not part of the exported repository.
var packageOwnEntries = []string{
	"CLAUDE.md", "specs", "tasks", "tests", "agents", "tools", ".githooks", ".gitattributes", "manifest.json",
	"id-registry.json", "verification-reports", // the package and its build records
	"reconciliation-report.md", "source-fidelity.md", "swarm-init-prompt.md", // the package's generation reports
}

// repositoryRootEntries are the top-level entries the repository allows: the seven folders, the README, the licence
// and the Go module files (go.sum beside go.mod).
var repositoryRootEntries = []string{
	"cmd", "internal", "schemas", "corpus", "repro", "testdata", ".github",
	"README.md", "LICENSE", "go.mod", "go.sum",
}

// requiredRootEntries are the entries the repository cannot lack (go.sum is allowed, not required).
var requiredRootEntries = []string{
	"cmd", "internal", "schemas", "corpus", "repro", "testdata", ".github", "README.md", "LICENSE", "go.mod",
}

// committedReports are the only files of repro/.
var committedReports = []string{"repro/js-sdk-1766.json", "repro/wallet-backend-729.json"}

// gitFiles lists the files git knows under the module root, with forward slashes, relative to it: the tracked files,
// and with untracked also the untracked files git does not ignore. Outside a git work tree, or with no git, it skips
// the test, saying why.
func gitFiles(t *testing.T, untracked bool) []string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("skipped: no git on PATH; the guard reads the files git tracks")
	}
	probe := exec.Command(git, "rev-parse", "--is-inside-work-tree")
	probe.Dir = readmeModuleRoot
	if out, err := probe.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Skipf("skipped: the module root is not inside a git work tree (%s); the guard reads the files git tracks", strings.TrimSpace(string(out)))
	}
	args := []string{"ls-files", "-z"}
	if untracked {
		args = append(args, "--cached", "--others", "--exclude-standard")
	}
	cmd := exec.Command(git, args...)
	cmd.Dir = readmeModuleRoot
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, ee.Stderr)
		}
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	if len(files) == 0 {
		t.Fatal("git lists no file under the module root")
	}
	return files
}

// topEntry is a git path's top-level entry.
func topEntry(p string) string {
	top, _, _ := strings.Cut(p, "/")
	return top
}

// isPackageOwn says whether a git path lies in one of the build package's own entries.
func isPackageOwn(p string) bool { return slices.Contains(packageOwnEntries, topEntry(p)) }

// layoutFindings returns one finding per top-level entry of files that the repository does not allow, the build
// package's own entries ignored.
func layoutFindings(files []string) []string {
	seen := map[string]bool{}
	var f []string
	for _, p := range files {
		top := topEntry(p)
		if seen[top] || slices.Contains(packageOwnEntries, top) || slices.Contains(repositoryRootEntries, top) {
			continue
		}
		seen[top] = true
		f = append(f, top)
	}
	sort.Strings(f)
	return f
}

// TestRepositoryLayout: the tracked files' top-level entries are the ones the repository allows, the build package's
// own entries aside; every required entry is there; and repro/ holds exactly the two reports, tracked or not yet
// tracked.
func TestRepositoryLayout(t *testing.T) {
	tracked := gitFiles(t, false)
	if extra := layoutFindings(tracked); len(extra) != 0 {
		t.Errorf("the repository's root holds %q; the repository allows only %q (the package's own %q aside)", extra, repositoryRootEntries, packageOwnEntries)
	}
	for _, e := range requiredRootEntries {
		if _, err := os.Stat(filepath.Join(readmeModuleRoot, e)); err != nil {
			t.Errorf("the repository's root lacks %s: %v", e, err)
		}
	}
	var repro []string
	for _, p := range gitFiles(t, true) {
		if topEntry(p) == "repro" {
			repro = append(repro, p)
		}
	}
	sort.Strings(repro)
	if !slices.Equal(repro, committedReports) {
		t.Errorf("repro/ holds %q; want exactly %q and nothing else", repro, committedReports)
	}
}

// TestLayoutFindings proves the guard is not vacuous: an extra root file or folder is found once, the allowed and the
// package's own entries never; a package entry is ignored by its exact name only.
func TestLayoutFindings(t *testing.T) {
	files := []string{
		"README.md", "LICENSE", "go.mod", "go.sum", "cmd/ltk/main.go", "internal/x/y.go", "schemas/embed.go",
		"corpus/catalogue.json", "repro/js-sdk-1766.json", "testdata/a", ".github/workflows/ci.yml",
		"CLAUDE.md", "specs/a.md", "tasks/001.json", "tests/a.md", "agents/a.md", "tools/a.mjs", ".githooks/pre-commit",
		".gitattributes", "manifest.json", "id-registry.json", "verification-reports/a.txt",
		"reconciliation-report.md", "source-fidelity.md", "swarm-init-prompt.md",
		"Makefile", "docs/a.md", "docs/b.md", "CLAUDE.md.bak", "ltk",
	}
	if got, want := layoutFindings(files), []string{"CLAUDE.md.bak", "Makefile", "docs", "ltk"}; !slices.Equal(got, want) {
		t.Errorf("findings %q, want %q", got, want)
	}
	if len(packageOwnEntries) != 14 {
		t.Errorf("%d package entries; the build package has 14", len(packageOwnEntries))
	}
}

// TestRepositoryFoldersHoldWhatTheSpecSays: each folder holds what the repository promises, read from the file system,
// so it runs without git too.
func TestRepositoryFoldersHoldWhatTheSpecSays(t *testing.T) {
	root := readmeModuleRoot
	// corpus/: the catalogue, its schema copy, and per entry the XDR file and its decoded JSON beside it.
	cat, err := catalogue.ReadCatalogue(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"catalogue.json", "catalogue.schema.json"}
	for _, e := range cat.Entries {
		want = append(want, e.FilePath, strings.TrimSuffix(e.FilePath, path.Ext(e.FilePath))+".json")
	}
	sort.Strings(want)
	if got := filesUnder(t, filepath.Join(root, "corpus")); !slices.Equal(got, want) {
		t.Errorf("corpus/ holds %q, want %q", got, want)
	}
	if len(cat.Entries) != 3 {
		t.Errorf("corpus/ holds %d entries, want the three seed entries", len(cat.Entries))
	}
	// schemas/: the catalogue's JSON Schema and its embedding file only.
	if got := filesUnder(t, filepath.Join(root, "schemas")); !slices.Equal(got, []string{"catalogue.schema.json", "embed.go"}) {
		t.Errorf("schemas/ holds %q, want the schema and embed.go only", got)
	}
	// repro/: the two reports and nothing else.
	var reports []string
	for _, r := range committedReports {
		reports = append(reports, strings.TrimPrefix(r, "repro/"))
	}
	if got := filesUnder(t, filepath.Join(root, "repro")); !slices.Equal(got, reports) {
		t.Errorf("repro/ holds %q, want %q and nothing else", got, reports)
	}
	// The code of the two reproductions under internal/repro/, with the SDK reproduction's script; the CI workflow
	// under .github/; small corpora under testdata/, some broken on purpose.
	for _, p := range []string{
		"internal/repro/walletbackend/run.go", "internal/repro/jssdk/run.go", "internal/repro/jssdk/repro.mjs",
		".github/workflows/ci.yml", "testdata/corpus/valid/catalogue.json",
		"testdata/compare/ledger-only/a", "testdata/compare/reproduced-changed/b",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("the repository lacks a promised file: %v", err)
		}
	}
	if broken := filesUnder(t, filepath.Join(root, "testdata", "corpus")); len(broken) < 2 {
		t.Errorf("testdata/corpus holds %d files: a valid corpus and broken ones are expected", len(broken))
	}
}

// filesUnder lists the files under dir, relative to it, with forward slashes, sorted.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		files = append(files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}
