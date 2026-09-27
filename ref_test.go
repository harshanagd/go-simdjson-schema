package jsonschema

import (
	"encoding/json"
	"errors"
	"testing"
)

// $ref follows v6's compile-time-resolved pointer into the target subschema.
func TestRefFollowsTarget(t *testing.T) {
	s := `{"properties":{"a":{"$ref":"#/$defs/str"}},"$defs":{"str":{"type":"string"}}}`
	validates(t, s, `{"a":"x"}`, true)
	validates(t, s, `{"a":1}`, false) // target type:string rejects the number
}

// A recursive tree ($ref cycle through different instance nodes) must keep
// descending, not short-circuit — the invalid inner field must be caught.
func TestRefRecursiveTree(t *testing.T) {
	s := `{"$id":"mem://tree","type":"object",
		"properties":{"child":{"$ref":"mem://tree"},"v":{"type":"number"}},
		"required":["v"]}`
	validates(t, s, `{"v":1,"child":{"v":2}}`, true)
	validates(t, s, `{"v":1,"child":{"v":"bad"}}`, false) // inner v not a number
	validates(t, s, `{"v":1,"child":{}}`, false)          // inner missing required v
}

// A self-referential schema on a self-referential instance node terminates via
// the cycle guard rather than looping forever.
func TestRefSelfCycleTerminates(t *testing.T) {
	s := `{"$id":"mem://s","$ref":"mem://s","type":"object"}`
	validates(t, s, `{}`, true) // re-entry at the same node imposes no new constraint
}

// Two sibling instance nodes validated against the same $ref-bearing schema are
// independent, not a cycle — the second must not be waved through as "seen".
func TestRefSiblingScalarsNotACycle(t *testing.T) {
	s := `{"type":"array","items":{"$ref":"#/$defs/str"},"$defs":{"str":{"type":"string"}}}`
	validates(t, s, `["a","b"]`, true)
	validates(t, s, `["a",1]`, false) // second item must still be type-checked
}

// A $dynamicRef carrying a dynamic anchor needs runtime-scope resolution we do
// not implement, so it stays gated rather than silently using the static target.
func TestDynamicRefWithAnchorGated(t *testing.T) {
	s := compileJSON(t, `{"$id":"mem://r","type":"array","items":{"$dynamicRef":"#items"},
		"$defs":{"foo":{"$dynamicAnchor":"items","type":"string"}}}`)
	inst, err := decodeSuiteJSON(json.RawMessage(`["x"]`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("want ErrNotImplemented for anchored $dynamicRef, got %v", err)
	}
}

// A $recursiveRef whose target carries $recursiveAnchor:true is resolved by v6
// through the runtime scope; following the static pointer would wrongly resolve
// nested nodes to the base (lax) schema — a false PASS. It must stay gated.
func TestRecursiveRefWithAnchorGated(t *testing.T) {
	s := compileJSON(t, `{"$schema":"https://json-schema.org/draft/2019-09/schema",
		"$id":"mem://tree","$recursiveAnchor":true,
		"type":"object","properties":{"children":{"type":"array","items":{"$recursiveRef":"#"}}}}`)
	inst, err := decodeSuiteJSON(json.RawMessage(`{"children":[{}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("want ErrNotImplemented for anchored $recursiveRef, got %v", err)
	}
}
