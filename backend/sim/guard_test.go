package sim

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func checkSimulatorArchitecture(sources map[string]string) error {
	names := make([]string, 0, len(sources))
	for name := range sources {
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(names))
	for _, name := range names {
		file, err := parser.ParseFile(fset, name, sources[name], 0)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if path == "time" || path == "os" || path == "net" || strings.HasPrefix(path, "net/") {
				return fmt.Errorf("%s: forbidden simulator import %s", name, path)
			}
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return fmt.Errorf("no production simulator files inspected")
	}
	// Resolve aliases, dot imports, and shadowed names to their actual objects.
	info := &types.Info{Uses: make(map[*ast.Ident]types.Object)}
	conf := types.Config{Importer: importer.Default()}
	if _, err := conf.Check("sim", fset, files, info); err != nil {
		return fmt.Errorf("resolve simulator symbols: %w", err)
	}
	for _, file := range files {
		var violation error
		ast.Inspect(file, func(node ast.Node) bool {
			id, ok := node.(*ast.Ident)
			if !ok || violation != nil {
				return violation == nil
			}
			fn, ok := info.Uses[id].(*types.Func)
			if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "math/rand" {
				return true
			}
			if fn.Type().(*types.Signature).Recv() == nil && fn.Name() != "New" && fn.Name() != "NewSource" {
				// Reject function-value references too, so aliasing cannot hide a call.
				violation = fmt.Errorf("%s: forbidden package-level math/rand.%s", fset.Position(id.Pos()), fn.Name())
			}
			return violation == nil
		})
		if violation != nil {
			return violation
		}
	}
	return nil
}

func TestSimulatorArchitecture(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sources := make(map[string]string)
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[path] = string(data)
	}
	if err := checkSimulatorArchitecture(sources); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureGuardCases(t *testing.T) {
	for _, path := range []string{"time", "os", "net", "net/http", "net/url"} {
		t.Run(path, func(t *testing.T) {
			source := fmt.Sprintf("package sim; import forbidden %q; var _ = forbidden", path)
			err := checkSimulatorArchitecture(map[string]string{"sim.go": source})
			if err == nil || !strings.Contains(err.Error(), "forbidden simulator import "+path) {
				t.Fatalf("got %v, want forbidden import", err)
			}
		})
	}
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"global call", `package sim; import "math/rand"; func f() int { return rand.Intn(10) }`, true},
		{"aliased global", `package sim; import r "math/rand"; func f() int { return r.Intn(10) }`, true},
		{"dot global", `package sim; import . "math/rand"; func f() int { return Intn(10) }`, true},
		{"function value", `package sim; import r "math/rand"; var f = r.Intn`, true},
		{"owned RNG", `package sim; import r "math/rand"; func f() int { rng := r.New(r.NewSource(42)); return rng.Intn(10) }`, false},
		{"dot constructors", `package sim; import . "math/rand"; func f() int { return New(NewSource(42)).Intn(10) }`, false},
		{"shadowed alias", `package sim; import rand "math/rand"; func f() int { rand := rand.New(rand.NewSource(42)); return rand.Intn(10) }`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSimulatorArchitecture(map[string]string{
				"sim.go":          tc.source,
				"ignored_test.go": `package sim; import "os"; var _ = os.Stdout`,
			})
			if tc.reject && (err == nil || !strings.Contains(err.Error(), "forbidden package-level math/rand.")) {
				t.Fatalf("got %v, want package-level RNG rejection", err)
			}
			if !tc.reject && err != nil {
				t.Fatal(err)
			}
		})
	}
}
