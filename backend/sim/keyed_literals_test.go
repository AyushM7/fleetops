package sim

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const keyedLiteralSimPath = "github.com/AyushM7/fleetops/backend/sim"

type keyedLiteralImporter struct {
	types.Importer
	sim *types.Package
}

func (i keyedLiteralImporter) Import(path string) (*types.Package, error) {
	if path == keyedLiteralSimPath {
		return i.sim, nil
	}
	return i.Importer.Import(path)
}

func inspectKeyedLiterals(fset *token.FileSet, files []*ast.File, path string, imports types.Importer) (*types.Package, []string, error) {
	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
	conf := types.Config{Importer: imports}
	pkg, err := conf.Check(path, fset, files, info)
	if err != nil {
		return nil, nil, err
	}
	var violations []string
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			named, ok := types.Unalias(info.TypeOf(literal)).(*types.Named)
			if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != keyedLiteralSimPath {
				return true
			}
			switch named.Obj().Name() {
			case "State", "RobotState", "JobState", "StationState", "FaultState", "Config":
				for _, element := range literal.Elts {
					if _, keyed := element.(*ast.KeyValueExpr); !keyed {
						violations = append(violations, fmt.Sprintf("%s: positional %s literal", fset.Position(literal.Pos()), named.Obj().Name()))
						break
					}
				}
			}
			return true
		})
	}
	return pkg, violations, nil
}

func readKeyedLiteralFiles(t *testing.T, fset *token.FileSet, directory string) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find Go files in %s: %v (found %d)", directory, err, len(paths))
	}
	var files []*ast.File
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

func TestSimulatorKeyedLiterals(t *testing.T) {
	fset := token.NewFileSet()
	imports := keyedLiteralImporter{Importer: newSimulatorImporter()}
	for _, directory := range []string{".", filepath.Join("..", "cmd", "simrun")} {
		path := keyedLiteralSimPath
		if directory != "." {
			path = "github.com/AyushM7/fleetops/backend/cmd/simrun"
		}
		pkg, violations, err := inspectKeyedLiterals(fset, readKeyedLiteralFiles(t, fset, directory), path, imports)
		if err != nil {
			t.Fatal(err)
		}
		for _, violation := range violations {
			t.Error(violation)
		}
		if directory == "." {
			imports.sim = pkg
		}
	}
}

func TestKeyedLiteralGuardCases(t *testing.T) {
	for _, name := range []string{"State", "RobotState", "JobState", "StationState", "FaultState", "Config"} {
		for _, tc := range []struct {
			name, expression string
			reject           bool
		}{
			{name: "positional", expression: name + "{1}", reject: true},
			{name: "pointer", expression: "&" + name + "{1}", reject: true},
			{name: "slice", expression: "[]" + name + "{{1}}", reject: true},
			{name: "array", expression: "[1]" + name + "{{1}}", reject: true},
			{name: "map", expression: "map[string]" + name + "{\"x\": {1}}", reject: true},
			{name: "alias", expression: "Alias{1}", reject: true},
			{name: "keyed", expression: name + "{Value: 1}"},
			{name: "empty", expression: name + "{}"},
			{name: "unrelated", expression: "Point{1}"},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				source := fmt.Sprintf("package sim; type %s struct { Value int }; type Alias = %s; type Point struct { X int }; var _ = %s", name, name, tc.expression)
				fset := token.NewFileSet()
				file, err := parser.ParseFile(fset, "fixture.go", source, 0)
				if err != nil {
					t.Fatal(err)
				}
				pkg, violations, err := inspectKeyedLiterals(fset, []*ast.File{file}, keyedLiteralSimPath, importer.Default())
				if err != nil {
					t.Fatal(err)
				}
				want := []string(nil)
				if tc.reject {
					want = []string{"positional " + name + " literal"}
				}
				var got []string
				for _, violation := range violations {
					if !strings.HasPrefix(violation, "fixture.go:1:") {
						t.Fatalf("missing source location: %s", violation)
					}
					got = append(got, violation[strings.Index(violation, "positional "):])
				}
				if !slices.Equal(got, want) {
					t.Fatalf("violations = %v, want %v", got, want)
				}
				checkQualifiedKeyedLiteral(t, name, pkg)
			})
		}
	}
}

func checkQualifiedKeyedLiteral(t *testing.T, name string, pkg *types.Package) {
	t.Helper()
	fset := token.NewFileSet()
	source := fmt.Sprintf("package main; import model %q; var _ = model.%s{1}", keyedLiteralSimPath, name)
	file, err := parser.ParseFile(fset, "qualified.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	imports := keyedLiteralImporter{Importer: importer.Default(), sim: pkg}
	_, violations, err := inspectKeyedLiterals(fset, []*ast.File{file}, "main", imports)
	if err != nil || len(violations) != 1 || !strings.Contains(violations[0], "positional "+name+" literal") {
		t.Fatalf("qualified literal: violations = %v, error = %v", violations, err)
	}
}
