package luamerge_test

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge"
)

// testModulePath is the import path of package luamerge.
const testModulePath = "github.com/tarantool-contrib/go-moonscript/luamerge"

// testPublicType is a type of the public API with the methods it documents:
// those of a pointer to it, which include the type's own.
type testPublicType struct {
	typ     reflect.Type
	methods []string
}

// testPublicTypes are the types package luamerge declares, by name.
func testPublicTypes() map[string]testPublicType {
	return map[string]testPublicType{
		"Options":          {reflect.TypeFor[luamerge.Options](), []string{}},
		"TransformOptions": {reflect.TypeFor[luamerge.TransformOptions](), []string{}},
		"Result":           {reflect.TypeFor[luamerge.Result](), []string{}},
		"Warning":          {reflect.TypeFor[luamerge.Warning](), []string{}},
		"WarningKind":      {reflect.TypeFor[luamerge.WarningKind](), []string{"String"}},
		"UnresolvedError":  {reflect.TypeFor[luamerge.UnresolvedError](), []string{"Error", "Unwrap"}},
	}
}

// testPublicValues are the functions, variables and constants package
// luamerge declares, by name, with their types.
func testPublicValues() map[string]reflect.Type {
	return map[string]reflect.Type{
		"Bundle":              reflect.TypeOf(luamerge.Bundle),
		"DefaultOptions":      reflect.TypeOf(luamerge.DefaultOptions),
		"LoadOptions":         reflect.TypeOf(luamerge.LoadOptions),
		"ErrNoEntry":          reflect.TypeOf(&luamerge.ErrNoEntry).Elem(),
		"ErrModuleNotFound":   reflect.TypeOf(&luamerge.ErrModuleNotFound).Elem(),
		"WarnDynamicRequire":  reflect.TypeOf(luamerge.WarnDynamicRequire),
		"WarnSkipped":         reflect.TypeOf(luamerge.WarnSkipped),
		"WarnUnresolved":      reflect.TypeOf(luamerge.WarnUnresolved),
		"WarnCModule":         reflect.TypeOf(luamerge.WarnCModule),
		"WarnNonLua":          reflect.TypeOf(luamerge.WarnNonLua),
		"WarnIndirectRequire": reflect.TypeOf(luamerge.WarnIndirectRequire),
		"WarnShadowsBuiltin":  reflect.TypeOf(luamerge.WarnShadowsBuiltin),
		"WarnTokenScan":       reflect.TypeOf(luamerge.WarnTokenScan),
	}
}

// testMethodNames returns the names of the methods of typ, in order.
func testMethodNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumMethod())
	for method := range typ.Methods() {
		names = append(names, method.Name)
	}

	return names
}

// testInternalTypes returns the types of internal packages that typ is or
// reaches: through its exported fields, its elements, the methods of the type
// and of a pointer to it, and the parameters and results of a function.
func testInternalTypes(typ reflect.Type) []string {
	var found []string

	seen := make(map[reflect.Type]bool)

	var walk func(typ reflect.Type)

	walk = func(typ reflect.Type) {
		if seen[typ] {
			return
		}

		seen[typ] = true

		if strings.Contains("/"+typ.PkgPath()+"/", "/internal/") {
			found = append(found, typ.String())
		}

		for method := range typ.Methods() {
			walk(method.Type)
		}

		//nolint:exhaustive // Only the kinds made of other types lead on.
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
			walk(typ.Elem())
		case reflect.Map:
			walk(typ.Key())
			walk(typ.Elem())
		case reflect.Struct:
			for field := range typ.Fields() {
				if field.IsExported() {
					walk(field.Type)
				}
			}
		case reflect.Func:
			for in := range typ.Ins() {
				walk(in)
			}

			for out := range typ.Outs() {
				walk(out)
			}
		default:
		}

		// The methods with a pointer receiver.
		if typ.Name() != "" && typ.Kind() != reflect.Interface {
			walk(reflect.PointerTo(typ))
		}
	}

	walk(typ)

	return found
}

// TestPublicAPI_Types checks that every type of the public API is defined in
// package luamerge rather than aliased from another package, has the methods
// it documents and no others, and reaches no type of an internal package.
// The types used to be aliases of internal ones: go doc showed none of their
// fields, and methods of the configuration such as ResolveRoot could be
// called on Options.
func TestPublicAPI_Types(t *testing.T) {
	t.Parallel()

	for name, tt := range testPublicTypes() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testModulePath, tt.typ.PkgPath(), "the package that defines %s", tt.typ)
			assert.Equal(t, tt.methods, testMethodNames(reflect.PointerTo(tt.typ)), "the methods of *%s", tt.typ)
			assert.Empty(t, testInternalTypes(tt.typ), "internal types reachable from %s", tt.typ)
		})
	}
}

// TestPublicAPI_Values checks that no function, variable or constant of the
// public API takes, returns or holds a type of an internal package.
func TestPublicAPI_Values(t *testing.T) {
	t.Parallel()

	for name, typ := range testPublicValues() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, testInternalTypes(typ), "internal types reachable from %s", typ)
		})
	}
}

// TestPublicAPI_Declarations checks, on the package as go doc reads it, that
// it declares no type alias and that the names it exports are those that
// TestPublicAPI_Types and TestPublicAPI_Values check, so that a new one
// cannot bypass them.
func TestPublicAPI_Declarations(t *testing.T) {
	t.Parallel()

	// Arrange: the package's source files, without its tests.
	paths, err := filepath.Glob("*.go")
	require.NoError(t, err)

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(paths))

	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		require.NoError(t, err)

		files = append(files, file)
	}

	// Act.
	pkg, err := doc.NewFromFiles(fset, files, testModulePath)
	require.NoError(t, err)

	var exported, aliases []string

	addValues := func(values []*doc.Value) {
		for _, value := range values {
			exported = append(exported, value.Names...)
		}
	}
	addFuncs := func(funcs []*doc.Func) {
		for _, fn := range funcs {
			exported = append(exported, fn.Name)
		}
	}

	addValues(pkg.Consts)
	addValues(pkg.Vars)
	addFuncs(pkg.Funcs)

	for _, typ := range pkg.Types {
		exported = append(exported, typ.Name)

		addValues(typ.Consts)
		addValues(typ.Vars)
		addFuncs(typ.Funcs)

		for _, spec := range typ.Decl.Specs {
			if spec, ok := spec.(*ast.TypeSpec); ok && spec.Assign.IsValid() {
				aliases = append(aliases, spec.Name.Name)
			}
		}
	}

	// Assert.
	assert.Empty(t, aliases, "a type alias makes another package's type and its methods part of the API")
	assert.ElementsMatch(t,
		slices.Concat(slices.Collect(maps.Keys(testPublicTypes())), slices.Collect(maps.Keys(testPublicValues()))),
		exported, "the exported names")
}
