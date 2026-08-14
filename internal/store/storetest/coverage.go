package storetest

import (
	"reflect"
	"sort"
	"strings"
	"time"
)

// UnpopulatedFields returns the fields of value that are zero everywhere they
// occur.
//
// It exists to keep a round-trip test honest. A round-trip proves that
// persistence preserves the fields the fixture sets, and says nothing about the
// ones it leaves at their zero value: a field that is never persisted and never
// populated round-trips perfectly. Asserting that this function returns nothing
// turns "the fixture happens to cover the mapping" into a checked property.
//
// Field paths ignore slice indexes, so a field set on one element of a slice
// counts as populated: a read-only repository binding legitimately has no
// branch, and requiring one everywhere would describe a task that cannot exist.
//
// Several values of one type may be passed, and a field any of them populates
// counts as covered. Some fields exclude each other — a task carries the reason
// it failed only while it is failed, and the fixture that is in review cannot
// also be — so coverage of those is a union over fixtures rather than a demand
// that one fixture hold a state no task could be in.
func UnpopulatedFields(values ...any) []string {
	populated := make(map[string]bool)
	for _, value := range values {
		walk("", reflect.ValueOf(value), populated)
	}

	var unpopulated []string
	for path, seen := range populated {
		if !seen {
			unpopulated = append(unpopulated, path)
		}
	}
	sort.Strings(unpopulated)
	return unpopulated
}

// timeType is treated as a leaf: a timestamp is populated or it is not, and its
// internal representation is not a field anyone persists.
var timeType = reflect.TypeOf(time.Time{})

func walk(path string, value reflect.Value, populated map[string]bool) {
	switch {
	case !value.IsValid():
		record(path, false, populated)

	case value.Kind() == reflect.Pointer, value.Kind() == reflect.Interface:
		if value.IsNil() {
			record(path, false, populated)
			return
		}
		record(path, true, populated)
		walk(path, value.Elem(), populated)

	case value.Type() == timeType:
		record(path, !value.Interface().(time.Time).IsZero(), populated)

	case value.Kind() == reflect.Struct:
		if path != "" {
			record(path, !value.IsZero(), populated)
		}
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			walk(join(path, field.Name), value.Field(i), populated)
		}

	case value.Kind() == reflect.Slice, value.Kind() == reflect.Map:
		record(path, value.Len() > 0, populated)
		if value.Kind() == reflect.Slice {
			for i := 0; i < value.Len(); i++ {
				walk(path, value.Index(i), populated)
			}
		}

	default:
		record(path, !value.IsZero(), populated)
	}
}

// record marks a field path as populated when any occurrence of it is non-zero.
func record(path string, nonZero bool, populated map[string]bool) {
	if path == "" {
		return
	}
	populated[path] = populated[path] || nonZero
}

func join(path, field string) string {
	if path == "" {
		return field
	}
	return strings.Join([]string{path, field}, ".")
}
