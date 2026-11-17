package contract

// The Contract parity test. It reads the TypeScript notation of record, when present beside the module, parses the
// non-test Go source of this package with go/parser, and asserts in both directions: every union's value set, every
// interface's property names (in order, with optional and nullable properties and the property types mapped to Go),
// the exit codes and every named constant. A second pass compares the package's run-time registry (registry.go,
// EXIT_CODES, CHECK_ORDER) with the notation. When the notation is absent, as it is in the exported repository, the
// tests skip and name the file.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const sharedTypesPath = "../../specs/shared-types.ts"

// nonLiteralTypes are the type aliases of the notation that are not unions of string literals. The parity test skips
// them as unions; the mirror holds JsonValue, JsonObject and ListOutput as Go aliases, ExitCode as an integer type, and
// ReproReport as no type (each reproduction writes its own report).
var nonLiteralTypes = []string{"ExitCode", "JsonObject", "JsonValue", "ListOutput", "ReproReport"}

// ─── The notation's reader ──────────────────────────────────────────────────────────────────────────────────────────

type tsProp struct {
	name     string
	optional bool
	typ      string
}

type tsModel struct {
	unions       map[string][]string // union name → values, in order
	nonLiteral   []string            // type aliases that are not unions of string literals, sorted
	interfaces   map[string][]tsProp // interface name → properties, in order
	consts       map[string]int      // numeric named constant → value
	exitCodes    map[string]int      // EXIT_CODES key → code
	arrays       map[string][]string // readonly array constant → values, in order
	arrayTypes   map[string]string   // readonly array constant → element type
	unrecognized []string            // export statements or members the reader could not read
}

// stripComments blanks every // and /* */ comment, keeping newlines and every string literal (a quote inside a
// comment never opens a string; a // inside a string never opens a comment).
func stripComments(src string) string {
	out := []byte(src)
	for i := 0; i < len(out); i++ {
		switch c := out[i]; {
		case c == '"' || c == '\'' || c == '`':
			for i++; i < len(out) && out[i] != c; i++ {
				if out[i] == '\\' {
					i++
				}
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			for ; i < len(out) && out[i] != '\n'; i++ {
				out[i] = ' '
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			end := strings.Index(string(out[i+2:]), "*/")
			stop := len(out)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			for ; i < stop; i++ {
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
			i--
		}
	}
	return string(out)
}

// splitTop splits s on sep where sep is outside quotes and brackets.
func splitTop(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\'':
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

var (
	reExportStart = regexp.MustCompile(`(?m)^export\s`)
	reTypeAlias   = regexp.MustCompile(`(?s)^export type (\w+)\s*=\s*(.*?);\s*$`)
	reInterface   = regexp.MustCompile(`(?s)^export interface (\w+)\s*\{(.*)\}\s*$`)
	reNumConst    = regexp.MustCompile(`(?s)^export const (\w+)\s*=\s*(\d+)\s+as\s+const\s*;\s*$`)
	reObjConst    = regexp.MustCompile(`(?s)^export const (\w+)\s*=\s*\{(.*)\}\s*as\s+const\s*;\s*$`)
	reArrayConst  = regexp.MustCompile(`(?s)^export const (\w+)\s*:\s*readonly\s+(\w+)\[\]\s*=\s*\[(.*)\]\s*as\s+const\s*;\s*$`)
	reLiteral     = regexp.MustCompile(`^(?:"([^"]*)"|'([^']*)')$`)
	reProperty    = regexp.MustCompile(`^(\w+)(\?)?\s*:\s*(.+?)$`)
	reObjEntry    = regexp.MustCompile(`^(\w+)\s*:\s*(\d+)$`)
)

// literal returns the value of a quoted string literal (either quote character).
func literal(s string) (string, bool) {
	m := reLiteral.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	if strings.HasPrefix(strings.TrimSpace(s), `"`) {
		return m[1], true
	}
	return m[2], true
}

func parseTS(src string) tsModel {
	m := tsModel{unions: map[string][]string{}, interfaces: map[string][]tsProp{}, consts: map[string]int{},
		exitCodes: map[string]int{}, arrays: map[string][]string{}, arrayTypes: map[string]string{}}
	code := stripComments(src)
	starts := reExportStart.FindAllStringIndex(code, -1)
	for k, s := range starts {
		end := len(code)
		if k+1 < len(starts) {
			end = starts[k+1][0]
		}
		stmt := strings.TrimSpace(code[s[0]:end])
		switch {
		case reTypeAlias.MatchString(stmt):
			g := reTypeAlias.FindStringSubmatch(stmt)
			var values []string
			isLiteral := true
			for _, member := range splitTop(g[2], '|') {
				if strings.TrimSpace(member) == "" {
					continue // the leading | of a multi-line union
				}
				v, ok := literal(member)
				if !ok {
					isLiteral = false
					break
				}
				values = append(values, v)
			}
			if isLiteral && len(values) > 0 {
				m.unions[g[1]] = values
			} else {
				m.nonLiteral = append(m.nonLiteral, g[1])
			}
		case reInterface.MatchString(stmt):
			g := reInterface.FindStringSubmatch(stmt)
			props := []tsProp{}
			for _, line := range strings.Split(strings.ReplaceAll(g[2], ";", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				p := reProperty.FindStringSubmatch(line)
				if p == nil {
					m.unrecognized = append(m.unrecognized, g[1]+": "+line)
					continue
				}
				props = append(props, tsProp{name: p[1], optional: p[2] == "?", typ: strings.TrimSpace(p[3])})
			}
			m.interfaces[g[1]] = props
		case reNumConst.MatchString(stmt):
			g := reNumConst.FindStringSubmatch(stmt)
			n, _ := strconv.Atoi(g[2])
			m.consts[g[1]] = n
		case reArrayConst.MatchString(stmt):
			g := reArrayConst.FindStringSubmatch(stmt)
			values := []string{}
			for _, member := range splitTop(g[3], ',') {
				if strings.TrimSpace(member) == "" {
					continue // a trailing comma
				}
				v, ok := literal(member)
				if !ok {
					m.unrecognized = append(m.unrecognized, g[1]+": "+strings.TrimSpace(member))
					continue
				}
				values = append(values, v)
			}
			m.arrays[g[1]], m.arrayTypes[g[1]] = values, g[2]
		case reObjConst.MatchString(stmt) && reObjConst.FindStringSubmatch(stmt)[1] == "EXIT_CODES":
			g := reObjConst.FindStringSubmatch(stmt)
			for _, member := range splitTop(g[2], ',') {
				member = strings.TrimSpace(member)
				if member == "" {
					continue
				}
				e := reObjEntry.FindStringSubmatch(member)
				if e == nil {
					m.unrecognized = append(m.unrecognized, "EXIT_CODES: "+member)
					continue
				}
				n, _ := strconv.Atoi(e[2])
				m.exitCodes[e[1]] = n
			}
		default:
			m.unrecognized = append(m.unrecognized, firstLine(stmt))
		}
	}
	sort.Strings(m.nonLiteral)
	return m
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func readTS(t *testing.T) (tsModel, string) {
	t.Helper()
	b, err := os.ReadFile(sharedTypesPath)
	if os.IsNotExist(err) {
		t.Skipf("%s is absent (it is not exported with the repository): no notation to hold the mirror to", sharedTypesPath)
	}
	if err != nil {
		t.Fatalf("read %s: %v", sharedTypesPath, err)
	}
	return parseTS(string(b)), string(b)
}

// ─── The mirror's reader ────────────────────────────────────────────────────────────────────────────────────────────

type goField struct {
	tag       string
	omitempty bool
	typ       string // the field's type expression, e.g. "*string", "[]ReproducedValue"
}

type goConst struct {
	typ   string // "" when untyped
	value string // unquoted string value, or number text
}

type goModel struct {
	stringTypes map[string]bool
	intTypes    map[string]bool
	aliases     map[string]string    // alias name → aliased type expression
	structs     map[string][]goField // exported struct → fields with json tags, in order
	consts      map[string]goConst
	vars        map[string]ast.Expr // exported var → its value expression
}

func exprString(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.ArrayType:
		if e.Len == nil {
			return "[]" + exprString(e.Elt)
		}
		return "[" + exprString(e.Len) + "]" + exprString(e.Elt)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.Ellipsis:
		return "..."
	}
	return fmt.Sprintf("%T", e)
}

func parseGo(srcs map[string]string) (goModel, error) {
	m := goModel{stringTypes: map[string]bool{}, intTypes: map[string]bool{}, aliases: map[string]string{},
		structs: map[string][]goField{}, consts: map[string]goConst{}, vars: map[string]ast.Expr{}}
	fset := token.NewFileSet()
	names := make([]string, 0, len(srcs))
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, srcs[name], 0)
		if err != nil {
			return m, err
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, s := range gd.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					if s.Assign.IsValid() {
						m.aliases[s.Name.Name] = exprString(s.Type)
						continue
					}
					switch typ := s.Type.(type) {
					case *ast.Ident:
						if typ.Name == "string" {
							m.stringTypes[s.Name.Name] = true
						} else {
							m.intTypes[s.Name.Name] = true
						}
					case *ast.StructType:
						fields := []goField{}
						for _, fld := range typ.Fields.List {
							if fld.Tag == nil {
								continue
							}
							tag, _ := strconv.Unquote(fld.Tag.Value)
							parts := strings.Split(reflect.StructTag(tag).Get("json"), ",")
							fields = append(fields, goField{tag: parts[0], omitempty: slices.Contains(parts[1:], "omitempty"),
								typ: exprString(fld.Type)})
						}
						m.structs[s.Name.Name] = fields
					}
				case *ast.ValueSpec:
					for i, n := range s.Names {
						if !n.IsExported() {
							continue
						}
						var value ast.Expr
						if i < len(s.Values) {
							value = s.Values[i]
						}
						if gd.Tok == token.VAR {
							m.vars[n.Name] = value
							continue
						}
						c := goConst{}
						if id, ok := s.Type.(*ast.Ident); ok {
							c.typ = id.Name
						}
						if lit, ok := value.(*ast.BasicLit); ok {
							c.value = lit.Value
							if lit.Kind == token.STRING {
								c.value, _ = strconv.Unquote(lit.Value)
							}
						}
						m.consts[n.Name] = c
					}
				}
			}
		}
	}
	return m, nil
}

func readGo(t *testing.T) (goModel, map[string]string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	srcs := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		srcs[f] = string(b)
	}
	m, err := parseGo(srcs)
	if err != nil {
		t.Fatal(err)
	}
	return m, srcs
}

// ─── The comparison ─────────────────────────────────────────────────────────────────────────────────────────────────

// constSuffix turns a notation value or key into the suffix of its Go name: usage_error → UsageError,
// CHANGE_TRUST_MALFORMED → ChangeTrustMalformed, wallet-backend-729 → WalletBackend729, 2a0a84ea → 2a0a84ea,
// 17.2.0 → 17_2_0, usageError → UsageError.
func constSuffix(s string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' }) {
		if strings.ToUpper(part) == part && strings.ToLower(part) != part {
			part = strings.ToLower(part)
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return strings.ReplaceAll(b.String(), ".", "_")
}

// goTypeFor maps a property type of the notation to the Go type expression the mirror must use.
func goTypeFor(ts tsModel, typ string) string {
	typ = strings.TrimSpace(typ)
	if members := splitTop(typ, '|'); len(members) == 2 && strings.TrimSpace(members[1]) == "null" {
		return "*" + goTypeFor(ts, members[0])
	}
	if strings.HasSuffix(typ, "[]") {
		return "[]" + goTypeFor(ts, strings.TrimSuffix(typ, "[]"))
	}
	if v, ok := literal(typ); ok {
		for name, values := range ts.unions {
			if slices.Contains(values, v) {
				return name
			}
		}
		return "?literal " + typ
	}
	if c, ok := strings.CutPrefix(typ, "typeof "); ok {
		if _, isConst := ts.consts[strings.TrimSpace(c)]; isConst {
			return "int"
		}
		return "?" + typ
	}
	switch typ {
	case "string":
		return "string"
	case "number":
		return "int"
	case "boolean":
		return "bool"
	}
	return typ
}

// allowedVars are the exported variables of the mirror: the notation's two non-scalar constants and the registry.
var allowedVars = []string{"CHECK_ORDER", "Constants", "EXIT_CODES", "ExitCodes", "Interfaces", "Unions"}

// parityProblems compares the notation with the mirror's source and returns every mismatch, in both directions.
func parityProblems(ts tsModel, g goModel) []string {
	var p []string
	add := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	// Unions: one string type each, its constants named Type+Suffix, the value sets equal.
	for u, values := range ts.unions {
		if !g.stringTypes[u] {
			add("union %s: no Go string type %s", u, u)
		}
		for _, v := range values {
			name := u + constSuffix(v)
			c, ok := g.consts[name]
			switch {
			case !ok:
				add("union %s: value %q has no Go constant %s", u, v, name)
			case c.typ != u || c.value != v:
				add("union %s: constant %s is %s %q, the notation %s %q", u, name, c.typ, c.value, u, v)
			}
		}
	}
	for name, c := range g.consts {
		if values, isUnion := ts.unions[c.typ]; isUnion {
			if !slices.Contains(values, c.value) || name != c.typ+constSuffix(c.value) {
				add("constant %s %s = %q: not a value of the notation's union %s %v", name, c.typ, c.value, c.typ, values)
			}
			continue
		}
		if c.typ != "" {
			add("constant %s: type %s is not a union of the notation", name, c.typ)
			continue
		}
		want, ok := ts.consts[name]
		switch {
		case !ok:
			add("constant %s: declared in Go, absent from the notation", name)
		case c.value != strconv.Itoa(want):
			add("constant %s: Go %s, the notation %d", name, c.value, want)
		}
	}
	for name, want := range ts.consts {
		if _, ok := g.consts[name]; !ok {
			add("constant %s = %d: missing in Go", name, want)
		}
	}
	for n := range g.stringTypes {
		if _, ok := ts.unions[n]; !ok {
			add("type %s: a Go string type with no union in the notation", n)
		}
	}

	// Non-literal types: ExitCode an integer type; JsonValue, JsonObject and ListOutput aliases.
	for n := range g.intTypes {
		if n != "ExitCode" {
			add("type %s: a Go integer type absent from the notation", n)
		}
	}
	if !g.intTypes["ExitCode"] || !slices.Contains(ts.nonLiteral, "ExitCode") {
		add("ExitCode: the notation's type alias and the Go integer type must both exist")
	}
	wantAliases := map[string]string{"JsonValue": "json.RawMessage", "JsonObject": "json.RawMessage", "ListOutput": "[]ListEntry"}
	for n, typ := range wantAliases {
		if !slices.Contains(ts.nonLiteral, n) {
			add("alias %s: absent from the notation", n)
		}
		if g.aliases[n] != typ {
			add("alias %s: Go %q, want %q", n, g.aliases[n], typ)
		}
	}
	for n := range g.aliases {
		if _, ok := wantAliases[n]; !ok {
			add("alias %s: declared in Go, absent from the mirror's rules", n)
		}
	}

	// Interfaces: one struct each, the json tags the property names in order, omitempty exactly on the optional
	// properties, the field types the notation's types mapped to Go.
	for n, props := range ts.interfaces {
		fields, ok := g.structs[n]
		if !ok {
			add("interface %s: no Go struct %s", n, n)
			continue
		}
		var tags, names []string
		for _, f := range fields {
			tags = append(tags, f.tag)
		}
		for _, pr := range props {
			names = append(names, pr.name)
		}
		if !slices.Equal(tags, names) {
			add("interface %s: Go json tags [%s], the notation [%s]", n, strings.Join(tags, ","), strings.Join(names, ","))
			continue
		}
		for i, pr := range props {
			f := fields[i]
			if f.omitempty != pr.optional {
				add("interface %s.%s: omitempty %v, optional in the notation %v", n, pr.name, f.omitempty, pr.optional)
			}
			want := goTypeFor(ts, pr.typ)
			if pr.optional && !strings.HasPrefix(want, "*") && !strings.HasPrefix(want, "[]") {
				want = "*" + want
			}
			if f.typ != want {
				add("interface %s.%s: Go type %s, the notation %q maps to %s", n, pr.name, f.typ, pr.typ, want)
			}
		}
	}
	for n := range g.structs {
		if _, ok := ts.interfaces[n]; !ok {
			add("struct %s: declared in Go, absent from the notation", n)
		}
	}

	// EXIT_CODES: the keyed composite literal's values equal the notation's.
	if lit, ok := g.vars["EXIT_CODES"].(*ast.CompositeLit); ok {
		got := map[string]int{}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if v, ok := kv.Value.(*ast.BasicLit); ok {
				n, _ := strconv.Atoi(v.Value)
				got[exprString(kv.Key)] = n
			}
		}
		want := map[string]int{}
		for k, v := range ts.exitCodes {
			want[constSuffix(k)] = v
		}
		if !reflect.DeepEqual(got, want) {
			add("EXIT_CODES: Go %v, the notation %v", got, want)
		}
	} else {
		add("EXIT_CODES: no Go variable with a keyed composite literal")
	}

	// Readonly arrays: the Go array's elements name the union constants of the notation's values, in order.
	for n, values := range ts.arrays {
		lit, ok := g.vars[n].(*ast.CompositeLit)
		if !ok {
			add("array %s: no Go variable with a composite literal", n)
			continue
		}
		var got []string
		for _, el := range lit.Elts {
			c := g.consts[exprString(el)]
			if c.typ != ts.arrayTypes[n] {
				add("array %s: element %s is not a %s constant", n, exprString(el), ts.arrayTypes[n])
			}
			got = append(got, c.value)
		}
		if !slices.Equal(got, values) {
			add("array %s: Go %v, the notation %v", n, got, values)
		}
	}
	for n := range g.vars {
		if !slices.Contains(allowedVars, n) {
			add("variable %s: declared in Go, absent from the notation and the registry", n)
		}
	}
	sort.Strings(p)
	return p
}

// runtimeProblems compares the package's run-time values (the registry, EXIT_CODES, CHECK_ORDER) with the notation.
func runtimeProblems(ts tsModel) []string {
	var p []string
	add := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }
	if !reflect.DeepEqual(Unions, ts.unions) {
		add("Unions: Go %v, the notation %v", Unions, ts.unions)
	}
	if !reflect.DeepEqual(ExitCodes, ts.exitCodes) {
		add("ExitCodes: Go %v, the notation %v", ExitCodes, ts.exitCodes)
	}
	exit := reflect.ValueOf(EXIT_CODES)
	for k, want := range ts.exitCodes {
		f := exit.FieldByName(constSuffix(k))
		if !f.IsValid() || int(f.Int()) != want {
			add("EXIT_CODES.%s: want %d", constSuffix(k), want)
		}
	}
	if exit.NumField() != len(ts.exitCodes) {
		add("EXIT_CODES: Go has %d fields, the notation %d keys", exit.NumField(), len(ts.exitCodes))
	}
	if !reflect.DeepEqual(Constants, ts.consts) {
		add("Constants: Go %v, the notation %v", Constants, ts.consts)
	}
	var order []string
	for _, c := range CHECK_ORDER {
		order = append(order, string(c))
	}
	if !slices.Equal(order, ts.arrays["CHECK_ORDER"]) {
		add("CHECK_ORDER: Go %v, the notation %v", order, ts.arrays["CHECK_ORDER"])
	}
	for n, props := range ts.interfaces {
		typ, ok := Interfaces[n]
		if !ok {
			add("Interfaces: no entry for %s", n)
			continue
		}
		var tags, names []string
		for i := range typ.NumField() {
			tags = append(tags, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
		}
		for _, pr := range props {
			names = append(names, pr.name)
		}
		if !slices.Equal(tags, names) {
			add("Interfaces[%s]: json tags %v, the notation %v", n, tags, names)
		}
		if typ.Name() != n {
			add("Interfaces[%s]: the Go type is %s", n, typ.Name())
		}
	}
	for n := range Interfaces {
		if _, ok := ts.interfaces[n]; !ok {
			add("Interfaces: %s is absent from the notation", n)
		}
	}
	sort.Strings(p)
	return p
}

// checkShape guards the reader: the notation's shape as of this mirror. A new declaration makes this fail, so a
// change to the notation is never read silently.
func checkShape(t *testing.T, ts tsModel) {
	t.Helper()
	if len(ts.unrecognized) > 0 {
		t.Errorf("the notation reader could not read: %q", ts.unrecognized)
	}
	if !slices.Equal(ts.nonLiteral, nonLiteralTypes) {
		t.Errorf("non-literal type aliases %v, want %v", ts.nonLiteral, nonLiteralTypes)
	}
	if len(ts.unions) != 10 || len(ts.interfaces) != 32 || len(ts.consts) != 5 || len(ts.exitCodes) != 4 || len(ts.arrays) != 1 {
		t.Errorf("the notation reader found %d unions, %d interfaces, %d constants, %d exit codes, %d arrays; want 10, 32, 5, 4, 1",
			len(ts.unions), len(ts.interfaces), len(ts.consts), len(ts.exitCodes), len(ts.arrays))
	}
}

// TestContractParity holds the mirror's source and its run-time registry to the TypeScript notation of record.
func TestContractParity(t *testing.T) {
	ts, _ := readTS(t)
	checkShape(t, ts)
	g, _ := readGo(t)
	for _, problem := range parityProblems(ts, g) {
		t.Error(problem)
	}
	for _, problem := range runtimeProblems(ts) {
		t.Error(problem)
	}
}

// TestContractParityNamedSpellings: the notation's own spellings appear in non-test source.
func TestContractParityNamedSpellings(t *testing.T) {
	_, srcs := readGo(t)
	var all strings.Builder
	for _, s := range srcs {
		all.WriteString(s)
	}
	for _, s := range []string{"EXIT_CODES", "CHANGE_TRUST_MALFORMED", "PRE_FIX_RETRY_LIMIT", "PRE_FIX_RESTART_COUNT",
		"WALLET_BACKEND_729_PROTOCOL_VERSION", "FALLBACK_PROTOCOL_VERSION", "CHECK_ORDER", "COMMIT_RUN_DEADLINE_MINUTES"} {
		if !strings.Contains(all.String(), s) {
			t.Errorf("%s: not in the non-test source of internal/contract", s)
		}
	}
}

// TestNotationReaderEdges drives the reader with every edge input the notation has or may grow: a multi-line union
// with a leading |, a one-value union, either quote character, non-literal aliases, quotes and a // inside comments,
// an optional property, typeof and literal property types, `as const`, comments inside EXIT_CODES, a readonly array
// with a trailing comma, and a comment holding a brace.
func TestNotationReaderEdges(t *testing.T) {
	src := `// header with "quotes" and 'apostrophes' — and a brace {
/** Doc with "CAP-0040" and a } brace. */
export type Multi =
  | "a_b"
  | 'c-d'
  | "E_F";

export type One = "ONLY_VALUE";
export type Num = (typeof EXIT_CODES)[keyof typeof EXIT_CODES];
export type Json =
  | string
  | { [key: string]: Json };
export type List = Item[];

export const EXIT_CODES = {
  /** Success: "ok", see http://example.org/x. */
  success: 0,
  // trailing comment, with a comma
  usageError: 2,
} as const;

export const LIMIT = 5 as const;

export const ORDER: readonly Multi[] = [
  "a_b",
  'c-d',
] as const;

export interface Item {
  /** The id, e.g. "x; y". */
  id: string;
  kind: "ONLY_VALUE";
  version: typeof LIMIT;
  maybe?: Item;
  gone: string | null;
  many: Multi[];
}
`
	ts := parseTS(src)
	if len(ts.unrecognized) > 0 {
		t.Fatalf("unrecognized: %q", ts.unrecognized)
	}
	if want := map[string][]string{"Multi": {"a_b", "c-d", "E_F"}, "One": {"ONLY_VALUE"}}; !reflect.DeepEqual(ts.unions, want) {
		t.Errorf("unions %v, want %v", ts.unions, want)
	}
	if want := []string{"Json", "List", "Num"}; !slices.Equal(ts.nonLiteral, want) {
		t.Errorf("non-literal %v, want %v", ts.nonLiteral, want)
	}
	if want := map[string]int{"success": 0, "usageError": 2}; !reflect.DeepEqual(ts.exitCodes, want) {
		t.Errorf("exit codes %v, want %v", ts.exitCodes, want)
	}
	if want := map[string]int{"LIMIT": 5}; !reflect.DeepEqual(ts.consts, want) {
		t.Errorf("consts %v, want %v", ts.consts, want)
	}
	if got := ts.arrays["ORDER"]; !slices.Equal(got, []string{"a_b", "c-d"}) || ts.arrayTypes["ORDER"] != "Multi" {
		t.Errorf("ORDER %v of %s", got, ts.arrayTypes["ORDER"])
	}
	want := []tsProp{{"id", false, "string"}, {"kind", false, `"ONLY_VALUE"`}, {"version", false, "typeof LIMIT"},
		{"maybe", true, "Item"}, {"gone", false, "string | null"}, {"many", false, "Multi[]"}}
	if !reflect.DeepEqual(ts.interfaces["Item"], want) {
		t.Errorf("Item %v, want %v", ts.interfaces["Item"], want)
	}
	for typ, wantGo := range map[string]string{`"ONLY_VALUE"`: "One", "typeof LIMIT": "int", "string | null": "*string",
		"Multi[]": "[]Multi", "number": "int", "boolean": "bool", "CheckReport | null": "*CheckReport"} {
		if got := goTypeFor(ts, typ); got != wantGo {
			t.Errorf("goTypeFor(%s) = %s, want %s", typ, got, wantGo)
		}
	}
	if got := parseTS("export function f() {}\n").unrecognized; len(got) != 1 {
		t.Errorf("an unknown export statement must be reported, got %q", got)
	}

	// The Go side: ",omitempty" is stripped from the tag name and recorded.
	g, err := parseGo(map[string]string{"x.go": "package x\ntype S struct {\n\tA *int `json:\"a,omitempty\"`\n\tB string `json:\"b\"`\n}\n"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []goField{{"a", true, "*int"}, {"b", false, "string"}}; !reflect.DeepEqual(g.structs["S"], want) {
		t.Errorf("S %v, want %v", g.structs["S"], want)
	}
	for in, want := range map[string]string{"usage_error": "UsageError", "CHANGE_TRUST_MALFORMED": "ChangeTrustMalformed",
		"wallet-backend-729": "WalletBackend729", "2a0a84ea": "2a0a84ea", "17.2.0": "17_2_0", "usageError": "UsageError",
		"re_encode": "ReEncode"} {
		if got := constSuffix(in); got != want {
			t.Errorf("constSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestContractParityFailsOnDrift proves the check bites: each change to a copy of the notation, and each
// change to a copy of the mirror's source, makes it report a problem.
func TestContractParityFailsOnDrift(t *testing.T) {
	_, src := readTS(t)
	g, goSrcs := readGo(t)
	notation := []struct{ name, from, to string }{
		{"union value changed", `"round_trip"`, `"roundtrip"`},
		{"union value added to a multi-line union", "  | \"transaction_result_set_hash\";", "  | \"transaction_result_set_hash\"\n  | \"ledger_seq\";"},
		{"one-value union changed", `"CHANGE_TRUST_MALFORMED"`, `"CHANGE_TRUST_LOW_RESERVE"`},
		{"property added", "  bytesCompared: number;\n}", "  bytesCompared: number;\n  offset: number;\n}"},
		{"property renamed", "  storeStatePath: string;", "  storePath: string;"},
		{"property made nullable", "  ledger: number;", "  ledger: number | null;"},
		{"optional property made required", "networkRefusalEvidence?:", "networkRefusalEvidence:"},
		{"exit code changed", "  usageError: 2,", "  usageError: 4,"},
		{"constant changed", "COMMIT_RUN_DEADLINE_MINUTES = 15 as const", "COMMIT_RUN_DEADLINE_MINUTES = 16 as const"},
		{"constant added", "export const PRE_FIX_RESTART_COUNT = 1 as const;", "export const PRE_FIX_RESTART_COUNT = 1 as const;\nexport const EXTRA = 2 as const;"},
		{"CHECK_ORDER reordered", "  \"round_trip\",\n  \"expected_values\",", "  \"expected_values\",\n  \"round_trip\","},
		{"interface added", "export type ReproReport", "export interface Extra {\n  x: string;\n}\n\nexport type ReproReport"},
	}
	for _, c := range notation {
		if !strings.Contains(src, c.from) {
			t.Fatalf("%s: the text %q is not in %s", c.name, c.from, sharedTypesPath)
		}
		ts := parseTS(strings.Replace(src, c.from, c.to, 1))
		if len(parityProblems(ts, g))+len(runtimeProblems(ts)) == 0 {
			t.Errorf("%s: the parity check passed a drifted notation", c.name)
		}
	}
	ts := parseTS(src)
	mirror := []struct{ name, from, to string }{
		{"union constant value changed", `ErrorCodeUsageError         ErrorCode = "usage_error"`, `ErrorCodeUsageError         ErrorCode = "usage-error"`},
		{"union constant removed", "\tFixRoleFix       FixRole = \"fix\"\n", ""},
		{"json tag renamed", "`json:\"bytesCompared\"`", "`json:\"bytes_compared\"`"},
		{"omitempty added", "`json:\"error\"`", "`json:\"error,omitempty\"`"},
		{"nullable made a value", "Check *CheckReport `json:\"check\"`", "Check CheckReport `json:\"check\"`"},
		{"exit code changed", "UsageError:         2,", "UsageError:         5,"},
		{"constant changed", "const PRE_FIX_RETRY_LIMIT = 5", "const PRE_FIX_RETRY_LIMIT = 6"},
		{"CHECK_ORDER reordered", "\tCheckNameRoundTrip,\n\tCheckNameExpectedValues,", "\tCheckNameExpectedValues,\n\tCheckNameRoundTrip,"},
	}
	for _, c := range mirror {
		found := false
		drifted := map[string]string{}
		for f, s := range goSrcs {
			if !found && strings.Contains(s, c.from) {
				s, found = strings.Replace(s, c.from, c.to, 1), true
			}
			drifted[f] = s
		}
		if !found {
			t.Fatalf("%s: the text %q is not in the mirror's source", c.name, c.from)
		}
		dg, err := parseGo(drifted)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(parityProblems(ts, dg)) == 0 {
			t.Errorf("%s: the parity check passed a drifted mirror", c.name)
		}
	}
}
