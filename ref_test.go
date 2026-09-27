package jsonschema

import (
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

// An anchored $dynamicRef resolves through the runtime dynamic scope (section
// 7b): with only the bookend anchor in scope it behaves as the lexical target.
func TestDynamicRefWithAnchorResolves(t *testing.T) {
	s := `{"$id":"mem://r","type":"array","items":{"$dynamicRef":"#items"},
		"$defs":{"foo":{"$dynamicAnchor":"items","type":"string"}}}`
	validates(t, s, `["x"]`, true) // items resolve to type:string
	validates(t, s, `[1]`, false)  // a non-string item is rejected by the anchor
}

// A $recursiveRef with $recursiveAnchor:true resolves through the runtime scope
// to the outermost recursive-anchor resource, so nested nodes are constrained by
// the extending schema — the invalid inner value must be caught.
func TestRecursiveRefWithAnchorResolves(t *testing.T) {
	s := `{"$schema":"https://json-schema.org/draft/2019-09/schema",
		"$id":"mem://tree","$recursiveAnchor":true,
		"type":"object","properties":{"v":{"type":"number"},
		"children":{"type":"array","items":{"$recursiveRef":"#"}}}}`
	validates(t, s, `{"v":1,"children":[{"v":2}]}`, true)
	validates(t, s, `{"v":1,"children":[{"v":"bad"}]}`, false) // inner v not a number
}
