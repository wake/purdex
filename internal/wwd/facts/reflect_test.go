package facts

import (
	"reflect"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var timeType = reflect.TypeOf(time.Time{})

// fullFacts is a wire.Facts with every field non-zero: every pointer points,
// every slice has an element, every string is set.
func fullFacts() wire.Facts {
	var f wire.Facts
	fill(reflect.ValueOf(&f).Elem())
	return f
}

func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem())
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(v.Index(0))
	case reflect.Struct:
		if v.Type() == timeType {
			v.Set(reflect.ValueOf(time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			fill(v.Field(i))
		}
	case reflect.String:
		if v.Type().Name() == "Zone" {
			v.SetString("normal")
		} else if v.Type().Name() == "OwnerKind" {
			v.SetString("job")
		} else {
			v.SetString("s")
		}
	case reflect.Int, reflect.Int64:
		v.SetInt(3)
	case reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Bool:
		v.SetBool(true)
	}
}

func sameShape(a, b wire.Facts) bool { return reflect.DeepEqual(a, b) }

// aliased reports whether a and b share any pointer or slice backing array.
func aliased(a, b any) bool { return alias(reflect.ValueOf(a), reflect.ValueOf(b)) }

func alias(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Ptr:
		if a.IsNil() || b.IsNil() {
			return false
		}
		return a.Pointer() == b.Pointer() || alias(a.Elem(), b.Elem())
	case reflect.Slice:
		if a.Len() == 0 || b.Len() == 0 {
			return false
		}
		if a.Pointer() == b.Pointer() {
			return true
		}
		for i := 0; i < a.Len() && i < b.Len(); i++ {
			if alias(a.Index(i), b.Index(i)) {
				return true
			}
		}
	case reflect.Struct:
		if a.Type() == timeType {
			return false
		}
		for i := 0; i < a.NumField(); i++ {
			if alias(a.Field(i), b.Field(i)) {
				return true
			}
		}
	}
	return false
}
