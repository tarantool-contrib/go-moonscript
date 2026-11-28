package luamerge

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luamerge/internal/config"
	"github.com/tarantool/go-luamerge/internal/graph"
)

// testConfigNames maps the name of a field of Options to the name of the
// field of the internal configuration that holds the same setting, where the
// two differ.
func testConfigNames() map[string]string {
	return map[string]string{"NoArgFix": "ArgFix"}
}

// testLeaves returns the fields of value, a struct, that are not structs
// themselves, by their dotted names, such as "Transform.Minify".
func testLeaves(value reflect.Value, prefix string) map[string]reflect.Value {
	leaves := make(map[string]reflect.Value)

	for field, fieldValue := range value.Fields() {
		name := prefix + field.Name

		if fieldValue.Kind() == reflect.Struct {
			maps.Copy(leaves, testLeaves(fieldValue, name+"."))
			continue
		}

		leaves[name] = fieldValue
	}

	return leaves
}

// testSetNonZero sets field, a string, a bool or a []string, to a value other
// than its zero value.
func testSetNonZero(t *testing.T, field reflect.Value) {
	t.Helper()

	//nolint:exhaustive // A setting is a string, a bool or a list; any other kind fails the test.
	switch field.Kind() {
	case reflect.String:
		field.SetString("x")
	case reflect.Bool:
		field.SetBool(true)
	case reflect.Slice:
		field.Set(reflect.ValueOf([]string{"x"}))
	default:
		require.Failf(t, "unexpected field", "no non-zero value for a field of kind %s", field.Kind())
	}
}

// testChanged returns the names of the fields that hold another value in got
// than in base, in order.
func testChanged(base, got map[string]reflect.Value) []string {
	var changed []string

	for name, field := range got {
		if !reflect.DeepEqual(base[name].Interface(), field.Interface()) {
			changed = append(changed, name)
		}
	}

	slices.Sort(changed)

	return changed
}

// TestConfigFromOptions_EveryField checks that each field of Options reaches
// the field of the internal configuration that holds the same setting, and
// no other one, so that Bundle neither drops a setting nor applies it as
// another.
func TestConfigFromOptions_EveryField(t *testing.T) {
	t.Parallel()

	var zero Options

	base := testLeaves(reflect.ValueOf(configFromOptions(zero)), "")

	for name := range testLeaves(reflect.ValueOf(zero), "") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			var opts Options

			testSetNonZero(t, testLeaves(reflect.ValueOf(&opts).Elem(), "")[name])

			want := name
			if configName, ok := testConfigNames()[name]; ok {
				want = configName
			}

			// Act.
			cfg := configFromOptions(opts)

			// Assert.
			assert.Equal(t, []string{want}, testChanged(base, testLeaves(reflect.ValueOf(cfg), "")))
		})
	}
}

// TestOptionsFromConfig_EveryField checks that each setting of the internal
// configuration reaches the field of Options that holds it, and no other
// one, so that LoadOptions neither drops a setting nor returns it as another.
func TestOptionsFromConfig_EveryField(t *testing.T) {
	t.Parallel()

	var zero config.Config

	base := testLeaves(reflect.ValueOf(optionsFromConfig(zero)), "")
	optionNames := make(map[string]string)

	for optionName, configName := range testConfigNames() {
		optionNames[configName] = optionName
	}

	for name := range testLeaves(reflect.ValueOf(zero), "") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			var cfg config.Config

			testSetNonZero(t, testLeaves(reflect.ValueOf(&cfg).Elem(), "")[name])

			want := name
			if optionName, ok := optionNames[name]; ok {
				want = optionName
			}

			// Act.
			opts := optionsFromConfig(cfg)

			// Assert.
			assert.Equal(t, []string{want}, testChanged(base, testLeaves(reflect.ValueOf(opts), "")))
		})
	}
}

// TestWarningsFromGraph_Kinds checks that every kind of the internal graph
// package converts by value to the WarningKind of the same name, as
// warningsFromGraph converts it, and that no value names a kind on one side
// only.
func TestWarningsFromGraph_Kinds(t *testing.T) {
	t.Parallel()

	known := 0

	for kind := range 64 {
		want := graph.WarningKind(kind).String()
		assert.Equal(t, want, WarningKind(kind).String(), "kind %d", kind)

		if want != "unknown" {
			known++
		}
	}

	assert.Positive(t, known, "the kinds compared")
}
