// Package schema builds shape-only JSON Schemas from the Go structs that
// already define a worker handoff: one property per JSON field, recursively
// through nested structs and slices, additionalProperties: false at every
// object level, and nothing else — no pattern, no minLength/maxLength, no
// cross-field rule (WND-97).
//
// That scope is deliberate, not partial. Probed live against both Claude
// Code and Codex, a value constraint the model cannot legitimately satisfy
// (a citation pattern when no line number is known) does not make it
// refuse — it fabricates a value that satisfies the grammar, which for a
// citation is worse than today: a schema-forced "file.go:0" passes every
// gate and sends the next reader to the wrong place. So shape is the whole
// job here; value semantics stay in the Go validators, which can
// drop-and-report a defect rather than force compliance with it.
//
// required is passed by the caller rather than derived, for the same
// reason: a field's absence is either structural (meaningless whichever
// branch the handoff takes — the one field that names the branch, such as
// "verdict" or "status") or conditional (required only on one branch, such
// as a rejection's reason). Reflection cannot tell those apart; the Go
// validator already does, deliberately, one level lower than this package
// looks. Requiring a conditional field would force the same fabrication the
// package doc above warns about, so FromStruct never infers "required" —
// it only ever states what the caller supplies, and nested objects (an
// array's item type) never carry a required list at all.
package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// FromStruct builds the shape-only schema for v, a struct or pointer to one.
// required names the top-level properties whose absence is meaningless on
// every branch the handoff can take — see the package doc for why that list
// is supplied, never inferred.
func FromStruct(v any, required ...string) json.RawMessage {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("schema: FromStruct wants a struct, got %s", t))
	}
	b, err := json.Marshal(objectNode(t, required))
	if err != nil {
		// Unreachable: objectNode only ever builds maps of strings, bools,
		// slices and further such maps — nothing json.Marshal can refuse.
		panic(fmt.Sprintf("schema: %s: %v", t, err))
	}
	return b
}

func objectNode(t reflect.Type, required []string) map[string]any {
	props := map[string]any{}
	for name, f := range jsonFields(t) {
		props[name] = typeNode(f.Type)
	}
	n := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		n["required"] = required
	}
	return n
}

func typeNode(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": typeNode(t.Elem())}
	case reflect.Struct:
		// Nested objects never carry "required" — see the package doc.
		return objectNode(t, nil)
	default:
		panic(fmt.Sprintf("schema: unsupported field kind %s (%s)", t.Kind(), t))
	}
}

// jsonFields returns t's exported JSON fields keyed by their JSON name,
// flattening embedded (anonymous) struct fields exactly as encoding/json
// does — a schema for Revision (which embeds Draft) must see Draft's fields
// at its own top level, not nested under a "draft" property nobody's
// handoff ever writes.
func jsonFields(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue // unexported
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			for k, v := range jsonFields(ft) {
				out[k] = v
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f
	}
	return out
}
