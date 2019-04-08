package msgprpc

import (
	"reflect"
)

// ConvertToInterfaceSlice is a helper function to convert a typed slice to []interface{}{}
// Example:
// 	v := ConvertToInterfaceSlice([]float64{1.1, 1.2, 1.3})
func ConvertToInterfaceSlice(from interface{}) []interface{} {
	fv := reflect.ValueOf(from)
	if fv.Kind() != reflect.Slice {
		panic("from isn't a slice")
	}

	ln := fv.Len()
	out := make([]interface{}, ln, ln)
	for i := 0; i < ln; i++ {
		out[i] = fv.Index(i).Interface()
	}

	return out
}

// ConvertFromInterfaceSlice is a helper function to convert from an `[]interface{}` slice to a typed slice.
// Example:
// 	v := ConvertFromInterfaceSlice([]interface{}{1.1, 1.2, 1.3}, []float64(nil)).([]float64)
func ConvertFromInterfaceSlice(from, to interface{}) interface{} {
	fv, tt := reflect.ValueOf(from), reflect.TypeOf(to)
	if fv.Kind() != reflect.Slice || fv.Type().Elem().Kind() != reflect.Interface {
		panic("from isn't a []interface{} slice")
	}

	if tt.Kind() != reflect.Slice {
		panic("to isn't a slice")
	}

	ln := fv.Len()

	nt := reflect.MakeSlice(tt, ln, ln)

	for i := 0; i < ln; i++ {
		x := fv.Index(i).Interface()
		nt.Index(i).Set(reflect.ValueOf(x))
	}

	return nt
}
