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

func TestUnknownKeywordIsUnsupported(t *testing.T) {
	// properties is still gated (object applicator subschemas are a later slice).
	s := compileJSON(t, `{"properties":{"a":{"type":"string"}}}`)
	inst, err := decodeSuiteJSON(json.RawMessage(`{"a":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("want ErrNotImplemented for properties, got nil (silent pass)")
	}
}

func TestBooleanIsNotANumber(t *testing.T) {
	validates(t, `{"type":"integer"}`, `true`, false) // a bool must not satisfy a numeric type
	validates(t, `{"type":"number"}`, `false`, false)
}

func TestUniqueItemsValueEquality(t *testing.T) {
	validates(t, `{"uniqueItems":true}`, `[1,1.0]`, false) // 1 and 1.0 are equal by value
}

// A draft-07 schema asserts format by default, so v6 populates Format. We must
// gate it as unsupported, not silently pass invalid input.
func TestFormatAssertionIsGated(t *testing.T) {
	s := compileJSON(t, `{"$schema":"http://json-schema.org/draft-07/schema#","format":"ipv4"}`)
	inst, err := decodeSuiteJSON(json.RawMessage(`"999.999.999.999"`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(inst); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("want ErrNotImplemented for asserted format, got %v", err)
	}
}
