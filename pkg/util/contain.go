package util

import "reflect"

func Contains(list interface{}, elem interface{}) bool {
	values := reflect.ValueOf(list)

	if values.Kind() == reflect.Slice {
		for i := 0; i < values.Len(); i++ {
			item := values.Index(i).Interface()

			target := reflect.ValueOf(elem).Convert(reflect.TypeOf(item)).Interface()
			if ok := reflect.DeepEqual(item, target); ok {
				return true
			}
		}
	}
	return false
}
