package jsonschema

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNotInverts(t *testing.T) {
	validates(t, `{"not":{"type":"string"}}`, `42`, true)   // not-a-string passes
	validates(t, `{"not":{"type":"string"}}`, `"x"`, false) // a string fails
}

func TestAllOfRequiresAll(t *testing.T) {
	validates(t, `{"allOf":[{"type":"integer"},{"minimum":5}]}`, `7`, true)
	validates(t, `{"allOf":[{"type":"integer"},{"minimum":5}]}`, `3`, false) // fails the second
}

func TestOneOfExactlyOne(t *testing.T) {
	s := `{"oneOf":[{"type":"integer"},{"type":"string"}]}`
	validates(t, s, `3`, true)     // integer only → exactly one
	validates(t, s, `"x"`, true)   // string only → exactly one
	validates(t, s, `true`, false) // neither → zero matches, fails

	// Overlapping subschemas: an integer ≥5 matches both → fails oneOf.
	two := `{"oneOf":[{"type":"integer"},{"minimum":5}]}`
	validates(t, two, `7`, false) // integer AND ≥5 → two matches
	validates(t, two, `3`, true)  // integer only (3 < 5) → exactly one
}

func TestIfThenElse(t *testing.T) {
	s := `{"if":{"type":"integer"},"then":{"minimum":10},"else":{"type":"string"}}`
	validates(t, s, `15`, true)    // integer → then: ≥10 ✓
	validates(t, s, `5`, false)    // integer → then: ≥10 ✗
	validates(t, s, `"x"`, true)   // not integer → else: string ✓
	validates(t, s, `true`, false) // not integer → else: string ✗
}

// An anchored $dynamicRef nested inside each in-place applicator must resolve
// through the runtime dynamic scope (section 7b) — the threading carries the
// scope into every applicator branch. Each case constrains items to type:string
// via the dynamic anchor; a non-string must be rejected through the applicator.
func TestApplicatorThreadsDynamicScope(t *testing.T) {
	ref := `"items":{"$dynamicRef":"#items"}`
	defs := `"$defs":{"foo":{"$dynamicAnchor":"items","type":"string"}}`
	// Each schema wraps an array-typed subschema (carrying the $dynamicRef) in one
	// applicator, so the anchor must resolve inside that applicator's branch.
	cases := map[string]string{
		"anyOf": `{"$id":"mem://a","anyOf":[{"type":"array",` + ref + `}],` + defs + `}`,
		"allOf": `{"$id":"mem://b","allOf":[{"type":"array",` + ref + `}],` + defs + `}`,
		"oneOf": `{"$id":"mem://c","oneOf":[{"type":"array",` + ref + `}],` + defs + `}`,
		"then":  `{"$id":"mem://e","if":{"type":"array"},"then":{"type":"array",` + ref + `},` + defs + `}`,
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			good, err := decodeSuiteJSON(json.RawMessage(`["x"]`))
			if err != nil {
				t.Fatal(err)
			}
			bad, err := decodeSuiteJSON(json.RawMessage(`[1]`))
			if err != nil {
				t.Fatal(err)
			}
			s := compileJSON(t, schema)
			if err := s.Validate(good); err != nil {
				t.Fatalf("%s: want string item to pass, got %v", name, err)
			}
			if err := s.Validate(bad); err == nil || errors.Is(err, ErrNotImplemented) {
				t.Fatalf("%s: want non-string item rejected via the anchor, got %v", name, err)
			}
		})
	}
}
