package jsonschema

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestProperties(t *testing.T) {
	s := `{"properties":{"a":{"type":"integer"}}}`
	validates(t, s, `{"a":1}`, true)
	validates(t, s, `{"a":"x"}`, false) // wrong type for a
	validates(t, s, `{"b":"x"}`, true)  // unconstrained property
}

func TestAdditionalPropertiesFalse(t *testing.T) {
	s := `{"properties":{"a":{}},"additionalProperties":false}`
	validates(t, s, `{"a":1}`, true)
	validates(t, s, `{"a":1,"b":2}`, false) // b is additional, not allowed
}

func TestPatternProperties(t *testing.T) {
	s := `{"patternProperties":{"^x":{"type":"integer"}}}`
	validates(t, s, `{"x1":1}`, true)
	validates(t, s, `{"x1":"nope"}`, false)
	validates(t, s, `{"y":"anything"}`, true) // no pattern match, unconstrained
}

func TestPropertyNames(t *testing.T) {
	s := `{"propertyNames":{"maxLength":3}}`
	validates(t, s, `{"ab":1}`, true)
	validates(t, s, `{"abcd":1}`, false) // key too long
}

func TestContainsDefaultMinOne(t *testing.T) {
	s := `{"contains":{"type":"integer"}}`
	validates(t, s, `[1,"x"]`, true) // at least one integer
	validates(t, s, `["x","y"]`, false)
}

func TestMinMaxContains(t *testing.T) {
	s := `{"contains":{"type":"integer"},"minContains":2,"maxContains":3}`
	validates(t, s, `[1,2]`, true)
	validates(t, s, `[1]`, false)       // fewer than minContains
	validates(t, s, `[1,2,3,4]`, false) // more than maxContains
}

func TestPrefixItemsAndItems(t *testing.T) {
	s := `{"prefixItems":[{"type":"integer"},{"type":"string"}],"items":{"type":"boolean"}}`
	validates(t, s, `[1,"x",true,false]`, true)
	validates(t, s, `[1,"x",5]`, false) // tail item not boolean
	validates(t, s, `["x","x"]`, false) // first prefix item not integer
}

// A gated keyword nested inside an object applicator must propagate.
func TestObjectApplicatorPropagatesUnsupported(t *testing.T) {
	// unevaluatedProperties is still gated (section 9); nesting it inside a
	// property subschema must surface as unsupported, not a silent pass.
	s := compileJSON(t, `{"properties":{"a":{"unevaluatedProperties":false}}}`)
	inst, err := decodeSuiteJSON(json.RawMessage(`{"a":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("want ErrNotImplemented propagated from nested gated keyword, got %v", err)
	}
}

// A definite failure in a conjunction must win over a gated sibling regardless
// of map-iteration order — the verdict must be a deterministic INVALID, never a
// run-dependent "unsupported". Property "a" definitely fails its type; sibling
// "b" carries a gated unevaluatedProperties. Run enough times to shuffle map order.
func TestConjunctionPrefersDefiniteFailure(t *testing.T) {
	s := compileJSON(t, `{"properties":{"a":{"type":"integer"},"b":{"unevaluatedProperties":false}}}`)
	inst, err := decodeSuiteJSON(json.RawMessage(`{"a":"not-an-int","b":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		verr := s.Validate(inst)
		if verr == nil || errors.Is(verr, ErrNotImplemented) {
			t.Fatalf("want a definite ValidationError (a fails type), got %v", verr)
		}
	}
}
