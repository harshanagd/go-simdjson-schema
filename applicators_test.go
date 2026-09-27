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

// A subschema using an unimplemented keyword must propagate ErrNotImplemented
// through EVERY applicator, not be silently treated as a non-match — otherwise
// a dropped guard could flip a verdict. One case per helper.
func TestApplicatorPropagatesUnsupported(t *testing.T) {
	// An anchored $dynamicRef is still gated (needs runtime-scope resolution).
	// Nesting it inside each applicator must surface as unsupported rather than a
	// silent non-match. defs supplies the $dynamicAnchor the ref names.
	ref := `"$dynamicRef":"#x"`
	defs := `"$defs":{"d":{"$dynamicAnchor":"x","type":"string"}}`
	cases := map[string]string{
		"anyOf": `{"$id":"mem://a","anyOf":[{` + ref + `}],` + defs + `}`,
		"allOf": `{"$id":"mem://b","allOf":[{` + ref + `}],` + defs + `}`,
		"oneOf": `{"$id":"mem://c","oneOf":[{` + ref + `}],` + defs + `}`,
		"not":   `{"$id":"mem://d","not":{` + ref + `},` + defs + `}`,
		"if":    `{"$id":"mem://e","if":{` + ref + `},"then":{"type":"object"},` + defs + `}`,
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			s := compileJSON(t, schema)
			inst, err := decodeSuiteJSON(json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Validate(inst); !errors.Is(err, ErrNotImplemented) {
				t.Fatalf("%s: want ErrNotImplemented propagated, got %v", name, err)
			}
		})
	}
}
