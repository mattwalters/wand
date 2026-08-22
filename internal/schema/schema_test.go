package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/mattwalters/wand/internal/schema"
)

// node is a loosely-typed view of a generated schema, convenient for
// assertions without re-implementing a JSON Schema parser in the test.
type node struct {
	Type                 string                     `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties *bool                      `json:"additionalProperties"`
	Items                json.RawMessage            `json:"items"`
}

func decode(t *testing.T, raw json.RawMessage) node {
	t.Helper()
	var n node
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("generated schema is not valid JSON: %v\n%s", err, raw)
	}
	return n
}

type leaf struct {
	Type string `json:"type"`
}

func decodeLeaf(t *testing.T, raw json.RawMessage) leaf {
	t.Helper()
	var l leaf
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatalf("generated property schema is not valid JSON: %v\n%s", err, raw)
	}
	return l
}

// Objection mirrors internal/plan's shape: the ticket's own worked example
// for how a nested array-of-object schema must be built, so this test holds
// the load-bearing case (WND-45: additionalProperties: false one layer
// inside an array, not only at the top).
type Objection struct {
	Target      string `json:"target"`
	Summary     string `json:"summary"`
	Consequence string `json:"consequence"`
}

type Critique struct {
	Verdict    string      `json:"verdict"`
	Objections []Objection `json:"objections,omitempty"`
}

func TestFromStructMatchesTheWorkedExample(t *testing.T) {
	raw := schema.FromStruct(Critique{}, "verdict")
	top := decode(t, raw)

	if top.Type != "object" {
		t.Fatalf("top-level type = %q, want object", top.Type)
	}
	if top.AdditionalProperties == nil || *top.AdditionalProperties {
		t.Error("top-level additionalProperties must be false")
	}
	if got := top.Required; len(got) != 1 || got[0] != "verdict" {
		t.Errorf("required = %v, want [verdict]", got)
	}
	if len(top.Properties) != 2 {
		t.Errorf("properties = %v, want exactly verdict and objections", top.Properties)
	}
	if decodeLeaf(t, top.Properties["verdict"]).Type != "string" {
		t.Errorf("verdict property is not a string: %s", top.Properties["verdict"])
	}

	objections := decode(t, top.Properties["objections"])
	if objections.Type != "array" {
		t.Fatalf("objections type = %q, want array", objections.Type)
	}
	items := decode(t, objections.Items)
	if items.Type != "object" {
		t.Fatalf("objections.items type = %q, want object", items.Type)
	}
	// The load-bearing assertion: additionalProperties: false one layer
	// inside the array, not only at the top level. A schema missing this
	// would not have caught WND-45's stray field inside objections[].
	if items.AdditionalProperties == nil || *items.AdditionalProperties {
		t.Error("objections[].additionalProperties must be false — this is what catches a renamed field nested in an array")
	}
	if len(items.Required) != 0 {
		t.Errorf("objections[].required = %v, want none — nested objects never carry a required list", items.Required)
	}
	for _, want := range []string{"target", "summary", "consequence"} {
		if _, ok := items.Properties[want]; !ok {
			t.Errorf("objections[] is missing property %q", want)
		}
	}
	if len(items.Properties) != 3 {
		t.Errorf("objections[] properties = %v, want exactly target/summary/consequence", items.Properties)
	}
}

type embedded struct {
	Inner
	Extra string `json:"extra"`
}

type Inner struct {
	Name string `json:"name"`
}

func TestFromStructFlattensEmbeddedStructs(t *testing.T) {
	top := decode(t, schema.FromStruct(embedded{}))
	for _, want := range []string{"name", "extra"} {
		if _, ok := top.Properties[want]; !ok {
			t.Errorf("properties = %v, want %q flattened in from the embedded struct", top.Properties, want)
		}
	}
	if _, ok := top.Properties["Inner"]; ok {
		t.Error("the embedded struct must not appear as its own nested property")
	}
}

type withIgnored struct {
	Kept    string `json:"kept"`
	Ignored string `json:"-"`
}

func TestFromStructOmitsDashTaggedFields(t *testing.T) {
	top := decode(t, schema.FromStruct(withIgnored{}))
	if _, ok := top.Properties["kept"]; !ok {
		t.Error("kept field missing from schema")
	}
	if _, ok := top.Properties["Ignored"]; ok {
		t.Error(`a field tagged json:"-" must not appear in the schema`)
	}
	if len(top.Properties) != 1 {
		t.Errorf("properties = %v, want exactly one", top.Properties)
	}
}

type withPointer struct {
	Estimate *int `json:"estimate,omitempty"`
}

func TestFromStructUnwrapsPointers(t *testing.T) {
	top := decode(t, schema.FromStruct(withPointer{}))
	if got := decodeLeaf(t, top.Properties["estimate"]).Type; got != "integer" {
		t.Errorf("estimate type = %q, want integer (unwrapped from *int)", got)
	}
}

func TestFromStructNoRequiredOmitsTheKey(t *testing.T) {
	raw := schema.FromStruct(Inner{})
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["required"]; ok {
		t.Errorf(`schema carries a "required" key with nothing passed in: %s`, raw)
	}
}
