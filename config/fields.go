package config

import (
	"encoding"
	"reflect"
	"strings"
	"time"
)

type field struct {
	key        string
	def        string
	hasDefault bool
}

var (
	timeType            = reflect.TypeFor[time.Time]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// collectFields walks t and returns the dotted key of every leaf field, named
// the way mapstructure names them, so each can get a default and an env binding.
func collectFields(t reflect.Type, prefix string) []field {
	var fields []field
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, squash := keyName(sf)
		if name == "-" {
			continue
		}

		ft := sf.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}

		if ft.Kind() == reflect.Struct && !isLeaf(ft) {
			if squash {
				fields = append(fields, collectFields(ft, prefix)...)
			} else {
				fields = append(fields, collectFields(ft, prefix+name+".")...)
			}
			continue
		}

		def, ok := sf.Tag.Lookup("default")
		fields = append(fields, field{key: prefix + name, def: def, hasDefault: ok})
	}
	return fields
}

// keyName returns the field's key from its mapstructure tag, or its lowercased
// name when the tag has none, and whether the field is squashed into its parent.
func keyName(sf reflect.StructField) (string, bool) {
	tag := sf.Tag.Get("mapstructure")
	name, opts, _ := strings.Cut(tag, ",")
	squash := false
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == "squash" {
			squash = true
		}
	}
	if name == "" {
		name = sf.Name
	}
	return strings.ToLower(name), squash
}

// isLeaf reports whether a struct type is decoded as a single value.
func isLeaf(t reflect.Type) bool {
	return t == timeType || reflect.PointerTo(t).Implements(textUnmarshalerType)
}

func envName(prefix, key string) string {
	name := strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
	if prefix == "" {
		return name
	}
	return strings.ToUpper(prefix) + "_" + name
}
