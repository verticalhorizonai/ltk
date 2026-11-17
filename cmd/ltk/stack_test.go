package main

// The Stack constraint test: the tool is built with Go 1.25, Stellar's Go SDK, Docker and GitHub Actions. It checks
// what the build is pinned to:
//
//   - Go 1.25: go.mod's go directive is 1.25 or 1.25.N, and a toolchain line, if any, names go1.25.N;
//   - Stellar's Go SDK: github.com/stellar/go-stellar-sdk is a direct requirement, and the --also-go
//     cross-check's codec (internal/generate/xdrcodec) imports its xdr package;
//   - Docker: every image reference in internal/orchestration/images.go is pinned by digest, never by tag, and the five
//     images the tool runs are those references (the two quickstarts, the Go build image, TimescaleDB, Node);
//   - GitHub Actions: .github/workflows/ci.yml exists and installs Go with actions/setup-go from go.mod's directive
//     (`go-version-file: go.mod`) or at 1.25.
//
// go.mod is read with a small line reader: the dependency guard allows no module beyond the two direct requirements.

import (
	"bufio"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/verticalhorizonai/ltk/internal/orchestration"
)

// The two direct requirements the stack allows besides the standard library: Stellar's Go SDK, and the JSON Schema
// validator the catalogue and report schemas are checked with.
const (
	stellarSDKModule = "github.com/stellar/go-stellar-sdk"
	jsonSchemaModule = "github.com/santhosh-tekuri/jsonschema/v6"
)

// goModFile is go.mod as the stack and dependency guards read it.
type goModFile struct {
	module    string
	goVersion string
	toolchain string
	direct    []string // modules required without `// indirect`
	indirect  []string
}

// readGoMod reads the module root's go.mod.
func readGoMod(t *testing.T) goModFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	return parseGoMod(string(b))
}

// parseGoMod reads the module, go, toolchain and require lines of a go.mod, in its line and block forms.
func parseGoMod(text string) goModFile {
	var m goModFile
	inRequire := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		code, comment, _ := strings.Cut(line, "//")
		fields := strings.Fields(code)
		indirect := strings.TrimSpace(comment) == "indirect"
		switch {
		case len(fields) == 0:
		case inRequire && fields[0] == ")":
			inRequire = false
		case inRequire:
			m.addRequire(fields[0], indirect)
		case fields[0] == "module" && len(fields) == 2:
			m.module = fields[1]
		case fields[0] == "go" && len(fields) == 2:
			m.goVersion = fields[1]
		case fields[0] == "toolchain" && len(fields) == 2:
			m.toolchain = fields[1]
		case fields[0] == "require" && len(fields) == 2 && fields[1] == "(":
			inRequire = true
		case fields[0] == "require" && len(fields) >= 3:
			m.addRequire(fields[1], indirect)
		}
	}
	return m
}

func (m *goModFile) addRequire(path string, indirect bool) {
	if indirect {
		m.indirect = append(m.indirect, path)
	} else {
		m.direct = append(m.direct, path)
	}
}

var (
	reGo125        = regexp.MustCompile(`^1\.25(\.[0-9]+)?$`)
	reToolchain125 = regexp.MustCompile(`^go1\.25\.[0-9]+$`)
	reDigestImage  = regexp.MustCompile(`^[a-z0-9._/-]+@sha256:[0-9a-f]{64}$`)
	reGoString     = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")
)

// TestStackGo125AndTheSDK: Go 1.25 and Stellar's Go SDK, whose xdr package the
// --also-go cross-check's codec uses.
func TestStackGo125AndTheSDK(t *testing.T) {
	m := readGoMod(t)
	if !reGo125.MatchString(m.goVersion) {
		t.Errorf("go.mod's go directive is %q, want 1.25 or 1.25.N", m.goVersion)
	}
	if m.toolchain != "" && !reToolchain125.MatchString(m.toolchain) {
		t.Errorf("go.mod's toolchain is %q, want go1.25.N", m.toolchain)
	}
	if !slices.Contains(m.direct, stellarSDKModule) {
		t.Errorf("go.mod's direct requirements %q lack Stellar's Go SDK, %s", m.direct, stellarSDKModule)
	}
	dir := filepath.Join(readmeModuleRoot, "internal", "generate", "xdrcodec")
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	usesXDR := false
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, imp := range f.Imports {
				usesXDR = usesXDR || imp.Path.Value == strconv.Quote(stellarSDKModule+"/xdr")
			}
		}
	}
	if !usesXDR {
		t.Errorf("internal/generate/xdrcodec does not import %s/xdr: the --also-go cross-check must use the SDK's XDR code", stellarSDKModule)
	}
}

// TestStackImagesPinnedByDigest: every string literal of images.go that names an
// image by digest or by the latest tag is a digest-pinned reference; there are five, and they are the images every
// command's image set names.
func TestStackImagesPinnedByDigest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(readmeModuleRoot, "internal", "orchestration", "images.go"))
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, lit := range reGoString.FindAllString(string(b), -1) {
		s, err := strconv.Unquote(lit)
		if err != nil || !strings.Contains(s, "@sha256:") && !strings.Contains(s, ":latest") {
			continue
		}
		if !reDigestImage.MatchString(s) {
			t.Errorf("images.go names the image %q, not pinned by digest", s)
		}
		if !slices.Contains(refs, s) {
			refs = append(refs, s)
		}
	}
	if len(refs) != 5 {
		t.Errorf("images.go pins %d images, want the five the tool runs (two quickstarts, Go, TimescaleDB, Node): %q", len(refs), refs)
	}
	for _, set := range [][]string{orchestration.GenerateImages, orchestration.CheckImages, orchestration.WalletBackendImages, orchestration.JsSdkImages} {
		for _, img := range set {
			if !slices.Contains(refs, img) {
				t.Errorf("the image %q of a command's image set is not one of images.go's pinned references", img)
			}
		}
	}
}

// TestStackGitHubActions: continuous integration is a GitHub Actions workflow that installs Go
// 1.25 with actions/setup-go.
func TestStackGitHubActions(t *testing.T) {
	wf := readWorkflow(t)
	ok := false
	for _, s := range wf.steps("build-and-test") {
		if strings.HasPrefix(s.uses, "actions/setup-go@") {
			ok = s.with["go-version-file"] == "go.mod" || strings.HasPrefix(s.with["go-version"], "1.25")
		}
	}
	if !ok {
		t.Error(".github/workflows/ci.yml does not install Go 1.25 with actions/setup-go (go-version-file: go.mod)")
	}
}

// TestParseGoMod holds the go.mod reader to both require forms and the indirect comment.
func TestParseGoMod(t *testing.T) {
	m := parseGoMod("module x\n\ngo 1.25.0\ntoolchain go1.25.9\n\nrequire a/b v1.0.0\nrequire (\n\tc/d v1 // indirect\n\te/f v2\n)\n")
	if m.module != "x" || m.goVersion != "1.25.0" || m.toolchain != "go1.25.9" || !slices.Equal(m.direct, []string{"a/b", "e/f"}) || !slices.Equal(m.indirect, []string{"c/d"}) {
		t.Errorf("%+v", m)
	}
}
