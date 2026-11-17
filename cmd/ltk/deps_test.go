package main

// The Dependency guard: the tool is one command-line program, with no server, no database and no web interface, and
// this guard shows it.
//
// From the source, with no tool (it runs everywhere):
//   - go.mod's direct requirements are exactly Stellar's Go SDK and the JSON Schema validator;
//   - every Go file of the module, tests included, imports only the standard library, the module's own packages, the SDK
//     and the validator: a test-only import would be a third requirement;
//   - no non-test file imports database/sql or a database driver, or uses the server symbols http.ListenAndServe,
//     http.ListenAndServeTLS, http.Serve, http.ServeTLS, http.Server, net.Listen, net.ListenPacket, net.ListenTCP,
//     net.ListenUDP or net.ListenUnix; net/http is imported only by internal/generate (the RPC client) and
//     internal/orchestration (readiness probing);
//   - the container-free packages (internal/contract, internal/model, internal/schema, internal/catalogue,
//     internal/generate/xdrcodec, schemas) never reach internal/orchestration, internal/generate (xdrcodec aside) or
//     internal/repro, not even transitively, and never import os/exec: they cannot start docker.
//
// With the go tool (it skips, saying why, without one): `go list -deps ./cmd/ltk` names the standard library, the
// module, the SDK, the validator and modules go.mod requires for them, and no other module.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// containerFreePackages are the pure layer, which never starts a container, by import path relative to the module.
var containerFreePackages = []string{
	"internal/contract", "internal/model", "internal/schema", "internal/catalogue", "internal/generate/xdrcodec", "schemas",
}

// databaseImport says whether an import path is database/sql or a database driver.
func databaseImport(p string) bool {
	if p == "database/sql" || strings.HasPrefix(p, "database/sql/") {
		return true
	}
	for _, d := range []string{"github.com/lib/pq", "github.com/jackc/pgx", "github.com/mattn/go-sqlite3", "modernc.org/sqlite",
		"github.com/go-sql-driver/mysql", "github.com/denisenkom/go-mssqldb", "github.com/microsoft/go-mssqldb",
		"go.mongodb.org/mongo-driver", "github.com/redis/go-redis", "go.etcd.io/bbolt", "github.com/dgraph-io/badger"} {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// serverSymbols are the server entry points of net/http and net.
var serverSymbols = map[string][]string{
	"net/http": {"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS", "Server"},
	"net":      {"Listen", "ListenPacket", "ListenTCP", "ListenUDP", "ListenUnix"},
}

// goSource is one parsed Go file of the module.
type goSource struct {
	rel     string // the file, relative to the module root, with forward slashes
	pkg     string // its directory, relative to the module root
	test    bool
	imports []string
	file    *ast.File
}

// moduleSources parses every Go file under root's cmd/, internal/ and schemas/, testdata/ folders aside.
func moduleSources(t *testing.T, root string) []goSource {
	t.Helper()
	var out []goSource
	fset := token.NewFileSet()
	for _, top := range []string{"cmd", "internal", "schemas"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			s := goSource{rel: filepath.ToSlash(rel), pkg: filepath.ToSlash(filepath.Dir(rel)), test: strings.HasSuffix(p, "_test.go"), file: f}
			for _, imp := range f.Imports {
				ip, _ := strconv.Unquote(imp.Path.Value)
				s.imports = append(s.imports, ip)
			}
			out = append(out, s)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no Go source under %s", root)
	}
	return out
}

// sourceGuardFindings holds a module's sources to the guard's source rules; module is its module path.
func sourceGuardFindings(sources []goSource, module string) []string {
	var f []string
	allowedThirdParty := []string{stellarSDKModule, jsonSchemaModule}
	httpUsers := []string{"internal/generate", "internal/orchestration"}
	graph := map[string][]string{} // non-test imports between the module's packages
	execUsers := map[string]bool{}
	for _, s := range sources {
		for _, ip := range s.imports {
			first, _, _ := strings.Cut(ip, "/")
			switch {
			case ip == module || strings.HasPrefix(ip, module+"/"):
				if !s.test {
					graph[s.pkg] = append(graph[s.pkg], strings.TrimPrefix(strings.TrimPrefix(ip, module), "/"))
				}
			case !strings.Contains(first, "."):
				// the standard library
			case !slices.ContainsFunc(allowedThirdParty, func(m string) bool { return ip == m || strings.HasPrefix(ip, m+"/") }):
				f = append(f, s.rel+": imports "+ip+", a module beyond the SDK and the validator")
			}
			if s.test {
				continue
			}
			if databaseImport(ip) {
				f = append(f, s.rel+": imports "+ip+", a database (the tool has no database)")
			}
			if ip == "net/http" && !slices.Contains(httpUsers, s.pkg) {
				f = append(f, s.rel+": imports net/http outside internal/generate and internal/orchestration")
			}
			if ip == "os/exec" {
				execUsers[s.pkg] = true
			}
		}
		if !s.test {
			f = append(f, serverSymbolFindings(s)...)
		}
	}
	for _, pure := range containerFreePackages {
		for _, reached := range reachable(graph, pure) {
			switch {
			case reached == "internal/orchestration" || strings.HasPrefix(reached, "internal/orchestration/"),
				reached == "internal/repro" || strings.HasPrefix(reached, "internal/repro/"),
				(reached == "internal/generate" || strings.HasPrefix(reached, "internal/generate/")) && reached != "internal/generate/xdrcodec":
				f = append(f, pure+" reaches "+reached+": a container-free package must not")
			}
			if execUsers[reached] {
				f = append(f, pure+" reaches "+reached+", which imports os/exec: a container-free package must not start a program")
			}
		}
	}
	return f
}

// serverSymbolFindings finds the server symbols a file uses, through whatever name it imports net/http or net under.
func serverSymbolFindings(s goSource) []string {
	names := map[string]string{} // local name -> import path
	for _, imp := range s.file.Imports {
		ip, _ := strconv.Unquote(imp.Path.Value)
		if _, ok := serverSymbols[ip]; !ok {
			continue
		}
		name := ip[strings.LastIndex(ip, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = ip
	}
	var f []string
	ast.Inspect(s.file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok {
			if ip, ok := names[x.Name]; ok && slices.Contains(serverSymbols[ip], sel.Sel.Name) {
				f = append(f, s.rel+": uses "+ip+"."+sel.Sel.Name+", a server (the tool runs no server)")
			}
		}
		return true
	})
	return f
}

// reachable is every package from reaches through graph, from itself aside, sorted.
func reachable(graph map[string][]string, from string) []string {
	seen := map[string]bool{from: true}
	queue := []string{from}
	var out []string
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, q := range graph[p] {
			if !seen[q] {
				seen[q] = true
				out = append(out, q)
				queue = append(queue, q)
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestDependencyGuardDirectRequirements: go.mod requires directly the SDK and
// the validator, and nothing else.
func TestDependencyGuardDirectRequirements(t *testing.T) {
	m := readGoMod(t)
	direct := slices.Clone(m.direct)
	slices.Sort(direct)
	if want := []string{jsonSchemaModule, stellarSDKModule}; !slices.Equal(direct, want) {
		t.Errorf("go.mod's direct requirements are %q, want exactly %q", direct, want)
	}
}

// TestDependencyGuardSource: the module's sources hold the guard's rules: no
// database, no server, no module beyond the two, net/http in its two packages only, and container-free packages that
// cannot reach Docker.
func TestDependencyGuardSource(t *testing.T) {
	sources := moduleSources(t, readmeModuleRoot)
	for _, finding := range sourceGuardFindings(sources, readGoMod(t).module) {
		t.Error(finding)
	}
	pure := 0
	for _, s := range sources {
		if slices.Contains(containerFreePackages, s.pkg) && !s.test {
			pure++
		}
	}
	if pure == 0 {
		t.Error("no source of the container-free packages was read")
	}
}

// TestDependencyGuardFindsADatabaseAndAServer: the guard is not vacuous, and fails on an import of database/sql. A
// module whose command imports database/sql, starts a server and imports another module, and whose container-free
// package reaches the orchestration, is found on each count.
func TestDependencyGuardFindsADatabaseAndAServer(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cmd/x/main.go", "package main\n\nimport (\n\t\"database/sql\"\n\tweb \"net/http\"\n\t\"github.com/lib/pq\"\n)\n\nvar _ = sql.Open\nvar _ = pq.Driver{}\n\nfunc main() { _ = web.ListenAndServe(\":80\", nil); var s web.Server; _ = s }\n")
	write("internal/catalogue/c.go", "package catalogue\n\nimport _ \"m/internal/orchestration\"\n")
	write("internal/orchestration/o.go", "package orchestration\n\nimport (\n\t\"net\"\n\t\"os/exec\"\n)\n\nvar _ = exec.Command\nvar _, _ = net.Listen(\"tcp\", \":0\")\n")
	f := sourceGuardFindings(moduleSources(t, root), "m")
	for _, want := range []string{
		"cmd/x/main.go: imports database/sql, a database",
		"cmd/x/main.go: imports github.com/lib/pq, a module beyond",
		"cmd/x/main.go: imports github.com/lib/pq, a database",
		"cmd/x/main.go: imports net/http outside",
		"cmd/x/main.go: uses net/http.ListenAndServe, a server",
		"cmd/x/main.go: uses net/http.Server, a server",
		"internal/orchestration/o.go: uses net.Listen, a server",
		"internal/catalogue reaches internal/orchestration",
		"internal/catalogue reaches internal/orchestration, which imports os/exec",
	} {
		if !slices.ContainsFunc(f, func(s string) bool { return strings.HasPrefix(s, want) }) {
			t.Errorf("no finding %q in %q", want, f)
		}
	}
}

// TestDependencyGuardGoList: `go list -deps ./cmd/ltk` names the standard
// library, the module, the SDK, the validator and modules go.mod requires for them, and nothing else. It needs the go
// tool; without one it skips, saying why (the source rules above still run).
func TestDependencyGuardGoList(t *testing.T) {
	goBin := goTool()
	if goBin == "" {
		t.Skip("skipped: no go tool on PATH or under runtime.GOROOT() to run `go list -deps ./cmd/ltk`; the source rules of TestDependencyGuardSource hold without it")
	}
	m := readGoMod(t)
	cmd := exec.Command(goBin, "list", "-deps", "-f", "{{if not .Standard}}{{with .Module}}{{.Path}}{{end}}{{end}}", "./cmd/ltk")
	cmd.Dir = readmeModuleRoot
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list -deps ./cmd/ltk: %v\n%s", err, stderr)
	}
	allowed := append([]string{m.module, stellarSDKModule, jsonSchemaModule}, m.indirect...)
	seen := map[string]bool{}
	for _, mod := range strings.Fields(string(out)) {
		seen[mod] = true
		if !slices.Contains(allowed, mod) {
			t.Errorf("ltk depends on the module %s, which is neither the SDK, the validator nor one go.mod requires for them", mod)
		}
	}
	for _, mod := range []string{m.module, stellarSDKModule, jsonSchemaModule} {
		if !seen[mod] {
			t.Errorf("go list -deps ./cmd/ltk does not name %s", mod)
		}
	}
	mods := make([]string, 0, len(seen))
	for mod := range seen {
		mods = append(mods, mod)
	}
	sort.Strings(mods)
	t.Logf("modules of ./cmd/ltk: %s", strings.Join(mods, ", "))
}
