package jsonschema

import (
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

// An anchored $dynamicRef nested inside an object applicator resolves through
// the runtime scope (section 7b): the property subschema constrains via the
// dynamic anchor, so a mismatching value is rejected — not silently passed.
func TestObjectApplicatorResolvesDynamicRef(t *testing.T) {
	s := `{"$id":"mem://o","type":"array","items":{"$dynamicRef":"#el"},
		"$defs":{"foo":{"$dynamicAnchor":"el","type":"string"}}}`
	validates(t, s, `["x"]`, true)
	validates(t, s, `[1]`, false) // element must be a string per the resolved anchor
}

// conj must let a definite failure outrank a deferred ErrNotImplemented
// regardless of the order results arrive (map iteration is unordered), so a
// conjunction with any definite failure is a deterministic INVALID, never a
// run-dependent "unsupported". No keyword still gates per-subschema through the
// public API (references, applicators, unevaluated*, format are all
// implemented; only whole-document content/custom vocabularies gate), so this
// precedence is exercised directly on conj rather than via Compile.
func TestConjunctionPrefersDefiniteFailure(t *testing.T) {
	fail := &ValidationError{Msg: "definite"}
	unsup := ErrNotImplemented
	// Both arrival orders must resolve to the definite failure.
	var a conj
	a.add(unsup)
	a.add(fail)
	if a.result() != error(fail) {
		t.Fatalf("unsupported-then-failure: want the definite failure, got %v", a.result())
	}
	var b conj
	b.add(fail)
	b.add(unsup)
	if b.result() != error(fail) {
		t.Fatalf("failure-then-unsupported: want the definite failure, got %v", b.result())
	}
	// With only an unsupported branch, the deferred unsupported is the verdict.
	var c conj
	c.add(unsup)
	if !errors.Is(c.result(), ErrNotImplemented) {
		t.Fatalf("unsupported-only: want ErrNotImplemented, got %v", c.result())
	}
}
