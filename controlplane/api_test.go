package controlplane_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPublicAPISnapshot pins the exported Go API of the controlplane packages. The declarations (signatures,
// types, exported fields, constants; no bodies or comments) of every non-test file are printed in a stable order
// and compared with testdata/golden/api.txt. Methods of unexported types are left out and a type of another
// package is printed with its import path ("net/http".Request), not the alias of the file.
//
// A line that disappears or changes is a breaking change of a package with a semver promise: it needs a new major
// path, not an edit of the golden file. A line that is only added is compatible: run `task controlplane-api-update`
// (GOLDEN_UPDATE=1) and commit the file with the change.
func TestPublicAPISnapshot(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	for _, dir := range []string{".", "flagcheck", "s3publish"} {
		out.WriteString("## " + filepath.ToSlash(filepath.Join("controlplane", dir)) + "\n")

		for _, decl := range exportedDeclarations(t, dir) {
			out.WriteString(decl + "\n")
		}

		out.WriteString("\n")
	}

	checkGolden(t, "api.txt", []byte(out.String()))
}

// exportedDeclarations returns the printed exported declarations of the production files of dir, sorted.
func exportedDeclarations(t *testing.T, dir string) []string {
	t.Helper()

	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var decls []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoError(t, parseErr)

		if !ast.FileExports(file) {
			continue
		}

		imports := importNames(file)

		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if !receiverIsExported(fn) {
					continue
				}

				fn.Body = nil
			}

			qualify(decl, imports)

			var buf bytes.Buffer

			require.NoError(t, printer.Fprint(&buf, fset, decl))
			decls = append(decls, buf.String())
		}
	}

	slices.Sort(decls)

	return decls
}

// receiverIsExported is false for a method of an unexported type: it is not part of the API.
func receiverIsExported(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return true
	}

	expr := fn.Recv.List[0].Type

	for {
		switch typ := expr.(type) {
		case *ast.StarExpr:
			expr = typ.X
		case *ast.IndexExpr:
			expr = typ.X
		case *ast.IndexListExpr:
			expr = typ.X
		case *ast.Ident:
			return typ.IsExported()
		default:
			return true
		}
	}
}

// importNames maps the name a file uses for each import to the import path.
func importNames(file *ast.File) map[string]string {
	names := map[string]string{}

	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}

		var name string

		if spec.Name != nil {
			name = spec.Name.Name
		} else {
			name = path[strings.LastIndex(path, "/")+1:]
			name = strings.TrimSuffix(name, versionSuffix(name))
		}

		names[name] = path
	}

	return names
}

// versionSuffix returns the ".v3" of "yaml.v3", the only form of a version in a package name used here.
func versionSuffix(name string) string {
	idx := strings.LastIndex(name, ".v")
	if idx < 0 {
		return ""
	}

	return name[idx:]
}

// qualify rewrites every pkg.Name of a foreign type or value in decl to "import/path".Name, so that the snapshot
// names the package and not the alias a file happens to use.
func qualify(decl ast.Decl, imports map[string]string) {
	ast.Inspect(decl, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if ident, isIdent := sel.X.(*ast.Ident); isIdent {
			if path, known := imports[ident.Name]; known {
				ident.Name = strconv.Quote(path)
			}
		}

		return true
	})
}
