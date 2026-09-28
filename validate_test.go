package jsonschema

import (
	"encoding/json"
	"errors"
	"testing"
)

// compileJSON and validateJSON are test-only conveniences: they decode JSON text
// with UseNumber (so the integer/number distinction survives) and drive the
// public Compile/Validate API. The package itself exposes no bytes API — the
// eventual JSON-bytes entry point will parse a go-simdjson tape, not encoding/json.
func compileJSON(t *testing.T, schema string) *Schema {
	t.Helper()
	doc, err := decodeSuiteJSON(json.RawMessage(schema))
	if err != nil {
		t.Fatalf("decode schema %s: %v", schema, err)
	}
	s, err := Compile(doc)
	if err != nil {
		t.Fatalf("compile %s: %v", schema, err)
	}
	return s
}

// validates is a terse helper: compile schema JSON, validate instance JSON,
// assert the verdict.
func validates(t *testing.T, schema, instance string, want bool) {
	t.Helper()
	s := compileJSON(t, schema)
	inst, err := decodeSuiteJSON(json.RawMessage(instance))
	if err != nil {
		t.Fatalf("decode instance %s: %v", instance, err)
	}
	if got := s.Validate(inst) == nil; got != want {
		t.Fatalf("schema=%s instance=%s: want valid=%v, got err=%v", schema, instance, want, s.Validate(inst))
	}
}

func TestTypeIntegerMatchesIntegralFloat(t *testing.T) {
	validates(t, `{"type":"integer"}`, `1.0`, true)  // zero fractional part is an integer
	validates(t, `{"type":"integer"}`, `1.1`, false) // fractional part is not
}

func TestExactDecimalBounds(t *testing.T) {
	// float64 cannot represent 0.0001; exact rationals must not drift.
	validates(t, `{"multipleOf":0.0001}`, `0.0075`, true)
	validates(t, `{"minimum":1.0}`, `1.0`, true) // boundary point
}

func TestConstNumberEquality(t *testing.T) {
	validates(t, `{"const":1}`, `1.0`, true) // 1 == 1.0 by value
}

// A custom vocabulary the compiler surfaces as an Extension is the remaining
// gated surface (references incl. anchored dynamic refs, applicators,
// unevaluated*, and format are all implemented). A meta-schema declaring an
// unknown vocabulary as required makes v6 populate Extensions, which
// usesUnimplemented gates to ErrNotImplemented — the false-PASS floor for a
// schema outside the implemented surface.
func TestCustomVocabularyIsUnsupported(t *testing.T) {
	meta := `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"$id":"mem://meta",
		"$vocabulary":{
			"https://json-schema.org/draft/2020-12/vocab/core":true,
			"mem://vocab/custom":true
		},
		"$dynamicAnchor":"meta",
		"allOf":[{"$ref":"https://json-schema.org/draft/2020-12/schema"}]
	}`
	schema := `{"$schema":"mem://meta","type":"string"}`
	sDoc, err := decodeSuiteJSON(json.RawMessage(schema))
	if err != nil {
		t.Fatal(err)
	}
	mDoc, err := decodeSuiteJSON(json.RawMessage(meta))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(sDoc, WithResource("mem://meta", mDoc))
	if err != nil {
		// An unknown required vocabulary can be rejected at compile time, which is
		// also a valid "unsupported" signal.
		return
	}
	inst, err := decodeSuiteJSON(json.RawMessage(`"y"`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("want ErrNotImplemented for a custom vocabulary extension, got %v", err)
	}
}

func TestBooleanIsNotANumber(t *testing.T) {
	validates(t, `{"type":"integer"}`, `true`, false) // a bool must not satisfy a numeric type
	validates(t, `{"type":"number"}`, `false`, false)
}

// An off-contract Go type (int64 — not one of json.Number/float64/int) is
// KindUnknown: it satisfies no `type` and triggers no numeric assertion, exactly
// as the pre-seam code left an unrecognised value untouched. Guards the Kind()
// default arm against collapsing unknowns into KindNumber.
func TestUnknownGoTypeIsNotANumber(t *testing.T) {
	s := compileJSON(t, `{"type":"number","minimum":0}`)
	if s.Validate(int64(5)) == nil {
		t.Fatal("int64 must not satisfy type:number")
	}
	// A schema with only a numeric bound must not error on the unknown value.
	s2 := compileJSON(t, `{"minimum":0}`)
	if err := s2.Validate(int64(5)); err != nil {
		t.Fatalf("unknown type under a bound must not error, got %v", err)
	}
}

func TestUniqueItemsValueEquality(t *testing.T) {
	validates(t, `{"uniqueItems":true}`, `[1,1.0]`, false) // 1 and 1.0 are equal by value
}

// A draft-07 schema asserts format by default, so an invalid value fails and a
// valid one passes — no gating.
func TestFormatAssertsOnDraft7(t *testing.T) {
	s := compileJSON(t, `{"$schema":"http://json-schema.org/draft-07/schema#","format":"ipv4"}`)
	bad, err := decodeSuiteJSON(json.RawMessage(`"999.999.999.999"`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(bad); err == nil || errors.Is(err, ErrNotImplemented) {
		t.Fatalf("want a format violation, got %v", err)
	}
	good, err := decodeSuiteJSON(json.RawMessage(`"192.168.0.1"`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(good); err != nil {
		t.Fatalf("want valid ipv4 to pass, got %v", err)
	}
}

// On draft2020-12 format is annotation-only by default, so an invalid value
// passes unless assertion is opted in with WithFormatAssertion.
func TestFormatAnnotationOnlyByDefaultOn2020(t *testing.T) {
	doc, err := decodeSuiteJSON(json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"ipv4"}`))
	if err != nil {
		t.Fatal(err)
	}
	bad, err := decodeSuiteJSON(json.RawMessage(`"not-an-ip"`))
	if err != nil {
		t.Fatal(err)
	}

	annot, err := Compile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := annot.Validate(bad); err != nil {
		t.Fatalf("annotation-only: want pass, got %v", err)
	}

	asserted, err := Compile(doc, WithFormatAssertion())
	if err != nil {
		t.Fatal(err)
	}
	if err := asserted.Validate(bad); err == nil {
		t.Fatalf("WithFormatAssertion: want a format violation, got nil")
	}
}

// WithRegexpEngine lets a pattern using an ECMA-262 construct Go RE2 rejects
// (here \cc, a control-character escape) compile and match, where the default
// engine fails to compile the schema at all.
func TestWithRegexpEngineAcceptsEcmaPattern(t *testing.T) {
	doc, err := decodeSuiteJSON(json.RawMessage(`{"type":"string","pattern":"^\\cc$"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(doc); err == nil {
		t.Fatalf("default RE2 engine: want compile to reject \\cc, got nil")
	}
	s, err := Compile(doc, WithRegexpEngine(dlclarkCompile))
	if err != nil {
		t.Fatalf("regexp2 engine: want compile to accept \\cc, got %v", err)
	}
	inst, err := decodeSuiteJSON(json.RawMessage(`"\u0003"`)) // \cc == U+0003
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst); err != nil {
		t.Fatalf("regexp2 engine: want \\u0003 to match ^\\cc$, got %v", err)
	}
}
