package jsonschema

import "testing"

// A sibling allOf that evaluates a property counts for unevaluatedProperties.
func TestUnevaluatedSeesSiblingAllOf(t *testing.T) {
	s := `{"allOf":[{"properties":{"a":{}}}],"unevaluatedProperties":false}`
	validates(t, s, `{"a":1}`, true)        // a evaluated by allOf
	validates(t, s, `{"a":1,"b":2}`, false) // b unevaluated
}

// A property evaluated through a $ref counts (section 7 had to land first).
func TestUnevaluatedSeesRef(t *testing.T) {
	s := `{"$ref":"#/$defs/x","unevaluatedProperties":false,"$defs":{"x":{"properties":{"a":{}}}}}`
	validates(t, s, `{"a":1}`, true)
	validates(t, s, `{"a":1,"b":2}`, false)
}

// additionalProperties:true evaluates every extra property.
func TestUnevaluatedWithAdditionalTrue(t *testing.T) {
	s := `{"properties":{"a":{}},"additionalProperties":true,"unevaluatedProperties":false}`
	validates(t, s, `{"a":1,"b":2}`, true) // b evaluated by additionalProperties:true
}

// A failing anyOf branch must NOT contribute its evaluations. Branch 0
// successfully evaluates "a" but fails a sibling requirement, so its scratch
// marks (including "a") must be discarded — otherwise unevaluatedProperties:false
// would wrongly accept "a".
func TestUnevaluatedFailedBranchDoesNotContribute(t *testing.T) {
	s := `{"anyOf":[{"properties":{"a":{}},"required":["z"]},{"properties":{"x":{}}}],
		"unevaluatedProperties":false}`
	validates(t, s, `{"a":1}`, false) // branch 0 evaluates a but fails (needs z); marks discarded
}

// Nested unevaluated*: an inner owner's evaluations propagate to the outer. bar
// is length 3, so it satisfies the outer {maxLength:2} ONLY if the inner
// unevaluatedProperties:true evaluated it (removing it from the outer's remainder)
// — a regression in inner→outer propagation makes this fail.
func TestUnevaluatedNestedPropagatesUp(t *testing.T) {
	s := `{"properties":{"foo":{}},"allOf":[{"unevaluatedProperties":true}],
		"unevaluatedProperties":{"type":"string","maxLength":2}}`
	validates(t, s, `{"foo":1,"bar":"xxx"}`, true) // inner true evaluates bar; outer never sees it
}

// unevaluatedItems sees prefixItems-covered indices.
func TestUnevaluatedItemsWithPrefix(t *testing.T) {
	s := `{"prefixItems":[{"type":"number"}],"unevaluatedItems":false}`
	validates(t, s, `[1]`, true)    // index 0 covered by prefixItems
	validates(t, s, `[1,2]`, false) // index 1 unevaluated
}
