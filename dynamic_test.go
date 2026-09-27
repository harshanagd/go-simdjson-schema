package jsonschema

import (
	"encoding/json"
	"testing"
)

// Section 7b: anchored $dynamicRef / $recursiveRef resolution over the runtime
// dynamic scope, reconstructed fork-free (dynamic.go). Each test fails against a
// static-follow implementation, pinning the scope-resolution behaviour.

// A $dynamicRef resolves to the OUTERMOST matching $dynamicAnchor in scope, not
// its lexical target: the extending "root" resource's anchor overrides the
// bookend in the referenced "list" resource.
func TestDynamicRefResolvesToOuterScope(t *testing.T) {
	// root declares items -> {type:string}; list's bookend items is unconstrained.
	// A $dynamicRef "#items" inside list must resolve to root's stricter anchor.
	s := `{
		"$id":"https://ex/root",
		"$ref":"list",
		"$defs":{
			"foo":{"$dynamicAnchor":"items","type":"string"},
			"list":{"$id":"list","type":"array","items":{"$dynamicRef":"#items"},
				"$defs":{"items":{"$dynamicAnchor":"items"}}}
		}
	}`
	validates(t, s, `["ok"]`, true)
	validates(t, s, `[1]`, false) // resolved to root's type:string, so a number fails
}

// With no matching $dynamicAnchor in the extending scope, a $dynamicRef falls
// back to its lexical (bookend) target — behaving as a plain $ref.
func TestDynamicRefFallsBackToLexical(t *testing.T) {
	// No outer resource redefines "el"; the ref resolves to the local anchor,
	// which constrains items to a string.
	s := `{"$id":"https://ex/only","type":"array","items":{"$dynamicRef":"#el"},
		"$defs":{"el":{"$dynamicAnchor":"el","type":"string"}}}`
	validates(t, s, `["x"]`, true)
	validates(t, s, `[1]`, false)
}

// $recursiveRef with $recursiveAnchor:true resolves to the outermost recursive
// anchor in scope, applying the extending schema at every nesting depth.
func TestRecursiveRefNesting(t *testing.T) {
	s := `{"$schema":"https://json-schema.org/draft/2019-09/schema",
		"$id":"https://ex/tree","$recursiveAnchor":true,
		"anyOf":[{"type":"integer"},
			{"type":"object","additionalProperties":{"$recursiveRef":"#"}}]}`
	validates(t, s, `2`, true)
	validates(t, s, `{"a":{"b":3}}`, true)     // integers valid at every depth
	validates(t, s, `{"a":{"b":"no"}}`, false) // a string leaf is not integer-or-object
}

// A $dynamicAnchor living in an un-$ref'd $defs is invisible to the compiled
// pointer graph but reached by the document-walk pass (buildAnchorMap), so it
// still constrains. Its presence (strict) vs a lexical-only bookend (lenient)
// must produce different verdicts — pinning the un-$ref'd-$defs anchor path.
func TestDynamicRefUnreferencedDefsAnchor(t *testing.T) {
	// root's items anchor lives in $defs/foo, which nothing $refs; the bookend in
	// the list resource is unconstrained. The document walk must find $defs/foo
	// so items resolve to type:string.
	strict := `{"$id":"https://ex/r1","$ref":"list",
		"$defs":{
			"foo":{"$dynamicAnchor":"items","type":"string"},
			"list":{"$id":"list","type":"array","items":{"$dynamicRef":"#items"},
				"$defs":{"items":{"$dynamicAnchor":"items"}}}
		}}`
	validates(t, strict, `["x"]`, true)
	validates(t, strict, `[1]`, false) // resolved to $defs/foo type:string

	// Same shape without the outer $defs/foo anchor: the ref resolves only to the
	// unconstrained bookend, so any item passes (lenient).
	lenient := `{"$id":"https://ex/r2","$ref":"list",
		"$defs":{
			"list":{"$id":"list","type":"array","items":{"$dynamicRef":"#items"},
				"$defs":{"items":{"$dynamicAnchor":"items"}}}
		}}`
	validates(t, lenient, `["x"]`, true)
	validates(t, lenient, `[1]`, true) // no stricter anchor in scope
}

// A $ref to the full 2020-12 metaschema (which uses $dynamicRef internally) must
// validate a schema document — the defs.json metaschema case. This exercises the
// remote-resource anchor path (anchors on the loaded metaschema's resources).
func TestRefToMetaschemaValidatesSchema(t *testing.T) {
	doc, err := decodeSuiteJSON(json.RawMessage(`{"$ref":"https://json-schema.org/draft/2020-12/schema"}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(doc)
	if err != nil {
		t.Fatal(err)
	}
	// A valid schema document passes; a schema with a wrong-typed keyword fails.
	good, _ := decodeSuiteJSON(json.RawMessage(`{"type":"string"}`))
	bad, _ := decodeSuiteJSON(json.RawMessage(`{"type":1}`)) // type must be string/array
	if err := s.Validate(good); err != nil {
		t.Fatalf("valid schema doc should pass the metaschema, got %v", err)
	}
	if err := s.Validate(bad); err == nil {
		t.Fatal("invalid schema doc (type:1) should fail the metaschema")
	}
}
