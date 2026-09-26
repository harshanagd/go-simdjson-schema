// Package jsonschema is a JSON Schema validator backed by the go-simdjson tape.
//
// It is a drop-in replacement for github.com/santhosh-tekuri/jsonschema v6: the
// compile-then-validate API is the same, but validation walks the SIMD-parsed
// tape instead of a map[string]any. See .kiro/specs/jsonschema-validator for the
// design.
//
// This is the initial API surface. The tape evaluator is not implemented yet;
// Validate returns ErrNotImplemented. The compliance harness (suite_test.go)
// records this as a scoreboard rather than failing the build, so sections can be
// brought green one keyword at a time.
package jsonschema

import "errors"

// ErrNotImplemented is returned by Validate until the tape evaluator lands.
var ErrNotImplemented = errors.New("jsonschema: tape evaluator not implemented")

// Schema is a compiled JSON Schema. For now it holds the raw schema document;
// it will grow into the lowered instruction stream (design.md option B→C).
type Schema struct {
	// doc is the schema as decoded JSON (any). Placeholder until the compiler
	// (reused from v6) and the lowering pass are wired in.
	doc any
}

// Compile turns a decoded JSON Schema document into a *Schema.
//
// The real implementation will reuse v6's compiler for
// $ref/$id/anchor/vocabulary/draft-detection, then lower to the tape evaluator's
// instruction stream. For now it just retains the document.
func Compile(doc any) (*Schema, error) {
	return &Schema{doc: doc}, nil
}

// Validate reports whether the instance satisfies the schema.
//
// Not implemented yet: always returns ErrNotImplemented.
func (s *Schema) Validate(instance any) error {
	return ErrNotImplemented
}
