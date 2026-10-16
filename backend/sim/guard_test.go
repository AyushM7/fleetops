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
	conf := types.Config{Importer: newSimulatorImporter()}
	pkg, err := conf.Check("sim", fset, files, info)
	if err != nil {
		return fmt.Errorf("resolve simulator symbols: %w", err)
	}
	if err := checkExportedSimMethods(pkg); err != nil {
		return err
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

type simulatorImporter struct {
	types.Importer
	local map[string]*types.Package
}

func newSimulatorImporter() *simulatorImporter {
	return &simulatorImporter{Importer: importer.Default(), local: make(map[string]*types.Package)}
}

func (i *simulatorImporter) Import(path string) (*types.Package, error) {
	var directory string
	switch path {
	case "github.com/AyushM7/fleetops/backend/routing":
		directory = "routing"
	case "github.com/AyushM7/fleetops/backend/scheduler":
		directory = "scheduler"
	default:
		return i.Importer.Import(path)
	}
	if pkg := i.local[path]; pkg != nil {
		return pkg, nil
	}
	paths, err := filepath.Glob(filepath.Join("..", directory, "*.go"))
	if err != nil || len(paths) == 0 {
		return nil, fmt.Errorf("locate %s sources: %v (found %d)", directory, err, len(paths))
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range paths {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	conf := types.Config{Importer: i.Importer}
	pkg, err := conf.Check(path, fset, files, nil)
	if err == nil {
		i.local[path] = pkg
	}
	return pkg, err
}

func checkExportedSimMethods(pkg *types.Package) error {
	sim := pkg.Scope().Lookup("Sim")
	if sim == nil {
		return nil
	}
	allowed := make(map[string]*types.Func)
	for _, name := range []string{"Reader", "Commander", "Driver"} {
		role := pkg.Scope().Lookup(name)
		if role == nil {
			return fmt.Errorf("missing simulator role %s", name)
		}
		contract, ok := role.Type().Underlying().(*types.Interface)
		if !ok {
			return fmt.Errorf("simulator role %s must be an interface", name)
		}
		contract.Complete()
		for i := 0; i < contract.NumMethods(); i++ {
			method := contract.Method(i)
			allowed[method.Name()] = method
		}
	}
	methods := types.NewMethodSet(types.NewPointer(sim.Type()))
	for i := 0; i < methods.Len(); i++ {
		method := methods.At(i).Obj().(*types.Func)
		if !method.Exported() || method.Name() == "Tick" {
			continue
		}
		contract := allowed[method.Name()]
		if contract == nil || !types.Identical(method.Type(), contract.Type()) {
			return fmt.Errorf("unexpected exported Sim method %s: absent from or incompatible with role interfaces", method.Name())
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

func TestArchitectureGuardResolvesRouting(t *testing.T) {
	source := `package sim; import "github.com/AyushM7/fleetops/backend/routing"; var _ = routing.FindPath`
	if err := checkSimulatorArchitecture(map[string]string{"fixture.go": source}); err != nil {
		t.Fatal(err)
	}
	imports := newSimulatorImporter()
	const path = "github.com/AyushM7/fleetops/backend/routing"
	first, err := imports.Import(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := imports.Import(path)
	if err != nil || first != second {
		t.Fatalf("routing importer did not preserve package identity: %v", err)
	}
}

func TestArchitectureGuardResolvesScheduler(t *testing.T) {
	const path = "github.com/AyushM7/fleetops/backend/scheduler"
	source := fmt.Sprintf("package sim; import %q; var _ = scheduler.Assign", path)
	if err := checkSimulatorArchitecture(map[string]string{"fixture.go": source}); err != nil {
		t.Fatal(err)
	}
	imports := newSimulatorImporter()
	first, err := imports.Import(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := imports.Import(path)
	if err != nil || first != second {
		t.Fatalf("scheduler importer did not preserve package identity: %v", err)
	}
}

func TestExportedSimMethodGuardCases(t *testing.T) {
	const fixture = `package sim
type Reader interface { Snapshot() int }
type Commander interface { Reader; SetCharging() }
type Driver interface { Reader; Inject() }
type Sim struct{}
func (Sim) Snapshot() int { return 0 }
func (*Sim) SetCharging() {}
func (*Sim) Inject() {}
func (*Sim) Tick() {}
`
	for _, tc := range []struct {
		name, extra, want string
	}{
		{name: "accounted methods"},
		{name: "unexported helper", extra: "func (*Sim) helper() {}"},
		{name: "unexpected pointer method", extra: "func (*Sim) Unexpected() {}", want: "unexpected exported Sim method Unexpected"},
		{name: "unexpected value method", extra: "func (Sim) Other() {}", want: "unexpected exported Sim method Other"},
		{name: "unrelated exported method", extra: "type Other struct{}; func (Other) Extra() {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSimulatorArchitecture(map[string]string{"fixture.go": fixture + tc.extra})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("guard = %v, want %s", err, tc.want)
			}
		})
	}
	t.Run("incompatible signature", func(t *testing.T) {
		source := strings.Replace(fixture, "func (Sim) Snapshot() int { return 0 }", "func (Sim) Snapshot() string { return \"\" }", 1)
		err := checkSimulatorArchitecture(map[string]string{"fixture.go": source})
		if err == nil || !strings.Contains(err.Error(), "unexpected exported Sim method Snapshot") {
			t.Fatalf("guard = %v, want incompatible Snapshot rejection", err)
		}
	})
}
