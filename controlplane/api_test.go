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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPublicAPISnapshot pins the exported Go API of the controlplane packages. The declarations (signatures,
// types, exported fields, constants; no bodies or comments) of every non-test file are printed in a stable order
// and compared with testdata/golden/api.txt.
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

		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				fn.Body = nil
			}

			var buf bytes.Buffer

			require.NoError(t, printer.Fprint(&buf, fset, decl))
			decls = append(decls, buf.String())
		}
	}

	slices.Sort(decls)

	return decls
}
